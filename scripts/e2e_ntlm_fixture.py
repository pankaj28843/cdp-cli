#!/usr/bin/env python3
"""Loopback-only synthetic NTLM fixture. Never use real account credentials."""
import base64
import http.client
import http.server
import json
import select
import shutil
import struct
import subprocess
import threading

USER = "fixture-user"
DOMAIN = "FIXTURE"
PASSWORD = "fixture-password"


def helper(protocol, password=PASSWORD):
    return subprocess.Popen(
        [shutil.which("ntlm_auth"), "--helper-protocol=" + protocol,
         "--username=" + USER, "--domain=" + DOMAIN, "--password=" + password],
        stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
        text=True, bufsize=1)


def exchange(process, message):
    process.stdin.write(message + "\n")
    process.stdin.flush()
    if not select.select([process.stdout], [], [], 3)[0]:
        raise RuntimeError("synthetic NTLM helper deadline")
    result = process.stdout.readline().strip()
    return result.partition(" ")[0], result.partition(" ")[2]


def stop(process):
    if process:
        process.terminate()
        process.wait(timeout=3)


class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def setup(self):
        super().setup()
        self.connection.settimeout(8)
        self.ntlm = None
        self.authenticated = False

    def finish(self):
        stop(self.ntlm)
        super().finish()

    def log_message(self, *_):
        pass

    def reply(self, status, challenge=None):
        body = b"synthetic NTLM fixture"
        self.send_response(status)
        self.send_header("Content-Length", str(len(body)))
        if challenge:
            if self.server.offer_negotiate and challenge == "NTLM":
                self.send_header("WWW-Authenticate", "Negotiate")
            self.send_header("WWW-Authenticate", challenge)
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path in ("/", "/favicon.ico"):
            return self.reply(200)
        if self.authenticated:
            self.server.counts["accepted"] += 1
            return self.reply(200)
        authorization = self.headers.get("Authorization", "")
        if authorization.startswith("Negotiate "):
            self.server.counts["negotiate_requests"] += 1
        if not authorization.startswith("NTLM "):
            return self.reply(401, "NTLM")
        token = authorization[5:]
        try:
            raw = base64.b64decode(token, validate=True)
            if raw[:8] != b"NTLMSSP\0":
                raise ValueError()
            message_type = struct.unpack_from("<I", raw, 8)[0]
            if message_type == 1:
                stop(self.ntlm)
                self.ntlm = helper("squid-2.5-ntlmssp")
                status, response = exchange(self.ntlm, "YR " + token)
                if status != "TT":
                    raise ValueError()
                self.server.counts["type1"] += 1
                return self.reply(401, "NTLM " + response)
            if message_type != 3 or self.ntlm is None:
                raise ValueError()

            def field(offset):
                length, _, position = struct.unpack_from("<HHI", raw, offset)
                if position + length > len(raw):
                    raise ValueError()
                return raw[position:position + length].decode("utf-16le")

            domain_matches = field(28).casefold() == DOMAIN.casefold()
            username_matches = field(36).casefold() == USER.casefold()
            self.server.counts["type3"] += 1
            self.server.counts["domain_matches"] += domain_matches
            self.server.counts["username_matches"] += username_matches
            status, _ = exchange(self.ntlm, "KK " + token)
            self.authenticated = status == "AF" and username_matches and domain_matches
            self.server.counts["accepted" if self.authenticated else "denied"] += 1
            return self.reply(200 if self.authenticated else 401,
                              None if self.authenticated else "NTLM")
        except (ValueError, struct.error, UnicodeError):
            self.server.counts["denied"] += 1
            return self.reply(401, "NTLM")


def create_server():
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    server.daemon_threads = True
    server.offer_negotiate = False
    server.counts = dict.fromkeys(("accepted", "denied", "type1", "type3", "domain_matches", "username_matches", "negotiate_requests"), 0)
    return server


def selftest():
    server = create_server()
    threading.Thread(target=server.serve_forever, daemon=True).start()
    try:
        for case, password, expected in (("valid", PASSWORD, 200), ("wrong", "fixture-wrong-password", 401)):
            client = helper("ntlmssp-client-1", password)
            connection = http.client.HTTPConnection("127.0.0.1", server.server_port, timeout=5)
            try:
                connection.request("GET", "/protected")
                response = connection.getresponse()
                assert response.status == 401
                response.read()
                _, token = exchange(client, "YR")
                connection.request("GET", "/protected", headers={"Authorization": "NTLM " + token})
                response = connection.getresponse()
                challenge = response.getheader("WWW-Authenticate")
                assert response.status == 401 and challenge.startswith("NTLM ")
                response.read()
                _, token = exchange(client, "TT " + challenge[5:])
                connection.request("GET", "/protected", headers={"Authorization": "NTLM " + token})
                response = connection.getresponse()
                assert response.status == expected, "synthetic NTLM result mismatch"
                response.read()
                print(json.dumps({"case": case, "http_status": response.status, "passed": True}))
            finally:
                connection.close()
                stop(client)
        assert server.counts["accepted"] == 1 and server.counts["denied"] == 1
        assert server.counts["domain_matches"] == 2 and server.counts["username_matches"] == 2
        print(json.dumps({"synthetic_ntlm_selftest": "passed", **server.counts}))
    finally:
        server.shutdown()
        server.server_close()


if __name__ == "__main__":
    selftest()
