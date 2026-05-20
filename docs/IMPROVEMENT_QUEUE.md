# Improvement Queue

This queue turns current backlog, Chrome DevTools MCP research, CDP docs, HN
signal, and GitHub issue signal into concrete implementation candidates. Items
marked `planned` may be represented in help as `not_implemented`, but only if
the behavior is stable and covered by E2E checks.

## Recently Verified

- Daemon-backed auto-connect status, command routing, target/page listing,
  screenshots, console/network capture, raw protocol discovery/examples/exec,
  storage controls, page control, wait/query/observe commands, input automation,
  accessibility/perf/memory probes, and emulation for viewport/media/user-agent/
  geolocation/CPU/network are covered by unit checks and installed E2E metadata.
- Cross-agent layout is normalized: `AGENTS.md` is canonical, compatibility
  instruction/skill paths are relative symlinks, and Copilot instructions point
  back to `AGENTS.md`.

## Near-Term Queue

1. Split `internal/cli/commands.go` into focused files without changing
   behavior.
2. Add a public-safe artifact redaction check for bundles, traces, storage
   dumps, heap snapshots, and logs.
3. Add screenshot device presets.
4. Add full-page screenshot tiling for very tall pages.
5. Add HAR export.
6. Add request/response body artifact saving.
7. Add WebSocket frame observation.
8. Add request blocking.
9. Add response mocking.
10. Add performance trace start/stop.
11. Add performance insight summaries for LCP, CLS, long tasks, and blocking
    requests.
12. Add Lighthouse wrapper with report artifacts.
13. Add isolated browser context support for safe test flows.
14. Add replayable workflow transcripts that reference artifact paths.
15. Add comparison/diff support for two debug bundles.
16. Add extension list/reload/action support where Chrome permits it.
17. Add frame-scoped command execution beyond frame listing.
18. Add richer protocol compatibility hints for workflows before execution.

## Research Signals

- Chrome's active-session flow makes explicit user approval and visible browser
  control indicators part of the product contract.
- The CDP docs confirm the protocol is domain-based, changes frequently at
  tip-of-tree, and exposes `/json/protocol` for the browser's current schema.
- Chrome DevTools MCP's tool reference groups useful agent capabilities into
  input, navigation, emulation, performance, network, debugging, extensions,
  and memory.
- GitHub issue signal favors lazy/scoped tab attachment, debug bundles, clearer
  agent-facing errors, and avoiding eager work on many-tab profiles.
- HN signal favors direct browser-state verification, compact evidence, and
  avoiding workflows that force agents to infer browser state from source code.

## Source Index

- Chrome DevTools MCP active-session flow:
  https://developer.chrome.com/blog/chrome-devtools-mcp-debug-your-browser-session
- Chrome DevTools MCP tool reference:
  https://github.com/ChromeDevTools/chrome-devtools-mcp/blob/main/docs/tool-reference.md
- Chrome DevTools Protocol:
  https://chromedevtools.github.io/devtools-protocol/
- Network capture across navigation request:
  https://github.com/ChromeDevTools/chrome-devtools-mcp/issues/88
- Unified debug bundle request:
  https://github.com/ChromeDevTools/chrome-devtools-mcp/issues/632
- Repeated prompt / long-session issue:
  https://github.com/ChromeDevTools/chrome-devtools-mcp/issues/1094
- Frozen/discarded tabs issue:
  https://github.com/ChromeDevTools/chrome-devtools-mcp/issues/1230
- Many-tab hang issue:
  https://github.com/ChromeDevTools/chrome-devtools-mcp/issues/1921
