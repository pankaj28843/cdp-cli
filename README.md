# cdp-cli

`cdp` is an agent-oriented Chrome DevTools Protocol CLI, written in Go.

The goal is a long-running local CDP process that can attach to a user-approved running Chrome session, keep that session warm, reconnect predictably, and expose browser debugging workflows through a shell interface that agents can inspect with `--help` and compose with `jq`.

## Status

Active implementation. The command tree, JSON/error conventions, connection
memory, browser readiness probes, target/page listing, page open/eval/wait/
observe/snapshot/html/DOM/CSS/layout commands, input automation, screenshots,
console and network capture, emulation for viewport/media/user-agent/
geolocation/CPU/network, accessibility/performance/memory probes, raw CDP
discovery/examples/exec, Web Storage/cookie/IndexedDB/Cache Storage/service
worker controls, headed/default-profile and managed-headless browser runtime
modes, and cron-safe `daemon keepalive` plus page cleanup commands are in place.

## Intended Shape

```bash
cdp browser mode get --json
cdp daemon start --auto-connect --json
cdp daemon status --json
cdp doctor --check scheduled-tasks --json
cdp doctor --check browser-health --json
cdp doctor --check headless-security --json
cdp --browser-mode headed daemon keepalive --auto-connect --repair --display :0 --json
cdp --browser-mode headless browser profile seed --strategy managed --json
cdp --browser-mode headless daemon keepalive --repair --json
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
cdp workflow web-research extract --url-file tmp/research/visit-urls.txt --out-dir tmp/research/pages --json
cdp protocol search screenshot --json
cdp protocol examples Page.captureScreenshot --json
cdp protocol exec Browser.getVersion --json
cdp protocol exec Runtime.evaluate --target <target-id> --params '{"expression":"document.title","returnByValue":true}' --json
cdp protocol exec Page.captureScreenshot --target <target-id> --params '{"format":"png"}' --save tmp/page.png --json
```

## Browser Runtime Modes

`headed` is the default. It uses the visible, human-approved Chrome/default-profile
flow and keeps browser access behind the local daemon socket after the user has
approved Chrome remote debugging.

`headless` is for unattended agent work. It launches managed Chrome with a
cdp-owned profile, loopback-only remote debugging, and mode-specific daemon
runtime files. The `managed` seed strategy creates an empty owner-only profile.
The explicit `copy-default` strategy replaces that managed profile with a local
full-state snapshot of Chrome's default profile for developer-controlled harness
work, preserving browser-state files such as cookies, Local Storage, IndexedDB,
extensions, history, and cache in the local cdp-owned profile. Normal JSON
summaries report metadata and counts rather than copied file values, and cron
uses `managed` by default so profile snapshots are operator initiated. When
headless is already running, explicit `copy-default` can stop the headless
daemon, reseed, and start headless again; headless is disposable managed agent
infrastructure.

```bash
cdp browser mode get --json
CDP_BROWSER_MODE=headless cdp browser mode get --json
cdp --browser-mode headless browser profile seed --strategy managed --json
cdp --browser-mode headless browser profile seed --strategy copy-default --json
cdp --browser-mode headless browser profile status --json
cdp --browser-mode headless daemon keepalive --repair --json
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

## Daemon Keepalive

`cdp daemon keepalive` is safe to run from cron or a user timer. It acquires a
mode-specific per-connection lock before any active probe, exits successfully when
another keepalive already owns that lock, and starts or repairs the selected-mode
daemon only when the selected connection is not healthy.

The managed path is available through first-class cron commands:

```bash
cdp cron status --json
cdp cron diff --json
cdp cron install --profile agent --json
cdp cron remove --json
cdp cron heal headed --json
```

`cdp cron install --profile agent --json` renders and installs the full managed
block, including mode-explicit headed healing, headless keepalive, health, profile
seeding, and page cleanup entries. Use `cdp cron diff --json` before installing to
inspect the intended block without mutating the current crontab.

Use explicit `--browser-mode` for scheduled cleanup so headed and headless page
records cannot be confused. Verify the current Linux user's scheduled tasks with:

```bash
cdp cron status --json
cdp cron diff --json
cdp cron install --profile agent --json
cdp doctor --check scheduled-tasks --json
```

## Principles

- Agent-first help: the CLI should teach agents how to use it without source inspection.
- Machine-readable by default when asked: `--json` and `--jq` are first-class.
- Safe default-profile access: never silently expose browser data; make attachment explicit and inspectable.
- Managed headless isolation: headless mode uses a cdp-owned empty profile and loopback-only debugging; default-profile copying is deferred unless a separate security review promotes it.
- Human-in-loop auto-connect: when Chrome approval is pending, agents should inspect `cdp daemon status --json`, `cdp doctor --check daemon --json`, and logs, then stop and report the required human Allow action instead of retrying start/stop loops.
- Daemon-held browser access: browser commands route through the local daemon so the user can approve Chrome/default-profile access once and reuse that held session from short CLI invocations.
- Browser resource budget: page creation is guarded by a default budget of 15 page tabs and 5 windows. Use `cdp pages --json` or `cdp doctor --check browser-budget --json` before stressful workflows; cleanup should prefer the direct headless fix: `cdp --browser-mode headless page cleanup --created-by cdp --idle-for 30m --close --force --json`.
- Formal browser invariants: daemon boundary, explicit profile access, lazy discovery, bounded page creation, unambiguous target selection, conservative cleanup, and JSON error envelopes are tracked in `docs/FORMAL_INVARIANTS.md`.
- Progressive disclosure: high-level workflows for common debugging, raw CDP passthrough for full protocol reach.
- Heavy artifacts by reference: screenshots, traces, heap snapshots, and dumps should be saved to files.

## Development

```bash
make verify
make install
make e2e-installed
```

`make install` copies the binary to `$(HOME)/.local/bin` by default. Override
with `PREFIX=/usr/local` or another install prefix.

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
