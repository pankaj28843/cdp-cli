#!/usr/bin/env python3
"""Run explicit dependency faults while monitoring uninterrupted API processes.

The owner supplies command arrays and health probes in an ephemeral JSON plan.
Health probes are URLs or commands emitting the same health JSON, allowing
node-local checks alongside client-path checks.
Optional transport observers retain path failures separately, backed by a named
authoritative probe. HTTP errors and process restarts always fail qualification.
Commands never run through a shell here; output is discarded to avoid retaining
provider/browser data. Only allowlisted health metadata reaches the report.
"""

import argparse
import json
import os
import pathlib
import signal
import subprocess
import threading
import time
import urllib.error
import urllib.request


def monitor_local(url, duration, output):
    """Record every local API observation even while the controller is offline."""
    os.umask(0o077)
    output = pathlib.Path(output)
    stop_file = output.with_suffix(".stop")
    incoming = output.with_suffix(".incoming")
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    evidence = {
        "samples": 0,
        "failures": 0,
        "restarts": 0,
        "max_seconds": 0,
        "started_at": time.time(),
        "completed": False,
    }
    identity = ""
    deadline = time.monotonic() + duration

    def save():
        document = {
            "status": "failed" if evidence["failures"] or evidence["restarts"] else "ok",
            "uptime": {"process_started_at": identity},
            "recorded_health": evidence,
        }
        incoming.write_text(json.dumps(document), encoding="utf-8")
        incoming.replace(output)

    while time.monotonic() < deadline and not stop_file.exists():
        started = time.monotonic()
        try:
            with opener.open(url, timeout=4) as response:
                body = json.load(response)
            current = body["uptime"]["process_started_at"]
            if body.get("status") not in ("ok", "degraded") or not current:
                raise ValueError("invalid local health")
            if identity and current != identity:
                evidence["restarts"] += 1
            identity = current
        except Exception as error:
            evidence["failures"] += 1
            if isinstance(error, urllib.error.HTTPError):
                error.close()
        evidence["samples"] += 1
        evidence["last_sample_at"] = time.time()
        evidence["max_seconds"] = max(evidence["max_seconds"], time.monotonic() - started)
        save()
        time.sleep(max(0, min(1 - (time.monotonic() - started), deadline - time.monotonic())))
    evidence["completed"] = True
    save()


def recorded_health(source):
    # Reconnect to retrieve cumulative local evidence, not to substitute a
    # slower timeout for the resident worker's four-second API deadline.
    deadline = time.monotonic() + 30
    while True:
        try:
            response = subprocess.run(
                source["command"],
                check=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.DEVNULL,
                text=True,
                timeout=8,
            )
            break
        except (subprocess.TimeoutExpired, subprocess.CalledProcessError) as error:
            if isinstance(error, subprocess.CalledProcessError) and error.returncode != 255:
                raise
            if time.monotonic() >= deadline:
                raise
            time.sleep(1)
    body = json.loads(response.stdout)
    evidence = body["recorded_health"]
    if (
        evidence["samples"] < 1
        or evidence["failures"]
        or evidence["restarts"]
        or abs(time.time() - evidence["last_sample_at"]) > 5
    ):
        raise ValueError("local availability evidence failed or expired")
    return body


def command(argv, timeout=60, metadata_output=False, abort=None):
    process = subprocess.Popen(
        argv,
        stdout=None if metadata_output else subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        start_new_session=True,
    )
    deadline = time.monotonic() + timeout
    while process.poll() is None:
        if time.monotonic() >= deadline or (abort is not None and abort.is_set()):
            os.killpg(process.pid, signal.SIGTERM)
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait()
            return False
        time.sleep(0.1)
    return process.returncode == 0


def run(plan):
    stop = threading.Event()
    failed = threading.Event()
    records = {}
    observers = plan.get("transport_observers", {})
    for observer, authority in observers.items():
        if (
            observer not in plan["health"]
            or authority not in plan["health"]
            or authority in observers
        ):
            raise ValueError("transport observer requires an authoritative health probe")
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def probe(name, url):
        record = records.setdefault(name, {"samples": 0, "failures": 0, "restarts": 0})
        try:
            if isinstance(url, dict):
                body = recorded_health(url)
                record["recorded_health"] = body["recorded_health"]
            elif isinstance(url, list):
                response = subprocess.run(
                    url,
                    check=True,
                    stdout=subprocess.PIPE,
                    stderr=subprocess.DEVNULL,
                    text=True,
                    timeout=8,
                )
                body = json.loads(response.stdout)
            else:
                with opener.open(url, timeout=4) as response:
                    body = json.load(response)
            started = body["uptime"]["process_started_at"]
            if body.get("status") not in ("ok", "degraded") or not started:
                raise ValueError("invalid health")
            if "started" in record and record["started"] != started:
                record["restarts"] += 1
                failed.set()
            record["started"] = started
            record["last_success"] = time.monotonic()
        except Exception as error:
            record["failures"] += 1
            failure_type = type(error).__name__
            if isinstance(error, urllib.error.HTTPError):
                failure_type += ":" + str(error.code)
                error.close()
            elif isinstance(error, urllib.error.URLError):
                failure_type += ":" + type(error.reason).__name__
            elif isinstance(error, subprocess.CalledProcessError):
                failure_type += ":" + str(error.returncode)
            failure_types = record.setdefault("failure_types", {})
            failure_types[failure_type] = failure_types.get(failure_type, 0) + 1
            authority_name = observers.get(name)
            authority = records.get(authority_name, {})
            if authority_name and isinstance(plan["health"][authority_name], dict):
                deadline = time.monotonic() + 35
                while (
                    not failed.is_set()
                    and time.monotonic() < deadline
                    and time.monotonic() - authority.get("last_success", float("-inf")) > 5
                ):
                    time.sleep(0.1)
            backed_transport_failure = (
                name in observers
                and (
                    isinstance(error, (OSError, subprocess.TimeoutExpired))
                    or isinstance(error, subprocess.CalledProcessError)
                    and error.returncode == 255
                )
                and not isinstance(error, urllib.error.HTTPError)
                and time.monotonic() - authority.get("last_success", float("-inf")) <= 5
            )
            if not backed_transport_failure:
                failed.set()
        record["samples"] += 1

    def monitor(name, url):
        interval = 10 if isinstance(url, dict) else 1
        while not stop.is_set():
            start = time.monotonic()
            probe(name, url)
            stop.wait(max(0, interval - (time.monotonic() - start)))

    # No fault is authorized until every serving endpoint passes baseline.
    for name, url in plan["health"].items():
        probe(name, url)
    if failed.is_set():
        raise RuntimeError("API baseline failed; no fault applied")
    threads = [
        threading.Thread(target=monitor, args=item, daemon=True) for item in plan["health"].items()
    ]
    for thread in threads:
        thread.start()
    results = []
    try:
        for scenario in plan["scenarios"]:
            started = time.monotonic()
            accepted = False
            try:
                if failed.is_set() or not command(scenario["fault"]):
                    raise RuntimeError("fault rejected or API unavailable")
                if scenario.get("during") and not command(
                    scenario["during"], 240, metadata_output=True, abort=failed
                ):
                    raise RuntimeError("direct transport proof failed during fault")
                remaining = scenario.get("hold_seconds", 0) - (time.monotonic() - started)
                if remaining > 0:
                    failed.wait(remaining)
                if failed.is_set():
                    raise RuntimeError("API availability failed; restoring dependency")
            finally:
                if scenario.get("restore") and not command(scenario["restore"]):
                    raise RuntimeError("dependency restoration failed")
            deadline = time.monotonic() + scenario.get("recovery_timeout", 300)
            while not failed.is_set() and time.monotonic() < deadline:
                if command(scenario["ready"], 30):
                    accepted = True
                    break
                failed.wait(10)
            results.append(
                {
                    "scenario": scenario["name"],
                    "recovered": accepted,
                    "elapsed_seconds": round(time.monotonic() - started, 1),
                }
            )
            if not accepted or failed.is_set():
                raise RuntimeError("recovery or uninterrupted availability gate failed")
    finally:
        # Collect the complete local history through the end of the last fault.
        for name, source in plan["health"].items():
            if isinstance(source, dict):
                probe(name, source)
        stop.set()
        for name, thread in zip(plan["health"], threads):
            thread.join(
                timeout=40 if any(isinstance(v, dict) for v in plan["health"].values()) else 10
            )
            if thread.is_alive():
                records[name]["failures"] += 1
                records[name].setdefault("failure_types", {})["monitor_shutdown_timeout"] = 1
                failed.set()
        for record in records.values():
            record.pop("started", None)
            record.pop("last_success", None)
        print(
            json.dumps(
                {
                    "scenarios": results,
                    "apis": records,
                    "availability_pass": not failed.is_set(),
                    "reachability_pass": all(
                        not r["failures"] and not r["restarts"] for r in records.values()
                    ),
                    "transport_observers": observers,
                }
            ),
            flush=True,
        )
    if failed.is_set():
        raise RuntimeError("API availability gate failed")
    return 0


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("plan", nargs="?")
    parser.add_argument("--monitor")
    parser.add_argument("--duration", type=float, default=1800)
    parser.add_argument("--output")
    args = parser.parse_args()
    if args.monitor:
        if not args.output or args.duration <= 0:
            parser.error("local monitor needs --output and a positive duration")
        monitor_local(args.monitor, args.duration, args.output)
        raise SystemExit(0)
    if not args.plan:
        parser.error("a plan or --monitor is required")
    with open(args.plan, encoding="utf-8") as source:
        plan = json.load(source)
    raise SystemExit(run(plan))
