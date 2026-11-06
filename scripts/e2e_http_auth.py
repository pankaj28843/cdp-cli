#!/usr/bin/env python3
"""Synthetic localhost HTTP Basic acceptance through the installed daemon CLI."""
import base64
import contextlib
import http.server
import json
import os
import pathlib
import shutil
import subprocess
import sys
import tempfile
import threading


@contextlib.contextmanager
def fixture_state():
    root = pathlib.Path(tempfile.mkdtemp(prefix="cdp-auth-e2e-"))
    try:
        yield root
    except BaseException:
        print(json.dumps({"synthetic_fixture_state_retained": str(root)}), file=sys.stderr)
        raise
    else:
        shutil.rmtree(root)


def main():
    binary = shutil.which(sys.argv[1] if len(sys.argv) > 1 else "cdp")
    assert binary, "cdp executable unavailable"
    with fixture_state() as root:
        config = root / "config.json"
        # Exercise HTTP auth rather than the shared host's transient load.
        # Dedicated policy tests cover resource-budget rejection.
        config.write_text(json.dumps({"browser": {"resource_budget": {
            "min_free_memory_mb": 1, "min_free_disk_mb": 1, "max_load_per_cpu": 999999}}}))
        env = dict(os.environ, XDG_CONFIG_HOME=str(root / "config"),
                   CDP_TEST_AUTH_USER="fixture-user", CDP_TEST_AUTH_PASS="fixture-password")
        prefix = [binary, "--browser-mode", "headless", "--state-dir", str(root / "state"),
                  "--config", str(config)]

        def run(*args, environment=None):
            result = subprocess.run(prefix + list(args) + ["--json"], env=environment or env,
                                    capture_output=True, text=True, timeout=60)
            if result.returncode:
                try:
                    error = json.loads(result.stdout)
                    detail = {key: error.get(key) for key in ("code", "message")}
                except ValueError:
                    detail = {"exit_code": result.returncode}
                raise AssertionError("synthetic cdp command failed: " + " ".join(args[:2]) + " " + json.dumps(detail))
            return json.loads(result.stdout)

        servers = []
        targets = []
        try:
            startup = run("daemon", "keepalive", "--repair")
            print(json.dumps({"startup": {key: startup.get(key) for key in ("state", "action")},
                              "environment": {key: startup.get("environment", {}).get(key) for key in ("allowed", "state", "network")}}))
            assert startup.get("state") in ("healthy", "started", "repaired"), "synthetic browser startup was skipped"
            assert run("daemon", "health")["health"]["state"] == "healthy"
            for case in ("valid", "wrong", "cross-origin"):
                counts = {"accepted": 0, "denied": 0, "foreign_authorization": 0, "foreign_requests": 0}

                class Handler(http.server.BaseHTTPRequestHandler):
                    def do_GET(self):
                        if self.path in ("/", "/favicon.ico"):
                            self.send_response(200)
                        elif self.path == "/foreign":
                            counts["foreign_requests"] += 1
                            counts["foreign_authorization"] += bool(self.headers.get("Authorization"))
                            self.send_response(401)
                            self.send_header("WWW-Authenticate", 'Basic realm="foreign"')
                        else:
                            expected = "Basic " + base64.b64encode(b"fixture-user:fixture-password").decode()
                            accepted = self.headers.get("Authorization") == expected
                            counts["accepted" if accepted else "denied"] += 1
                            self.send_response((302 if self.path == "/redirect" else 200) if accepted else 401)
                            if accepted and self.path == "/redirect":
                                self.send_header("Location", foreign_url + "/foreign")
                            if not accepted:
                                self.send_header("WWW-Authenticate", 'Basic realm="fixture"')
                        self.end_headers()
                        self.wfile.write(b"synthetic fixture")

                    def log_message(self, *_):
                        pass

                def serve():
                    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
                    servers.append(server)
                    threading.Thread(target=server.serve_forever, daemon=True).start()
                    return "http://127.0.0.1:" + str(server.server_port)

                origin, foreign_url = serve(), serve()
                target = run("open", origin + "/")["page"]["id"]
                targets.append(target)
                path = "/redirect" if case == "cross-origin" else "/basic"
                run("eval", "history.replaceState(null, '', " + json.dumps(path) + ")", "--target", target)
                credentials = dict(env)
                if case == "wrong":
                    credentials["CDP_TEST_AUTH_PASS"] = "fixture-wrong-password"
                report = run("network", "auth", "--origin", origin, "--username-env", "CDP_TEST_AUTH_USER",
                             "--password-env", "CDP_TEST_AUTH_PASS", "--target", target,
                             "--reload", "--duration", "2s", environment=credentials)
                assert report["ok"] and report["cleanup_complete"], "auth cleanup incomplete"
                if case == "valid":
                    assert counts["accepted"] >= 1, "valid Basic did not reach HTTP 200"
                elif case == "wrong":
                    assert counts["accepted"] == 0 and 1 <= counts["denied"] <= 3, "wrong Basic not bounded"
                else:
                    assert counts["accepted"] >= 1 and counts["foreign_requests"] >= 1 and counts["foreign_authorization"] == 0, "cross-origin credential boundary failed"
                run("page", "close", "--target", target)
                targets.remove(target)
                print(json.dumps({"case": case, "passed": True, **counts}))
        finally:
            for target in targets:
                subprocess.run(prefix + ["page", "close", "--target", target, "--json"], env=env, capture_output=True, timeout=15)
            stopped = subprocess.run(prefix + ["daemon", "stop", "--force-managed", "--json"],
                                     env=env, capture_output=True, timeout=30)
            for server in servers:
                server.shutdown()
                server.server_close()
            if stopped.returncode:
                try:
                    failure = json.loads(stopped.stdout)
                    detail = {key: failure.get(key) for key in ("code", "class", "message")}
                except ValueError:
                    detail = {"exit_code": stopped.returncode}
                print(json.dumps({"owned_cleanup_failed": detail}), file=sys.stderr)
                if sys.exc_info()[0] is None:
                    raise AssertionError("owned headless cleanup failed")


if __name__ == "__main__":
    main()
