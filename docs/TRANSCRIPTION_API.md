# VoxInput transcription API

`cdp transcription` exposes one provider-neutral local service for VoxInput.
The public contract is compatible with the OpenAI Whisper file API and adds
completed-file SSE plus an OpenAI-shaped realtime transcription WebSocket.

## Start the service

The safe default is loopback. Set a token whenever another process or host can
reach the listener.

```bash
cdp transcription serve \
  --token local-development-token \
  --default-provider chatgpt-web \
  --print-ready
```

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
  http://localhost:8765/openapi.json
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
    base_url="http://localhost:8765/v1",
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

Audio is persisted before dispatch under the cdp-cli state directory. The
configured `--max-audio-bytes` budget prunes old audio while retaining JSON
request/result records. Failed requests therefore remain searchable and can
be retried by a caller that still has the recorded audio path.

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
