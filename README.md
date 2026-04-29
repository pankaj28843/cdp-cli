# cdp-cli

`cdp` is an agent-oriented Chrome DevTools Protocol CLI, written in Go.

The goal is a long-running local CDP process that can attach to a user-approved running Chrome session, keep that session warm, reconnect predictably, and expose browser debugging workflows through a shell interface that agents can inspect with `--help` and compose with `jq`.

## Status

Initial scaffold. The command tree, JSON/error conventions, and project layout are in place. Browser attachment and CDP execution are planned next.

## Intended Shape

```bash
cdp daemon start --auto-connect --prime --reconnect 30s --json
cdp pages --json | jq '.pages[] | {id,title,url}'
cdp snapshot --interactive-only --limit 50 --json
cdp protocol search screenshot --json
cdp protocol exec Page.captureScreenshot --params '{"format":"png"}' --save tmp/page.png --json
cdp workflow console-errors --json
```

## Principles

- Agent-first help: the CLI should teach agents how to use it without source inspection.
- Machine-readable by default when asked: `--json` and `--jq` are first-class.
- Safe default-profile access: never silently expose browser data; make attachment explicit and inspectable.
- Progressive disclosure: high-level workflows for common debugging, raw CDP passthrough for full protocol reach.
- Heavy artifacts by reference: screenshots, traces, heap snapshots, and dumps should be saved to files.

## Development

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
