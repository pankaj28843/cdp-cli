# VoxInput transcription API

`cdp transcription` exposes one provider-neutral local service for VoxInput.
The public contract is compatible with the OpenAI Whisper file API and adds
completed-file SSE plus an OpenAI-shaped realtime transcription WebSocket.
The root page is a self-contained human dogfood app at `/demo.html` (also `/`).

The service reads the owner cdp config's `agents.disabled_providers` policy.
Disabled authenticated providers are omitted from ordinary `/healthz` and
`/v1/models` output and are rejected before adapter dispatch with the stable
`provider_disabled`/`disabled_by_config` error. Local providers remain
independent. Use `cdp workflow agent providers --include-disabled --json` for
the explicit diagnostic projection.

## Start the service

The service defaults to the dual-stack wildcard primary listener on
`[::]:28765` and the cleartext companion on `[::]:28766`, without bearer-token
authentication. On Linux, IPv6 must be enabled by the host kernel for the
wildcard to accept IPv6; with the normal `bindv6only=0` setting the same socket
also accepts IPv4. Keep both listeners on a trusted network or behind an
operator-managed access boundary. Configure TLS on the primary listener when
HTTPS access is needed; the HTTP companion remains available for trusted
private clients that cannot install the self-signed certificate.

```bash
cdp transcription serve \
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
  --address '[::]:28765' \
  --http-address '[::]:28766' \
  --default-provider chatgpt-web \
  --tls-cert /path/to/lan-cert.pem \
  --tls-key /path/to/lan-key.pem
```

For a private LAN dogfood session, the embedded demo can run without a token:

```bash
cdp transcription service install \
  --address '[::]:28765' \
  --http-address '[::]:28766' \
  --default-provider chatgpt-web \
  --providers chatgpt-web,microsoft-365-web \
  --fixture-dir /path/to/cdp-cli/testdata/transcription-fixtures \
  --probe-interval 1m \
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

Use `--providers` to persist an explicit provider allowlist in the user
service. For a ChatGPT-only deployment, set `--default-provider chatgpt-web
--providers chatgpt-web`; requests for every other provider are then rejected
by the service boundary even if those adapters are available in the binary.
For a provider-neutral cdp-cli demo or live check, allow both web adapters with
`--providers chatgpt-web,microsoft-365-web` and choose ChatGPT or Microsoft 365
in the rendered provider selector. VoxKey itself remains ChatGPT-only in v1.
The service also persists the selected browser mode and display session so
headed Linux services can use the same authenticated browser owned by cdp.

Native service installation requires the checked-in WebM corpus through
`--fixture-dir`. The corpus must contain exactly 100 validated WebM files. The
installed service selects one fixture from the least-recently-used weighted
pool immediately at startup and then every minute by default. Each
configured provider path is exercised independently: file upload for
`file: true`, and the native paced PCM WebSocket path for `realtime: true`.
`--probe-interval` can be changed for a deployment; setting it to zero uses the
one-minute default rather than disabling the safety gate. Probe evidence is
considered stale after three missed minutes, so the health supervisor can
restart a degraded headed path promptly. One fixture is
selected per cadence and the corpus rotates over time, keeping the recurring
probe bounded while exercising all 100 checked-in inputs.

`GET /healthz` and `GET /v1/models` accept the explicit diagnostic query
`?include_disabled=true`. Ordinary responses omit providers disabled by
`agents.disabled_providers`; diagnostic responses include them with
`ready: false` and `reason: "disabled_by_config"`. The ordinary health status
is calculated from enabled providers only. Diagnostic output is for repair and
inspection, not an admission override.

Desktop browsers generally allow microphone access on `localhost`. Mobile
browsers require a secure origin for a LAN address. For private-LAN dogfooding,
the CLI can generate and reuse a self-signed certificate in one command:

```bash
cdp transcription service install \
  --address '[::]:28765' \
  --http-address '[::]:28766' \
  --default-provider chatgpt-web \
  --providers chatgpt-web,microsoft-365-web \
  --fixture-dir /path/to/cdp-cli/testdata/transcription-fixtures \
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
  --address '[::]:28765' \
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
    {"scheme": "https", "address": "[::]:28765", "tls": true},
    {"scheme": "http", "address": "[::]:28766", "tls": false}
  ],
  "providers": []
}
```

Health is intentionally unauthenticated and contains capability/readiness
metadata only; it never includes bearer tokens or provider session material.
When synthetic probes are enabled, `status: "ok"` means every enabled,
advertised provider path has recently completed a real fixture transcription
successfully. The freshness window is fifteen minutes by default, so startup
and any failed or stale path are reported as `status: "degraded"`; this is not
a cached “service process is alive” signal. Each provider entry exposes the
aggregate `probe_ready`, `last_probe_at`, `probe_age_seconds`, and redacted
`probe_reason`, plus `file_probe` and `realtime_probe` objects when those paths
are advertised. Path status age is measured from the last successful probe, so
a failed retry cannot make an old success appear fresh. A realtime failure can
therefore be diagnosed directly while the file fallback remains visible as
healthy; provider `ready` is conservative and becomes false until every
advertised path recovers.

Probe state is owner-only metadata at `<state-dir>/probe-state.json`. It stores
fixture IDs, timestamps, and redacted result codes only—never fixture audio,
transcript text, request headers, cookies, or tokens.

To use an OpenAI-compatible local ASR server instead:

```bash
cdp transcription serve \
  --default-provider local \
  --local-base-url http://localhost:9000/v1
```

If the local service exposes realtime on a different origin, add
`--local-realtime-base-url`. Both URLs should normally end in `/v1`.

The OpenAPI document is available from the running service and from the CLI:

```bash
curl http://localhost:28766/openapi.json
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
errors. With a fixture corpus enabled, the synthetic probe is the normal hot
path: each bounded probe checks cached auth and refreshes only when the
provider's evidence is actually stale. This avoids a second browser-opening
refresh loop. A separate service-owned coordinator remains available as an
explicit `--auth-refresh-interval` opt-in and defaults to an hourly cadence
when no fixture probe is configured. A request also calls the same provider's
freshness hook before dispatch or the first realtime chunk. ChatGPT and
Microsoft 365 keep their existing provider-specific refresh owner, so an
expired session has an on-demand self-healing path without a provider-specific
cron job. If the browser-free ChatGPT replay is rejected with a typed
authentication/authorization response, the adapter makes one lazy headed-
browser repair/retry; ordinary successful requests never open a browser. The
shared retry policy is bounded at three total attempts with 1-second and
2-second waits. Its 4-second slot is retained for policies with a larger
future attempt budget. A stale-auth observation is therefore repaired and
retried before the request is returned as unavailable.

Audio is copied to a bounded transaction file before dispatch. By default that
file is removed when the request or WebSocket ends; JSON request/result records
remain searchable. Add `--persist-audio` for the explicit durable-cache mode,
where the configured `--max-audio-bytes` budget prunes old audio independently
from records.

## Linux deployment

The service is a normal Go CLI process and does not require macOS APIs. Build
the binary for the target Linux architecture, create the dedicated `cdp`
system account, and install the machine-wide unit with
`cdp transcription service install --system`; the renderer places state under
`/var/lib/cdp-cli` and runs the API and headed daemon as `cdp:cdp`, never as
root. Browser-backed ChatGPT and Microsoft 365 providers also need the
configured headed cdp-cli browser runtime and the provider's existing auth
evidence. The local OpenAI-compatible provider does not require a browser;
browser-backed file adapters may need `ffprobe`/`ffmpeg` when `duration_ms` or
audio decoding cannot be supplied by the caller.

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
