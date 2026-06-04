# Formal Browser Workflow Invariants

These invariants define the safety and liveness rules for cdp-cli browser workflows. They are intentionally executable: each row names the owning code, existing coverage, and the next check to add when the invariant changes.

## Safety Invariants

| Invariant | Owner | Existing coverage | Next checks |
| --- | --- | --- | --- |
| Browser commands route through the daemon runtime; short CLI invocations must not bypass the selected-mode daemon socket to dial Chrome directly, even when managed headless metadata records a Chrome endpoint. | `internal/cli/page_commands.go`, `internal/cli/protocol_commands.go`, `internal/daemon/runtime.go` | `TestPagesUsesRunningDaemonByDefaultJSON`, `TestHeadlessPagesRequireDaemonEvenWithManagedMetadata`, daemon runtime tests | Add a negative test for stale socket/connection mismatch returning a structured connection error. |
| Default-profile access is explicit and recoverable; diagnostics must not silently trigger repeated approval prompts. | `internal/cli/daemon_commands.go`, `internal/daemon/status.go` | daemon status and doctor tests | Add a status test proving approval-pending output includes human recovery commands and no active probe by default. |
| Page and target listing stay lazy; discovery may read target metadata but must not attach to pages. | `internal/cli/page_commands.go`, `internal/cdp/targets.go` | `TestEvalExactTargetIDSkipsTargetListing`, page/target JSON tests | Add explicit `pages`/`targets` tests that fail if `Target.attachToTarget` is called. |
| Page creation is bounded by tab/window budget; at the configured limit is already a hard stop. | `internal/cdp/budget.go`, `internal/cli/open_commands.go` | `TestOpenRefusesOverBudgetJSON`, budget package tests | Add a window-limit boundary test and document any future policy change from `>=` to `>`. |
| Target prefix selection must never attach to an ambiguous page. | `internal/cli/page_commands.go` | `TestEvalAmbiguousTargetPrefixFailsBeforeAttach` | Add similar coverage for page action commands if target resolution forks. |
| Cleanup is conservative by default: selected, visible, and attached pages are retained unless an explicit close/force path applies. | `internal/cli/page_commands.go` | `TestPageCleanupJSON` | Add selected-page retention coverage and forced targeted cleanup coverage. |
| JSON failures use stable envelopes with `ok=false`, `code`, `err_class`, `message`, and remediation when useful. | `internal/cli/errors.go`, `internal/cli/schema_catalog.go` | root/schema/error tests, browser workflow tests | Add table coverage for daemon/browser command errors that currently flow through generic errors. |
| Heavy or private browser data is written as artifact references, not embedded in default JSON output. | workflow and artifact command files, `internal/artifacts` | debug bundle, screenshot, memory tests, artifact safety redaction tests | Extend the shared artifact safety scan to future HAR, trace, heap, and workflow transcript artifacts. |
| Screenshot capture metadata must be enough to reproduce framing decisions without embedding image data in JSON. | `internal/cli/artifact_commands.go`, `internal/cdp/page.go`, responsive workflow files | screenshot artifact tests, render screenshot tests, screenshot preset metadata tests, tile coverage tests, tile manifest tests | Add stitched-image tests only if a future workflow combines tiles into one image. |
| Network interception workflows must release every paused request before command exit. | future request blocking/mocking commands, `internal/cdp` event loop helpers | none yet | Add timeout and cancellation tests that prove blocked/mocked sessions disable interception and continue or fail paused requests. |
| Streamed trace and body artifacts must stay bounded, close protocol handles, and emit safety metadata. | future HAR/body/trace commands, `internal/artifacts` | network capture body limits, perf workflow artifact tests, artifact scanner truncation tests | Add synthetic stream tests for `IO.read` EOF, max bytes, scanner results, and handle cleanup. |
| Isolated browser contexts must be explicitly created, reported, and disposed without changing default-profile command behavior. | future context workflow commands, `internal/cdp/page.go`, `internal/cdp/targets.go` | target rows expose `browser_context_id`; open/page tests cover default behavior | Add forced-error tests proving created contexts are disposed or returned with recovery commands. |
| Workflow transcripts must preserve ordered evidence while referencing artifacts by path and redacting private payloads. | future transcript helpers, workflow command files, `internal/artifacts` | debug bundle artifact-list tests and artifact safety scans | Add transcript schema tests and synthetic leak scans for safe transcript mode. |
| Debug-bundle diffs must be deterministic and summarize private sections instead of copying full browser payloads. | future debug-bundle diff helpers, `internal/cli/workflow_debug_bundle.go` | debug bundle fixture tests, storage diff pattern | Add identical-input, partial-bundle, and missing-section tests. |
| Browser runtime mode and connection mode remain distinct; `browser_mode` may be headed/headless while `connection_mode` remains browser_url/auto_connect. | `internal/config/config.go`, `internal/cli/root.go`, `internal/cli/connection_commands.go` | browser mode resolver tests, `TestConnectionResolveIncludesBrowserModeJSON`, schema/E2E checks | Add migration coverage if future config files introduce more browser modes. |
| Managed headless profile state is cdp-owned and owner-only. The default `managed` seed creates an empty profile; the explicit `copy-default` seed may stop a live headless daemon, replace the profile with a local full-state snapshot of Chrome's default profile, then heal headless. It preserves browser-state files in local cdp-owned state while omitting only root Chrome runtime artifacts needed for launch correctness. `copy-default` must not be cron default, must not point the managed profile at the live default profile, and must not embed copied file values in default JSON summaries or repo artifacts. | `internal/browser/managed.go`, `internal/cli/browser_commands.go`, `internal/cli/cron_commands.go` | managed profile tests, full-state copy-default fixture tests, browser profile command tests, cron tests, headless-security doctor tests | Add copy-default quiescence or platform-encryption diagnostics if future validation shows copied profile files are present but unusable for expected authenticated workflows. |
| Headless remote debugging endpoints must be loopback-only and held behind the daemon boundary. | `internal/browser/managed.go`, `internal/cli/daemon_commands.go`, `internal/cli/info_commands.go` | loopback endpoint tests, managed keepalive tests, headless-security doctor tests | Add OS process-start verification before strengthening owned-process signaling. |
| Headed and headless runtime artifacts must not collide: runtime files, sockets, logs, keepalive locks, selections, and cleanup records are mode-scoped. | `internal/daemon/runtime.go`, `internal/state/state.go`, `internal/cli/page_commands.go` | runtime path tests, keepalive lock tests, page selection/cleanup mode tests | Add a full coexistence E2E once live Chrome coverage can run both modes in one fixture. |
| Extension workflows must classify unsupported or experimental protocol support before mutating browser state. | future extension command files, `internal/cli/protocol_commands.go` | protocol discovery and compat tests | Add fixture tests for missing `Extensions.*` methods and local-path redaction. |
| Frame-scoped execution must fail ambiguous frame selection before evaluating user code. | future frame resolver helpers, `internal/cli/frame_commands.go`, eval/query commands | frame listing tests and target ambiguity tests | Add call-count tests proving `Runtime.evaluate` is not called for ambiguous frames. |

## Liveness Properties

| Property | Owner | Existing coverage | Next checks |
| --- | --- | --- | --- |
| `daemon keepalive` either reaches a ready selected-mode runtime or fails with bounded, structured recovery information. Headless keepalive may create/reuse managed Chrome before daemon hold. | `internal/cli/daemon_commands.go`, `internal/browser/managed.go`, `internal/daemon/runtime.go` | daemon keepalive tests, managed metadata persistence tests | Add stale runtime/socket cleanup coverage on failed start. |
| Once the daemon runtime is ready, page commands can make progress through the RPC loop without requiring source inspection. | `internal/daemon/runtime.go`, `internal/cli/page_commands.go` | installed E2E, daemon runtime tests | Add RPC envelope tests for invalid request, missing method, timeout, and cancellation. |
| Protocol metadata remains usable when live `/json/protocol` is unavailable, and fallback source is labeled. | `internal/cdp/protocol.go`, `internal/cli/protocol_commands.go` | protocol command tests | Add an explicit live-unavailable fallback test with source labeling. |
| Rendered extraction waits for useful/stable content but remains bounded by timeout and artifact limits. | `internal/cli/workflow_rendered_extract.go` | rendered extraction tests | Add timeout-path coverage with partial artifact metadata. |
| Workflow compatibility checks remain useful before execution by naming required protocol methods, source, severity, and fallback commands. | `internal/cli/protocol_commands.go`, future workflow requirement registry | protocol compat tests and installed E2E schema checks | Extract compatibility requirements into a table and test every registered workflow. |

## Verification Loop

Run the repo gate after changing any invariant owner:

```bash
make verify
make install
make e2e-installed
```

For browser-facing changes, also exercise the installed binary against the live/synthetic browser path:

```bash
cdp --help
cdp doctor --json
cdp doctor --check headless-security --json
cdp pages --json
make e2e-demo-installed
```
