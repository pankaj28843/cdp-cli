# Known Issues and Repair Paths

This is a short public-safe repair map for failures that can look like
provider bugs. It records the observable symptom, the bounded response, and
the smallest code area to change. It does not contain browser state, captures,
or provider conversation data.

## ChatGPT thinking controls

If ChatGPT exposes a reasoning slider whose range, accessible meaning, or
current value is not provable, the ask fails before Send. This is intentional:
the command must not guess from a CSS class, stale label, or an assumed zero
minimum.

Inspect the structured selection metadata in the operation result, then update
the semantic slider observation and its synthetic tests in
`internal/webagent/chatgpt/selection.go` and `selection_test.go`. Preserve the
proof that the visible minimum and requested numeric target were observed
before the single Send boundary. A renamed label alone must not make a prior
selection proof pass.

## Perplexity route changes

Perplexity can render a temporary post-send route before replacing it with the
final conversation route. The final route is followed only when its rendered
prompt fingerprint exactly matches the submitted prompt. If the route changes
without that proof, the operation returns an incomplete, non-read state with
no retry command; never send the prompt again.

Reproduce route transitions with synthetic observations and update
`internal/webagent/perplexity/ask.go` plus
`ask_recovery_test.go`. Keep the initial route as lineage and expose the
verified final route for read-only detail/await commands.

## Provider alias or metadata failures

Run the non-mutating checks first:

```bash
cdp --browser-mode headed pages --json
cdp workflow agent providers --json
cdp workflow agent chatgpt capabilities --json
```

If the headed page check is healthy, a provider failure is usually a workflow
or provider-auth problem rather than a reason to create another browser target.
Reinstall the managed binary with `make install`, then rerun
`make e2e-agent-workflows-installed`. Provider-specific `doctor`, `auth refresh`,
or `capabilities refresh` commands are explicit workflow operations; run only
the operation shown as supported by the capability contract. If a new site
change is not covered, file a focused feature request with a synthetic
reproduction, semantic target evidence, and the smallest failing contract
rather than adding a speculative selector framework.

## Headless suspended environments

Some developer environments suspend the managed headless browser between
commands. A failed wake/repair check is an environment limitation, not proof
that headed default-profile access is broken. Record the structured diagnostic,
leave the daemon untouched when repair is not authorized, and use the headed
page check only when a human-approved headed session is already available.

## Invocation-lease cancellation cleanup

A timed-out workflow can race the daemon's independent lease reclamation. If
the lease is already gone when cleanup starts, the CLI now retries the exact
workflow-owned target through the same daemon without that expired lease and
still requires an exact target-gone observation. It does not broaden this into
an unowned retry for other errors or targets.

If this regresses, start with
`TestDiagnosticWorkflowCancellationStillCleansOwnedPage` and the lease
classifier in `internal/daemon/runtime.go`; keep the repair at the cleanup
boundary and preserve the daemon as the browser ownership boundary.

## Small-fix workflow

1. Add or update a synthetic regression that proves the failing boundary.
2. Change the smallest semantic observation or translation function.
3. Run the focused package tests, `go vet ./...`, and the installed gate.
4. Update this map only when the failure mode or repair command changes.

Do not add a general self-healing selector framework. Keep locator fallbacks
semantic, bounded, and fail-closed when identity is ambiguous.
