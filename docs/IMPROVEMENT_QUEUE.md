# Improvement Queue

This queue turns current backlog, Chrome DevTools MCP research, CDP docs, HN
signal, and GitHub issue signal into concrete implementation candidates. Items
marked `planned` may be represented in help as `not_implemented`, but only if
the behavior is stable and covered by E2E checks.

## Recently Verified

- Daemon-backed auto-connect status, command routing, target/page listing,
  screenshots, console capture, raw protocol discovery, and raw protocol exec
  all pass `make verify`, installed E2E, and smoke checks.
- Cross-agent layout is normalized: `AGENTS.md` is canonical, compatibility
  instruction/skill paths are relative symlinks, and Copilot instructions point
  back to `AGENTS.md`.

## Near-Term Queue

1. Implement `cdp page reload`.
2. Implement `cdp page back`.
3. Implement `cdp page forward`.
4. Implement `cdp page close`.
5. Implement `cdp page activate`.
6. Add `cdp wait text <needle>`.
7. Add `cdp wait selector <css>`.
8. Add `cdp text <selector>` as a compact visible-text command.
9. Add `cdp html <selector>` with truncation and artifact fallback.
10. Add `cdp dom query <selector>` with tag, text, role, rect, href, and uid.
11. Add `cdp css inspect <selector>` for computed style and box data.
12. Add `cdp layout overflow` for text/container overflow diagnostics.
13. Implement target-scoped `cdp network` request capture.
14. Add `cdp workflow console-errors`.
15. Add `cdp workflow network-failures`.
16. Add `cdp workflow verify <url>`.
17. Add `cdp workflow debug-bundle` with console, network, snapshot,
    screenshot, page metadata, and artifact references.
18. Add `cdp protocol examples <Domain.method>`.
19. Add `cdp doctor --capabilities`.
20. Add schema catalog entries for every new JSON shape.
21. Split `internal/cli/commands.go` into focused files without changing
    behavior.
22. Add a public-safe artifact redaction check for bundles, traces, and logs.
23. Add page-selection memory for the last explicitly selected target.
24. Add project-scoped default page selection.
25. Add `--include-url` and `--exclude-url` filters for page/target commands.
26. Add bounded daemon event buffering for console and network events.
27. Add `cdp daemon logs` with redaction and no page content by default.
28. Add typed error codes for invalid JSON, artifact failures, and unsupported
    browser capabilities instead of generic `usage` or `internal`.
29. Add viewport emulation.
30. Add media/color-scheme emulation.
31. Add user-agent emulation.
32. Add geolocation emulation.
33. Add network throttling presets.
34. Add CPU throttling.
35. Add click/fill/type/press/hover input commands.
36. Add dialog observe/accept/dismiss commands.
37. Add file upload.
38. Add frame listing and `--frame` selection.
39. Add accessibility-tree snapshots.
40. Add screenshot device presets.
41. Add full-page screenshot tiling for very tall pages.
42. Add HAR export.
43. Add request/response body artifact saving.
44. Add WebSocket frame observation.
45. Add request blocking.
46. Add response mocking.
47. Add performance trace start/stop.
48. Add performance insight summaries for LCP, CLS, long tasks, and blocking
    requests.
49. Add Lighthouse wrapper with report artifacts.
50. Add JS heap snapshot artifact capture.
51. Add CPU and heap metric probes.
52. Add storage inspection with redaction.
53. Add storage clear with explicit confirmation.
54. Add ServiceWorker inspection and unregister/reload workflows.
55. Add extension list/reload/action support where Chrome permits it.
56. Add isolated browser context support for safe test flows.
57. Add replayable workflow transcripts that reference artifact paths.
58. Add comparison/diff support for two debug bundles.

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
