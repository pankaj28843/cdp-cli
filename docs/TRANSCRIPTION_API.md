# VoxInput transcription API

`cdp transcription` exposes one provider-neutral local service for VoxInput.
The public contract is compatible with the OpenAI Whisper file API and adds
completed-file SSE plus an OpenAI-shaped realtime transcription WebSocket.
The root page is a self-contained human dogfood app at `/demo.html` (also `/`).

## Start the service

The safe default is loopback. Set a token whenever another process or host can
reach the listener.

```bash
cdp transcription serve \
  --token local-development-token \
  --default-provider chatgpt-web \
  --print-ready
```

The primary listener is the existing transport. `--http-address` is an
explicit second, cleartext listener for private clients such as a shell on a
trusted Tailscale/LAN network; it uses the same API, health contract, and
provider selection as the primary listener. Keep the primary listener on
HTTPS for browser microphone access. HTTP is not a browser microphone
permission workaround: mobile browsers still need an HTTPS secure origin.

```bash
cdp transcription serve \
  --address 0.0.0.0:28765 \
  --http-address 0.0.0.0:28766 \
  --token local-development-token \
  --default-provider chatgpt-web \
  --tls-cert /path/to/lan-cert.pem \
  --tls-key /path/to/lan-key.pem
```

For a private LAN dogfood session, the embedded demo can run without a token:

```bash
cdp transcription service install \
  --address 0.0.0.0:28765 \
  --http-address 0.0.0.0:28766 \
  --default-provider chatgpt-web \
  --tls-self-signed \
  --tls-host <this-machine-LAN-IP> \
  --tls-host localhost
```

Open the reported `https://<this-machine-LAN-IP>:28765/demo.html` URL from a
desktop or mobile browser and allow microphone access. The service is
supervised by a macOS user-level LaunchAgent or a Linux `systemd --user` unit.
Inspect or control it with:

```bash
cdp transcription service status
cdp transcription service restart
cdp transcription service stop
```

Desktop browsers generally allow microphone access on `localhost`. Mobile
browsers require a secure origin for a LAN address. For private-LAN dogfooding,
the CLI can generate and reuse a self-signed certificate in one command:

```bash
cdp transcription service install \
  --address 0.0.0.0:28765 \
  --http-address 0.0.0.0:28766 \
  --default-provider chatgpt-web \
  --tls-self-signed \
  --tls-host 192.168.5.249 \
  --tls-host localhost
```

It stores the certificate and owner-only private key under
`~/.cdp-cli/tls/`, restarts the user service, and reports the HTTPS demo URLs.
Replace `192.168.5.249` with the Mac's current LAN address. If the address
changes, rerun the command with the new `--tls-host`; use `--tls-regenerate`
only when the existing certificate needs to be replaced. On the first visit,
Safari or Chrome may show a self-signed-certificate warning. Continue through
that warning for private testing, or install the certificate on the phone and
enable its trust if the browser does not permit microphone access after the
exception.

For a certificate issued by a local CA or another trusted authority, provide
the certificate and key explicitly instead:

```bash
cdp transcription service install \
  --address 0.0.0.0:28765 \
  --tls-cert /path/to/lan-cert.pem \
  --tls-key /path/to/lan-key.pem
```

The demo automatically switches its WebSocket transport to `wss://` when it is
opened over HTTPS.

For a self-signed certificate, command-line probes need certificate verification
disabled explicitly, for example:

```bash
curl -k https://192.168.5.249:28765/healthz
```

The API retains JSON request/result records but uses ephemeral transaction
media by default. Use `--persist-audio` only when the API itself must retain
audio for a later retry; VoxInput may still keep its own local retry copy.

## Debugging a live session

Every service instance writes a bounded, owner-only metadata trace next to its
request records at `~/.cdp-cli/transcription/trace.jsonl` (or
`<state-dir>/transcription/trace.jsonl`). It records request ID, provider,
phase, audio byte/chunk counts, attempts, duration, and sanitized error
metadata. It never records audio, transcript text, cookies, or bearer tokens.
The active file rotates once it reaches 8 MiB; the previous file is kept as
`trace.jsonl.previous`.

```bash
tail -f ~/.cdp-cli/transcription/trace.jsonl
find ~/.cdp-cli/transcription/requests -name record.json -print
# Linux user service
journalctl --user -u cdp-transcription.service -f
```

Use the same trace file and `launchctl`/service log path on macOS. The
`/healthz` response advertises `observability.request_records` and
`observability.trace_file` without exposing the local filesystem path.

The service reports the selected transport and every configured listener from
`GET /healthz`, so consumers can diagnose a wrong scheme or port without
guessing:

```json
{
  "status": "ok",
  "contract_version": "voxinput-transcription/v1",
  "transport": "http",
  "default_provider": "chatgpt-web",
  "listeners": [
    {"scheme": "https", "address": "0.0.0.0:28765", "tls": true},
    {"scheme": "http", "address": "0.0.0.0:28766", "tls": false}
  ],
  "providers": []
}
```

Health is intentionally unauthenticated and contains capability/readiness
metadata only; it never includes bearer tokens or provider session material.

To use an OpenAI-compatible local ASR server instead:

```bash
cdp transcription serve \
  --token local-development-token \
  --default-provider local \
  --local-base-url http://localhost:9000/v1
```

If the local service exposes realtime on a different origin, add
`--local-realtime-base-url`. Both URLs should normally end in `/v1`.

The OpenAPI document is available from the running service and from the CLI:

```bash
curl -H 'Authorization: Bearer local-development-token' \
  http://localhost:28765/openapi.json
cdp transcription spec > openapi.json
```

## File API

The service implements:

- `POST /v1/audio/transcriptions`
- `POST /v1/audio/translations` (currently `whisper-1` only)
- `GET /v1/models`
- `GET /healthz`

Supported file extensions are `mp3`, `mp4`, `mpeg`, `mpga`, `m4a`, `wav`, and
`webm`. The compatibility limit is 25 MiB per upload and ten minutes per
recording. `duration_ms` is an optional VoxInput field used by browser-backed
providers when the capture layer already knows the duration.

The standard OpenAI client can point at the local service by changing only its
base URL and API key:

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://localhost:28766/v1",
    api_key="local-development-token",
)
with open("speech.webm", "rb") as audio:
    result = client.audio.transcriptions.create(
        model="whisper-1",
        file=audio,
    )
print(result.text)
```

`response_format` supports `json`, `text`, `verbose_json`, `srt`, and `vtt`.
`stream=true` returns completed-file SSE events named
`transcript.text.delta` and `transcript.text.done`; it does not pretend that a
whole-file provider produced token-level partials.

The demo keeps the primary journey deliberately small: choose a provider, press
**Start talking**, then stop to finalize. Whole-file providers show one final
transcript after the upload; realtime providers show a short live preview while
bounded PCM chunks are sent and then replace it with the committed result. A
collapsed **Advanced API controls** section exposes multipart
transcription/translation, SSE, OpenAPI, and realtime controls for testing the
full contract. The browser keeps recent results in local storage; the server
does not need to retain uploaded media.

## Realtime API

The WebSocket endpoint is `GET /v1/realtime`. OpenAI-shaped clients may use
`?model=whisper-1`; VoxInput clients may use `?intent=transcription`.
Clients send:

1. `session.update` with a transcription session and optional language/prompt.
2. `input_audio_buffer.append` with base64 mono signed 16-bit PCM at 24 kHz.
   Keep each chunk at or below 1 MiB.
3. `input_audio_buffer.commit` to request the final transcript.

The server emits `session.created`, `input_audio_buffer.committed`, normalized
transcription deltas, a completed event, or a structured `error` event. Every
audio chunk is durably appended before provider dispatch, so a disconnect
leaves a retryable PCM file and request record.

## Providers and resilience

The registry currently supports:

- `local`: any configured OpenAI-compatible HTTP/WebSocket service.
- `chatgpt-web`: the existing cdp-cli ChatGPT workflow and auth evidence.
- `microsoft-365-web`: the existing cdp-cli Microsoft 365/AugLoop workflow.

Provider adapters are the effect boundary. The API core owns persistence,
request validation, WebSocket framing, event reduction, and provider-neutral
errors. A single service-owned auth coordinator checks every online provider
proactively at startup and every ten minutes (configurable with
`--auth-refresh-interval`), in parallel and with independent time bounds. A
request also calls the same provider's freshness hook before dispatch or the
first realtime chunk. ChatGPT and Microsoft 365 keep their existing
provider-specific refresh owner, so an expired session has a self-healing path
without a provider-specific cron job. The shared retry policy is bounded at
three total attempts with 1-second and 2-second waits; its 4-second slot is
retained for policies with a larger future attempt budget.

Audio is copied to a bounded transaction file before dispatch. By default that
file is removed when the request or WebSocket ends; JSON request/result records
remain searchable. Add `--persist-audio` for the explicit durable-cache mode,
where the configured `--max-audio-bytes` budget prunes old audio independently
from records.

## Linux deployment

The service is a normal Go CLI process and does not require macOS APIs. Build
the binary for the target Linux architecture, configure the cdp-cli state
directory and provider runtime, and run `cdp transcription serve` under the
host’s service manager. Browser-backed ChatGPT and Microsoft 365 providers
also need the configured cdp-cli browser runtime and the provider’s existing
auth evidence. The local OpenAI-compatible provider does not require a
browser; browser-backed file adapters may need `ffprobe`/`ffmpeg` when
`duration_ms` or audio decoding cannot be supplied by the caller.

## Compatibility gate

The repository runs a real external-client check rather than only testing its
own handlers:

```bash
make e2e-openai-compat
```

The gate starts synthetic HTTP and WebSocket upstreams, launches the local
service, and pins a maintained `openai-python` revision to exercise multipart
transcription, translation, text output, SSE, and realtime WebSocket behavior.
To pass an existing local MP3, WAV, or WebM through the same external-client
boundary without committing it, set `VOX_E2E_AUDIO` to that file path before
running `scripts/e2e_openai_compat.sh`.
