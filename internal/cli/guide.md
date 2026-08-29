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

The stream and tap collectors keep the historical defaults of Page, Network,
Runtime, and Log. Their --enable flag also accepts other target-scoped CDP
domains using protocol spelling, for example DOM or Performance:

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

## Lifecycle and safety

Keep created tabs attributable with task/run metadata and close disposable
targets after verification. Use cdp page cleanup --json for a dry-run and a
narrow, explicit filter before any close operation. Check daemon health and
resource budgets before parallel work.

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
