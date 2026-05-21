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
runtime files. The managed seed strategy creates an empty profile; it does not
copy cookies, saved passwords, payment data, autofill, browsing history, request
headers, tokens, or arbitrary files from a default Chrome profile.

```bash
cdp browser mode get --json
CDP_BROWSER_MODE=headless cdp browser mode get --json
cdp --browser-mode headless browser profile seed --strategy managed --json
cdp --browser-mode headless browser profile status --json
cdp --browser-mode headless daemon keepalive --repair --json
cdp doctor --check headless-security --json
```

`browser_mode` is separate from `connection_mode`: the browser mode is `headed` or
`headless`, while the connection mode remains `browser_url` or `auto_connect`.
Browser commands in both modes route through the daemon; short commands do not
fall back to dialing Chrome directly.

## Daemon Keepalive

`cdp daemon keepalive` is safe to run from cron or a user timer. It acquires a
mode-specific per-connection lock before any active probe, exits successfully when
another keepalive already owns that lock, and starts or repairs the selected-mode
daemon only when the selected connection is not healthy.

```cron
* * * * * flock -n $HOME/.cdp-cli/locks/keepalive-headed.lock env DISPLAY=:0 XDG_RUNTIME_DIR=/run/user/$(id -u) $HOME/.local/bin/cdp --browser-mode headed daemon keepalive --auto-connect --repair --display :0 --json >> $HOME/.cdp-cli/keepalive-headed.log 2>&1
* * * * * flock -n $HOME/.cdp-cli/locks/keepalive-headless.lock $HOME/.local/bin/cdp --browser-mode headless daemon keepalive --repair --json >> $HOME/.cdp-cli/keepalive-headless.log 2>&1
*/5 * * * * flock -n $HOME/.cdp-cli/locks/headless-health.lock CDP_BIN=$HOME/.local/bin/cdp CDP_LOG_DIR=$HOME/.cdp-cli bash /path/to/cdp-cli/scripts/cdp-headless-healthcheck.sh >> $HOME/.cdp-cli/headless-health.log 2>&1
0 */6 * * * flock -n $HOME/.cdp-cli/locks/headless-profile-seed.lock $HOME/.local/bin/cdp --browser-mode headless browser profile seed --strategy copy-default --if-older-than 6h --json >> $HOME/.cdp-cli/profile-seed-headless.log 2>&1
* * * * * flock -n $HOME/.cdp-cli/locks/page-cleanup-headless.lock $HOME/.local/bin/cdp --browser-mode headless page cleanup --created-by cdp --idle-for 30m --close --max 10 --json >> $HOME/.cdp-cli/page-cleanup-headless.log 2>&1
```

Use explicit `--browser-mode` for scheduled cleanup so headed and headless page
records cannot be confused. The headless health-check script validates keepalive,
health telemetry, navigation, DOM text, JavaScript evaluation, and screenshots; it
writes JSON artifacts under `$HOME/.cdp-cli/headless-health/` and creates a
feature-request candidate after repeated failures. Verify the current Linux user's scheduled tasks with:

```bash
cdp doctor --check scheduled-tasks --json
crontab -l | grep cdp
```

## Principles

- Agent-first help: the CLI should teach agents how to use it without source inspection.
- Machine-readable by default when asked: `--json` and `--jq` are first-class.
- Safe default-profile access: never silently expose browser data; make attachment explicit and inspectable.
- Managed headless isolation: headless mode uses a cdp-owned empty profile and loopback-only debugging; default-profile copying is deferred unless a separate security review promotes it.
- Human-in-loop auto-connect: when Chrome approval is pending, agents should inspect `cdp daemon status --json`, `cdp doctor --check daemon --json`, and logs, then stop and report the required human Allow action instead of retrying start/stop loops.
- Daemon-held browser access: browser commands route through the local daemon so the user can approve Chrome/default-profile access once and reuse that held session from short CLI invocations.
- Browser resource budget: page creation is guarded by a default budget of 15 page tabs and 5 windows. Use `cdp pages --json` or `cdp doctor --check browser-budget --json` before stressful workflows; cleanup should prefer mode-explicit `cdp --browser-mode headless page cleanup --created-by cdp --idle-for 30m --close --json`.
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
