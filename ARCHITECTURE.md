# cdp-cli Architecture

`cdp` is a shell-first Chrome DevTools Protocol CLI for coding agents. The
architecture is intentionally small: keep browser protocol mechanics in
`internal/cdp`, local connection memory in `internal/state`, daemon lifecycle in
`internal/daemon`, and command composition in `internal/cli`.

## Design Rules

- Agent-visible behavior is the product. Every command needs clear help, stable
  JSON, jq-friendly fields, and actionable recovery commands.
- Browser access is explicit. Default-profile auto-connect requires user
  approval and the CLI must not persist cookies, headers, screenshots, traces,
  page text, or private profile data.
- Page listing stays lazy. Use browser target metadata for discovery; attach to
  a page only when a page-scoped command actually needs it.
- Heavy outputs are artifacts. Screenshots, traces, heap snapshots, HAR files,
  and debug bundles are written to files and referenced by path in JSON.
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
| `internal/browser` | Browser endpoint probing and auto-connect endpoint resolution | Persistent state |
| `internal/daemon` | Keepalive runtime files, process status, runtime client | User-facing command formatting |
| `internal/state` | Disk-backed connection metadata | Browser/page content |
| `internal/output` | JSON, compact JSON, jq filtering | Command semantics |

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
