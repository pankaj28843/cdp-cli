# Improvement Queue

This document is the public-safe ledger for cdp-cli improvement work. Durable
execution plans live outside this public repository as PRPs; this file only
records high-level lane status, safe plan identifiers, dependency notes, and
research sources.

## Queue Status

Status checked 2026-05-21.

Open loose queue items: **0**.

All previously listed near-term items are either implemented or assigned to an
external PRP with validation gates. Add new ideas to external feature requests
and PRPs first, then update this ledger with the public-safe plan identifier.

## Recently Verified

- Daemon-backed auto-connect status, command routing, target/page listing,
  screenshots, console/network capture, raw protocol discovery/examples/exec,
  storage controls, page control, wait/query/observe commands, input automation,
  accessibility/perf/memory probes, and emulation for viewport/media/user-agent/
  geolocation/CPU/network are covered by unit checks and installed E2E metadata.
- Network capture, WebSocket capture, and storage snapshot now share artifact
  safety metadata with redaction mode, shareability classification, unsafe
  opt-in warnings, changed sensitive fields, and synthetic leak-scan tests.
- Screenshot capture supports shared viewport presets (`desktop`, `laptop`,
  `tablet`, `mobile`, `iphone-12`) and explicit tall-page tiling with tile
  artifacts and a manifest path in JSON.
- Page cleanup dry-runs expose close intent (`would_close_count`,
  `close_required`, and follow-up commands), so agents can tell when cleanup is
  observing versus actually closing candidates.
- Cross-agent layout is normalized: `AGENTS.md` is canonical, compatibility
  instruction/skill paths are relative symlinks, and Copilot instructions point
  back to `AGENTS.md`.

## Planned Lanes

| Lane | Previous items | Status | External PRP | Next gate |
| --- | --- | --- | --- | --- |
| CLI command composition | 1 | implemented | `2026-05-21T050405-cli-command-composition-refactor.md` | Current `internal/cli/commands.go` is already only a package placeholder. |
| Artifact safety | 2 | implemented | `2026-05-20T215839-public-safe-artifact-redaction-guard.md` | Reuse `internal/artifacts` for future HAR, trace, body, heap, bundle, and transcript artifacts. |
| Screenshot ergonomics | 3-4 | implemented | `2026-05-21T002011-screenshot-presets-and-tall-page-tiling.md` | Reuse tile manifest metadata for future stitched-image workflows. |
| Network evidence artifacts | 5-7 | partial | `2026-05-21T002011-network-evidence-artifacts.md` | HAR export landed; body and WebSocket artifact modes remain. |
| Network controls | 8-9 | ready | `2026-05-21T002011-network-control-workflows.md` | Implement request blocking before Fetch-based response mocking. |
| Performance evidence | 10-12 | ready | `2026-05-21T002011-performance-trace-and-insights.md` | Add bounded trace streaming before insight and Lighthouse report wrappers. |
| Isolated browser contexts | 13 | planned | `2026-05-21T050405-isolated-browser-contexts.md` | Design daemon-backed context lifecycle and cleanup guarantees. |
| Workflow transcripts | 14 | planned | `2026-05-21T050405-replayable-workflow-transcripts.md` | Define transcript schema and safe artifact policy. |
| Debug bundle diff | 15 | planned | `2026-05-21T050405-debug-bundle-diff.md` | Define offline bundle JSON diff contract. |
| Extension support | 16 | planned | `2026-05-21T050405-extension-support.md` | Start with capability discovery and unsupported-state classification. |
| Frame-scoped execution | 17 | planned | `2026-05-21T050405-frame-scoped-execution.md` | Define explicit frame selector semantics before adding scoped execution. |
| Protocol workflow compatibility | 18 | planned | `2026-05-21T050405-protocol-workflow-compatibility.md` | Extract workflow requirement sets into a testable registry. |

## Empty Queue Policy

- Do not keep numbered backlog items in this file after they have an external
  PRP owner.
- Do not add implementation plans, local paths, screenshots, traces, logs,
  cookies, tokens, request headers, or private browser content to this public
  repo.
- For a new idea, create or update `~/feature-requests/cdp-cli/` and
  `~/plan-prps/cdp-cli/`, then add one row to `Planned Lanes`.
- A row can move to `implemented` only after `make verify`, `make install`, and
  `make e2e-installed` pass for the shipped change.

## Dependency Notes

- The artifact guard is the shared path for HAR, response bodies, WebSocket
  payloads, traces, heap snapshots, debug bundles, and workflow transcripts
  because those artifacts can contain private page state.
- Screenshot tiling should emit metadata for viewport, DPR, clip, tile count,
  stitch mode, manifest path, and output paths without embedding image bytes in
  JSON.
- `Network.getResponseBody` returns direct body text or base64 data, so HAR/body
  export must default to bounded capture, explicit redaction metadata, and local
  warnings for unsafe opt-ins.
- `Fetch.enable` pauses matching requests until continued, fulfilled, or failed.
  Request mocking needs cleanup/fail-open tests so it cannot hang a target.
- `Tracing.start` supports `ReturnAsStream`, and `IO.read` reads arbitrary
  stream chunks. Trace work should write files through bounded streaming and
  close handles rather than returning trace data in JSON.
- `Target.createBrowserContext` and `Target.disposeBrowserContext` make isolated
  context workflows possible, but lifecycle cleanup must be explicit and
  daemon-backed.
- Extension support varies by Chrome protocol version and must be capability
  checked before any mutating command is exposed.
- Frame-scoped execution must use explicit frame selection; ambiguous frame
  matches must fail before user code is evaluated.

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
