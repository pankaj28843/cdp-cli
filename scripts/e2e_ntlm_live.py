#!/usr/bin/env python3
"""Installed Chromium/CDP synthetic NTLM acceptance, isolated headless state."""
import json
import os
import pathlib
import shutil
import subprocess
import sys
import tempfile
import threading
from e2e_ntlm_fixture import create_server


def main():
    assert shutil.which("ntlm_auth"), "NTLM fixture requires Samba ntlm_auth on PATH"
    root = pathlib.Path(tempfile.mkdtemp(prefix="cdp-ntlm-live-"))
    config = root / "config.json"
    config.write_text(json.dumps({"browser": {"resource_budget": {
        "min_free_memory_mb": 1, "min_free_disk_mb": 1, "max_load_per_cpu": 999999}}}))
    binary = shutil.which(sys.argv[1] if len(sys.argv) > 1 else "cdp")
    assert binary, "cdp executable unavailable"
    identity_result = subprocess.run([binary, "version", "--json"], capture_output=True,
                                     text=True, timeout=15, check=True)
    identity = json.loads(identity_result.stdout)
    expected_commit = subprocess.run(["git", "-C", str(pathlib.Path(__file__).resolve().parent.parent),
                                      "rev-parse", "HEAD"], capture_output=True, text=True,
                                     timeout=15, check=True).stdout.strip()
    assert identity.get("verified") and identity.get("provenance") == "managed", "managed installation required"
    assert identity.get("commit") == expected_commit, "installed commit differs from fixture checkout"
    print(json.dumps({"installed_identity": {key: identity.get(key) for key in
                                            ("commit", "dirty", "verified", "provenance")}}))
    env = dict(os.environ, XDG_CONFIG_HOME=str(root / "config"),
               CDP_TEST_AUTH_USER="FIXTURE\\fixture-user", CDP_TEST_AUTH_PASS="fixture-password")
    prefix = [binary, "--browser-mode", "headless", "--state-dir", str(root / "state"), "--config", str(config)]

    def run(*args, environment=None):
        result = subprocess.run(prefix + list(args) + ["--json"], env=environment or env,
                                capture_output=True, text=True, timeout=60)
        payload = json.loads(result.stdout)
        if result.returncode:
            raise AssertionError(json.dumps({key: payload.get(key) for key in ("code", "class", "message")}))
        return payload

    servers, targets = [], []
    success = False
    try:
        startup = run("daemon", "keepalive", "--repair")
        assert startup.get("state") in ("healthy", "started", "repaired"), "headless startup skipped"
        assert run("daemon", "health")["health"]["state"] == "healthy"
        for case in ("default-basic-deny", "explicit-ntlm-valid", "explicit-ntlm-wrong", "negotiate-plus-ntlm"):
            server = create_server()
            server.offer_negotiate = case == "negotiate-plus-ntlm"
            servers.append(server)
            threading.Thread(target=server.serve_forever, daemon=True).start()
            origin = "http://127.0.0.1:" + str(server.server_port)
            target = run("open", origin + "/")["page"]["id"]
            targets.append(target)
            run("eval", "history.replaceState(null, '', '/protected')", "--target", target)
            credentials = dict(env)
            if case.endswith("wrong"):
                credentials["CDP_TEST_AUTH_PASS"] = "fixture-wrong-password"
            scheme = [] if case.startswith("default") else ["--scheme", "ntlm"]
            report = run("network", "auth", "--origin", origin, "--username-env", "CDP_TEST_AUTH_USER",
                         "--password-env", "CDP_TEST_AUTH_PASS", "--target", target, "--reload",
                         "--duration", "3s", *scheme, environment=credentials)
            assert report["ok"] and report["cleanup_complete"], "auth interception cleanup incomplete"
            counts = dict(server.counts)
            print(json.dumps({"case": case, "counts": counts, "actions": report["actions"]}))
            if case.startswith("default"):
                assert counts["accepted"] == 0 and counts["type1"] == 0 and report["actions"]["provided"] == 0
                assert report["actions"]["canceled"] >= 1
            elif case == "negotiate-plus-ntlm":
                assert counts["accepted"] >= 1 and counts["type1"] >= 1 and counts["type3"] >= 1, "dual-offer NTLM did not authenticate"
                assert counts["domain_matches"] == counts["type3"] and counts["username_matches"] == counts["type3"], "dual-offer identity mismatch"
                print(json.dumps({"dual_offer_authenticated": counts["accepted"] >= 1,
                                  "dual_offer_credential_actions": report["actions"]["provided"]}))
            else:
                assert counts["type1"] >= 1 and counts["type3"] >= 1, "multi-round handshake missing"
                assert counts["domain_matches"] == counts["type3"] and counts["username_matches"] == counts["type3"], "explicit identity mismatch"
                if case.endswith("valid"):
                    assert counts["accepted"] >= 1 and counts["denied"] == 0, "valid NTLM did not reach HTTP200"
                else:
                    assert counts["accepted"] == 0 and 1 <= counts["denied"] <= 2, "wrong NTLM not bounded"
            run("page", "close", "--target", target)
            targets.remove(target)
        success = True
    finally:
        for target in targets:
            subprocess.run(prefix + ["page", "close", "--target", target, "--json"], env=env, capture_output=True, timeout=15)
        stopped = subprocess.run(prefix + ["daemon", "stop", "--force-managed", "--json"], env=env, capture_output=True, timeout=30)
        for server in servers:
            server.shutdown()
            server.server_close()
        if stopped.returncode:
            print(json.dumps({"owned_cleanup_failed": True}), file=sys.stderr)
            if sys.exc_info()[0] is None:
                raise AssertionError("owned headless cleanup failed")
        elif success:
            shutil.rmtree(root)
            print(json.dumps({"ntlm_live": "passed", "owned_cleanup": "passed"}))
        else:
            print(json.dumps({"synthetic_fixture_state_retained": str(root)}), file=sys.stderr)


if __name__ == "__main__":
    main()
