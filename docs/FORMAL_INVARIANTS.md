# Formal Browser Workflow Invariants

These invariants define the safety and liveness rules for cdp-cli browser workflows. They are intentionally executable: each row names the owning code, existing coverage, and the next check to add when the invariant changes.

## Safety Invariants

| Invariant | Owner | Existing coverage | Next checks |
| --- | --- | --- | --- |
| Browser commands route through the daemon runtime; short CLI invocations must not bypass the approved daemon socket to dial Chrome directly. | `internal/cli/page_commands.go`, `internal/daemon/runtime.go` | `TestPagesUsesRunningDaemonByDefaultJSON`, daemon runtime tests | Add a negative test for stale socket/connection mismatch returning a structured connection error. |
| Default-profile access is explicit and recoverable; diagnostics must not silently trigger repeated approval prompts. | `internal/cli/daemon_commands.go`, `internal/daemon/status.go` | daemon status and doctor tests | Add a status test proving approval-pending output includes human recovery commands and no active probe by default. |
| Page and target listing stay lazy; discovery may read target metadata but must not attach to pages. | `internal/cli/page_commands.go`, `internal/cdp/targets.go` | `TestEvalExactTargetIDSkipsTargetListing`, page/target JSON tests | Add explicit `pages`/`targets` tests that fail if `Target.attachToTarget` is called. |
| Page creation is bounded by tab/window budget; at the configured limit is already a hard stop. | `internal/cdp/budget.go`, `internal/cli/open_commands.go` | `TestOpenRefusesOverBudgetJSON`, budget package tests | Add a window-limit boundary test and document any future policy change from `>=` to `>`. |
| Target prefix selection must never attach to an ambiguous page. | `internal/cli/page_commands.go` | `TestEvalAmbiguousTargetPrefixFailsBeforeAttach` | Add similar coverage for page action commands if target resolution forks. |
| Cleanup is conservative by default: selected, visible, and attached pages are retained unless an explicit close/force path applies. | `internal/cli/page_commands.go` | `TestPageCleanupJSON` | Add selected-page retention coverage and forced targeted cleanup coverage. |
| JSON failures use stable envelopes with `ok=false`, `code`, `err_class`, `message`, and remediation when useful. | `internal/cli/errors.go`, `internal/cli/schema_catalog.go` | root/schema/error tests, browser workflow tests | Add table coverage for daemon/browser command errors that currently flow through generic errors. |
| Heavy or private browser data is written as artifact references, not embedded in default JSON output. | workflow and artifact command files | debug bundle, screenshot, memory tests | Add a public-safe redaction scan over bundle/log artifacts. |
| Screenshot capture metadata must be enough to reproduce framing decisions without embedding image data in JSON. | `internal/cli/artifact_commands.go`, `internal/cdp/page.go`, responsive workflow files | screenshot artifact tests, render screenshot tests | Add preset and tile tests that assert viewport, DPR, clip/tile count, full-page mode, and output paths. |
| Network interception workflows must release every paused request before command exit. | future request blocking/mocking commands, `internal/cdp` event loop helpers | none yet | Add timeout and cancellation tests that prove blocked/mocked sessions disable interception and continue or fail paused requests. |
| Streamed trace and body artifacts must stay bounded, close protocol handles, and emit safety metadata. | future HAR/body/trace commands, artifact helpers | network capture body limits, perf workflow artifact tests | Add synthetic stream tests for `IO.read` EOF, max bytes, scanner results, and handle cleanup. |

## Liveness Properties

| Property | Owner | Existing coverage | Next checks |
| --- | --- | --- | --- |
| `daemon keepalive` either reaches a ready runtime or fails with bounded, structured recovery information. | `internal/cli/daemon_commands.go`, `internal/daemon/runtime.go` | daemon keepalive tests | Add stale runtime/socket cleanup coverage on failed start. |
| Once the daemon runtime is ready, page commands can make progress through the RPC loop without requiring source inspection. | `internal/daemon/runtime.go`, `internal/cli/page_commands.go` | installed E2E, daemon runtime tests | Add RPC envelope tests for invalid request, missing method, timeout, and cancellation. |
| Protocol metadata remains usable when live `/json/protocol` is unavailable, and fallback source is labeled. | `internal/cdp/protocol.go`, `internal/cli/protocol_commands.go` | protocol command tests | Add an explicit live-unavailable fallback test with source labeling. |
| Rendered extraction waits for useful/stable content but remains bounded by timeout and artifact limits. | `internal/cli/workflow_rendered_extract.go` | rendered extraction tests | Add timeout-path coverage with partial artifact metadata. |

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
cdp pages --json
make e2e-demo-installed
```
