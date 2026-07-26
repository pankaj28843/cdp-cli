# cdp-cli Architecture

`cdp` is a shell-first Chrome DevTools Protocol CLI for coding agents. The
architecture is intentionally small: keep browser protocol mechanics in
`internal/cdp`, local connection memory in `internal/state`, daemon lifecycle in
`internal/daemon`, and command composition in `internal/cli`.

## Design Rules

- Agent-visible behavior is the product. Every command needs clear help, stable
  JSON, jq-friendly fields, and actionable recovery commands.
- Browser access is explicit. Default-profile auto-connect requires user
  approval. Default command output and lifecycle state must not persist cookies,
  headers, screenshots, traces, page text, or private profile data. A concrete
  authenticated-provider package may persist its explicitly validated replay
  credential template only under owner-only cdp state; those values never enter
  recovery/admission files, normal JSON output, logs, or repository artifacts.
- Browser runtime mode is the primary user-facing selector. `browser_mode`
  chooses the runtime (`headed` or `headless`) for daemon, keepalive, and browser
  commands; `connection_mode` only describes how that daemon reaches Chrome
  (`browser_url` or `auto_connect`). Named connections are advanced endpoint or
  project overrides, not the normal headed/headless selector.
- Browser commands use the daemon as their only CDP entry point. The daemon owns
  the approved browser WebSocket and local RPC socket; short CLI invocations
  route through that socket instead of dialing Chrome directly.
- Authenticated provider workflows use an exact-target transaction. They check
  resource budget before one target creation, durably record ownership and
  `action_pending` before submission, classify dispatch as `performed`,
  `not_performed`, or `unknown`, and close only the recorded target.
- Every headed workflow that can activate a tab or dispatch browser input uses
  the canonical owner-only `<state-dir>/locks/headed-browser-input.lock`.
  Browserflow acquires it before target creation, an ask releases it immediately
  after its one raw-input Send, and exact target close is the fallback release.
  This lease is browser-wide and distinct from provider admission: two
  different providers must not race the same visible Chrome input surface.
- Provider concurrency and cooldown are cross-process policy. One owner-only
  admission lease serializes a provider. Ordinary minimum spacing waits inside
  the caller's existing context, releases/reacquires the state lock, and
  rechecks atomically; a hard provider cooldown still fails immediately.
  A disappeared active mutation and any released unknown outcome remain
  quarantined regardless of elapsed spacing. Only exact settled browserflow
  evidence, or the exact owner-only direct-action record for a replay with no
  browser target, plus explicit `--acknowledge-unknown` resolution permits
  future new work; orphaned read-only runs may be safely abandoned.
  Spacing/cooldown evidence is persisted without prompts, answers, cookies,
  headers, tokens, or raw captures.
- Headless mode launches Chrome with a cdp-owned managed profile under a
  non-default owner-only user data dir and loopback remote debugging. The
  default `managed` seed creates an empty profile. The explicit `copy-default`
  maintenance strategy replaces that managed profile with a local full-state
  snapshot of Chrome's default profile for developer-controlled harness work;
  if headless is live, the CLI may stop the headless daemon and owned managed
  Chrome, reseed, then heal headless. Copied browser-state files stay in local
  cdp-owned state and are not committed or embedded in default JSON summaries.
- Page listing stays lazy. Use browser target metadata for discovery; attach to
  a page only when a page-scoped command actually needs it.
- Heavy outputs are artifacts. Screenshots, traces, heap snapshots, HAR files,
  and debug bundles are written to files and referenced by path in JSON.
- Retention is allowlist-only. Automatic cleanup is confined to registered
  cdp-generated historical classes under the canonical state root, uses strict
  age boundaries plus hard active-log caps, never follows symlinks or crosses a
  filesystem boundary, and always retains browser profiles, runtime metadata,
  connections/page selections, registries, sockets, locks, current summaries,
  unknown paths, and external custom output directories.
- Raw CDP is a first-class escape hatch. High-level commands should cover common
  workflows, but agents must be able to discover and execute current protocol
  methods without waiting for wrappers.
- Refactors preserve behavior. Structural changes should keep tests and E2E
  output stable, then feature changes can build on the cleaner shape.

## Package Boundaries

| Package | Owns | Must Not Own |
| --- | --- | --- |
| `cmd/cdp` | Binary entry point and build metadata wiring | Browser logic |
| `internal/cli` | Cobra commands, output shaping, error envelopes | Raw WebSocket protocol loops |
| `internal/cdp` | CDP transport, target/page helpers, protocol metadata | CLI flag policy |
| `internal/artifacts` | Retention planning/execution, path and filesystem safety, atomic bounded managed logs | Browser/profile state discovery |
| `internal/admission` | Cross-process provider serialization, spacing, and cooldown state | Provider mechanics, browser access, or private request data |
| `internal/browserflow` | Exact target leases, shared headed-input lease, crash recovery journal, phase transitions, and tri-state irreversible-action dispatch | Provider selectors, Cobra, output rendering, or direct Chrome dialing |
| `internal/webagent` | Stable provider operation envelope, capability catalog, and concrete provider packages below it | Cobra, direct Chrome dialing, sibling-provider imports, or universal selector/workflow DSLs |
| `internal/browser` | Browser endpoint probing, auto-connect endpoint resolution, managed headless profile metadata and launch helpers | CLI output policy |
| `internal/daemon` | Mode-specific keepalive runtime files, sockets, logs, process status, runtime client | User-facing command formatting |
| `internal/state` | Disk-backed connection metadata and mode-scoped page selection | Browser/page content |
| `internal/output` | JSON, compact JSON, jq filtering | Command semantics |

Concrete provider packages live at `internal/webagent/<provider>`. They may use
the provider-neutral browserflow and CDP capabilities passed to them, but they
must not import `internal/cli`, another provider, the browser/daemon runtime, or
raw WebSocket transport. `internal/cli` constructs commands and renders the
stable envelope; it does not own provider selectors or lifecycle policy.

Provider credential templates live below
`<state-dir>/webagent/<provider>/` with `0700` directories and `0600` regular
files. Atomic state replacement and cross-process file locking come from
`internal/artifacts`; admission and browserflow journals store lifecycle facts
only. Claude derives its organization/list shape from a successful exact-session
headed request, then tries that stable HTTP shape once before a narrowly typed
rendered fallback. Gemini intentionally remains rendered-only: its owner state
contains safe auth booleans and observed runtime controls, not cookies or a
private request template. Its conversation list progressively advances the
unique rendered history scroller until the requested identity count or a stable
bottom, so the first partially hydrated batch is not accepted. Grok derives its
conversation-list request from the exact headed session, separately observes the
sanitized `/rest/modes` catalog, and uses the stable response-node plus
load-responses detail sequence after one visible Send. All three providers use
fresh-conversation asks and can refine an unknown raw delete click only from a
persisted same-target redirect postcondition. Gemini validates post-Send prompt
identity by temporarily intercepting the exact rendered `Copy prompt` control
inside its owned target; the system clipboard is never written, and the captured
prompt is hashed after outer trim only, so interior whitespace remains
identity-significant.

Architecture fitness tests mechanically reject Cobra outside `internal/cli`,
outward browserflow/admission dependencies, provider cross-imports, direct
Chrome discovery/dial tokens in policy packages, and provider use of raw
target/input methods that bypass the instrumented workflow boundary.

## Browser Runtime Modes

`headed` is the default runtime mode. It preserves the existing visible Chrome
flow: a human-approved default-profile browser session is held by the daemon, and
short commands talk to the daemon RPC socket.

`headless` is a daemon-held managed runtime for unattended agents. The CLI creates
cdp-owned local runtime state, launches Chrome with `--headless`,
`--remote-debugging-port=0`, and `--user-data-dir` pointing at the managed
profile, then validates the resulting endpoint is loopback-only. The managed
profile can be empty (`managed`) or intentionally replaced with an operator-run
full-state default-profile snapshot (`copy-default`). Managed status and doctor
output expose metadata such as pid, mode, profile path, seed strategy, copied
file count, and debugging port, but not ownership internals or copied file
values. Headless tabs created by cdp workflows are self-managed and disposable;
cron cleanup may force-close stale cdp-created headless pages.

Managed headless scheduled work is centralized through `daemon maintenance`.
The cron renderer installs one headless maintenance task that runs managed
process reconciliation, resource preflight, profile seed freshness checks,
daemon keepalive repair, synthetic health-check, page cleanup, and summary
artifact writes as ordered phases. Phase results stay inside the maintenance
phase array; top-level cron status links to task definitions, managed process
lifecycle state, and recent artifact paths for troubleshooting.

Managed process health is generation-scoped. The current owned generation is
identified independently of PID reuse; terminal exited/superseded generations
are diagnostic history and are never connection-probed as current dependencies.
Reconciliation holds the registry lock, atomically replaces the owner-only
regular registry, retains at most eight terminal generations younger than 24
hours, preserves active/indeterminate/unknown records, and reports compaction
and skip counts separately from current health.

Cron owns the lifecycle of its output. Headed keepalive and headless maintenance
run through a latest-run writer with an independent hard byte cap while already
holding their task lock, so the target log is bounded before child output opens.
A separately locked daily task applies the shared retention plan at most once a
day. Manual dry-run and apply use the same immutable plan; apply revalidates the
observed path, size, timestamp, root, symlink, and filesystem assumptions before
mutation and continues across independent candidate failures.

Runtime artifacts are mode-specific so headed and headless can coexist: headed
keeps the historical singleton paths, while headless uses its own runtime file,
socket, log, keepalive lock, selected page, and cleanup scope. Daemon lookup,
cleanup, and page selection must always resolve against the selected browser
mode before considering named connection overrides.

## Validation Contract

Every shipped improvement must pass:

```bash
make verify
make install
make e2e-installed
```

Browser-facing changes also need the synthetic live-site check:

```bash
make e2e-demo-installed
```

Then exercise the installed binary like an agent:

```bash
cdp --help
cdp version --json
cdp describe --json | jq '.commands.children | map(.name)'
cdp doctor --json
cdp daemon status --json
```

If Chrome is unavailable, commands should return classified JSON errors with
safe remediation commands. That is still a valid E2E signal.

## Capability Direction

Chrome DevTools MCP and the DevTools Protocol point to these durable areas:

- Navigation and target control: list, open, select, reload, close, back,
  forward, bring to front, and wait.
- Debugging evidence: JavaScript eval, console messages, page text snapshots,
  screenshots, DOM details, CSS/layout inspection, and debug bundles.
- Network evidence: request listing, failure summaries, response-body artifacts,
  HAR export, WebSocket events, blocking, and mocking.
- Emulation and input: viewport, media, user agent, geolocation, network/CPU
  throttling, click/fill/type/press/hover, dialogs, and uploads.
- Performance and memory: traces, Lighthouse, Core Web Vitals summaries, long
  tasks, heap snapshots, CPU metrics, and storage/service worker inspection.

Source references:

- Chrome DevTools MCP active-session flow:
  https://developer.chrome.com/blog/chrome-devtools-mcp-debug-your-browser-session
- Chrome DevTools MCP tool reference:
  https://github.com/ChromeDevTools/chrome-devtools-mcp/blob/main/docs/tool-reference.md
- Chrome DevTools Protocol:
  https://chromedevtools.github.io/devtools-protocol/
