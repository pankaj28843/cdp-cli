# cdp-cli Agent Guide

cdp is an agent-oriented Chrome DevTools Protocol CLI. It keeps browser
ownership, target discovery, connection state, and cleanup behind a local
daemon so commands stay small, inspectable, and safe to compose.

This guide is bundled with the installed binary. Use cdp guide --path when a
tool should read it as a file, or cdp guide --json when a structured result is
more convenient.

## First contact

Start with the command contract and runtime health:

    cdp --help
    cdp describe --json
    cdp doctor --json
    cdp browser mode get --json
    cdp --browser-mode headless daemon status --json

Use --browser-mode headed for an existing user-approved Chrome session and
--browser-mode headless for the managed unattended runtime. Browser-facing
commands go through the daemon; do not open a separate page WebSocket or ask a
human to approve a headed prompt during an unattended run.

## Sense, act, verify

Sense the page before acting, choose the narrowest command, then sense again to
prove the result. A successful command return is not proof that the page
changed.

    cdp pages --json
    cdp locator find Save --by text --json
    cdp click Save --strategy auto --wait-text Saved --json
    cdp snapshot --selector body --limit 50 --json

Prefer stable semantic locators and exact target IDs or URL filters. Use
--target-index only when page-only ordering is the intended contract. For a
visible split control whose center point is not the hit target, explicit
--strategy dom may use measured related pointer-event pseudo-element evidence;
an unrelated overlay remains blocked, including with --force. auto and
raw-input keep their trusted-input requirements.

The same explicit 1-based page selector works across direct lifecycle commands:
`page select`, `page reload`, `page back`, `page forward`, `page activate`, and
`page close`. It is mutually exclusive with each command's ID, URL, title, or
positional selector, and invalid or out-of-range values fail before mutation:

    cdp page select --target-index 2 --json
    cdp page reload --target-index 2 --json
    cdp page activate --target-index 2 --json

Ordinary screenshots use the same page-only selector. The selected target is
reported as `.target` and `.target_index`, while image bytes remain a local
artifact path. With `--navigate`, an indexed screenshot navigates that existing
page; without a target selector, the historical `--navigate` behavior still
creates a new tab:

    cdp screenshot --target-index 2 --out tmp/page.png --json
    cdp screenshot --target-index 2 --navigate 'https://example.com' --wait 2s --out tmp/page.png --json

Page-bound storage commands use the same mutually exclusive, 1-based page
selector. This includes Web Storage, cookies, IndexedDB, Cache Storage, and
Service Workers; workers do not consume page indexes. Cookie `--url` is the
cookie scope and may be combined with `--target-index`, while `--target`,
`--url-contains`, and `--title-contains` cannot be combined with it:

    cdp storage list --target-index 2 --include localStorage,sessionStorage --json
    cdp storage cookies list --target-index 2 --url 'https://example.com' --json
    cdp storage indexeddb dump app settings --target-index 2 --page-size 100 --json
    cdp storage cache list --target-index 2 --cache app-cache --json
    cdp storage service-workers list --target-index 2 --json

The existing-page workflows use that same page-only selector:

    cdp workflow action-capture --target-index 2 --action press:Enter --selector 'input[name=q]' --json
    cdp workflow console-errors --target-index 2 --wait 2s --json
    cdp workflow network-failures --target-index 2 --wait 2s --json
    cdp workflow submit-search 'Search' 'agentic engineering' --submit none --target-index 2 --json

Workers do not consume indexes. Each workflow rejects an index combined with
an explicit target, URL filter, or title filter before attaching, and reports
the selected index as `target_index` in JSON.

Indexed storage reports add `.target_index` without copying storage values into
selector evidence. Snapshot artifacts retain their existing redaction controls;
IndexedDB dump artifacts retain their local-record warning. `storage diff` only
compares two artifact paths and intentionally has no browser target selector.

Browser-backed `stop-state classify` uses the same page-only 1-based selector:

    cdp stop-state classify --target-index 2 --json

Supplying `--text`, `--title`, or `--url` keeps classification browser-free;
an explicit `--target-index` is rejected for those offline inputs rather than
being ignored. Indexed page classification reports `.target` and
`.target_index` while retaining the existing bounded stop-state summary.

Wait on an observable condition rather than a fixed sleep:

    cdp wait selector main --timeout 10s --json
    cdp wait text Ready --timeout 10s --json
    cdp wait load-state domcontentloaded --json

For a longer observation session, start one daemon-backed event stream and
redirect its JSONL output to an owner-local file. Then use the browser-free
event waiter; it reads history from a byte offset and also wakes when a
complete record is appended. The returned offset is the cursor for the next
wait, so an event is not matched twice:

    cdp events stream --target-index 1 --match Page.loadEventFired,Network.loadingFailed --json > tmp/events.jsonl &
    cdp events wait --file tmp/events.jsonl --method Page.loadEventFired --timeout 20s --json
    cdp events wait --file tmp/events.jsonl --from-offset 123 --method Network.loadingFailed --contains /api/ --print-offset --json
    cdp events interactions --target-index 1 --match click,scroll --duration 30s --max-events 50 --json > tmp/interactions.jsonl
    cdp events tap --target-index 1 --match Page.loadEventFired,Network.loadingFailed --duration 10s --max-events 50 --json

The stream and tap collectors keep the historical defaults of Page, Network,
Runtime, and Log. Their --enable flag also accepts other target-scoped CDP
domains using protocol spelling, for example DOM or Performance:

Both collectors accept the same mutually exclusive, 1-based page
`--target-index` selector. The bounded tap reports the selected value in
`.tap.target_index`; omit it to keep using target ID, URL, or title selectors.

    cdp events stream --enable DOM,Performance --match DOM.documentUpdated,Performance.metrics --json
    printf '+DOM.documentUpdated\n' | cdp events stream --enable page --json

Each generic domain is enabled as Domain.enable on the exact attached target
session. Browser-scoped domains, invalid identifiers, and domains that do not
expose an enable method fail explicitly; they are never reported as active.
Use cdp protocol domains --json and cdp protocol describe Domain.event --json
to confirm the live browser surface before relying on a less common domain.

Use repeated `--method` flags for any-of method matching and repeated `--contains`
flags for all-of line matching. cdp events wait accepts cdp-cli stream records
and raw CDP event records, ignores incomplete final lines until their newline,
and never opens a browser connection. It is a bounded blocking wait, not a
harness-level Monitor interrupt; subscribe to failure events as well as the
success event you expect.

For the interaction causes ordinary CDP events miss, use the source-inspired
binding bridge:

    cdp events interactions --target <target-id> --match click --max-events 1 --json

The observer attaches through the daemon, registers a unique `Runtime` binding,
installs a guarded listener for the current document and future documents, and
emits `ready`, `interaction`, and `stopped` JSONL records. Supported kinds are
`click`, `scroll`, `selectionchange`, and `keydown`; `--match` filters them.
The result is intentionally sanitized: it includes only event kind and safe
metadata such as coordinates, modifiers, scroll position, selection state, and
coarse target metadata. It never returns selection text, key values, input
values, HTML, cookies, screenshots, or raw page-controlled binding payloads.
Use `--duration`, `--max-events`, or the global `--timeout` to bound it. The
observer removes current listeners, the future-document script, and the
binding before detaching.

## JSON and errors

Use --json for automation and --jq for a narrow projection. Inspect the schema
before writing durable orchestration:

    cdp schema click --json
    cdp click Submit --json | jq '{ok,action,click,actionability}'

Errors have stable codes, classes, messages, and remediation commands. Read
the exact failed check and state before retrying. Preserve page artifacts as
paths; do not put screenshots, page text, cookies, headers, tokens, or traces
into shared logs or committed fixtures.

## Observation and raw CDP

Use cdp events stream for bounded JSONL observation with isolated event
subscriptions. Use cdp protocol search, cdp protocol examples, and cdp
protocol exec when a focused CDP escape hatch is more appropriate than a
high-level wrapper. The daemon remains the browser boundary for both paths.
For target-scoped raw execution, `--target-index N` selects the 1-based page
target order used by `cdp pages`; it is mutually exclusive with
`--target`, `--url-contains`, `--title-contains`, and
`--target-type`. Omit it to preserve browser-scoped execution, or use the
other selectors when a non-page target is intended:

    cdp protocol exec Runtime.evaluate --target-index 2 --params '{"expression":"document.title","returnByValue":true}' --json

`cdp page close --target-index N` uses the same page-only order for a bounded
disposable-tab close. It is mutually exclusive with `--target`,
`--url-contains`, and `--title-contains` on the close command:

    cdp page close --target-index 2 --wait-gone --json

The same explicit page selector is available on the core observation commands
`eval`, `observe`, `text`, `html`, and `snapshot`. A successful indexed result
includes `target_index` and the resolved `target`; the index remains 1-based
and worker targets are excluded just as they are from `cdp pages`:

    cdp eval 'document.title' --target-index 2 --json
    cdp observe --target-index 2 --json
    cdp text body --target-index 2 --json
    cdp html body --target-index 2 --json
    cdp snapshot --selector body --target-index 2 --json

Each command rejects `--target-index` together with `--target`,
`--url-contains`, or `--title-contains`; zero, negative, and out-of-range
indexes fail before page attachment.

The same explicit page selector is available on the read-only inspection
commands `frames`, `locator find`, `dom query`, `css inspect`, `layout
overflow`, and `a11y tree`, `a11y find`, `a11y node`, and `a11y snapshot`.
Successful indexed reports include `target_index` beside the resolved target;
the same invalid, conflicting, and out-of-range checks happen before
inspection:

    cdp frames --target-index 2 --json
    cdp locator find Save --target-index 2 --json
    cdp dom query button --target-index 2 --json
    cdp css inspect main --target-index 2 --json
    cdp layout overflow --target-index 2 --json
    cdp a11y tree --target-index 2 --json
    cdp a11y snapshot --target-index 2 --json

Performance and memory diagnostics use the same page-only selector:
`perf summary`, `memory counters`, and `memory heap-snapshot`. The heap
snapshot command keeps its existing local artifact-only output contract:

    cdp perf summary --target-index 2 --duration 5s --json
    cdp memory counters --target-index 2 --json
    cdp memory heap-snapshot --target-index 2 --out tmp/page.heapsnapshot --json

Bounded console and network observers use the same page-only selector:
`console`, `network`, `network capture`, and `network websocket`. Successful
indexed reports include `target_index` beside the resolved target. Network
artifacts retain their existing local-output and redaction rules; selecting a
target by index does not broaden what is captured or persisted:

    cdp console --target-index 2 --wait 2s --json
    cdp network --target-index 2 --wait 2s --json
    cdp network capture --target-index 2 --redact safe --wait 20s --json
    cdp network websocket --target-index 2 --redact safe --wait 20s --json

These observers reject zero, negative, out-of-range, and selector-conflicting
indexes before page attachment, and the index follows the page-only order
shown by `cdp pages`, excluding workers and other non-page targets.

The bounded network controls use the same page-only selector. `network block`
clears its URL rules and disables Network on exit; `network mock` releases every
paused request before disabling Fetch, including fail-open cleanup after an
action error. An indexed control reports `.target_index` but does not create,
close, or persist a page, and rule summaries never include mock bodies or
header values:

    cdp network block --target-index 2 --pattern '*://*/analytics/*' --duration 10s --json
    cdp network mock --target-index 2 --rule '{"url_pattern":"*://*/api/config","status":200,"body":"{\"enabled\":true}","max_matches":1}' --duration 10s --json

These controls reject zero, negative, out-of-range, and selector-conflicting
indexes before Network/Fetch interception is enabled. Workers do not consume
indexes; use `--target`, `--url-contains`, or `--title-contains` when an
explicit non-index selector is more appropriate.

Event-oriented waits use the same page-only selector for `request`, `response`,
`network-idle`, `dialog`, `file-chooser`, `popup`, and `download`. Indexed
success reports include `target_index` beside the resolved target:

    cdp wait response --target-index 2 --match-url /api --status 200 --json
    cdp wait dialog --target-index 2 --type confirm --action dismiss --json

For popup and download waits, the index selects the opener or triggering page;
Target and Browser events remain browser-scoped, so keep the existing URL,
title, filename, or state criteria when more than one page may produce an event.
Zero, negative, out-of-range, and selector-conflicting values fail before
attachment or event waiting.

Page-condition waits (`text`, `selector`, `url`, `locator`, `eval`, and
`load-state`) accept the same page-only selector. Indexed success reports
include `target_index` beside the resolved target, while eval keeps its
existing retry, stop-state, and attempt-artifact behavior:

    cdp wait text Ready --target-index 2 --json
    cdp wait eval 'window.__rendered === true' --target-index 2 --json
    cdp wait load-state load --target-index 2 --json

The index is validated before attachment and follows the page order shown by
`cdp pages`; zero, negative, out-of-range, and selector-conflicting values
remain typed usage or target-selection errors.

The full assertion family accepts the same page-only selector: `assert value`,
`text`, `url`, `title`, `count`, `attribute`, `class`, `focused`, `css`,
`role`, `name`, `aria-snapshot`, `attached`, `detached`, `visible`, `hidden`,
`in-viewport`, `enabled`, `disabled`, `editable`, `readonly`, `checked`,
`unchecked`, and `indeterminate`. An indexed report includes `target_index`
beside the selected page target and retains the assertion's locator, polling,
retry, and bounded failure diagnostics:

    cdp assert text Ready --target-index 2 --timeout 5s --json
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

Control-state and selection actions `focus`, `clear`, `check`, `uncheck`, and
`select` use the same mutually exclusive, page-only `--target-index`. Indexed
reports include `target_index` while preserving the commands' existing
mutation, locator, actionability, trial, auto-scroll, verification, and cleanup
contracts:

    cdp focus 'input[name=email]' --target-index 2 --json
    cdp clear 'input[name=email]' --target-index 2 --json
    cdp check 'Subscribe to newsletter' --by label --target-index 2 --json
    cdp uncheck 'Subscribe to newsletter' --by label --target-index 2 --json
    cdp select Plan pro --by label --target-index 2 --json

Zero, negative, out-of-range, and selector-conflicting indexes fail before page
attachment or form-control mutation; worker targets do not change page order.

Local file actions use the same page-only selector. `file` resolves an
`input[type=file]` and assigns a local path; detached `file chooser` accepts
either `--target` or `--target-index` to identify the page that emitted the
target-scoped backend node. Indexed success and bounded failure reports include
`target_index`, while path/basename/count metadata is retained and file
contents remain omitted:

    cdp file 'Upload file' tmp/upload.txt --by label --target-index 2 --json
    cdp file chooser 247 tmp/first.epub tmp/second.epub --target-index 2 --json

On `file`, the index is mutually exclusive with `--target`, `--url-contains`,
and `--title-contains`; on `file chooser`, it is mutually exclusive with
`--target`. Invalid, conflicting, and out-of-range indexes fail before page
attachment or file mutation, and workers do not change page order.

Form inspection uses the same page-only selector. `form values` lists visible
controls (or all controls with `--include-hidden`), while `form get` returns
one CSS-selected control. Indexed reports include `target_index`; invalid,
conflicting, and out-of-range indexes fail before page attachment:

    cdp form values --target-index 2 --json
    cdp form get 'input[name=email]' --target-index 2 --json

The index is mutually exclusive with `--target`, `--url-contains`, and
`--title-contains`, and workers do not change page order. Stable output
contracts are available with `cdp schema form-values --json` and
`cdp schema form-get --json`.

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
by a new action command cannot assume ownership of the pending dialog.

Pointer and viewport actions `hover`, `drag`, and `scroll` accept the same
mutually exclusive page-only selector. Indexed reports include
`target_index` beside the selected page while preserving pointer actionability,
offscreen auto-scroll, trial, dispatch, alignment, and viewport evidence:

    cdp hover 'Save changes' --by role --role button --target-index 2 --json
    cdp drag '.draggable' 10 20 --target-index 2 --json
    cdp scroll '#results' --target-index 2 --block center --json

`hover` and `drag` retain visible/stable/hit-test checks, optional `--trial`
and `--force` behavior, and bounded auto-scroll re-check. `scroll` retains its
attached/stable checks and non-mutating `--trial` mode. Invalid or
selector-conflicting indexes fail before page attachment or pointer/scroll
mutation; worker targets do not change page order.

## Target-scoped emulation

All direct emulation commands use the same mutually exclusive, page-only
`--target-index`: `viewport`, `clear`, `media`, `color-scheme`, `user-agent`,
`geolocation`, `timezone`, `locale`, `cpu`, and `network`. The 1-based index
follows the page order shown by `cdp pages`, excludes workers, and is resolved
before attachment. Indexed reports include `target_index` beside the selected
page while retaining each command's existing CDP payload, same-session
verification, cleanup command, and best-effort clear behavior:

    cdp emulate viewport --preset mobile --target-index 2 --json
    cdp emulate media --prefers-color-scheme dark --target-index 2 --json
    cdp emulate timezone --timezone-id UTC --target-index 2 --json
    cdp emulate locale --locale de-DE --target-index 2 --json
    cdp emulate cpu --rate 2 --target-index 2 --json
    cdp emulate network --preset slow-3g --target-index 2 --json
    cdp emulate clear --target-index 2 --json

The index cannot be combined with `--target`, `--url-contains`, or
`--title-contains`. Invalid, conflicting, and out-of-range values fail before
page attachment or emulation mutation; use the cleanup command in an indexed
report when a later command must address the same exact page.

## Page-load existing-page selection

`workflow page-load --target-index N` selects an existing page using the
1-based page order shown by `cdp pages`; workers and other non-page targets do
not consume indexes. The selected index is reported as `target_index` beside
the metadata-only target row. Zero, negative, out-of-range, and conflicting
index values fail before attachment or collector enablement:

    cdp workflow page-load --target-index 2 --wait 10s --json
    cdp workflow page-load https://example.com --target-index 2 --wait 10s --json

The index is mutually exclusive with `--target`, `--url-contains`, and
`--title-contains`. A positional URL without an existing-page selector keeps
the existing workflow-owned `about:blank` creation and navigation behavior;
adding an explicit index selects an existing page and preserves the current
positional-URL navigation behavior. Collector bounds, reload/cache policy,
storage-key privacy, content-state classification, and artifact paths are
unchanged.

## Diagnostic workflow existing-page selection

`workflow verify`, `workflow perf`, and `workflow a11y` accept
`--target-index N` to inspect an existing page using the 1-based page order
shown by `cdp pages`; workers and other non-page targets do not consume
indexes. The positional URL is optional only when an explicit index is
provided:

    cdp workflow verify --target-index 2 --wait 2s --json
    cdp workflow perf --target-index 2 --wait 5s --trace tmp/perf.json --json
    cdp workflow a11y --target-index 2 --wait 5s --json
    cdp workflow verify https://example.com --target-index 2 --wait 2s --json

With no URL, the selected page is observed in place. With a URL, the selected
page is navigated before collection. Both forms preserve the caller-owned
page and add `target_index` to JSON evidence. Without an index, a URL remains
required and keeps the workflow-owned disposable-page behavior. Invalid and
out-of-range indexes fail before attachment or collector/trace setup.

## Responsive-audit existing-page selection

`workflow responsive-audit --target-index N` reuses an existing page for each
configured viewport. The index is 1-based, follows the page order shown by
`cdp pages`, and excludes workers and other non-page targets. The positional
URL is optional with an explicit index:

    cdp workflow responsive-audit --target-index 2 --viewports desktop,mobile --include layout --wait 0s --json
    cdp workflow responsive-audit https://example.com --target-index 2 --viewports desktop,mobile --json

Without a URL, the workflow uses the selected page's current URL. With a URL,
it navigates that same caller-owned page before each viewport pass. Indexed
reports include `target_index` and the resolved target; the caller-owned page
is never closed. URL-only invocation retains the workflow-owned disposable
page behavior and closes the created page after the audit. Emulation cleanup,
collector bounds, and screenshot artifact references are unchanged. Invalid
and out-of-range indexes fail before attachment.

## Rendered-extract existing-page selection

`workflow rendered-extract --target-index N` selects an existing page using
the 1-based page order shown by `cdp pages`; workers and other non-page targets
do not consume indexes. The selected index is reported as `target_index` beside
the metadata-only target row, and an indexed extraction reuses the caller-owned
page without closing it:

    cdp workflow rendered-extract --target-index 2 --wait 10s --settle 0s --json
    cdp workflow rendered-extract https://example.com --target-index 2 --out-dir tmp/rendered-indexed --json

The index is mutually exclusive with `--target`, `--url-contains`, and
`--title-contains`; invalid, conflicting, and out-of-range values fail before
attachment. A positional URL without an existing-page selector keeps the
workflow-owned `about:blank` creation and cleanup behavior. Adding an explicit
index selects an existing page, preserves positional-URL navigation, and
retains caller-owned cleanup semantics.

## Debug-bundle existing-page selection

`workflow debug-bundle --target-index N` selects an existing page using the
1-based page order shown by `cdp pages`; workers and other non-page targets do
not consume indexes. The selected index is reported as `target_index`, and the
bundle keeps the selected caller-owned page in place while retaining its
default cache-bypassing reload behavior:

    cdp workflow debug-bundle --target-index 2 --since 5s --out-dir tmp/debug-bundle --json
    cdp workflow debug-bundle --target-index 2 --reload=false --ignore-cache=false --json

The index is mutually exclusive with `--target`, `--url-contains`, and
`--title-contains`, and cannot be combined with workflow-created `--url`.
Invalid, conflicting, and out-of-range values fail before attachment or
collector/artifact work. Without an index, `--url` retains its existing
workflow-owned creation and navigation behavior; indexed existing-page output
retains the current redaction and artifact-reference controls.

## Lifecycle and safety

Keep created tabs attributable with task/run metadata and close disposable
targets after verification. Use cdp page cleanup --json for a dry-run and a
narrow, explicit filter before any close operation. Check daemon health and
resource budgets before parallel work.

URL-owned collectors settle their disposable page on success and error exits:
`workflow feeds`, `workflow visible-posts`, the main `workflow hacker-news`
workflow, `workflow hacker-news collect`, `workflow reddit collect/posts`,
`workflow x collect`, `workflow linkedin collect`, and `workflow arxiv collect`.
Cleanup is bounded and restricted to the exact page each command created;
`--keep-open` is the explicit debugging lease for the workflows that expose it.
A failed collection remains the primary error even if cleanup also reports a
problem, while successful source-collection JSON reports its cleanup outcome.

Rendered extraction, Google Maps directions, and YouTube cookie workflows use
the same exact-target cleanup guard. Their cleanup result reports bounded
attempt and `target_gone` evidence, and `closed` is true only after the target
has disappeared from target listing. Cleanup runs before attached-session
release on fallback exits; caller-owned rendered-extract pages remain retained.

Provider workflows must report capability and authentication uncertainty
explicitly. Do not capture or replay credentials outside the repository's
documented owner-only provider contracts. CAPTCHA solving, token replay, and
challenge bypass are not general browser capabilities; stop or report an
explicit handoff when a genuine challenge is encountered.

## Installed-build proof

When validating behavior users will run, install the exact checkout and use
the binary from PATH:

    make verify
    make install
    cdp version --json
    make e2e-installed

For browser-facing changes, also run the synthetic installed browser loop. A
managed build should report its commit, verification state, and clean/dirty
source state so the tested binary is unambiguous.
