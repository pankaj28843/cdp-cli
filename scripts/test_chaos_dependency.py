"""Synthetic boundary checks for dependency chaos safeguards."""

import contextlib
import http.server
import io
import json
import pathlib
import socket
import sys
import tempfile
import threading
import time
import unittest
import urllib.error
from unittest import mock

import chaos_dependency


class DependencyChaosTest(unittest.TestCase):
    def test_resident_monitor_records_restart_across_controller_absence(self):
        opener = mock.Mock()
        count = [0]

        def response(*_args, **_kwargs):
            count[0] += 1
            identity = "old" if count[0] == 1 else "new"
            return io.BytesIO(
                json.dumps({"status": "ok", "uptime": {"process_started_at": identity}}).encode()
            )

        opener.open.side_effect = response
        clock = [0.0]
        with tempfile.TemporaryDirectory() as directory:
            output = pathlib.Path(directory) / "status.json"
            with (
                mock.patch.object(
                    chaos_dependency.urllib.request, "build_opener", return_value=opener
                ),
                mock.patch.object(chaos_dependency.time, "monotonic", side_effect=lambda: clock[0]),
                mock.patch.object(
                    chaos_dependency.time,
                    "sleep",
                    side_effect=lambda seconds: clock.__setitem__(0, clock[0] + seconds),
                ),
            ):
                chaos_dependency.monitor_local("https://synthetic.invalid", 1.05, output)
            body = json.loads(output.read_text())
            self.assertEqual(body["status"], "failed")
            self.assertEqual(body["recorded_health"]["restarts"], 1)
            self.assertTrue(body["recorded_health"]["completed"])
            self.assertEqual(output.stat().st_mode & 0o777, 0o600)

    def test_recorded_health_reconnects_but_rejects_failed_or_stale_history(self):
        body = {
            "status": "ok",
            "uptime": {"process_started_at": "stable"},
            "recorded_health": {
                "samples": 20,
                "failures": 0,
                "restarts": 0,
                "last_sample_at": time.time(),
            },
        }
        source = {"command": ["synthetic-read"]}
        response = mock.Mock(stdout=json.dumps(body))
        with (
            mock.patch.object(
                chaos_dependency.subprocess,
                "run",
                side_effect=[
                    chaos_dependency.subprocess.TimeoutExpired(source["command"], 8),
                    response,
                ],
            ) as run,
            mock.patch.object(chaos_dependency.time, "sleep"),
        ):
            self.assertEqual(
                chaos_dependency.recorded_health(source)["recorded_health"]["samples"], 20
            )
            self.assertEqual(run.call_count, 2)
        for field, value in (
            ("failures", 1),
            ("restarts", 1),
            ("last_sample_at", time.time() - 6),
            ("last_sample_at", time.time() + 6),
        ):
            with self.subTest(field=field, value=value):
                document = json.loads(json.dumps(body))
                document["recorded_health"][field] = value
                with mock.patch.object(
                    chaos_dependency.subprocess,
                    "run",
                    return_value=mock.Mock(stdout=json.dumps(document)),
                ):
                    with self.assertRaises(ValueError):
                        chaos_dependency.recorded_health(source)

    def test_transport_observer_needs_fresh_authority_and_cannot_hide_http_failure(self):
        for stale, http_failure in ((False, False), (True, False), (False, True)):
            with self.subTest(stale=stale, http_failure=http_failure):
                clock = [0.0]
                opener = mock.Mock()

                def response(url, **_kwargs):
                    if url.endswith("/authority"):
                        return io.BytesIO(
                            b'{"status":"ok","uptime":{"process_started_at":"stable"}}'
                        )
                    if stale:
                        clock[0] = 6.0
                    if http_failure:
                        raise urllib.error.HTTPError(url, 503, "unavailable", None, None)
                    raise TimeoutError()

                opener.open.side_effect = response
                plan = {
                    "health": {
                        "api": "https://synthetic.invalid/authority",
                        "lan": "https://synthetic.invalid/observer",
                    },
                    "transport_observers": {"lan": "api"},
                    "scenarios": [{"name": "observe", "fault": ["true"], "ready": ["true"]}],
                }
                output = io.StringIO()
                with (
                    mock.patch.object(
                        chaos_dependency.urllib.request, "build_opener", return_value=opener
                    ),
                    mock.patch.object(
                        chaos_dependency.time, "monotonic", side_effect=lambda: clock[0]
                    ),
                    contextlib.redirect_stdout(output),
                ):
                    if stale or http_failure:
                        with self.assertRaises(RuntimeError):
                            chaos_dependency.run(plan)
                    else:
                        chaos_dependency.run(plan)
                        report = json.loads(output.getvalue())
                        self.assertTrue(report["availability_pass"])
                        self.assertFalse(report["reachability_pass"])
                        self.assertGreater(report["apis"]["lan"]["failures"], 0)
                        self.assertNotIn("last_success", report["apis"]["api"])

    def test_last_inflight_probe_failure_cannot_return_success(self):
        opener = mock.Mock()
        baseline = True
        started = threading.Event()
        release = threading.Event()

        def response(*_args, **_kwargs):
            nonlocal baseline
            if baseline:
                baseline = False
                return io.BytesIO(b'{"status":"ok","uptime":{"process_started_at":"stable"}}')
            started.set()
            release.wait(5)
            raise urllib.error.HTTPError(
                "https://synthetic.invalid", 503, "unavailable", None, None
            )

        def command(*_args, **_kwargs):
            self.assertTrue(started.wait(5))
            return True

        original_join = threading.Thread.join

        def join(thread, timeout=None):
            release.set()
            return original_join(thread, timeout)

        opener.open.side_effect = response
        with (
            mock.patch.object(chaos_dependency.urllib.request, "build_opener", return_value=opener),
            mock.patch.object(chaos_dependency, "command", side_effect=command),
            mock.patch.object(threading.Thread, "join", join),
            contextlib.redirect_stdout(io.StringIO()),
            self.assertRaises(RuntimeError),
        ):
            chaos_dependency.run(
                {
                    "health": {"synthetic": "https://synthetic.invalid/health"},
                    "scenarios": [{"name": "observe", "fault": ["true"], "ready": ["true"]}],
                }
            )

    def test_command_health_probe(self):
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            chaos_dependency.run(
                {
                    "health": {
                        "synthetic-loopback": [
                            sys.executable,
                            "-c",
                            'print(\'{"status":"ok","uptime":{"process_started_at":"stable"}}\')',
                        ]
                    },
                    "scenarios": [
                        {
                            "name": "observe",
                            "fault": [sys.executable, "-c", "pass"],
                            "ready": [sys.executable, "-c", "pass"],
                        }
                    ],
                }
            )
        report = json.loads(output.getvalue())
        self.assertTrue(report["availability_pass"])
        self.assertGreaterEqual(report["apis"]["synthetic-loopback"]["samples"], 1)

    def test_http_failure_reports_status_without_response_or_url(self):
        opener = mock.Mock()
        baseline = True

        def response(*_args, **_kwargs):
            nonlocal baseline
            if baseline:
                baseline = False
                return io.BytesIO(b'{"status":"ok","uptime":{"process_started_at":"stable"}}')
            raise urllib.error.HTTPError(
                "https://synthetic.invalid/?token=sensitive-example",
                503,
                "sensitive-example-response",
                None,
                None,
            )

        opener.open.side_effect = response
        output = io.StringIO()
        with mock.patch.object(
            chaos_dependency.urllib.request, "build_opener", return_value=opener
        ):
            with contextlib.redirect_stdout(output), self.assertRaises(RuntimeError):
                chaos_dependency.run(
                    {
                        "health": {"synthetic": "https://synthetic.invalid/health"},
                        "scenarios": [
                            {
                                "name": "observe",
                                "fault": [sys.executable, "-c", "pass"],
                                "ready": [sys.executable, "-c", "pass"],
                            }
                        ],
                    }
                )
        report = json.loads(output.getvalue())
        failures = report["apis"]["synthetic"]["failure_types"]
        self.assertEqual(set(failures), {"HTTPError:503"})
        self.assertGreaterEqual(failures["HTTPError:503"], 1)
        self.assertNotIn("sensitive-example", output.getvalue())
        self.assertNotIn("synthetic.invalid", output.getvalue())

    def test_restart_during_fault_fails_and_restores(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            fault = root / "fault"
            restored = root / "restored"

            class Health(http.server.BaseHTTPRequestHandler):
                def do_GET(self):
                    self.send_response(200)
                    self.end_headers()
                    self.wfile.write(
                        json.dumps(
                            {
                                "status": "ok",
                                "uptime": {
                                    "process_started_at": "new" if fault.exists() else "old"
                                },
                            }
                        ).encode()
                    )

                def log_message(self, *_):
                    pass

            server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Health)
            thread = threading.Thread(target=server.serve_forever, daemon=True)
            thread.start()
            touch = lambda path: [
                sys.executable,
                "-c",
                "import pathlib,sys; pathlib.Path(sys.argv[1]).touch()",
                str(path),
            ]
            plan = {
                "health": {"synthetic": f"http://127.0.0.1:{server.server_port}"},
                "scenarios": [
                    {
                        "name": "synthetic-restart",
                        "fault": touch(fault),
                        "restore": touch(restored),
                        "hold_seconds": 3,
                        "ready": [sys.executable, "-c", "pass"],
                    }
                ],
            }
            output = io.StringIO()
            try:
                with contextlib.redirect_stdout(output):
                    with self.assertRaises(RuntimeError):
                        chaos_dependency.run(plan)
                self.assertTrue(restored.exists())
                report = json.loads(output.getvalue())
                self.assertFalse(report["availability_pass"])
                self.assertGreater(report["apis"]["synthetic"]["restarts"], 0)
                self.assertNotIn("started", report["apis"]["synthetic"])
            finally:
                server.shutdown()
                server.server_close()
                thread.join()

    def test_failed_baseline_never_faults(self):
        with tempfile.TemporaryDirectory() as directory:
            fault = pathlib.Path(directory) / "fault"
            with socket.socket() as listener:
                listener.bind(("127.0.0.1", 0))
                unused_port = listener.getsockname()[1]
            with self.assertRaises(RuntimeError):
                chaos_dependency.run(
                    {
                        "health": {"missing": f"http://127.0.0.1:{unused_port}"},
                        "scenarios": [
                            {
                                "fault": [
                                    sys.executable,
                                    "-c",
                                    "import pathlib,sys; pathlib.Path(sys.argv[1]).touch()",
                                    str(fault),
                                ]
                            }
                        ],
                    }
                )
            self.assertFalse(fault.exists())


if __name__ == "__main__":
    unittest.main()
