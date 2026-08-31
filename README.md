# cdp-cli

`cdp` is an agent-oriented Chrome DevTools Protocol CLI, written in Go.

The goal is a long-running local CDP process that can attach to a user-approved running Chrome session, keep that session warm, reconnect predictably, and expose browser debugging workflows through a shell interface that agents can inspect with `--help` and compose with `jq`.

## Status

Active implementation. The command tree, JSON/error conventions, connection
memory, browser readiness probes, target/page listing, page open/eval/wait/
observe/snapshot/html/DOM/CSS/layout commands, input automation, screenshots,
console and network capture, emulation for viewport/media/user-agent/
geolocation/timezone/locale/CPU/network, browser permissions,
accessibility/performance/memory probes, raw CDP
discovery/examples/exec, Web Storage/cookie/IndexedDB/Cache Storage/service
worker controls, headed/default-profile and managed-headless browser runtime
modes, cron-safe `daemon keepalive`, and managed headless maintenance commands
are in place. Authenticated web-agent workflows now have a provider-neutral,
capability-backed command and schema surface. Claude, Gemini, and Grok doctor,
no-turn auth refresh, fresh-conversation ask, list/detail/await/delete, and
exact-target cleanup verticals are live-proven; all other provider operations remain
explicitly planned until their installed vertical is proven.

The provider-neutral VoxInput transcription service is available through
`cdp transcription serve`. It exposes OpenAI/Whisper-compatible file and
translation endpoints, completed-file SSE, an OpenAI-shaped realtime
WebSocket, a self-contained `/demo.html` dogfood app, ephemeral transaction
media with durable result records, bounded provider health probes,
explicit online-auth refresh, and local/ChatGPT/Claude/Gemini/Microsoft 365/Bing
provider routing. User-level service installation
is available through `cdp transcription service install` on macOS and Linux.
See
[docs/TRANSCRIPTION_API.md](docs/TRANSCRIPTION_API.md). Authenticated
transcription providers use browser/CDP only to learn and refresh the
owner-only request template for each provider's dictation transport; every
uploaded file is sent through that direct HTTP or WebSocket transaction. See
[docs/SANITIZATION.md](docs/SANITIZATION.md).
Health-probe subprocesses are bounded as well: decoded realtime PCM and
ffprobe diagnostics are capped, and cancellation terminates only the owned
probe process group where the platform supports process groups. Cron and native
transcription-service manager diagnostics use the same bounded, owned-process
boundary; a truncated crontab is reported as unclassified rather than empty.
Explicit `--jq` filtering retains the caller-requested result, bounds only jq
diagnostics, and cancels the owned jq process tree where supported.
Managed browser process-table probes retain only complete output within a hard
byte budget and surface overflow or probe failure instead of treating a partial
or empty process set as proof that no managed browser exists.
Managed headless Chrome launch remains process-group-owned through metadata,
registry, and active-port readiness failures; only a successfully ready launch
is detached, so wrapper descendants cannot survive a pre-readiness error.
Headed Chrome presence probes use the same bounded owned-process boundary and
fail closed on an unknown process table, so a probe failure cannot be mistaken
for an absent browser and trigger a duplicate launch.
When a headed keepalive starts Chrome itself, that launch remains owned until a
supported window check succeeds; readiness failure terminates and reaps only
the newly started process tree, and only a ready launch is detached.
Darwin profile-use and headed-window scans use the complete process-table
boundary too; an unknown scan is reported before any Launch Services window
action is attempted.
Native remote-debugging approval discovery and its short-lived macOS/Linux
helpers use the same bounded owned-process boundary. Only complete PID or
metadata reports are parsed; helper overflow, failure, and cancellation remain
explicit and never become an approval result.
Darwin Launch Services, AppleScript, and System Events transport uses the same
boundary with bounded script input/output. A canceled script never broadly
signals the shared System Events service; native approval remains exact and
still requires the daemon transport proof.

For phone or tablet microphone testing over a private LAN, install the service
with `--tls-self-signed --tls-host <LAN-IP>` and open the reported `https://`
demo URL; plain HTTP on a LAN address is not a microphone-capable browser
origin.

## Intended Shape

```bash
cdp browser mode get --json
cdp guide --path
cdp daemon start --auto-connect --json
cdp daemon status --json
cdp doctor --check scheduled-tasks --json
cdp doctor --check browser-health --json
cdp doctor --check headless-security --json
cdp --browser-mode headed daemon keepalive --auto-connect --repair --probe active --macos-self-heal-approval --display :0 --json
cdp --browser-mode headless browser profile seed --strategy managed --json
cdp --browser-mode headless daemon keepalive --repair --json
cdp --browser-mode headless daemon maintenance --json
cdp pages --json | jq '.pages[] | {id,title,url}'
cdp page select --url-contains example.com --json
cdp page select --target-index 2 --json
cdp open https://example.com --json
cdp page reload --target-index 2 --json
cdp page back --target-index 2 --json
cdp page forward --target-index 2 --json
cdp page activate --target-index 2 --json
cdp page close --target-index 2 --wait-gone --json
cdp open https://example.com --new-tab=false --target-index 2 --json
cdp open https://example.com --reuse --target-index 2 --json
cdp eval 'document.title' --json
cdp eval 'document.title' --target-index 2 --json
cdp observe --json
cdp observe --target-index 2 --json
cdp wait text Ready --timeout 10s --json
cdp wait eval 'window.__rendered === true' --target-index 2 --json
cdp events stream --target-index 1 --match Page.loadEventFired,Network.loadingFailed --json > tmp/events.jsonl
cdp events wait --file tmp/events.jsonl --method Page.loadEventFired --timeout 20s --json
cdp events interactions --target-index 1 --match click,scroll --duration 30s --max-events 50 --json > tmp/interactions.jsonl
cdp events tap --target-index 1 --match Page.loadEventFired,Network.loadingFailed --duration 10s --max-events 50 --json
cdp events stream --target-index 1 --enable DOM,Performance --match DOM.documentUpdated,Performance.metrics --json
cdp snapshot --selector body --limit 50 --json
cdp perf summary --target-index 2 --duration 5s --json
cdp memory counters --target-index 2 --json
cdp memory heap-snapshot --target-index 2 --out tmp/page.heapsnapshot --json
cdp screenshot --out tmp/page.png --json
cdp screenshot --target-index 2 --out tmp/page.png --json
cdp console --errors --target-index 2 --wait 2s --json
cdp network --failed --target-index 2 --wait 2s --json
cdp network capture --target-index 2 --redact safe --wait 2s --json
cdp network capture --target-index 2 --redact none --out tmp/network.local.json --json
cdp network block --target-index 2 --pattern '*://*/analytics/*' --duration 10s --json
cdp network mock --target-index 2 --rule '{"url_pattern":"*://*/api/config","status":200,"body":"{\"enabled\":true}","max_matches":1}' --duration 10s --json
cdp wait response --target-index 2 --match-url /api --status 200 --json
cdp emulate viewport --preset mobile --target-index 2 --json
cdp emulate media --prefers-color-scheme dark --target-index 2 --json
cdp emulate network --preset slow-3g --json
cdp emulate clear --target-index 2 --json
cdp storage list --target-index 2 --include localStorage,sessionStorage,cookies --json
cdp storage cookies list --target-index 2 --url https://example.com --json
cdp storage indexeddb list --target-index 2 --json
cdp storage indexeddb get app settings feature --target-index 2 --json
cdp storage cache list --target-index 2 --cache app-cache --json
cdp storage cache get app-cache http://localhost:5173/api/me --target-index 2 --json
cdp storage service-workers list --target-index 2 --json
cdp workflow visible-posts 'https://x.com/<handle>' --limit 5 --json
cdp workflow responsive-audit --target-index 2 --viewports desktop,mobile --include layout --wait 0s --json
cdp workflow responsive-audit 'https://example.com' --viewports desktop --include screenshot --out-dir tmp/responsive-audit --json
cdp workflow web-research serp --query-file tmp/research/queries.txt --out-dir tmp/research --json
cdp workflow web-research serp --query-file tmp/research/queries.txt --serp all --parallel-engines --out-dir tmp/research-all --json
cdp workflow web-research extract --url-file tmp/research/visit-urls.txt --out-dir tmp/research/pages --json
cdp workflow pdf-to-markdown tmp/downloads/paper.pdf --out-dir tmp/paper-markdown --json
cdp workflow agent providers --json
cdp --browser-mode headed pages --json
cdp workflow agent claude capabilities --json
cdp workflow agent claude doctor --json
cdp workflow agent claude auth refresh --json
cdp workflow agent gemini capabilities --json
cdp workflow agent gemini capabilities refresh --json
cdp workflow agent grok capabilities --json
cdp workflow agent grok capabilities refresh --json
cdp transcription serve --default-provider chatgpt-web --providers chatgpt-web,claude-web,gemini-web,microsoft-365-web,bing-web
cdp transcription spec > openapi.json
cdp schema webagent-operation --json
cdp protocol search screenshot --json
cdp protocol describe Page.captureScreenshot --official --json
cdp protocol examples Page.captureScreenshot --json
cdp protocol exec Browser.getVersion --json
cdp protocol exec Runtime.evaluate --target <target-id> --params '{"expression":"document.title","returnByValue":true}' --json
cdp protocol exec Runtime.evaluate --target-id <target-id> --params '{"expression":"document.title","returnByValue":true}' --json
cdp protocol exec Runtime.evaluate '{"expression":"document.title","returnByValue":true}' --target-index 2 --json
cdp protocol exec Runtime.evaluate --target-index 2 --params '{"expression":"document.title","returnByValue":true}' --json
cdp protocol exec Runtime.evaluate --target-type service_worker --url-contains chrome-extension:// --params '{"expression":"Object.keys(globalThis).slice(0,50)","returnByValue":true}' --json
cdp protocol exec Page.captureScreenshot --target <target-id> --params '{"format":"png"}' --save tmp/page.png --json
cdp Runtime.evaluate '{"expression":"document.title","returnByValue":true}' --target-index 2 --json
```

For compatibility with chrome-agent one-shot commands, protocol exec also
accepts one optional positional JSON object after Domain.method. The canonical
--params flag and the positional form are mutually exclusive; both use the
same local valid-JSON/object-shape checks before browser setup.

The shorter source-compatible one-shot form may omit `protocol exec` entirely:
`cdp Domain.method [JSON_PARAMS]`. It is only a routing alias; execution still
uses the daemon-backed protocol path and accepts the same target, validation,
output, and artifact flags.

For one-shot target selection, `--target-id` is an explicit source-compatible
alias for cdp-cli's `--target`, and `--url` aliases `--url-contains`. Supply only
one spelling of either alias pair. cdp-cli keeps `--target-index` explicit and
does not infer an index from a numeric `--target` value.

The root bare one-shot form is the source-compatible exception: a short
all-digit `--target` is interpreted as a 1-based page index, while
`--target-id` forces ID-prefix matching. Direct `protocol exec --target` keeps
its established ID/prefix meaning.

The same `--target-id` and `--url` aliases are available on the daemon-backed
persistent attach equivalents `events stream` and `events interactions`, and
on `page close` for the source target-aware stop flow. Supply only one spelling
from either alias pair; the existing exact-session filtering and settled close
behavior are unchanged.

Protocol discovery uses the selected live Chrome schema by default. Add
`--official` to `metadata`, `domains`, `search`, `describe`, `examples`, or
`compat` for browser-free online discovery from the official browser and
JavaScript tip-of-tree schemas; output preserves both source URLs. The official
source requires network access and can differ from the selected live Chrome.
Without `--json`, `protocol describe` prints a compact actionable signature
with description, flags, typed required/optional parameters, and returns.

Add `--validate` to check the method, browser/target scope, required parameters,
and unknown parameters against the selected protocol before attachment or
execution. `protocol exec --validate --official` validates against the official
schema, but execution still runs through the daemon. Omit `--validate` to
preserve the forward-compatible raw CDP escape hatch.

When Chrome rejects a raw command, JSON output uses
`code: "cdp_command_failed"`, `err_class: "protocol"`, and exit 1 while
preserving the method plus Chrome's numeric code and message under `data`.
This is a command/parameter problem, not a daemon connectivity failure; use the
reported `protocol describe` and `protocol examples` remediation commands.

For target-scoped raw execution, `--target-index N` selects the 1-based page
order shown by `cdp pages`. That page-only order is deterministic ascending
full target ID, independent of Chrome's response order. The index cannot be
combined with `--target`, `--url-contains`, `--title-contains`, or
`--target-type`; omit it for
browser-scoped execution or use the other selectors for non-page targets.
Opening or closing a page can still renumber the set; prefer an exact/short ID
or unique URL/title selector for durable follow-up. Pages JSON reports the
policy as `index_order: "target_id_ascending"`.
Page JSON includes the full `id` and an uppercase eight-character `short_id`;
it also includes the current 1-based page-only `index`. Generic target rows
publish the same `short_id`. `--target` accepts a unique case-insensitive ID
prefix and rejects ambiguity with bounded metadata-only candidate short IDs.
Ambiguity errors also return bounded exact `candidate_ids`, and not-found or
out-of-range errors return bounded exact `available_ids`, so a corrected retry
remains possible when eight-character short IDs collide. Existing
`candidate_short_ids` and `available_short_ids` remain available for concise
display. Page-only errors pair those IDs with bounded `candidate_indexes` or
`available_indexes`; generic protocol target errors remain index-free.
Without `--json`, the same safe evidence is printed as bounded recovery rows:
page errors show index, short ID, and exact full ID; generic target errors show
short and full ID. A count-only omission row makes a truncated ten-target list
explicit. URLs and titles are not copied into those rows.
Plain errors also print up to ten terminal-safe `Next steps:` from the same
effective remediation policy exposed by JSON; multiline, control-bearing, and
oversized entries are omitted.
URL/title substring selectors also require one unique page; duplicate matches
return the same bounded candidate evidence instead of selecting the first tab.
Page commands reject combinations of target ID, URL, title, and index selector
modes instead of silently prioritizing one; raw protocol filters remain conjunctive.
Plain `cdp pages` prints index, short ID, full ID, URL, and quoted title; plain
`cdp targets` prints short ID, full ID, type, URL, and quoted title. Raw protocol
ID, URL, title, and type filters combine and must leave one target.

The direct emulation family uses the same page-only selector: `viewport`,
`clear`, `media`, `color-scheme`, `user-agent`, `geolocation`, `timezone`,
`locale`, `cpu`, and `network` all accept `--target-index N`. The index is
mutually exclusive with `--target`, `--url-contains`, and `--title-contains`,
is resolved before attachment, and does not count worker targets. Indexed
reports include `target_index`; the mutation remains scoped to the selected
page and `clear` remains best-effort across the existing emulation overrides:

    cdp emulate viewport --preset mobile --target-index 2 --json
    cdp emulate timezone --timezone-id UTC --target-index 2 --json
    cdp emulate network --preset slow-3g --target-index 2 --json
    cdp emulate clear --target-index 2 --json

Browser-backed `stop-state classify` also accepts the same page-only index.
Its `--text`, `--title`, and `--url` inputs remain browser-free; an explicit
index is rejected with those offline inputs instead of being silently ignored.
Indexed page classification reports `.target` and `.target_index` alongside
the existing bounded stop-state summary, while page text remains outside the
target metadata:

    cdp stop-state classify --target-index 2 --json
    cdp stop-state classify --text 'Sign in to continue' --json

Event-oriented waits (`request`, `response`, `network-idle`, `dialog`,
`file-chooser`, `popup`, and `download`) accept the same mutually exclusive
page-only selector. Successful indexed reports include `target_index` beside
the resolved target. For popup and download waits, the index selects the
opener or triggering page; the resulting browser-scoped event still needs its
existing URL, title, filename, or status criteria when concurrent pages are
active.

The same explicit page index is available on `page select`, `page reload`,
`page back`, `page forward`, `page activate`, and `page close`; each rejects a
conflicting ID, URL, title, or positional selector.

Core observation commands also accept the same mutually exclusive selector:
`eval`, `observe`, `text`, `html`, and `snapshot`. Their successful JSON
reports include `target_index` when an index was supplied, alongside the
resolved page target evidence. The index is 1-based and follows the page-only
order shown by `cdp pages`, so worker and other non-page targets do not shift
the selection.

The full assertion family accepts the same page-only selector: `assert value`,
`text`, `url`, `title`, `count`, `attribute`, `class`, `focused`, `css`,
`role`, `name`, `aria-snapshot`, `attached`, `detached`, `visible`, `hidden`,
`in-viewport`, `enabled`, `disabled`, `editable`, `readonly`, `checked`,
`unchecked`, and `indeterminate`. Indexed reports include `target_index`
beside the selected target while preserving each assertion's locator, polling,
retry, and failure diagnostics:

    cdp assert text 'Ready' --target-index 2 --timeout 5s --json
    cdp assert checked 'Subscribe to newsletter' --by label --target-index 2 --json

The assertion index is mutually exclusive with `--target`, `--url-contains`,
and `--title-contains`; invalid and out-of-range values fail before page
attachment.

The text and keyboard actions `fill`, `type`, `insert-text`, and `press` also
accept the same mutually exclusive page-only selector. The selected page is
resolved before actionability checks or input dispatch, and indexed success or
bounded actionability failure data includes `target_index`:

    cdp fill 'Search' Aarhus --by label --target-index 2 --json
    cdp type 'Search' Aarhus --by label --target-index 2 --json
    cdp insert-text '[contenteditable=true]' hello --target-index 2 --json
    cdp press Enter --selector 'input[name=q]' --target-index 2 --json

These commands retain their existing locator, trusted-input, mutation, wait,
and cleanup behavior. Zero, negative, out-of-range, and selector-conflicting
indexes fail before page attachment; worker targets do not change the page
order.

Pointer and viewport actions `hover`, `drag`, and `scroll` accept the same
mutually exclusive page-only selector. Indexed reports include
`target_index` beside the selected page while retaining pointer actionability,
offscreen auto-scroll, trial, dispatch, alignment, and viewport evidence:

    cdp hover 'Save changes' --by role --role button --target-index 2 --json
    cdp drag '.draggable' 10 20 --target-index 2 --json
    cdp scroll '#results' --target-index 2 --block center --json

`hover` and `drag` preserve their visible/stable/hit-test checks, optional
`--trial` and `--force` behavior, and bounded auto-scroll re-check. `scroll`
preserves its attached/stable checks and non-mutating `--trial` mode. Zero,
negative, out-of-range, and selector-conflicting indexes fail before page
attachment or pointer/scroll mutation; worker targets do not change page
order.

Control-state and selection actions `focus`, `clear`, `check`, `uncheck`, and
`select` accept the same mutually exclusive page-only selector. Indexed
reports include `target_index` beside the selected page while preserving each
command's existing mutation, locator, actionability, trial, auto-scroll, and
verification behavior:

    cdp focus 'input[name=email]' --target-index 2 --json
    cdp clear 'input[name=email]' --target-index 2 --json
    cdp check 'Subscribe to newsletter' --by label --target-index 2 --json
    cdp uncheck 'Subscribe to newsletter' --by label --target-index 2 --json
    cdp select Plan pro --by label --target-index 2 --json

Zero, negative, out-of-range, and selector-conflicting indexes fail before
page attachment or form-control mutation; workers do not change page order.

Local file actions also accept the page-only index. `file` resolves the input
and assigns a local file on the selected page; detached `file chooser` accepts
either its existing `--target` or `--target-index` selector because backend
node IDs are target-scoped. Indexed reports include `target_index`, retain
trial/actionability evidence, and expose only path/basename/count metadata;
file contents are never printed:

    cdp file 'Upload file' tmp/upload.txt --by label --target-index 2 --json
    cdp file chooser 247 tmp/first.epub tmp/second.epub --target-index 2 --json

The index is mutually exclusive with `--target`, `--url-contains`, and
`--title-contains` on `file`; for `file chooser` it is mutually exclusive with
`--target`. Invalid and out-of-range indexes fail before page attachment or
file mutation, and workers do not change page order.

Form inspection uses the same page-only selector. `form values` lists visible
controls (or all controls with `--include-hidden`), while `form get` returns
one CSS-selected control. Indexed reports include `target_index`; invalid,
conflicting, and out-of-range indexes fail before page attachment:

    cdp form values --target-index 2 --json
    cdp form get 'input[name=email]' --target-index 2 --json

The index is mutually exclusive with `--target`, `--url-contains`, and
`--title-contains`, and workers do not change page order. See
`cdp schema form-values --json` and `cdp schema form-get --json` for the
stable output contracts.

Direct dialog handling uses the same page-only selector. Without `--wait`,
`dialog accept` and `dialog dismiss` handle a dialog already owned by the
attached session. For an agent workflow that must wait for a dialog, start the
action command first so observation and handling stay on one session:

    cdp dialog accept --wait --type prompt --prompt-text yes --target-index 2 --json
    cdp dialog dismiss --wait --type confirm --target-index 2 --json

The index is mutually exclusive with `--target`, `--url-contains`, and
`--title-contains`. `--message` and `--message-contains` further filter
wait-mode events. Invalid selectors fail before page attachment or dialog
mutation, and the report includes `target_index` when an index is supplied.
The command's `--wait` mode is session-safe; a detached `wait dialog` followed
by a new action command cannot assume ownership of the pending dialog. Both
wait entry points release their attached session with an independent bounded
cleanup context after success, failure, cancellation, or timeout; they never
close the caller-owned page.

Read-only inspection commands use the same page-only selector: `frames`,
`locator find`, `dom query`, `css inspect`, `layout overflow`, and
`a11y tree`, `a11y find`, `a11y node`, and `a11y snapshot`. Successful indexed
reports include `target_index`; invalid, conflicting, and out-of-range values
fail before page attachment or inspection.

Performance and memory diagnostics also accept the same selector: `perf
summary`, `memory counters`, and `memory heap-snapshot`. Heap data remains a
local artifact reference, and indexed reports include `target_index` without
embedding the artifact payload.

Multi-engine SERP research runs engines concurrently and reuses one workflow tab
lane per engine, so large query files avoid opening a fresh tab for every
engine/query/page combination.

### Authenticated Provider Workflows

`cdp workflow agent providers --json` is the executable capability catalog for
authenticated web-agent providers. Each provider also exposes a browser-free
`capabilities` command. An operation is callable only when its capability has
`supported=true`; planned and unsupported behavior remains explicit.

All provider operations use the versioned `webagent-operation/v1` outer
envelope. Provider-specific result data remains under `data`; lifecycle fields
preserve stage, irreversible-action dispatch, exact conversation identity,
target evidence, cleanup, and safe next commands. Inspect the contract before
writing orchestration:

```bash
cdp workflow agent providers --json
cdp workflow agent chatgpt capabilities --json
cdp schema webagent-operation --json
cdp schema webagent-capabilities --json
cdp describe --command 'workflow agent' --json
```

Capability metadata does not attach to Chrome. Provider doctor/auth/ask/CRUD
commands are added only after their concrete installed browser workflows pass
the corresponding safety and usefulness gates.

Auth refresh treats missing browser evidence as uncertainty, not proof of
logout. Each supported provider gets one initial-load observation, one ordinary
reload observation, and one final cache-bypassing hard-reload observation
plus a grace wait on that final page. One overall readiness deadline is divided
across those three stages; it is never multiplied per stage. If the required
UI, cookie, or request evidence is still absent, the typed result reports
`auth_evidence_not_observed` and states that the browser session may remain
active.

ChatGPT `ask` and `conversations continue` keep the provider's current
thinking/model selections unless explicit flags or owner-only config say
otherwise. Thinking controls are reported in logical ascending order:
`Instant 5.5`, `Medium`, `High`, `Extra High`, `Pro`. `highest` is a selection
policy, not another thinking level; `--minimum-thinking` makes downgrade
failure explicit before Send. Models remain a separate control:

```bash
printf '%s' 'I’m about to ship this change and I’m uneasy about the retry path. Read the attached diff like a careful teammate: find the smallest concrete failure, show me where it happens, and suggest one falsifiable test.' |
  cdp workflow agent chatgpt ask \
    --stdin --file ./change.diff --thinking Medium \
    --model 'GPT-5.6 Sol' --json --timeout 10m
printf '%s' 'A paper boat has washed up at the lighthouse during a silver storm. Paint the moment the keeper opens it: cinematic, quietly hopeful, painterly realism, wide frame, no lettering.' |
  cdp workflow agent chatgpt ask \
    --stdin --tool create-image --thinking Pro \
    --model 'GPT-5.6 Sol' --json --timeout 40m
printf '%s' 'What changed in the official Agent Skills specification, and what should I change in my local skill library?' |
  cdp workflow agent chatgpt ask \
    --stdin --tool web-search --thinking Medium \
    --model 'GPT-5.6 Sol' --json --timeout 8m
printf '%s' 'Please check whether the GitHub connector is available here, without changing anything.' |
  cdp workflow agent chatgpt ask \
    --stdin --tool github --thinking Medium \
    --model 'GPT-5.6 Sol' --json --timeout 8m
printf '%s' 'Please check whether the OpenAI Platform connector is available here, without creating a key or changing settings.' |
  cdp workflow agent chatgpt ask \
    --stdin --tool openai-platform --thinking Medium \
    --model 'GPT-5.6 Sol' --json --timeout 8m
printf '%s' 'Turn the six Agent Skills review checks into a small, readable visual.' |
cdp workflow agent chatgpt ask \
    --stdin --tool visualize --thinking Pro \
    --model 'GPT-5.6 Sol' --json --timeout 40m
cdp workflow agent chatgpt transcribe \
    --file /path/to/whisper.webm --duration-ms 4200 --json
cdp workflow agent chatgpt conversations await <conversation-id> \
  --wait 40m --timeout 40m30s --json
cdp workflow agent chatgpt conversations download-attachments \
  <conversation-id> --output-dir ./designs --json
```

The attachment batch writes original provider bytes plus a deterministic
owner-only manifest. Existing paths are never replaced; `partial` preserves
successful files while making every failed or bounded item explicit. Provider
IDs, signed URLs, prompts, answers, auth state, and target IDs are excluded from
its public JSON and manifest.

`Instant` is for instant ideas, `Medium` is the practical daily setting, and
`Pro` is the deepest setting and takes the most time. Web search, GitHub, and
OpenAI Platform may pause at a provider-side `Answer now` control; the ask
workflow clicks that exact visible control at most once, never treating it as a
second Send. GitHub and OpenAI Platform selection/readback are supported even
when the connector answers that it is not connected; a visible pill does not
grant repository, organization, key, or billing access. Create image and
Visualize return `output_kind=image` and may have empty text. Image asks wait
for the full configured timeout because the UI may show a placeholder or stale
Retry control before decoded pixels arrive. If a final image is in an earlier
assistant turn while an empty trailing turn remains, that image-bearing turn is
authoritative. A stable zero-byte placeholder gets one bounded
`about:blank` → exact-conversation recovery; the provider never clicks Retry or
sends the prompt again. Deep research remains capability-only because its
report is hosted in an embedded sandbox whose readable lifecycle is not exposed
by the current page/frame boundary; Gmail remains capability-only until its
visible Connect step is explicitly authorized and proven. If image pixels do
not decode by the deadline, the result is incomplete but already submitted and
includes the exact await command.

Terminal text and image acceptance also require the fresh route, exactly one
user message, and a rendered prompt fingerprint matching the submitted prompt.
A stale prior answer or a textual image-service error cannot satisfy an image
ask; image tools need decoded pixels and otherwise remain incomplete.

The visible `Work` switch remains unsupported: this CLI verifies and submits
in `Chat` because it has no product-selection flag or Work lifecycle.

If the exact ChatGPT conversation shows `Stopped thinking` and its compose stop
control is absent or disabled, it is terminal. Consume any answer already
present through list/detail. If those reads expose no usable answer, record
terminal-without-review and do not poll again, click Continue or Submit,
replace or reattach files, or resubmit it.

Entitlement-specific defaults belong in the owner-only cdp config, never in
the open-source transport defaults:

```json
{
  "agents": {
    "chatgpt": {
      "thinking": "highest",
      "minimum_thinking": "extra-high",
      "model": "highest"
    }
  }
}
```

Unusable authenticated providers can be disabled in the same owner-only
config. On macOS the default file is
`~/Library/Application Support/cdp-cli/config.json`; use `--config <path>` for
another file. Values are canonicalized and aliases such as `chatgpt-web` and
`claude-web`, `gemini-web`, `microsoft-365-web`, and `bing-web` are accepted;
local transcription is
independent and cannot be disabled through this list. Bing Voice is
transcription-only: its direct public Speech WebSocket is available to the
transcription service, but it is not exposed as a general web-agent/search
provider.

The default config is self-maintaining: a first run writes the current
`schema_version`, and a valid legacy file is upgraded atomically while
preserving its values. If the implicit default file is malformed, cdp moves it
aside with an `.invalid-<timestamp>` suffix and continues with sane defaults.
An explicitly selected `--config` remains strict so project configuration
mistakes are never silently ignored.

```json
{
  "agents": {
    "disabled_providers": ["chatgpt"]
  }
}
```

Normal provider metadata omits disabled entries so clients do not advertise a
route that cannot run. Use `cdp workflow agent providers --include-disabled
--json` when diagnosing the policy; disabled entries carry the stable
`reason=disabled_by_config` code. Direct provider commands and transcription
requests fail before browser or adapter dispatch, and aggregate refresh keeps
enabled providers independent.

For headed providers, `cdp --browser-mode headed pages --json` returning open
tabs proves that the selected headed runtime is reachable. Each ask then opens
one fresh tab, verifies the live composer, applies any requested mode/model,
submits the exact prompt with one raw Send, reads the answer, preserves the
observed conversation ID, and closes only that tab. A failed process or missing
tab does not block a later ask; the later invocation starts with another fresh
tab.
An explicit headless mode is rejected before provider browser access, and an
ambient headless default is overridden for these operations.

Attached ChatGPT files must retain the exact requested basename before Send.
The final Send guard rechecks the resolved thinking/model, exact prompt, route,
and attachment. Long answer observation does not hold the short-lived
headed-browser input lease, so independent asks can overlap after submission.

Perplexity asks click one observed enabled `Submit` control rather than
assuming an Enter key submitted the question. The provider also follows a
temporary `/search/new/<id>` to the final `/search/<id>` route only when the
rendered prompt fingerprint still matches. A performed dispatch with
`raw_input_count=1` is never resent, including when stored-detail auth is
expired and the terminal answer is returned from the rendered page.

ChatGPT conversation list/detail/await use captured-template direct HTTP first.
Only an eligible auth/transport failure lazily initializes one headed fallback;
429 and usage failures never do. That fallback proves the live
signed-in UI and session cookie, prefers a freshly observed or previously
validated request shape when available, and lets the actual same-origin
read-only response decide readiness. Await repeats its capped backoff until the
requested deadline. Hydrated detail is incomplete whenever provider async state still
reports streaming or an unrecognized present state, even if the current
assistant node already says `finished_successfully` and `end_turn`.

Claude, Gemini, and Grok advertise browser-free `doctor`; no-turn headed `auth
refresh`; fresh-conversation headed `ask`; list/detail/await/delete
conversation operations. Every browser
operation owns and exact-closes one fresh target. An ambiguous or performed
provider mutation is never resubmitted.
All headed provider workflows share the owner-only
`<state-dir>/locks/headed-browser-input.lock` before target creation through
their final raw input; a single-Send ask releases it before answer polling.

Claude reads through its browser-observed stable HTTP shape and lazily uses one
exact-owned rendered fallback only for a typed browser-context rejection.
Claude auth refresh observes the organization/list request and keeps the
private replay template only in owner-only cdp state:

```bash
cdp workflow agent claude capabilities --jq '.data.operations[] | select(.supported)'
cdp workflow agent claude auth refresh --json
cdp workflow agent claude doctor --json
printf '%s' 'Review this design.' | cdp workflow agent claude ask --stdin --json
cdp workflow agent claude conversations list --limit 30 --json
cdp workflow agent claude conversations detail <conversation-id> --json
cdp workflow agent claude conversations await <conversation-id> --json
cdp workflow agent claude conversations delete <conversation-id> --json
```

Gemini deliberately stays rendered-only: there is no coded `batchexecute`
replay. Auth refresh persists only safe signed-in/session-cookie booleans.
Runtime capability refresh observes the current model mode, available modes,
and tool controls without submitting a prompt. Conversation listing waits for
the requested identity count or progressively advances Gemini's history
scroller to a stable bottom, so a partial first batch is not accepted as
complete. Post-Send prompt identity uses one strict rendered `Copy prompt`
control intercepted inside the owned target and hashes the captured prompt
after outer trim only; it does not write the observed copy through to the
system clipboard or collapse interior whitespace:

```bash
cdp workflow agent gemini capabilities --json
cdp workflow agent gemini auth refresh --json
cdp workflow agent gemini capabilities refresh --json
cdp workflow agent gemini doctor --json
printf '%s' 'Review this design.' | cdp workflow agent gemini ask --stdin --json
cdp workflow agent gemini conversations list --limit 30 --json
cdp workflow agent gemini conversations detail <conversation-id> --json
cdp workflow agent gemini conversations await <conversation-id> --json
cdp workflow agent gemini conversations delete <conversation-id> --json
```

Grok auth observes the signed-in conversation-list request, while runtime
capability refresh observes `/rest/modes` and selects only the available
provider-owned default. Ask verifies the exact prompt and mode, clicks Send
once, observes the same-target `/c/<id>` route, and returns canonical
stored detail from the response-node/load-responses sequence. A typed 401/403
may use one exact-owned rendered fallback. Delete resolves one strict
`Delete Chat` menu item and requires the same target to reach `/`
without the conversation id. Grok prompt identity normalizes only line endings
and whitespace-only blank lines observed in provider storage; every character
and indentation on non-empty lines remains identity-significant:

```bash
cdp workflow agent grok capabilities --json
cdp workflow agent grok auth refresh --json
cdp workflow agent grok capabilities refresh --json
cdp workflow agent grok doctor --json
printf '%s' 'Review this design.' | cdp workflow agent grok ask --stdin --json
cdp workflow agent grok conversations list --limit 30 --json
cdp workflow agent grok conversations detail <conversation-id> --json
cdp workflow agent grok conversations await <conversation-id> --json
cdp workflow agent grok conversations delete <conversation-id> --json
```

See `docs/AUTHENTICATED_PROVIDERS.md` for the direct ask lifecycle and
capability-truth rules.

### Exact-Date Google Queries

Web-research query files are line-oriented. Blank lines and lines whose first
non-space character is `#` are ignored. Each remaining row is either `query` or
`query<TAB>google-tbs-time-filter`. The second column is retained as
`time_filter` in output metadata and applied only to Google; other engines
ignore it.

Create a row containing a literal tab and run a headed Google search for an
exact date:

```bash
mkdir -p tmp/research
printf '%s\t%s\n' 'agentic engineering' 'cdr:1,cd_min:07/01/2026,cd_max:07/01/2026' > tmp/research/queries-exact-date.txt
cdp --browser-mode headed workflow web-research serp --query-file tmp/research/queries-exact-date.txt --serp google --out-dir tmp/research/exact-date --json
```

For progressive, human-reviewed Google research, keep one engine lane, sample
one result page, stop on the first blocked page, and set a conservative minimum
between navigation starts:

```bash
cdp --browser-mode headed workflow web-research serp \
  --query-file tmp/research/queries.txt \
  --serp google \
  --fallback-serp none \
  --parallel 1 \
  --navigation-delay 30s \
  --result-pages 1 \
  --fast-fail-blocked \
  --blocked-failure-threshold 1 \
  --progress stderr \
  --out-dir tmp/research/progressive-pass \
  --json
```

`--navigation-delay` is cancellable and applies between navigation starts
within each SERP engine lane; it does not delay the lane's first navigation.
Parallel engine lanes pace independently. Review the pass artifacts before
choosing another query or requesting deeper result pages. The delay is session
stewardship, not a substitute for stopping when a consent, CAPTCHA, unusual
traffic, authentication, or bot-check page appears.

### Local PDF Text-Layer Extraction

`workflow pdf-to-markdown` converts a local PDF's embedded text layer into
deterministic, page-separated Markdown. It is browser-free and never performs
OCR. Install its declared Poppler dependency first:

```bash
# Debian or Ubuntu
sudo apt-get install poppler-utils

# macOS with Homebrew
brew install poppler
```

When the PDF is on a website, acquire it separately through the visible headed
browser, then convert the downloaded local file:

```bash
pdf_target="$(
  cdp --browser-mode headed open 'https://example.com/paper' \
    --task-id pdf-download \
    --json |
    jq -r '.page.target_id'
)"
cdp --browser-mode headed click 'Download PDF' \
  --by role --role link \
  --target "$pdf_target" \
  --wait-download \
  --download-dir tmp/downloads \
  --json
cdp --browser-mode headed page close \
  --target "$pdf_target" \
  --wait-gone \
  --json
cdp workflow pdf-to-markdown tmp/downloads/paper.pdf \
  --out-dir tmp/paper-markdown \
  --json
```

When a completed download uses an explicit `--download-dir`, cdp retains a
sanitized plain filename derived from Chrome's suggested filename. Existing
files are never overwritten: collisions use `name (1).ext`, `name (2).ext`,
and so on. Download JSON reports the final retained path in `file_path`.

The output directory contains owner-only `document.md` and `metadata.json`.
The workflow copies the opened source bytes into a private temporary snapshot,
hashes those same bytes, and passes that snapshot to Poppler. This binds the
reported source SHA-256 to the exact bytes extracted even if the original path
changes concurrently. The snapshot is removed after extraction.

The JSON result and metadata record the source SHA-256, extraction engine,
`ocr_used: false`, per-page provenance, page/word/character/line statistics,
meaningful text coverage, and artifact hashes. Without `--out-dir`, the default
is an input-derived path under `tmp/pdf-to-markdown/`.

Meaningful coverage requires at least one page containing three alphanumeric
words and 12 alphanumeric characters, and at least five words and 24
alphanumeric characters across the document. Whitespace, lone page numbers,
or similarly sparse glyphs therefore fail before artifact creation with stable
code `text_layer_missing` and `data.reason: "ocr_required"`.

Extracted UTF-8 text is streamed into a bounded 64 MiB buffer. Larger output
fails with stable code `pdf_text_output_too_large` and
`data.max_output_bytes: 67108864`. Use a separate, explicitly chosen OCR tool
for scanned PDFs; this workflow will not silently change representations.
The owned `pdftotext` process uses bounded diagnostics and process-group
cancellation where supported. Cancellation and extraction failures report only
process-termination and truncation metadata; extracted text is never embedded in
error JSON.

## Browser Runtime Modes

`headed` is the default. It uses the visible, human-approved Chrome/default-profile
flow and keeps browser access behind the local daemon socket after the user has
approved Chrome remote debugging.

`headless` is for unattended agent work. It launches managed Chrome with a
cdp-owned profile, loopback-only remote debugging, and mode-specific daemon
runtime files. Headless failures never ask for `chrome://inspect` approval;
bounded repair retries return connection-class JSON with per-attempt evidence.
The `managed` seed strategy creates an empty owner-only profile.
The explicit `copy-default` strategy replaces that managed profile with a local
full-state snapshot of Chrome's default profile for developer-controlled harness
work, preserving browser-state files such as cookies, Local Storage, IndexedDB,
extensions, history, and cache in the local cdp-owned profile. Normal JSON
summaries report metadata and counts rather than copied file values, and cron
uses `managed` by default so profile snapshots are operator initiated. Configure
`browser.headless.profile_seed_strategy` and
`browser.headless.profile_refresh_after` when the managed cron block should keep
an explicit `copy-default` seed fresh on an age-gated cadence. When headless is
already running, explicit `copy-default` can stop the headless daemon, reseed,
and start headless again; headless is disposable managed agent infrastructure.

New managed launches may also use an owner-only source-compatible fingerprint
profile through `browser.headless.fingerprint_profile` or the overriding
`--fingerprint-profile <path>` flag. The bounded JSON profile requires
`userAgent`, `platform`, `vendor`, `language`, `timezone`, and viewport width
and height. cdp applies only user agent, window size, Chrome's launch-time
language flags, and the Chrome child's timezone; platform/vendor remain
compatibility fields and no
JavaScript or native navigator surface is rewritten. Paths and values stay out
of public status. A healthy running browser is never retrofitted: explicitly
stop it before launching with a changed profile.

```json
{
  "userAgent": "SyntheticDesktop/1.0",
  "platform": "SyntheticPlatform",
  "vendor": "SyntheticVendor",
  "language": "en-US",
  "timezone": "UTC",
  "viewport": {"width": 1280, "height": 800}
}
```

```bash
cdp browser mode get --json
CDP_BROWSER_MODE=headless cdp browser mode get --json
cdp --browser-mode headless browser profile seed --strategy managed --json
cdp --browser-mode headless browser profile seed --strategy copy-default --json
cdp --browser-mode headless browser profile seed --strategy copy-default --if-older-than 6h --json
cdp --browser-mode headless browser profile status --json
cdp --browser-mode headless daemon keepalive --repair --json
cdp --browser-mode headless daemon keepalive --repair --force --json
cdp --browser-mode headless --fingerprint-profile <profile.json> daemon keepalive --repair --json
cdp --browser-mode headless daemon maintenance --json
cdp doctor --check headless-security --json
```

`browser_mode` is the primary runtime selector. It chooses the headed or
headless daemon/runtime for daemon, keepalive, and browser commands. When the
user-level config or command line already selects a browser mode, agents should
not add `--connection` just to reach that mode:

```bash
cdp --browser-mode headed doctor --check browser-health --json
cdp --browser-mode headed pages --json
cdp --browser-mode headless doctor --check browser-health --json
cdp --browser-mode headless pages --json
```

`connection_mode` only describes how the selected daemon reaches Chrome:
`browser_url` or `auto_connect`. Saved named connections are advanced endpoint
or project overrides for cases with multiple browser URLs or explicit debugging
setups; they are not the normal headed/headless selector.

## Daemon Keepalive And Maintenance

`cdp daemon keepalive` is safe to run from cron or a user timer. It acquires a
mode-specific per-connection lock before any active probe, exits successfully when
another keepalive already owns that lock, and starts or repairs the selected-mode
daemon only when the selected connection is not healthy. A healthy headed tick is
a no-op: it does not activate Chrome, touch the remote-debugging preference, or
spawn another hold. Headed repair is bounded by a 20-second lease.
When a newer live hold owns the same mode-scoped runtime, an older detached
hold retires itself with a metadata-only `hold_superseded` log event;
transient endpoint failures remain retryable and the replacement runtime is
preserved.
Headless `--repair` and managed-process sweep also inventory adopted detached
holds. They reclaim only an exact cdp executable/argument match with matching
state root, mode, socket, profile, parent, and strong process-start generation;
lookalikes, PID reuse, missing or ambiguous evidence, and non-leader processes
are reported as skips and never signaled. The JSON result is metadata-only and
keeps the current daemon, Chrome, profile, socket, tabs, and connection intact.
Successful reclamation records a metadata-only `hold_reclaimed` marker, so
health excludes warnings from that retired PID while active-generation churn
still degrades health.
New daemon-hold startup remains process-group-owned until its mode-scoped
runtime socket is ready; a failed startup terminates and reaps only that
newly started hold tree before clearing its stale state, while a ready hold is
detached for continued keepalive.
Managed headless Chrome startup likewise observes its owned launcher while
waiting for `DevToolsActivePort`: an early exit is reaped and reported
immediately with its exit code and a bounded escaped stderr prefix, while the
existing launch retries remain bounded and a ready browser stays detached.
Private daemon runtime state also records an opaque process-start identity when
the host provides one. Stop verifies the PID and that identity before sending
signals or force-terminating the exact process group; a mismatch is treated as
stale state, and raw identity tokens remain out of public JSON and artifacts.
User-facing status, health, connection resolution, and browser readiness share
the same bounded identity-aware check and expose only a safe
`process_identity_state` (`running`, `process_not_running`,
`process_identity_mismatch`, or `process_identity_unavailable`). A strong-token
mismatch or unavailable probe cannot fall back to a PID-only healthy result.
Managed headless health performs the same bounded PID-plus-identity check when
the managed Chrome launch recorded a strong token: a recycled PID is reported
as an identity mismatch rather than a running browser, while legacy wall-clock
metadata remains compatible. Cdp-owned metadata locks also retain a private
process-start token when available; a mismatched owner is stale, while an
unavailable verification is kept as held/unknown so stale recovery cannot
remove a possibly live lock owner.
Legacy empty `flock` marker inspection is read-only and bounded through the
owned process boundary: exit 0 means unlocked, exit 1 means locked, and any
other exit, startup failure, cancellation, or deadline is unknown. Cron status
does not retain flock output or classify an unproven failure as a held lock.
Daemon-internal keepalive reuse, runtime-socket readiness, replacement
detection, post-launch readiness, and stop polling also verify the private
runtime process identity at each ownership-bearing decision. A timed-out stop
rechecks that identity before process-group escalation and refuses the forced
kill when verification mismatches or is unavailable; legacy runtimes without a
token retain PID-only compatibility.
Normal daemon stop also performs a final process/identity check immediately
before its interrupt. A changed, unavailable, canceled, or already-vanished
owner is not signaled, and stale cleanup remains conditional on the runtime
record still being current.
Runtime cleanup also preserves cancellation through its final filesystem
boundary: the mode-scoped runtime record is checked before removal, and the
expected socket is checked again before removal. A canceled cleanup cannot
continue into socket deletion; detached hold teardown keeps its explicit
background cleanup behavior.
The shared daemon process check carries the caller context through both PID
liveness decisions and strong identity. A canceled check is explicitly
non-running and cannot authorize cleanup, signaling, or escalation; the
context-free `ProcessRunning` and `RuntimeRunning` wrappers remain compatible.
Daemon RPC lease bookkeeping also carries the request context through lease
touches and target ownership registration/release. If a request disconnects
after creating a target, registration failure uses a separate bounded cleanup
context to close only that exact target; explicit lease recovery retains its
intentional background cleanup boundary.
Daemon RPC responses use one bounded writer. Final envelopes intentionally use
a cancellation-independent delivery context so an operation that just ended
can still return its existing error or result; a stuck peer is released by
closing only that exact RPC connection after five seconds, while normal JSON
responses remain complete.
The cdp-owned `.managed-processes.lock` uses the same private identity rule:
new lock records retain a start token when the host provides one, mismatched or
dead owners are stale, and an unavailable strong-token check is held/unknown
instead of being removed. Legacy PID-only lock records remain compatible, and
cleanup still requires the same regular file to be present.
Normal managed Chrome stop also rechecks the recorded root process-start identity
immediately before invoking its signal callback. A changed identity skips the
signal safely, while an unavailable or canceled final probe is an error; legacy
wall-clock metadata and force-recovery/descendant policy remain compatible.
Default managed-process signaling carries the active operation context through
its bounded SIGINT grace wait. Cancellation returns promptly without escalating
to leader kill; caller-provided signal callbacks retain their existing shape and
behavior.
Cron's read-only `/proc/locks` owner lookup is likewise context-aware and
bounded to a complete one-megabyte scan. Overflow, read failure, or cancellation
leaves owner attribution unknown, so a held empty flock marker is never aged or
repaired from partial evidence.
Daemon lock acquisition, stale cleanup, and cron status preserve their caller
context through private ownership inspection. If a strong process-identity
probe is canceled, the operation returns cancellation before it can remove or
replace a lock or render a stale claim; legacy `InspectLock` callers retain
their compatibility wrapper and public lock JSON remains unchanged.
Managed-browser ownership diagnostics use the same rule: browser health passes
its operation context through the strong process-identity probe, and a canceled
check remains unchecked and unowned rather than publishing a partial healthy
ownership result. Context-free callers retain the legacy
`VerifyManagedOwnership` wrapper.
Managed launch registry writes use the same caller-scoped rule: the bounded
registry-lock operation derives from `StartManagedChrome`, so pre-cancellation
and lock contention cannot publish a live record after launch observation has
stopped. Context-free callers retain the legacy
`RegisterManagedProcessLaunch` wrapper, and registry JSON remains unchanged.
Managed browser health applies the rule to its initial PID liveness probe too:
it checks the operation context before probing and preserves cancellation as a
safe non-running detail. Any following ownership inspection remains
caller-aware and cannot claim owned; no repair or signaling is triggered.
Context-free callers retain the legacy `ProcessRunning` wrapper. If a managed
wrapper/fork launcher exits while the cdp-owned profile's active loopback
DevTools endpoint remains usable, read-only health and keepalive diagnostics
may report endpoint-backed liveness with `liveness_source=debugging_endpoint`.
That fallback is profile/port-attributed when process evidence is available,
does not replace the recorded PID, and never authorizes ownership, cleanup,
repair, or signaling.
Before launch-capable Auto Heal work, cdp also requires a short
internet-reachability check and a recent awake observation. Offline ticks and
the first tick after a long wake/suspend gap return a structured
`state=environment_unavailable` skip, so they cannot activate Chrome or
request remote-debugging approval. Headed and headless repair share an
owner-only lease while the operation runs. Override the default connectivity
endpoint with `CDP_AUTO_HEAL_CONNECTIVITY_URL` when a network uses an approved
internal reachability URL.

For headed auto-connect, the managed cron task uses `--probe auto` and, on
macOS, `--macos-self-heal-approval`: it passively checks the existing runtime
first and only enters the bounded active repair path when health is not proven.
Repair starts or reuses the real daemon transport and drains only Chrome's exact
`Allow remote debugging?` sheet across all windows. The daemon becoming ready is
the transport proof; the accessibility click alone is never treated as success.
On Ubuntu/Linux the same bounded exact-title/action contract uses the embedded
AT-SPI helper and requires the distro `python3-pyatspi` package. Headless
keepalive remains fully unattended and starts or reuses the managed headless
Chrome runtime.

The managed path is available through first-class cron commands:

```bash
cdp cron status --json
cdp cron status --json | jq '{state,tasks,artifact_policy,last_cleanup,managed_processes,last_run_artifacts}'
cdp cron diff --json
cdp cron install --json
cdp cron remove --json
cdp cron heal headed --json
```

`cdp cron install --json` renders and installs the full managed
block, including mode-explicit headed daemon keepalive, the canonical
headless maintenance entry, and one daily artifact-prune task. Both per-minute
entries are intentionally short `cdp cron run <task-id>` calls. The Go runner
owns non-blocking advisory locking, exact task dispatch, environment setup, and
owner-only hard-bounded latest-run log replacement; crontab contains no inline
shell program. Overlap is a successful typed `already_running` skip. The
maintenance entry performs managed-process
sweep, resource preflight, profile seeding, daemon repair, synthetic
health-check, page cleanup, and summary artifact writes in one ordered flow.
Headed keepalive first performs a passive health check, so a healthy existing
Chrome runtime is a no-op: it does not launch another window, touch preferences,
or request native approval. Only an unhealthy runtime enters the bounded repair
path. Headed keepalive never opens provider pages, logs in, accepts provider
consent, or submits prompts. Its only scheduled UI action on macOS is the exact
native Chrome remote-debugging `Allow` button, and success still requires the
daemon transport to become ready. Other desktop platforms return a structured
placeholder until their accessibility adapter is implemented.
`browser preflight --open-readiness` and the synthetic headless
`daemon health-check` are ownership-aware: each reports bounded exact-target
cleanup metadata, and `closed` is true only after target-gone confirmation.
Crontab reads/writes and native transcription-service manager actions likewise
use bounded diagnostics and one owned process boundary; if crontab output is
too large, doctor reports the schedule as unclassified instead of treating a
partial read as an empty schedule. Managed cron and `artifacts run-managed`
children use that same owned process-tree boundary, while concurrent stdout and
stderr writes remain inside one synchronized hard-bounded latest-run log.
`--keep-open-readiness-tab` records deliberate retention. If cleanup fails,
the JSON error retains the primary readiness or health/artifact failure and a
safe exact-target recovery command; caller-owned pages are never included in
this cleanup.
Use `cdp cron diff --json` or
`cdp cron install --dry-run --json` before installing to inspect the intended
block without mutating the current crontab. Add an explicit browser mode to render
only one side:

```bash
cdp --browser-mode headed cron install --dry-run --json
cdp --browser-mode headless cron install --dry-run --json
cdp --config cdp.json cron install --dry-run --json
cdp cron install --artifact-retention 168h --max-log-size 64MiB --dry-run --json
```

When `--browser-mode headed` is explicit, cron status/install reuse the
selected headed connection's persisted browser URL if `--browser-url` and
`CDP_BROWSER_URL` are absent. This keeps a configured headed deployment stable
across scheduled invocations; an auto-connect connection remains URL-less.

The default artifact policy is exactly 168 hours of history and 64 MiB per
managed active log. Configure it with `artifacts.retention` and
`artifacts.max_log_size`, or override it with cron/prune flags:

```json
{
  "artifacts": {
    "retention": "168h",
    "max_log_size": "64MiB"
  }
}
```

Manual cleanup is a non-mutating plan unless `--apply` is explicit:

```bash
cdp artifacts prune --older-than 168h --max-log-size 64MiB --dry-run --json
cdp artifacts prune --older-than 168h --max-log-size 64MiB --apply --json
```

Cleanup is code-allowlisted. It can remove old timestamped health/maintenance
runs, rotated or legacy managed logs, and stale atomic-write temporaries. It
retains boundary/future-dated artifacts, current summaries, active runtime
state, browser profiles, connections and page selections, process registries,
sockets, locks, unknown names, symlinks, and custom output directories outside
the canonical state root. `cron status` and the scheduled-tasks doctor check
expose the effective policy and the latest prune result, including reclaimed
bytes and failures.

Use explicit `--browser-mode` for scheduled maintenance and cleanup so headed and
headless browser records cannot be confused. Verify the current Linux user's
scheduled tasks with:

```bash
cdp cron status --json
cdp cron diff --json
cdp cron install --json
cdp doctor --check scheduled-tasks --json
```

For unattended troubleshooting, start with the dry-run maintenance contract and
then inspect cron status artifacts before running repair:

```bash
cdp --browser-mode headless daemon maintenance --dry-run --json
cdp --browser-mode headless daemon maintenance --stale-lock-after 1s --json
cdp --browser-mode headless daemon stop --force-managed --stale-lock-after 10m --json
cdp --browser-mode headless daemon restart --force-managed --stale-lock-after 10m --json
cdp --browser-mode headless daemon maintenance --json
```

Explicit forced recovery removes only non-project managed-headless endpoint
memory, inactive eligible headless locks, managed Chrome runtime artifacts, and
processes whose profile, debugging-port, and process-tree evidence match the
selected cdp state directory. Its JSON includes reclaimed PIDs and safety checks.

## Principles

- Agent-first help: the CLI should teach agents how to use it without source inspection.
- Machine-readable by default when asked: `--json` and `--jq` are first-class.
- Safe default-profile access: never silently expose browser data; make attachment explicit and inspectable.
- Managed headless isolation: headless mode uses a cdp-owned profile and loopback-only debugging; default-profile copying is explicit through `copy-default`, stays local, reports only metadata/counts, and should be used only for developer-controlled harness work.
- Human-in-loop auto-connect: when Chrome approval is pending, agents should inspect `cdp daemon status --json`, `cdp doctor --check daemon --json`, and logs, then stop and report the required human Allow action instead of retrying start/stop loops.
- Daemon-held browser access: browser commands route through the local daemon so the user can approve Chrome/default-profile access once and reuse that held session from short CLI invocations.
- Indexed page navigation: `cdp open URL --new-tab=false --target-index N` navigates the exact Nth page in page-only order, while `--reuse --target-index N` reuses it; workers do not consume indexes, explicit indexed misses fail closed, and the selector cannot be combined with target ID/URL/title selectors.
- Browser resource budget: page creation is guarded by a default budget of 15 headed page tabs, 25 headless page tabs, and 5 windows. Use `cdp pages --json` or `cdp doctor --check browser-budget --json` before stressful workflows; override deliberately with `--max-tabs` or `browser.resource_budget.max_tabs`. Set `--max-renderer-processes` or `browser.resource_budget.max_renderer_processes` to enable the conservative renderer guard; when the limit is enabled but CDP cannot return process telemetry, new-page work is refused. `target_resource_attribution.state` is explicitly `unavailable` because CDP does not expose a stable target-to-renderer mapping. Prefer the direct headless cleanup fix: `cdp --browser-mode headless page cleanup --created-by cdp --idle-for 30m --close --force --wait-gone --max-attempts 3 --close-concurrency 4 --max 25 --json`.
- Formal browser invariants: daemon boundary, explicit profile access, lazy discovery, bounded page creation, unambiguous target selection, conservative cleanup, and JSON error envelopes are tracked in `docs/FORMAL_INVARIANTS.md`.
- Authenticated provider state, capability truth, and exact-target cleanup are documented in `docs/AUTHENTICATED_PROVIDERS.md`.
- Progressive disclosure: high-level workflows for common debugging, raw CDP passthrough for full protocol reach.
- Persistent event observation: `cdp events stream --json` attaches one exact page session, emits ready/event/subscription/stopped JSONL records, accepts `+Method`/`-Method` commands on stdin, and detaches on normal exit paths. Concurrent streams dequeue only their own session's events, so one observer cannot consume and discard another observer's records. Before each liveness heartbeat, the stream checks its captured daemon runtime registration: a definitive readable replacement stops it with metadata-only `reason=runtime_retired`, while missing, empty, corrupt, unreadable, or insufficient legacy state remains unknown and falls through to the existing read-only `Runtime.evaluate` heartbeat. That heartbeat runs on the exact session every 15 seconds and retires after two consecutive bounded failures, so a half-open daemon/page session does not wait forever. The `--enable` flag accepts the historical Page/Network/Runtime/Log defaults plus other target CDP domains such as `DOM` and `Performance`, using exact protocol spelling. Use `--duration`, `--max-events`, or the global `--timeout` to bound unattended runs; pipe the JSONL downstream instead of using `--jq`.
- Bounded event taps: `cdp events tap --target-index 1 --match Page.loadEventFired --duration 10s --json` uses the same mutually exclusive, 1-based page selector as `events stream`, and reports the selected index in `.tap.target_index` while retaining exact-session filtering and readiness cleanup.
- Bounded network controls: `cdp network block` and `cdp network mock` accept the same mutually exclusive, 1-based page-only `--target-index`; workers do not consume indexes and successful reports add `.target_index`. Blocking clears URL rules and disables Network; mocking releases paused requests before disabling Fetch, including fail-open cleanup, while rule summaries omit response bodies and header values.
- Network capture containment: `cdp network capture --out tmp/network.local.json --json` and the equivalent WebSocket command write the full local capture to the requested artifact and return only a privacy-safe manifest with counts, timestamps, safety metadata, and artifact references. Captured URLs, headers, request/response bodies, and WebSocket frames are omitted from stdout and stderr in artifact-only mode. Without `--out`, `--json` intentionally keeps records inline; use `--redact safe` before sharing them.
- Page-bound storage selection: `storage list/get/set/delete/clear/snapshot`, `storage cookies`, `storage indexeddb`, `storage cache`, and `storage service-workers` accept the same mutually exclusive, page-only 1-based `--target-index`. Workers do not consume indexes; cookie `--url` remains a storage-scope URL and may coexist with the index; `storage diff` remains artifact-only. Indexed reports add `.target_index` as metadata without copying storage values or cache bodies into selector evidence.
- Race-safe event waiting: `cdp events wait --file tmp/events.jsonl --method Page.loadEventFired --json` reads complete historical or appended records from a byte offset, supports repeated method/content predicates, and never opens a browser connection. Pass the returned `.offset` as `--from-offset` for the next wait; it is a blocking bounded wait, not a harness-level interrupt.
- Cause-aware interaction observation: `cdp events interactions --match click,scroll --json` adapts the source `Runtime.addBinding` bridge through the daemon, installs guarded listeners on current and future documents, and emits bounded metadata-only JSONL. Persistent observers reuse the event stream's daemon-registration check and read-only 15-second exact-session heartbeat, retiring after definitive runtime replacement or two consecutive session failures instead of polling forever after target loss. It intentionally omits selection text, key values, input values, HTML, cookies, screenshots, and arbitrary binding payloads; compose it with `events wait` when a file-backed wait is useful.
- Heavy artifacts by reference: screenshots, traces, heap snapshots, and dumps should be saved to files.
- Rendered screenshot ownership: `cdp screenshot render ./diagram.html --out tmp/diagram.png --json` closes its workflow-owned page with bounded exact-target `target_gone` confirmation before session release and reports metadata-only `.cleanup` evidence; cleanup failure preserves the primary error and a safe recovery command.
- Evidence bundles by manifest: use `cdp workflow debug-bundle --out-dir tmp/debug-bundle --task-id <task> --json` to arm collectors and hard-reload an existing target with ordinary HTTP cache bypass by default, then write a public-safe bundle manifest, command log, stage log, and local-only browser artifacts by path. `--target-index N` selects an existing page in 1-based page-only order and reports `.target_index`; workers do not consume indexes, and it cannot be combined with `--url` or the other target selectors. `--url` performs one collector-armed cache-bypassing navigation instead of a navigate-plus-reload pair. Use `--reload=false --ignore-cache=false` for passive/cache-faithful observation. This never clears cookies, browser cache, web storage, IndexedDB, CacheStorage, or service workers. Raw request, console, and snapshot payloads stay out of default JSON unless `--inline-payloads` is explicitly set.
- Signed-in YouTube cookies: use `cdp --browser-mode headed workflow youtube cookies --out ~/.local/state/yt-dlp/cookies.txt --json` to hard-refresh an owned YouTube tab, export current YouTube cookies from the headed profile as an owner-only Netscape file, and close the exact workflow tab. The command requires a signed-in YouTube cookie and never includes cookie values in its result.
- Google Translate workflow: `cdp --browser-mode headed workflow google-translate --url <https-url> --target en --json` keeps the initial Translate target and any newly created `translate.goog` result target in one lifecycle, releases sessions before bounded exact-target close, and reports `google_translate_cleanup` evidence plus a forced recovery command when a target does not settle. Scanned-PDF image bursting bounds Poppler diagnostics, terminates its owned process group on cancellation where supported, and requires regular non-empty source-page artifacts before browser translation.
- Action-window response bodies: `cdp workflow action-capture` omits response bodies by default. For local debugging only, opt in with `--include network --include-bodies json,text`; use `--body-url-contains /api/` to avoid unrelated background responses. `--body-limit` bounds each included body and the JSON emits a local/private-data warning. MIME filtering and body retrieval match `network capture`.
- Existing-page workflow selection: `workflow action-capture`, `workflow console-errors`, `workflow network-failures`, and `workflow submit-search` accept the same mutually exclusive, 1-based page-only `--target-index` as the direct commands. Workers do not consume indexes; combining the index with `--target`, `--url-contains`, or `--title-contains` fails before attachment, and successful JSON reports the selected index as `.target_index`.
- Page-load selection: `workflow page-load --target-index N` selects the 1-based existing page in `cdp pages` order and reports `.target_index`; workers do not consume indexes, and the index is mutually exclusive with the other page selectors. A positional URL still creates the workflow-owned page when no existing-page selector is supplied, while combining the URL with an explicit index navigates the selected existing page.
- Rendered-extract selection: `workflow rendered-extract --target-index N` selects the 1-based existing page in `cdp pages` order and reports `.target_index`; workers do not consume indexes, the index is mutually exclusive with `--target`, `--url-contains`, and `--title-contains`, and the selected caller-owned page is never closed. A positional URL alone keeps workflow-owned page creation, while a positional URL combined with an explicit index navigates and extracts from the selected existing page.
- Diagnostic workflow selection and cleanup: `workflow verify`, `workflow perf`, and `workflow a11y` accept `--target-index N` with the same 1-based page-only ordering, while `workflow page-load` follows the same selector contract. With an index, the URL is optional: no URL observes the caller-owned page in place, while a positional URL navigates that page before bounded collection. JSON reports `.target_index`; without an index, URL-only workflow-owned page creation remains unchanged. URL-created diagnostic pages report top-level `.cleanup` metadata and are closed with bounded exact-target `target_gone` confirmation before attached-session release; indexed pages report `skipped=true` and `reason=caller_owned`. If cleanup fails, the stable cleanup error includes the primary workflow/artifact error when one exists plus an exact recovery command.
- Workflow-owned collection cleanup: URL-owned feeds, visible-posts, Hacker News, Reddit, X, LinkedIn, and arXiv collectors close only the exact page they created, settle that close with bounded retries on success and error exits, and preserve the original collection error. The Reddit/X `--keep-open` lease remains explicit; successful source-collection JSON reports `created_page`, `closed`, and `close_error` cleanup metadata.
- Keep-open promotion safety: workflow-created pages are initially disposable; if `--keep-open` lease promotion fails, cdp closes only that just-created target with bounded exact-target settlement, preserves the stable `lease_target_policy_failed` error, and exposes primary-policy, close, and recovery metadata.
- Lighthouse process and artifact safety: `workflow lighthouse` remains attached to daemon-owned loopback Chrome, bounds external stdout/stderr, terminates its owned process group on cancellation where supported, and reports success only with validated regular non-empty JSON and HTML artifacts; report bytes remain file-backed and redaction-aware.
- Transcription converter process safety: Bing, Claude, Gemini, and Microsoft 365 keep their existing output bounds and direct provider replay, while all default ffmpeg conversions use one shared owned-process runner so cancellation terminates only that converter's process group where supported. Converter output and diagnostics remain out of public evidence.
- Shared rendered-workflow cleanup: rendered-extract, Google Maps directions, and YouTube cookie workflows close only their exact owned target, confirm `target_gone` with bounded retry evidence before reporting `closed`, and run fallback cleanup before releasing the attached session. Caller-owned rendered-extract pages and explicit keep-open leases remain retained.
- Collector readiness: long-running `events tap`, `console`, `network`, and `workflow page-load` collectors accept `--ready-file`. The parent directory must already be caller-owned and not group/world writable; cdp exclusively creates a 0600, value-safe readiness record only after exact-target attach and requested domain enables succeed, then removes only that same file on exit. External navigation should wait for this handshake instead of treating PID liveness as readiness.

## Development

```bash
make verify
make install
make e2e-installed
```

The public-web extraction lane is intentionally opt-in and is not part of
default CI. It exercises the installed binary against 12 real documentation
URLs and requires every URL to pass by default:

```bash
make install
make e2e-web-research-live-installed
CDP_E2E_BROWSER_MODE=headed make e2e-web-research-live-installed
```

Headless runs use an isolated temporary daemon. Headed runs only reuse an
already-approved, usable headed daemon and never start, repair, or stop it; do
not browse in that Chrome session while the headed check is running. Override
the corpus with `CDP_E2E_URL_FILE` containing 10–20 unique, fragment-free
HTTP(S) URLs. `CDP_E2E_MIN_SUCCESS` may relax a diagnostic run but cannot be
lower than 10; the default remains the full corpus size. Failed runs retain
their temporary evidence path, while successful evidence is removed unless
`CDP_E2E_KEEP_ARTIFACTS=1`. A run retries the full corpus once when every
reported page failure is explicitly retryable and no infrastructure failure was
reported; the first report is retained as `initial-report.json`. Retained
`run-metadata.json` and `evidence.json` record the exact URL set, browser mode,
UTC timestamps, requested/final URLs, readiness outcomes, and failure classes.
The real-site lane is diagnostic; deterministic synthetic tests remain the
merge gate.

`make install` copies the binary to `$(HOME)/.local/bin` by default. Override
with `PREFIX=/usr/local` or another install prefix.

Supported `make build`, `make install`, and cross-build paths inject a semantic
version, the full source commit, a reproducible RFC3339 source timestamp, and
the clean/dirty source state. Agents can verify the installed binary matches the
checkout with:

```bash
cdp --version
cdp version --json | jq --arg head "$(git rev-parse HEAD)" \
  '.verified and .commit == $head'
```

`cdp --version` and `cdp -V` are plain-text aliases for `cdp version` and
print the same verifiable build provenance.

Direct `go build` or `go install` remains available for development, but those
binaries intentionally report `verified: false`, `provenance: unverified`, and
placeholder source metadata because the supported linker contract was bypassed.

Individual checks:

```bash
make test
make vet
make build
```

Or directly:

```bash
go test ./...
go vet ./...
go build ./cmd/cdp
```

## Prior Art

- Chrome DevTools MCP: https://github.com/ChromeDevTools/chrome-devtools-mcp
- Chrome DevTools Protocol: https://chromedevtools.github.io/devtools-protocol/
- Rodney: https://github.com/simonw/rodney
- Rod: https://github.com/go-rod/rod
