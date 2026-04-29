# cdp-cli

`cdp` is an agent-oriented Chrome DevTools Protocol CLI, written in Go.

The goal is a long-running local CDP process that can attach to a user-approved running Chrome session, keep that session warm, reconnect predictably, and expose browser debugging workflows through a shell interface that agents can inspect with `--help` and compose with `jq`.

## Status

Early implementation. The command tree, JSON/error conventions, connection
memory, browser readiness probes, target/page listing, page open/eval/snapshot
commands, screenshot artifact capture, console/log capture, raw CDP discovery,
raw CDP execution, Web Storage/cookie/IndexedDB/Cache Storage/service worker
controls, and a default-profile auto-connect keepalive daemon with local
command routing plus a cron-safe `daemon keepalive` command are in place.

## Intended Shape

```bash
cdp daemon start --auto-connect --json
cdp daemon status --json
cdp daemon keepalive --auto-connect --display :0 --json
cdp pages --json | jq '.pages[] | {id,title,url}'
cdp page select --url-contains example.com --json
cdp open https://example.com --json
cdp eval 'document.title' --json
cdp snapshot --interactive-only --limit 50 --json
cdp screenshot --out tmp/page.png --json
cdp console --errors --wait 2s --json
cdp storage indexeddb list --url-contains localhost --json
cdp storage indexeddb get app settings feature --json
cdp storage cache list --url-contains localhost --json
cdp storage cache get app-cache http://localhost:5173/api/me --json
cdp storage service-workers list --url-contains localhost --json
cdp workflow visible-posts https://x.com/<handle> --limit 5 --json
cdp protocol search screenshot --json
cdp protocol exec Browser.getVersion --json
cdp protocol exec Runtime.evaluate --target <target-id> --params '{"expression":"document.title","returnByValue":true}' --json
cdp protocol exec Page.captureScreenshot --target <target-id> --params '{"format":"png"}' --save tmp/page.png --json
```

## Daemon Keepalive

`cdp daemon keepalive` is safe to run from cron or a user timer. It acquires a
per-connection lock before any active probe, exits successfully when another
keepalive already owns that lock, and starts or repairs the daemon only when the
selected connection is not healthy.

```cron
* * * * * DISPLAY=:0 XDG_RUNTIME_DIR=/run/user/$(id -u) $HOME/.local/bin/cdp daemon keepalive --auto-connect --display :0 --json >> $HOME/.cdp-cli/keepalive.log 2>&1
```

## Principles

- Agent-first help: the CLI should teach agents how to use it without source inspection.
- Machine-readable by default when asked: `--json` and `--jq` are first-class.
- Safe default-profile access: never silently expose browser data; make attachment explicit and inspectable.
- Daemon-held browser access: browser commands route through the local daemon so the user can approve Chrome/default-profile access once and reuse that held session from short CLI invocations.
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
