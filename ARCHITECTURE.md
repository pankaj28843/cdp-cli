# cdp-cli Architecture

`cdp` is a shell-first Chrome DevTools Protocol CLI for coding agents. The
architecture is intentionally small: keep browser protocol mechanics in
`internal/cdp`, local connection memory in `internal/state`, daemon lifecycle in
`internal/daemon`, and command composition in `internal/cli`.

## Design Rules

- Agent-visible behavior is the product. Every command needs clear help, stable
  JSON, jq-friendly fields, and actionable diagnostics.
- Browser access is explicit. Default-profile auto-connect requires user
  approval. Default command output and lifecycle state must not persist cookies,
  headers, screenshots, traces, page text, or private profile data. A concrete
  authenticated-provider package may persist its explicitly validated replay
  credential template only under owner-only cdp state; those values never enter
  lifecycle files, normal JSON output, logs, or repository artifacts.
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
  action intent before submission, classify dispatch as `performed`,
  `not_performed`, or `unknown`, and close only the recorded target.
- Transcription serving is direct provider transport. Browser/CDP may observe
  an authenticated audio request during bounded discovery and refresh the
  minimum owner-only replay template, but a file request opens no target and
  drives no website controls. The provider adapter replaces audio and dynamic
  request fields, sends provider-native HTTP or WebSocket traffic, and parses a
  terminal response. Auth rejection permits one refresh and one direct retry,
  never browser transcription. See `docs/SANITIZATION.md`.
- Auth readiness is provider-neutral: observe the initial navigation, then an
  ordinary reload, then a cache-bypassing hard reload with a final grace wait.
  One overall deadline is divided across at least three stages. Missing
  evidence remains `auth_evidence_not_observed`; an observation-infrastructure
  failure remains a connection error and is never converted into a logout
  claim.
- Every headed workflow that can activate a tab or dispatch browser input uses
  the canonical owner-only `<state-dir>/locks/headed-browser-input.lock`.
  Browserflow acquires it before target creation, an ask releases it immediately
  after its one raw-input Send, and exact target close is the fallback release.
  This short browser-wide lease prevents focus-sensitive input races while
  allowing independent answer observation to overlap. No provider-wide state
  from an earlier invocation blocks a fresh request.
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
- Short-lived CLI-owned external commands use bounded stdout/stderr and an
  owned cancellation boundary. Manager and jq diagnostics must not grow
  without bound or leave a descendant holding a pipe after cancellation;
  managed cron and artifact children use the same owned process-tree boundary,
  and shared stdout/stderr log writers synchronize their hard cap. Explicit jq
  stdout remains caller-requested rather than receiving an implicit result cap.
  Partial schedule output is never treated as a complete schedule.
- Managed browser process-table probes use the same owned process boundary on
  Unix external scans and retain only a complete table within a documented
  byte budget. Overflow or probe failure is explicit; partial output and an
  empty process set are never used as a success signal.
- Native remote-debugging approval discovery and its short-lived platform
  helpers use a bounded owned process boundary as well. Complete PID/JSON
  metadata is the only retained result; helper overflow, failure, or
  cancellation is explicit and cannot be interpreted as native approval.
- Darwin Launch Services, AppleScript, and System Events action transport uses
  the same boundary with bounded script input/output. The shared System Events
  service is never broadly signaled as if it were cdp-owned; exact native
  action matching and the daemon transport proof remain unchanged.
- A headed keepalive launch remains owned through the supported window
  readiness check. A failed readiness path terminates and reaps only the
  command started by that invocation; successful readiness is the detach
  boundary, while unsupported window adapters preserve the existing behavior.
- A detached daemon hold follows the same readiness boundary: its process
  group remains owned until the mode-scoped runtime socket is ready, failed
  startup is terminated and reaped before stale state is cleared, and only a
  ready hold is detached.
- Headless repair also inventories adopted detached `cdp daemon hold`
  processes. It can reclaim only an exact executable/argument match whose
  mode-scoped state root, connection mode, socket, profile, parent, and strong
  process-start identity agree with the current runtime; every ownership and
  generation check is repeated immediately before the exact process-group
  signal. Lookalikes, PID reuse, missing metadata, non-leaders, and runtime
  replacement remain untouched, and the public result contains only PIDs and
  stable reason labels.
- Detached runtime stop is identity-aware: the private mode-scoped runtime
  records an opaque OS process-start token when available, and stop verifies
  PID plus token before signaling or force-terminating the exact process group.
  A mismatched token is stale state and an unavailable verification fails
  closed; legacy runtime files without a token retain their prior compatibility
  behavior. Tokens and raw process probes never enter public JSON or evidence.
- User-facing runtime status and readiness use one bounded identity-aware
  process check. Its safe `process_identity_state` distinguishes running,
  stopped, mismatched, and unverifiable owners; a strong-token mismatch or
  unavailable probe cannot be retried into a PID-only healthy result.
- Daemon-internal runtime reuse, socket readiness, replacement detection,
  startup readiness, and stop polling use the same private identity check.
  Stop rechecks before forced process-group escalation and refuses escalation
  when a strong identity is mismatched or unavailable; legacy records without a
  token retain PID-only compatibility.
- Normal daemon runtime stop performs a final context-aware process check after
  its initial ownership check and immediately before `os.Interrupt`. A changed,
  unavailable, canceled, or vanished owner is never signaled; only the existing
  current-runtime guard may clear stale state.
- Daemon runtime cleanup carries cancellation through the final removal boundary:
  mode-scoped runtime state is checked immediately before removal, and the
  expected runtime socket is checked again before removal. A canceled cleanup
  cannot continue into socket deletion, while detached hold teardown retains
  its explicit background cleanup boundary.
- Daemon RPC lease bookkeeping carries the request context through touch,
  target ownership registration, target release, and lease state transitions.
  If a request disconnects after creating a target, registration failure still
  uses a separate bounded cleanup context to close only that exact target;
  explicit lease recovery remains a named background cleanup boundary.
- Daemon RPC response delivery uses one bounded context-aware writer. Final
  envelopes use a cancellation-independent delivery context so an operation
  that has just ended can still return its existing error or result envelope;
  the writer closes only the exact local RPC connection when its five-second
  bound expires, while normal responses remain complete and no browser
  transport is created.
- The shared daemon `CheckRuntimeProcess` carries its caller context through
  both PID liveness decisions and the strong identity probe. A canceled check
  is explicitly non-running and cannot authorize runtime cleanup, signaling,
  or escalation; context-free `ProcessRunning` and `RuntimeRunning` wrappers
  remain compatible.
- The cdp-owned managed-process registry lock also records a private process
  identity when available. Stale-lock cleanup removes a dead or mismatched
  owner, retains an unverifiable live owner, and keeps legacy PID-only lock
  files compatible while preserving same-file replacement protection.
- Default managed-process signaling carries its operation context through the
  bounded graceful wait; cancellation returns promptly without leader-kill
  escalation, while the existing direct PID signal/kill policy and caller-owned
  callback contract remain unchanged.
- Cron's read-only `/proc/locks` owner attribution uses the status context and a
  hard total-input bound. Only a complete successful scan can return owner
  evidence; overflow, read failure, or cancellation remains unknown and cannot
  make an empty flock marker look stale.
- Managed headless health uses the same private Chrome PID-plus-token evidence
  when available. A recycled PID is reported as an identity mismatch and never
  as a running managed browser; legacy wall-clock metadata remains compatible,
  while token verification failures are explicit and fail closed. If the
  recorded launcher PID is gone, read-only health/keepalive diagnostics may
  use the cdp-owned profile's active loopback DevTools endpoint as a
  source-aligned wrapper/fork liveness fallback; profile/port attribution is
  conservative and this evidence never authorizes ownership or cleanup.
- Cdp-owned metadata locks use the same private PID-plus-token rule when the
  host provides it. A mismatched owner is stale and replaceable; an unavailable
  identity probe remains held/unknown so recovery cannot remove a possibly live
  owner, while legacy lock files retain their prior liveness behavior.
- Context-bearing lock acquisition, stale cleanup, and cron status preserve
  their caller context through ownership inspection. An interrupted strong
  identity probe returns cancellation before any stale/recovery decision;
  legacy `InspectLock` callers retain the background-context compatibility
  wrapper.
- Managed-browser ownership diagnostics likewise derive their strong identity
  probe from the health operation context. Cancellation returns unchecked,
  unowned evidence instead of publishing a partial healthy result; the legacy
  `VerifyManagedOwnership` wrapper remains available for context-free callers.
- Persistent event streams also compare their captured daemon runtime
  registration before each exact-session heartbeat. A strong current runtime
  identity is present, a strong readable replacement retires the stream, and
  missing, empty, corrupt, unreadable, or insufficient legacy state remains
  unknown so the existing heartbeat can decide liveness. The check is
  metadata-only, stays inside the daemon boundary, and remains optional for
  older or fake clients.
- Managed launch registry writes likewise derive their bounded lock operation
  from the `StartManagedChrome` caller context. Pre-cancellation and lock
  contention return before publishing a live record; the legacy
  `RegisterManagedProcessLaunch` wrapper remains available for context-free
  callers, and the registry shape is unchanged.
- Managed browser health liveness likewise checks its caller context before
  the initial PID probe and uses a context-aware daemon liveness wrapper.
  Cancellation remains a safe non-running detail; any following ownership
  inspection still uses its own caller-aware contract and cannot claim owned,
  while no repair or signal is triggered. The legacy `ProcessRunning` wrapper
  remains available for context-free callers. Endpoint fallback is bounded and
  loopback/profile-bound, and it reports safe provenance without replacing the
  recorded PID or its identity evidence.
- Concurrent workflow workers resolve browser probe options into per-call
  copies; command-level connection state is not mutated while workers are
  running.
- Raw CDP is a first-class escape hatch. High-level commands should cover common
  workflows, but agents must be able to discover and execute current protocol
  methods without waiting for wrappers. `protocol exec --target-type` also
  supports non-page targets such as workers and service workers for bounded
  diagnostics; target selection remains unique before attachment.
- Refactors preserve behavior. Structural changes should keep tests and E2E
  output stable, then feature changes can build on the cleaner shape.

## Package Boundaries

| Package | Owns | Must Not Own |
| --- | --- | --- |
| `cmd/cdp` | Binary entry point and build metadata wiring | Browser logic |
| `internal/cli` | Cobra commands, output shaping, error envelopes, and bounded owned short-lived process probes | Raw WebSocket protocol loops |
| `internal/cdp` | CDP transport, target/page helpers, protocol metadata | CLI flag policy |
| `internal/artifacts` | Retention planning/execution, path and filesystem safety, atomic bounded managed logs | Browser/profile state discovery |
| `internal/browserflow` | Exact target leases, shared headed-input lease, lifecycle journal, phase transitions, and tri-state irreversible-action dispatch | Provider selectors, Cobra, output rendering, or direct Chrome dialing |
| `internal/webagent` | Stable provider operation envelope, capability catalog, and concrete provider packages below it | Cobra, direct Chrome dialing, sibling-provider imports, or universal selector/workflow DSLs |
| `internal/browser` | Browser endpoint probing, auto-connect endpoint resolution, managed headless profile metadata, launch helpers, and bounded process ownership evidence; managed Chrome remains owned through pre-readiness launch failures, detaches only after readiness, and rechecks the recorded root identity immediately before normal stop signaling | CLI output policy |
| `internal/daemon` | Mode-specific keepalive runtime files, sockets, logs, process status, runtime client | User-facing command formatting |
| `internal/processgroup` | Context-aware execution and explicit termination of one owned external process tree, with process-group cancellation where supported and a direct-process fallback elsewhere | Browser discovery, provider policy, unbounded output retention |
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
`internal/artifacts`; browserflow journals store lifecycle facts only.
Transcription replay templates follow `docs/SANITIZATION.md`: they contain
only the minimum direct-request material, regenerate per-request fields, and
never persist audio or transcript content. Conversation workflows remain
separate from that hot path. Claude transcription replays its authenticated
dictation WebSocket with 16 kHz mono PCM and never creates a conversation.
Claude conversation reads separately
derives its organization/list shape from a successful exact-session
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
outward browserflow dependencies, provider cross-imports, direct
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

Launch-capable Auto Heal is host-gated before browser lifecycle work. The
shared `internal/availability` package records only owner-local observation
timestamps, checks short internet reachability, detects a conservative
post-wake wall-clock gap, and serializes headed/headless repair with an
owner-only lease. Offline and post-wake results are successful structured
skips; passive diagnostics and dry-run plans remain available, and no Chrome
approval action is attempted while the gate is blocked.

Managed process health is generation-scoped. The current owned generation is
identified independently of PID reuse; terminal exited/superseded generations
are diagnostic history and are never connection-probed as current dependencies.
Reconciliation holds the registry lock, atomically replaces the owner-only
regular registry, retains at most eight terminal generations younger than 24
hours, preserves active/indeterminate/unknown records, and reports compaction
and skip counts separately from current health.

Daemon-hold health is generation-scoped as well. A successful orphan
reconciliation records only a metadata-only `hold_reclaimed` PID marker, and
natural `hold_superseded` markers identify the same retired generation. Health
filters warnings from those retired PIDs while retaining warnings from the
active runtime, so cleanup cannot turn active connection churn into a false
healthy result or let an old hold poison the current generation.

Cron owns the lifecycle of its output. Headed keepalive and headless maintenance
run through a latest-run writer with an independent hard byte cap while already
holding their task lock, so the target log is bounded before child output opens.
A separately locked daily task applies the shared retention plan at most once a
day. Manual dry-run and apply use the same immutable plan; apply revalidates the
observed path, size, timestamp, root, symlink, and filesystem assumptions before
mutation and continues across independent candidate failures.

Legacy empty `flock` marker inspection is also process-owned and bounded. The
non-blocking probe treats only exit 0 as unlocked and exit 1 as locked; startup,
other exit statuses, cancellation, and deadline are unknown rather than a
false lock claim. The probe never retains flock diagnostics or mutates the
marker, and its caller context flows from cron status.

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
make e2e-transcription-live-installed
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
