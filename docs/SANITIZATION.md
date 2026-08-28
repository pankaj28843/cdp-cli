# Transcription Discovery and Sanitization

This document is the source of truth for learning an authenticated web
provider's audio contract without turning its website into the production
transcription engine.

## Boundary

Browser/CDP has two bounded jobs:

1. During development or explicit deployment debugging, observe how the
   existing signed-in session authenticates and sends provider audio.
2. During scheduled or on-demand auth refresh, derive the minimum request
   template needed by the direct provider transport.

Normal transcription is browser-free. The service substitutes each newly
uploaded audio file into the cached provider request shape and sends the
provider-native HTTP or WebSocket transaction directly. It must not open a tab,
click dictation controls, use `getUserMedia`, record a website, or fall back to
UI transcription.

## Discovery Procedure

Use the existing signed-in browser session and the repository's CDP runtime.
Narrow the capture to the provider's own dictation or microphone operation.
When audio is needed to discover the transport, activate the provider's native
record control (press-and-hold or start, according to that UI), wait about two
seconds, play a known phrase through the Mac speakers with `say`, wait about two
seconds again, and click stop. Verify the known words in the unsent draft, then
discard it without submitting it to chat. The leading and trailing silence are
part of the development check because they expose speech truncation.

Capture the transaction unredacted while engineering it, including:

- exact endpoint, query, handshake, headers, cookies, body, and frame shape;
- HTTP method or WebSocket handshake/event names;
- stable fields and the classes of dynamic fields;
- body or frame structure and the audio substitution point;
- terminal response shape and typed auth-rejection signature;
- freshness/expiry metadata needed to decide when to refresh.

The unredacted capture is owner-only engineering input, not an artifact. Keep it
in memory or ephemeral local state, never commit it, never print it into logs or
evidence, and delete temporary captures before the session ends. Documentation
records the resulting protocol contract without credential values, audio,
transcript text, conversation text, or browser-profile data.

## Replay Template

Persist only fields required to reproduce the direct provider request,
including credential values when the request actually requires them. The
provider package must identify which values are:

- stable until auth refresh;
- regenerated for every request, such as request IDs, timestamps, boundaries,
  content length, and WebSocket connection identifiers;
- replaced with the current upload's audio bytes or chunks;
- forbidden from persistence, including audio and transcript content.

Provider state is runtime secret material. Its directory is owner-only mode
0700 and each state file is owner-only mode 0600. State must be atomically
replaced, bounded in size, and excluded from source control, support bundles,
diagnostics, and benchmark artifacts.

## Serving and Repair Invariants

With a fresh replay template:

- one file upload causes one provider-native direct transaction;
- no browser target is created, attached, navigated, or controlled;
- prior audio, transcript, request ID, timestamp, or length is not reused;
- provider text exists only long enough to build the API response.

A typed missing, stale, or rejected-auth result may run:

```text
direct attempt → one single-flight browser auth/template refresh → one direct retry
```

The refresh uses the existing signed-in session. It must not submit audio or
perform transcription. There is no website-transcription fallback. A second
auth rejection returns a typed failure.

## Logs, Traces, and Evidence

Production logs and validation evidence are metadata-only. Allowed fields
include:

- canonical provider and typed status/error code;
- request duration and input byte count;
- response text length and semantic-marker booleans;
- refresh attempted/succeeded booleans and timestamps;
- browser-target count before/after a hot-path proof.

Never log transcript text, audio samples, local audio paths, raw URLs with query
values, header/cookie values, auth state, request/response bodies, private page
content, or credential-shaped diagnostics. Retained evidence is constructed
independently from allowed metadata; it is never a masked, hashed, truncated,
transformed, or otherwise redacted version of the engineering capture.

## Tests and Fixtures

Checked-in tests use local fake HTTP/WebSocket servers, inert placeholders, and
synthetic audio only. They must cover:

- two different audio inputs produce two correctly substituted requests;
- dynamic fields are regenerated;
- fresh state causes zero browser calls;
- auth rejection refreshes once and retries direct once;
- malformed responses and timeouts become typed errors;
- serialized state excludes audio and transcript text and has owner-only mode;
- diagnostics and errors do not expose credential-shaped values.

Live provider qualification sends the checked-in synthetic file through
`/v1/audio/transcriptions` with an explicit provider. Providers run
sequentially. `/demo.html`, website recording, and provider UI are not provider
evidence.

## Scheduled Activity

Managed services keep the transcription fixture directory empty. Audio is used
only for explicit development, deployment smoke, deployment debugging, and
acknowledged chaos. Auth/capability refresh may recur on the configured cadence;
scheduled audio transcription must not.
