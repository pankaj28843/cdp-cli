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
- Network capture, WebSocket capture, and storage snapshot now share artifact
  safety metadata with redaction mode, shareability classification, unsafe
  opt-in warnings, changed sensitive fields, and synthetic leak-scan tests.
- Screenshot capture now supports shared viewport presets (`desktop`, `laptop`,
  `tablet`, `mobile`, `iphone-12`) and emits viewport/DPR metadata in JSON.
- Page cleanup dry-runs now expose close intent (`would_close_count`,
  `close_required`, and follow-up commands), so agents can tell when a cron-safe
  cleanup is observing versus actually closing candidates.
- Cross-agent layout is normalized: `AGENTS.md` is canonical, compatibility
  instruction/skill paths are relative symlinks, and Copilot instructions point
  back to `AGENTS.md`.

## Planning Snapshot

Status checked 2026-05-21. External PRP plans exist for the next implementation
lanes; keep those plans outside this public repo and update this queue only with
public-safe plan identifiers and outcomes.

| Lane | Queue items | Status | External PRP | Gate |
| --- | --- | --- | --- | --- |
| Artifact safety | 2 | implemented | `2026-05-20T215839-public-safe-artifact-redaction-guard.md` | Reuse `internal/artifacts` for future HAR, trace, body, heap, and transcript artifacts. |
| Screenshot ergonomics | 3-4 | partial | `2026-05-21T002011-screenshot-presets-and-tall-page-tiling.md` | Presets landed; tall-page tiling still planned. |
| Network evidence artifacts | 5-7 | planned | `2026-05-21T002011-network-evidence-artifacts.md` | Can proceed using `internal/artifacts` for artifact safety metadata and scans. |
| Network controls | 8-9 | planned | `2026-05-21T002011-network-control-workflows.md` | Blocked on Fetch pause/cleanup contracts. |
| Performance evidence | 10-12 | planned | `2026-05-21T002011-performance-trace-and-insights.md` | Can proceed using `internal/artifacts` for trace/report safety metadata and scans. |
| Isolation and handoff | 13-18 | queued | none yet | Plan after the first four lanes settle. |

## Near-Term Queue

1. Split `internal/cli/commands.go` into focused files without changing
   behavior.
2. Extend the shared public-safe artifact guard from network/storage into
   bundles, traces, heap snapshots, logs, HAR, and request/response body
   artifacts.
3. Extend screenshot device presets into any remaining screenshot subcommands
   that need reproducible viewport metadata.
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

## Dependency Notes

- The artifact guard is now the required shared path for HAR, response bodies,
  WebSocket payloads, traces, heap snapshots, and debug bundles because those
  artifacts can contain private page state.
- Screenshot presets and tall-page tiling are a lower privacy risk because they
  already write image artifacts by path, but they still need artifact metadata
  that tells agents exactly which viewport, DPR, clip, tile count, and stitch
  mode were used.
- `Network.getResponseBody` returns direct body text or base64 data, so HAR/body
  export must default to bounded capture, explicit redaction metadata, and local
  warnings for unsafe opt-ins.
- `Fetch.enable` pauses matching requests until continued, fulfilled, or failed.
  Request mocking therefore needs a cleanup/fail-open contract and tests for
  timeout paths so it cannot hang a target.
- `Tracing.start` supports `ReturnAsStream`, and `IO.read` reads arbitrary
  stream chunks. Trace work should write files via bounded streaming and close
  handles rather than returning trace data in JSON.

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
