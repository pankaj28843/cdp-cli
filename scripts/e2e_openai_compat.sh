#!/usr/bin/env bash
set -euo pipefail

binary="${1:-./bin/cdp}"
if [[ ! -x "$binary" ]]; then
  echo "missing executable: $binary" >&2
  exit 2
fi
if ! command -v uv >/dev/null 2>&1; then
  echo "uv is required for the pinned openai-python compatibility gate" >&2
  exit 2
fi
if ! command -v curl >/dev/null 2>&1; then
  echo "curl is required for the local readiness probe" >&2
  exit 2
fi

state_dir="$(mktemp -d)"
fixture_log="$state_dir/fixture.log"
server_log="$state_dir/server.log"
fixture_pid=""
server_pid=""

cleanup() {
  status=$?
  if [[ "$status" -ne 0 ]]; then
    echo "OpenAI compatibility fixture log:" >&2
    sed -n '1,160p' "$fixture_log" >&2 || true
    echo "OpenAI compatibility server log:" >&2
    sed -n '1,160p' "$server_log" >&2 || true
  fi
  if [[ -n "$server_pid" ]]; then
    kill "$server_pid" >/dev/null 2>&1 || true
    wait "$server_pid" >/dev/null 2>&1 || true
  fi
  if [[ -n "$fixture_pid" ]]; then
    kill "$fixture_pid" >/dev/null 2>&1 || true
    wait "$fixture_pid" >/dev/null 2>&1 || true
  fi
  rm -rf "$state_dir"
  return "$status"
}
trap cleanup EXIT

read -r fixture_http_port fixture_ws_port api_port < <(python3 - <<'PY'
import socket

ports = []
for _ in range(3):
    sock = socket.socket()
    sock.bind(("127.0.0.1", 0))
    ports.append(sock.getsockname()[1])
    sock.close()
print(*ports)
PY
)

uv run --quiet --with websockets python3 - "$fixture_http_port" "$fixture_ws_port" >"$fixture_log" 2>&1 <<'PY' &
import asyncio
import json
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import websockets

http_port = int(sys.argv[1])
ws_port = int(sys.argv[2])

class HTTPHandler(BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(length)
        stream = b'name="stream"' in body and b"true" in body.lower()
        response_format = b'name="response_format"' in body and b"text" in body
        if stream:
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Cache-Control", "no-cache")
            self.end_headers()
            for delta in ("fixture ", "stream transcript"):
                payload = json.dumps({"type": "transcript.text.delta", "delta": delta})
                self.wfile.write(f"event: transcript.text.delta\ndata: {payload}\n\n".encode())
            payload = json.dumps({"type": "transcript.text.done", "text": "fixture stream transcript"})
            self.wfile.write(f"event: transcript.text.done\ndata: {payload}\n\n".encode())
            return
        if response_format:
            self.send_response(200)
            self.send_header("Content-Type", "text/plain")
            self.end_headers()
            self.wfile.write(b"fixture text transcript")
            return
        text = "fixture translation" if self.path.endswith("/translations") else "fixture transcript"
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps({"text": text}).encode())

    def log_message(self, *_):
        return

async def realtime_handler(websocket):
    async for raw in websocket:
        event = json.loads(raw)
        print(f"realtime upstream event: {event.get('type')}", file=sys.stderr, flush=True)
        if event.get("type") == "session.update":
            await websocket.send(json.dumps({"type": "session.updated"}))
        elif event.get("type") == "input_audio_buffer.append":
            await websocket.send(json.dumps({
                "type": "conversation.item.input_audio_transcription.delta",
                "item_id": "item-0",
                "delta": "fixture ",
            }))
        elif event.get("type") == "input_audio_buffer.commit":
            await websocket.send(json.dumps({
                "type": "conversation.item.input_audio_transcription.completed",
                "item_id": "item-0",
                "transcript": "fixture realtime transcript",
            }))
            return

async def main():
    http_server = ThreadingHTTPServer(("127.0.0.1", http_port), HTTPHandler)
    threading.Thread(target=http_server.serve_forever, daemon=True).start()
    async with websockets.serve(realtime_handler, "127.0.0.1", ws_port):
        await asyncio.Future()

asyncio.run(main())
PY
fixture_pid=$!

for _ in $(seq 1 60); do
  if curl -sS "http://127.0.0.1:${fixture_http_port}/healthz" >/dev/null 2>&1; then
    break
  fi
  sleep 0.1
done

"$binary" --state-dir "$state_dir/cdp" transcription serve \
  --address "127.0.0.1:${api_port}" \
  --default-provider local \
  --local-base-url "http://127.0.0.1:${fixture_http_port}/v1" \
  --local-realtime-base-url "http://127.0.0.1:${fixture_ws_port}/v1" \
  --local-api-key upstream-token \
  --max-audio-bytes 10485760 \
  --print-ready >"$server_log" 2>&1 &
server_pid=$!

for _ in $(seq 1 60); do
  if curl -fsS "http://127.0.0.1:${api_port}/healthz" >/dev/null 2>&1; then
    break
  fi
  if ! kill -0 "$server_pid" >/dev/null 2>&1; then
    cat "$server_log" >&2
    exit 1
  fi
  sleep 0.1
done

export VOX_API_BASE_URL="http://127.0.0.1:${api_port}/v1"
export VOX_API_KEY="unused-no-bearer-key"

uv run --quiet --with httpx --with 'openai[realtime] @ git+https://github.com/openai/openai-python.git@753ab5c1a81cd85e8bf0aef4c04c51a2e8dae6cd' python3 - <<'PY'
import asyncio
import base64
import os
from pathlib import Path

import httpx
from openai import AsyncOpenAI, OpenAI

base_url = os.environ["VOX_API_BASE_URL"]
api_key = os.environ["VOX_API_KEY"]
client = OpenAI(base_url=base_url, api_key=api_key)

fixture_path = os.environ.get("VOX_E2E_AUDIO")
if fixture_path:
    path = Path(fixture_path)
    suffix = path.suffix.lower()
    mime_types = {
        ".m4a": "audio/m4a",
        ".mp3": "audio/mpeg",
        ".mp4": "audio/mp4",
        ".mpeg": "audio/mpeg",
        ".mpga": "audio/mpeg",
        ".wav": "audio/wav",
        ".webm": "audio/webm",
    }
    fixture = (path.name, path.read_bytes(), mime_types.get(suffix, "application/octet-stream"))
    if len(fixture[1]) == 0 or len(fixture[1]) > 25 * 1024 * 1024:
        raise AssertionError(f"fixture must be non-empty and at most 25 MB: {path}")
else:
    fixture = ("fixture.mp3", b"synthetic mp3 bytes", "audio/mpeg")
transcription = client.audio.transcriptions.create(model="whisper-1", file=fixture)
assert transcription.text == "fixture transcript", transcription

translation = client.audio.translations.create(model="whisper-1", file=fixture)
assert translation.text == "fixture translation", translation

plain = client.audio.transcriptions.create(model="whisper-1", file=fixture, response_format="text")
assert str(plain) == "fixture text transcript", plain

with httpx.stream(
    "POST",
    base_url + "/audio/transcriptions",
    files={"file": ("fixture.webm", b"synthetic webm bytes", "audio/webm")},
    data={"model": "whisper-1", "stream": "true"},
) as response:
    response.raise_for_status()
    stream_body = response.read().decode()
assert "transcript.text.delta" in stream_body, stream_body
assert "transcript.text.done" in stream_body, stream_body

async def realtime_check():
    async_client = AsyncOpenAI(base_url=base_url, api_key=api_key)
    async with async_client.realtime.connect(model="whisper-1") as connection:
        await connection.session.update(session={
            "type": "transcription",
            "audio": {"input": {"format": {"type": "audio/pcm", "rate": 24000}}},
        })
        await connection.input_audio_buffer.append(audio=base64.b64encode(b"pcmbytes").decode())
        await connection.input_audio_buffer.commit()
        event_types = []
        for _ in range(8):
            try:
                event = await asyncio.wait_for(connection.recv(), timeout=5)
            except asyncio.TimeoutError as exc:
                raise AssertionError(f"timed out waiting for realtime event; received={event_types}") from exc
            description = event.type
            if event.type == "error":
                description += f": {event.error}"
            event_types.append(description)
            if event.type == "conversation.item.input_audio_transcription.completed":
                break
        assert "conversation.item.input_audio_transcription.completed" in event_types, event_types

asyncio.run(realtime_check())
print("openai-python compatibility gate passed: multipart, translation, text, SSE, realtime WebSocket")
PY
