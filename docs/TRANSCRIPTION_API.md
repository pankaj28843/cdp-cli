# VoxInput transcription API

`cdp transcription` exposes one provider-neutral local service for VoxInput.
The public contract is compatible with the OpenAI Whisper file API and adds
completed-file SSE plus an OpenAI-shaped realtime transcription WebSocket.
The root page is a self-contained human dogfood app at `/demo.html` (also `/`).
It remembers the last valid provider used in browser `localStorage`, restores
that provider when the service still advertises it, and otherwise leaves the
request on the configured server default. Audio and transcripts use the
existing demo storage rules; the provider preference is only a small browser
selection value.

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
  --providers chatgpt-web,claude-web,gemini-web,microsoft-365-web,bing-web \
  --fixture-dir '' \
  --auth-refresh-interval 15m \
  --auth-refresh-offset 4m \
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

Native service-manager commands are bounded and run inside one owned process
boundary. Cancellation terminates the owned process group where supported, and
diagnostic overflow is reported as a stable safety-bound error instead of
returning an unbounded manager transcript.

For a reusable, cross-platform recovery check, run
`scripts/chaos_monkey_transcription.sh`. It detects Darwin/LaunchAgent and
Linux system or user `systemd`, reads the manager-owned PID, verifies a real
fixture transcription and `/healthz`, and performs no mutation by default:

```bash
scripts/chaos_monkey_transcription.sh --check
scripts/chaos_monkey_transcription.sh --scenario process-term
scripts/chaos_monkey_transcription.sh --scenario process-kill
scripts/chaos_monkey_transcription.sh --scenario service-restart
scripts/chaos_monkey_transcription.sh --scenario service-down
scripts/chaos_monkey_transcription.sh --scenario state-expired \
  --state-provider microsoft-365-web
```

Each chaos scenario first proves the baseline, then requires manager recovery,
green `/healthz`, and a non-empty real transcription. `all` runs the bounded
matrix sequentially. `state-expired` is for an actual provider host only: it
requires an explicit provider allowlist, backs up the exact auth/capability
JSON for ChatGPT, Claude, Gemini, or Microsoft 365, backdates only
`captured_at`, and proves the service refreshes the selected files;
failed runs restore the backups. The script has bounded polling and an exit
trap for recovery. It removes only its own temporary directory, never service
state, credentials, audio history, or unrelated processes. Use `--health-url`,
`--fixture`, and `--provider` for a tunnel or an external fixture. Run a
user-systemd check as the service account with a live user bus. On Linux, the
process that reads, expires, or refreshes provider state must be the account
that owns that state. A system-scoped manager's stop/start operations require
systemd privilege; keep that narrow manager boundary separate from the
owner-only state operation. Capture key/value output rather than transcript
text when retaining reliability evidence.

Use `--providers` to persist an explicit provider allowlist in the user
service. For a ChatGPT-only deployment, set `--default-provider chatgpt-web
--providers chatgpt-web`; requests for every other provider are then rejected
by the service boundary even if those adapters are available in the binary.
For a provider-neutral cdp-cli service, allow the available web adapters with
`--providers chatgpt-web,claude-web,gemini-web,microsoft-365-web,bing-web`.
The human demo can choose any advertised provider, but deployment evidence must
select each provider explicitly through the file-upload API. Bing
Voice is a direct, unauthenticated Speech WebSocket file adapter; it does not
submit a search. Search-only browser voice controls are outside this
transcription API because they are not standalone STT adapters.
The service also persists the selected browser mode and display session so
headed Linux services can refresh auth/request templates from the same
authenticated browser owned by cdp. The browser is not the transcription
transport.

### Provider file-upload validation

Provider qualification uses the public multipart endpoint, not `/demo.html`.
Run the installed gate with valid cached provider auth/request templates:

```bash
make e2e-transcription-live-installed
```

The gate starts a transient service and sends the checked-in synthetic WebM to
`/v1/audio/transcriptions` five times in sequence, with an explicit `provider`
field for `chatgpt-web`, `claude-web`, `gemini-web`, `microsoft-365-web`, and
`bing-web`. It requires non-empty text plus fixture-specific semantic markers.
Evidence contains only the provider ID, marker result, and text length; it does
not retain or print transcript text. Remote deployment smoke must use the same
multipart request against the deployed API and must select every enabled
provider explicitly and sequentially. Recording through the demo app proves
the demo, not the provider adapter, and is not a substitute.

Local probe subprocesses are bounded and owned: decoded realtime PCM is capped
at 16 MiB, ffprobe stdout and stderr are each capped at 64 KiB, and cancellation
terminates the exact subprocess group where the platform supports process
groups. Oversized decoder/diagnostic output becomes a stable probe error; PCM,
ffprobe output, and transcript text are never included in health evidence.

The browser-backed Bing, Claude, Gemini, and Microsoft 365 adapters apply the
same owned-process boundary to their local ffmpeg conversion step. Their
existing PCM/WebM output bounds and provider-specific replay/error behavior are
unchanged; cancellation terminates only the conversion process group where the
platform supports process groups. Converter output and diagnostics are not
returned as API evidence.

Authenticated providers derive the minimum owner-only request template from the
existing signed-in browser session during bounded discovery and auth refresh.
Discovery captures the provider's dictation transaction unredacted and keeps it
ephemeral; logs, evidence, and committed documentation remain credential-free.
Each completed upload then goes directly to that HTTP or WebSocket transport. A
serving request opens no tab, drives no dictation UI, and uses no browser or host
microphone. See `SANITIZATION.md`.

Set `--fixture-dir ''` to disable scheduled synthetic audio. This leaves
ordinary requests and the auth/capability coordinator active without sending
recurring transcriptions to provider accounts. Use an explicit smoke or chaos
command when a real-audio deployment or debugging check is required.

When `--fixture-dir` names the checked-in WebM corpus, it must contain exactly
100 validated files. The installed service selects one fixture from the
least-recently-used weighted pool immediately at startup and then every minute
by default. Each configured provider path is exercised independently: file
upload for `file: true`, and the native paced PCM WebSocket path for
`realtime: true`.
A synthetic probe uses cached provider capability/auth evidence and never
invokes a provider's auth or capability refresh hook. It exercises the same
direct provider adapter as an ordinary upload and opens no browser target. The
installed service's local auth/capability schedule is enabled by default and
runs the providers sequentially on the shared `--auth-refresh-interval`
cadence. A standalone installation should keep local mode enabled so expired
provider state is repaired before it breaks the next turn. A fleet may instead
designate one service as the browser authority and run leaf services with
`--auth-refresh-mode external --auth-refresh-interval 0s`. An external leaf
cannot launch browser repair: when configured, it invokes the absolute
`--external-auth-refresh-command` as `COMMAND refresh PROVIDER`, allowing an
operator-owned helper to request authority repair and synchronize state.
The default cadence is ten minutes, which stays ahead of Microsoft 365's
45-minute auth-evidence TTL and its 15-minute proactive refresh margin.
`--auth-refresh-offset` phases recurring refreshes against the wall clock and
must be shorter than the interval. Startup refresh remains immediate.
Independent local authorities should use distinct offsets. Externally managed
leaves have no offset because they do not perform provider-facing refresh. An
authority may explicitly enable `--auth-refresh-api`; the
`POST /v1/provider-auth/refresh` endpoint then accepts only a provider ID,
coalesces requests across providers, and suppresses browser work while either
the provider state or the last refresh attempt is newer than
`--auth-refresh-request-min-age` (45 minutes by default).
The first live probe waits for the first lifecycle pass to finish. Every later
probe performs the same cheap auth/capability preflight before exercising the
provider's real file or realtime transcription path; an auth rejection can
trigger one bounded provider-owned repair before the probe is marked failed.
This keeps the service warm without putting browser or provider-specific logic
in the HTTP health handler.
`--probe-interval` can be changed for a deployment; setting it to zero uses the
one-minute default rather than disabling the safety gate. Probe evidence is
considered stale after three missed minutes, so the health supervisor can
detect a degraded headed path promptly for explicit repair. One fixture is
selected per cadence and the corpus rotates over time, keeping the recurring
probe bounded while exercising all 100 checked-in inputs.
If an explicit Microsoft 365 refresh targets an account/runtime that does not
expose voice input, it returns `m365_voice_input_unavailable` with
`retry_safe: false` after exact target cleanup; callers should repair account
eligibility or runtime configuration rather than retrying on a timer.
The refresh path is account-agnostic: it first reads the live app-owned
`tokenProviders.augloop` provider and proves a direct AugLoop VoiceTile
WebSocket session, so a visible microphone button is not required. The same
path supports signed-in Microsoft 365 Personal and Work sessions; account
eligibility remains controlled by Microsoft. A legacy visible-control probe is
retained only for runtimes that do not expose the provider.

`GET /healthz` and `GET /v1/models` accept the explicit diagnostic query
`?include_disabled=true`. Ordinary responses omit providers disabled by
`agents.disabled_providers`; diagnostic responses include them with
`ready: false` and `reason: "disabled_by_config"`. The ordinary health status
is calculated from enabled providers only. Diagnostic output is for repair and
inspection, not an admission override.

Completed-file requests have a twelve-minute transport budget, covering the
advertised ten-minute audio bound plus provider setup and final-result
settling. This is intentional for Microsoft 365 WebM replay: its saved-file
adapter sends audio tiles at capture rate, so a one- or two-minute recording
must not be cut off by a two-minute HTTP listener deadline.

Desktop browsers generally allow microphone access on `localhost`. Mobile
browsers require a secure origin for a LAN address. For private-LAN dogfooding,
the CLI can generate and reuse a self-signed certificate in one command:

```bash
cdp transcription service install \
  --address '[::]:28765' \
  --http-address '[::]:28766' \
  --default-provider chatgpt-web \
  --providers chatgpt-web,claude-web,gemini-web,microsoft-365-web,bing-web \
  --fixture-dir '' \
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

### Mostly-silent audio

The file boundary has one provider-neutral content result for a known empty
turn. For the canonical mono 16-bit PCM WAV emitted by VoxInput, cdp-cli uses
30 ms energy windows, an adaptive low-percentile noise floor, and a
conservative signal floor. It is a near-silence preflight, not a speech
segmenter: a clear frame keeps the provider path open, and unsupported or
malformed formats fail open to the provider adapter.

When the preflight classifies the upload as mostly silent, the service does not
run auth repair or call the selected provider. It records a failed request and
returns the same response for every provider:

```json
{
  "error": {
    "type": "audio_content_error",
    "code": "mostly_silence",
    "message": "audio is mostly silence; record again and retry"
  }
}
```

The HTTP status is `422`. Clients should show a standard “Mostly silence
detected. Record again and retry.” recovery message, retain only their normal
bounded failed-audio copy, and avoid routing the same deterministic silence to
every provider. The cdp-cli service remains the second boundary for clients
that do not run the local preflight; provider-specific failures that happen
after dispatch remain provider failures.

## Debugging a live session

Every service instance writes a bounded, owner-only metadata trace next to its
request records at `~/.cdp-cli/transcription/trace.jsonl` (or
`<state-dir>/transcription/trace.jsonl`). It records request ID, provider,
phase, audio byte/chunk counts, attempts, duration, and typed error
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
successfully. The freshness window is three minutes by default, so startup
and any failed or stale path are reported as `status: "degraded"`; this is not
a cached “service process is alive” signal. A probe that is currently running
does not invalidate a still-fresh last success, so normal one-minute probing
does not create a false outage. Each provider entry exposes the
aggregate `probe_ready`, `last_probe_at`, `probe_age_seconds`, and typed
`probe_reason`, plus `file_probe` and `realtime_probe` objects when those paths
are advertised. Path status age is measured from the last successful probe, so
a failed retry cannot make an old success appear fresh. A realtime failure can
therefore be diagnosed directly while the file fallback remains visible as
healthy; provider `ready` is conservative and becomes false until every
advertised path recovers.

Probe state is owner-only metadata at `<state-dir>/probe-state.json`. It stores
fixture IDs, timestamps, and typed result codes only—never fixture audio,
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
- `chatgpt-web`: direct multipart replay using browser-refreshed request/auth
  evidence.
- `claude-web`: authenticated Claude dictation WebSocket replay using paced
  16 kHz mono linear PCM, `CloseStream`, `TranscriptText`, and
  `TranscriptEndpoint`; it creates no chat or conversation.
- `gemini-web`: direct completed-file replay using Gemini's minimum owner-only
  browser-derived request/auth template. The service accepts the API's bounded
  WAV/MP3/M4A/WebM compatibility inputs; non-WebM input is normalized to
  WebM/Opus with the installed `ffmpeg` binary before the Gemini replay.
- `microsoft-365-web`: direct Microsoft 365/AugLoop WebSocket transport using
  browser-refreshed auth evidence.
- `bing-web`: direct public Bing Speech SDK WebSocket transcription. It
  accepts completed audio files, decodes them to paced 16 kHz mono PCM, and
  does not require a Bing account session. It does not expose translation or
  the cdp realtime session contract.

Search-only browser voice controls are not transcription providers. Keeping
those controls in their respective search workflows does not make them
standalone STT adapters.

Provider adapters are the effect boundary. The API core owns persistence,
request validation, WebSocket framing, event reduction, and provider-neutral
errors. With a fixture corpus enabled for explicit development or debugging,
each bounded probe checks cached capability evidence and exercises the direct
provider file or realtime adapter. It does not call a provider refresh hook or
open a browser target. With an explicitly empty fixture directory, no synthetic
transcription is scheduled. A separate
service-owned coordinator is enabled by default whenever
`--auth-refresh-interval` is positive. A zero interval is valid for a transient
service or an explicitly external auth consumer; external mode rejects a
positive local schedule. Native service installation persists the mode and
derived setting, so a stale legacy environment flag cannot silently re-enable
lifecycle repair. The first probe is ordered after the coordinator's
initial auth/capability pass, and capabilities are refreshed only after that
provider's auth refresh succeeds. A request also calls the same provider's
freshness hook before dispatch or the first realtime chunk. In local mode,
authenticated providers keep provider-specific browser refresh owners. In
external mode, the same hook delegates to the configured authority helper and
cannot select the local headed runtime. Provider lifecycle work is serialized
per provider with request cancellation. If a direct replay receives a typed
auth rejection, the adapter runs one single-flight auth repair and then retries
the direct transport once: local mode refreshes its browser template, while
external mode invokes the authority helper and reloads shared state. It never
transcribes through provider UI.
The shared retry policy is bounded at three total attempts with 1-second and
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
`/var/lib/cdp-cli` and runs the API as `cdp:cdp`, never as root. A local auth
owner separately supplies its headed cdp-cli runtime for bounded
auth/request-template refresh. An external auth consumer instead uses a zero
refresh interval and an authority helper and requires no local browser daemon
or endpoint. Authenticated-provider transcription hot paths remain direct. The local
OpenAI-compatible provider does not require a browser; file adapters may need
`ffprobe`/`ffmpeg` when
`duration_ms`, audio decoding, or Gemini WebM/Opus normalization cannot be
supplied by the caller.

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
