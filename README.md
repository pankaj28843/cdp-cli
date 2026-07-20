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
are in place.

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
cdp protocol search screenshot --json
cdp protocol examples Page.captureScreenshot --json
cdp protocol exec Browser.getVersion --json
cdp protocol exec Runtime.evaluate --target <target-id> --params '{"expression":"document.title","returnByValue":true}' --json
cdp protocol exec Page.captureScreenshot --target <target-id> --params '{"format":"png"}' --save tmp/page.png --json
```

Multi-engine SERP research runs engines concurrently and reuses one workflow tab
lane per engine, so large query files avoid opening a fresh tab for every
engine/query/page combination.

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
tasks replace owner-only latest-run logs through a hard-bounded writer before
child output opens the target; they do not append indefinitely. The maintenance entry performs managed-process
sweep, resource preflight, profile seeding, daemon repair, synthetic
health-check, page cleanup, and summary artifact writes in one ordered flow.
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
- Progressive disclosure: high-level workflows for common debugging, raw CDP passthrough for full protocol reach.
- Heavy artifacts by reference: screenshots, traces, heap snapshots, and dumps should be saved to files.
- Evidence bundles by manifest: use `cdp workflow debug-bundle --out-dir tmp/debug-bundle --task-id <task> --json` to arm collectors and hard-reload an existing target with ordinary HTTP cache bypass by default, then write a public-safe bundle manifest, command log, stage log, and local-only browser artifacts by path. `--url` performs one collector-armed cache-bypassing navigation instead of a navigate-plus-reload pair. Use `--reload=false --ignore-cache=false` for passive/cache-faithful observation. This never clears cookies, browser cache, web storage, IndexedDB, CacheStorage, or service workers. Raw request, console, and snapshot payloads stay out of default JSON unless `--inline-payloads` is explicitly set.
- Collector readiness: long-running `events tap`, `console`, `network`, and `workflow page-load` collectors accept `--ready-file`. The parent directory must already be caller-owned and not group/world writable; cdp exclusively creates a 0600, value-safe readiness record only after exact-target attach and requested domain enables succeed, then removes only that same file on exit. External navigation should wait for this handshake instead of treating PID liveness as readiness.

## Development

```bash
make verify
make install
make e2e-installed
```

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
