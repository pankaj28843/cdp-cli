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

## Intended Shape

```bash
cdp browser mode get --json
cdp daemon start --auto-connect --json
cdp daemon status --json
cdp doctor --check scheduled-tasks --json
cdp doctor --check browser-health --json
cdp doctor --check headless-security --json
cdp --browser-mode headed daemon keepalive --auto-connect --repair --probe passive --display :0 --json
cdp --browser-mode headless browser profile seed --strategy managed --json
cdp --browser-mode headless daemon keepalive --repair --json
cdp --browser-mode headless daemon maintenance --json
cdp pages --json | jq '.pages[] | {id,title,url}'
cdp page select --url-contains example.com --json
cdp open https://example.com --json
cdp eval 'document.title' --json
cdp observe --json
cdp wait text Ready --timeout 10s --json
cdp snapshot --selector body --limit 50 --json
cdp screenshot --out tmp/page.png --json
cdp console --errors --wait 2s --json
cdp emulate network --preset slow-3g --json
cdp emulate clear --json
cdp storage indexeddb list --url-contains localhost --json
cdp storage indexeddb get app settings feature --json
cdp storage cache list --url-contains localhost --json
cdp storage cache get app-cache http://localhost:5173/api/me --json
cdp storage service-workers list --url-contains localhost --json
cdp workflow visible-posts 'https://x.com/<handle>' --limit 5 --json
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
cdp schema webagent-operation --json
cdp protocol search screenshot --json
cdp protocol examples Page.captureScreenshot --json
cdp protocol exec Browser.getVersion --json
cdp protocol exec Runtime.evaluate --target <target-id> --params '{"expression":"document.title","returnByValue":true}' --json
cdp protocol exec Page.captureScreenshot --target <target-id> --params '{"format":"png"}' --save tmp/page.png --json
```

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

```bash
cdp browser mode get --json
CDP_BROWSER_MODE=headless cdp browser mode get --json
cdp --browser-mode headless browser profile seed --strategy managed --json
cdp --browser-mode headless browser profile seed --strategy copy-default --json
cdp --browser-mode headless browser profile seed --strategy copy-default --if-older-than 6h --json
cdp --browser-mode headless browser profile status --json
cdp --browser-mode headless daemon keepalive --repair --json
cdp --browser-mode headless daemon keepalive --repair --force --json
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
daemon only when the selected connection is not healthy.

For headed auto-connect, scheduled keepalive uses `--probe passive`: if a prior
approved daemon runtime went stale while Chrome stayed open, keepalive restarts
the daemon from that last approved endpoint without opening pages or asking for a
new prompt. Use `--probe active` only for a human-managed repair when someone can
approve Chrome if needed. Headless keepalive remains fully unattended and starts
or reuses the managed headless Chrome runtime.

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
Headed keepalive is passive: it may repair the daemon against a previously
approved endpoint, but it never opens provider pages, logs in, accepts consent,
or submits prompts. Any Chrome approval remains a human action.
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
- Browser resource budget: page creation is guarded by a default budget of 15 headed page tabs, 25 headless page tabs, and 5 windows. Use `cdp pages --json` or `cdp doctor --check browser-budget --json` before stressful workflows; override deliberately with `--max-tabs` or `browser.resource_budget.max_tabs`, and prefer the direct headless cleanup fix: `cdp --browser-mode headless page cleanup --created-by cdp --idle-for 30m --close --force --wait-gone --max-attempts 3 --close-concurrency 4 --max 25 --json`.
- Formal browser invariants: daemon boundary, explicit profile access, lazy discovery, bounded page creation, unambiguous target selection, conservative cleanup, and JSON error envelopes are tracked in `docs/FORMAL_INVARIANTS.md`.
- Authenticated provider state, capability truth, and exact-target cleanup are documented in `docs/AUTHENTICATED_PROVIDERS.md`.
- Progressive disclosure: high-level workflows for common debugging, raw CDP passthrough for full protocol reach.
- Heavy artifacts by reference: screenshots, traces, heap snapshots, and dumps should be saved to files.
- Evidence bundles by manifest: use `cdp workflow debug-bundle --out-dir tmp/debug-bundle --task-id <task> --json` to arm collectors and hard-reload an existing target with ordinary HTTP cache bypass by default, then write a public-safe bundle manifest, command log, stage log, and local-only browser artifacts by path. `--url` performs one collector-armed cache-bypassing navigation instead of a navigate-plus-reload pair. Use `--reload=false --ignore-cache=false` for passive/cache-faithful observation. This never clears cookies, browser cache, web storage, IndexedDB, CacheStorage, or service workers. Raw request, console, and snapshot payloads stay out of default JSON unless `--inline-payloads` is explicitly set.
- Signed-in YouTube cookies: use `cdp --browser-mode headed workflow youtube cookies --out ~/.local/state/yt-dlp/cookies.txt --json` to hard-refresh an owned YouTube tab, export current YouTube cookies from the headed profile as an owner-only Netscape file, and close the exact workflow tab. The command requires a signed-in YouTube cookie and never includes cookie values in its result.
- Action-window response bodies: `cdp workflow action-capture` omits response bodies by default. For local debugging only, opt in with `--include network --include-bodies json,text`; use `--body-url-contains /api/` to avoid unrelated background responses. `--body-limit` bounds each included body and the JSON emits a local/private-data warning. MIME filtering and body retrieval match `network capture`.
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
cdp version --json | jq --arg head "$(git rev-parse HEAD)" \
  '.verified and .commit == $head'
```

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
