# Authenticated Provider Workflows

This document describes interactive web-agent operations such as `ask`,
`list`, and `delete`. Its fresh-tab lifecycle does not apply to
transcription. Transcription uses browser/CDP only for bounded discovery and
auth/request-template refresh, then sends audio through direct provider-native
HTTP or WebSocket transports as defined in
[`SANITIZATION.md`](SANITIZATION.md).

`cdp workflow agent` exposes authenticated provider operations through the
signed-in headed Chrome session selected by `--browser-mode headed`.
Provider `ask` operations reject an explicit headless mode before browser
access and force ambient/config defaults to the headed runtime.
Workflow-agent help omits the browser-mode selectors; direct low-level cdp
commands retain them for explicit diagnostics and maintenance.

```bash
cdp --browser-mode headed pages --json
cdp workflow agent providers --json
cdp workflow agent chatgpt capabilities --json
cdp schema webagent-operation --json
```

If the headed `pages` command returns open tabs, that runtime is reachable.
Provider asks use the existing signed-in session directly; they do not require
any provider-wide preflight based on earlier invocations.

## Ask Lifecycle

Every headed ask follows the same small lifecycle:

1. Open one fresh provider tab.
2. Verify the live composer and any explicitly requested mode or model.
3. Insert the exact prompt and optional attachment.
4. Perform one raw Send.
5. Observe the answer and conversation ID.
6. Close only the tab created by this invocation.

Independent asks may run concurrently. The browser-input lease serializes only
the focus-sensitive preparation and Send boundary; it is released before long
answer observation. A vanished process or tab may lose that invocation's
answer, but it does not poison or block the next fresh ask.

The command never closes sibling tabs and never reuses a tab from a previous
ask. It reports whether raw input was attempted, so callers can avoid
duplicating a request after an ambiguous transport failure.

Ask Alex is the one direct-HTTP exception: it resolves its exact course and
chapter context from browser-observed signed-in state, performs one POST, and
returns that response.

## Examples

```bash
printf '%s' 'I’m about to ship this change and I’m uneasy about the retry path. Read the attached diff like a careful teammate: find the smallest concrete failure, show me where it happens, and suggest one falsifiable test.' |
  cdp workflow agent chatgpt ask \
    --stdin --file ./change.diff --thinking Medium \
    --model 'GPT-5.6 Sol' --timeout 10m --json

printf '%s' 'A paper boat has washed up at the lighthouse during a silver storm. Paint the moment the keeper opens it: cinematic, quietly hopeful, painterly realism, wide frame, no lettering.' |
  cdp workflow agent chatgpt ask \
    --stdin --tool create-image --thinking Pro \
    --model 'GPT-5.6 Sol' --timeout 40m --json

printf '%s' 'What changed in the official Agent Skills specification, and what should I change in my local skill library?' |
  cdp workflow agent chatgpt ask \
    --stdin --tool web-search --thinking Medium \
    --model 'GPT-5.6 Sol' --timeout 8m --json

printf '%s' 'Please check whether the GitHub connector is available here, without changing anything.' |
  cdp workflow agent chatgpt ask \
    --stdin --tool github --thinking Medium \
    --model 'GPT-5.6 Sol' --timeout 8m --json

printf '%s' 'Turn the six Agent Skills review checks into a small, readable visual.' |
  cdp workflow agent chatgpt ask \
    --stdin --tool visualize --thinking Pro \
    --model 'GPT-5.6 Sol' --timeout 40m --json

printf '%s' 'Review this design.' |
  cdp workflow agent claude ask --stdin --json

printf '%s' 'Review this design.' |
  cdp workflow agent gemini ask --stdin --json

printf '%s' 'Review this implementation.' |
  cdp workflow agent grok ask --stdin --json

printf '%s' 'Review this implementation.' |
  cdp workflow agent perplexity ask --stdin --json

printf '%s' 'Critique this itinerary.' |
  cdp workflow agent tripadvisor ask --stdin --json
```

ChatGPT keeps the current thinking and model unless flags or owner-local config
request a selection. `Medium` is the practical daily setting; `Pro` takes the
most time for the deepest reasoning. `GPT-5.6 Sol` is a model, independent of
the reasoning setting. `highest` chooses the highest visible option;
`--minimum-thinking` fails before Send if the visible selection is below the
requested floor. Attached files must keep the requested basename and remain
visible at the final Send guard.

`Instant` is for instant ideas, `Medium` is the practical daily setting, and
`Pro` is the deepest setting and takes the most time. Image generation can
show a placeholder or stale Retry control before the decoded image arrives;
the image ask waits through the full configured timeout and treats an earlier
assistant turn containing a decoded image as authoritative over an empty
trailing assistant turn. Inspect `output_kind=image` and attachments even when
the text field is empty. If a stable zero-byte placeholder persists, the
provider performs one bounded `about:blank` → exact-conversation navigation
recovery, never a Retry click or a second Send; a deadline without decoded
pixels is returned as incomplete with the exact await command.

The verified direct tool values are `create-image`, `visualize`, `web-search`,
`github`, and `openai-platform`. Web search and the connector probes can show a
provider-side `Answer now` control; the workflow clicks that exact control at
most once and never counts it as another prompt submission. GitHub and OpenAI
Platform are honest connector probes: selection, one Send, and readable
response are supported, but the response may say that the connector is not
connected, which does not grant repository, organization, API-key, or billing
access. Deep research remains capability-only because its report is inside an
embedded sandbox not readable through the current page/frame boundary. Gmail
remains capability-only until its visible Connect step is authorized and
proven. The visible `Work` switch is also capability-only: this CLI verifies
and submits in `Chat` because it has no product-selection flag or Work
lifecycle. `Visualize` follows the same attachment-only image contract as
Create image and may take many minutes.

Rendered completion is causal, not merely textual: the same route, one user
message, and the submitted prompt fingerprint must be present. A stale answer
cannot satisfy a fresh ask. For image tools, a textual provider error is not an
image answer; decoded pixels are required, and the performed request remains
incomplete if they never arrive.

Perplexity asks use one identity-checked enabled `Submit` click. An Enter key
can leave the question in the root composer without submitting it, and the
provider may replace a temporary `/search/new/<id>` route with a final
`/search/<id>` route. The final route is accepted only when its rendered
question fingerprint matches; a performed dispatch is never resent.

## Conversation Reads

Where supported, `list`, `detail`, and `await` are read-only operations over an
observed conversation ID:

```bash
cdp workflow agent chatgpt conversations list --limit 30 --json
cdp workflow agent chatgpt conversations detail <conversation-id> --json
cdp workflow agent chatgpt conversations await <conversation-id> \
  --wait 40m --timeout 40m30s --json
cdp workflow agent chatgpt conversations download-attachments \
  <conversation-id> --output-dir ./designs --json
```

`download-attachments` exports every attachment owned by the canonical terminal
answer as bounded original provider bytes. It writes owner-only files and a
deterministic `chatgpt-attachments-manifest.json`, never overwrites an existing
path, and reports independent failures as `data.status=partial`. The public
result and manifest omit provider file/conversation identifiers, signed URLs,
prompts, answers, auth data, and browser target identities. A direct stable read
opens no tab; an eligible auth/transport failure may lazily use one fresh exact
headed target and exact-close it. Privacy-safe fallback results omit the target
identity while retaining truthful required/closed/failed cleanup state through
`cleanup.identity_omitted`.

Provider-specific `capabilities` output is the executable source of truth for
which read, continue, delete, and auth operations are installed.

ChatGPT hydrated detail exposes bounded current-assistant `attachments` with
image/file kind and safe available metadata such as alt text, stable source,
file identity, MIME type, byte size, and pixel dimensions. A finished current
assistant containing only an attachment is a terminal answer; it is not a
terminal-without-answer candidate. Signed URL query data and local or sandbox
paths are not emitted in attachment metadata; a safe basename may still be
reported as `file_name`. The direct Ask result returns that same safe attachment
array when its terminal answer came from hydrated detail.

When a ChatGPT conversation visibly ends with `Stopped thinking` and its
compose stop control is absent or disabled, preserve its exact ID and treat it
as terminal. Consume any assistant answer already present through list/detail.
If those reads expose no usable answer, record terminal-without-review and do
not poll again, click Continue or Submit, replace or reattach files, or
resubmit it.

For conversations that do not show this terminal UI condition, asynchronously
active detail remains a reason to wait.

## Exact-Target Cleanup

Every live invocation exact-closes the tab it created before returning. Normal
completion reports `cleanup.state=closed`. If Chrome disappears or exact close
cannot be proved, the result reports the exact target ID; it does not prescribe
a follow-up command and it never blocks a later fresh ask.

## Capability Changes

Before an operation becomes supported:

1. focused source tests and compile checks pass;
2. the installed binary exposes matching help and schema;
3. a real authenticated run proves the provider boundary;
4. the invocation exact-closes its own tab without changing sibling tabs.
