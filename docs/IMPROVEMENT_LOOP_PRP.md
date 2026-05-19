# cdp-cli Improvement Loop PRP

## Goal

Keep improving cdp-cli as an agent-experience-first browser CLI while dogfooding the installed `cdp` binary. The active PR must always reflect the overall change set in title and body.

## Status Snapshot (2026-05-20 01:05 Europe/Copenhagen)

- Current phase/step: Phase 2 verification-command iteration complete; next Phase 3 browser dogfooding/research synthesis.
- Repo reconciliation: branch `improve/formal-browser-invariants`, PR #1 open, local commits ahead pending commit/push for capability verify/evidence commands and PRP.
- Iteration contract: candidate=capability-specific `verify_commands`/`evidence_commands`; checker=`make verify && make install && make e2e-installed`; repair_budget=1 after one transient full-suite failure.
- Commands/evidence run: `go test ./internal/cli -run 'TestDoctorCapabilities|TestDoctorCapabilitiesSchema' -count=1` passed; first `make verify` failed once in pre-existing flaky `TestWorkflowWebResearchSERPPaginates`; focused rerun and full `go test ./internal/cli -count=1` passed; rerun `make verify` passed; `make install` and `make e2e-installed` passed; installed `cdp doctor --capabilities --json --jq ...` showed raw/artifacts verify and evidence commands.
- Files changed since last snapshot: `docs/IMPROVEMENT_LOOP_PRP.md`, `internal/cli/info_commands.go`, `internal/cli/root_test.go`, `internal/cli/schema_catalog.go`, `scripts/e2e.sh`.
- Stop tag: continuing.
- Exact next action: commit/push Phase 2, update PR title/body, then synthesize subagent/web/CDP research into a roadmap or next implementation slice.

## Status Snapshot (2026-05-20 00:37 Europe/Copenhagen)

- Current phase/step: Phase 1 complete; next Phase 2 checkbox is capability-specific verification commands.
- Repo reconciliation: branch `improve/formal-browser-invariants`, PR #1 open, latest pushed commit `1abbebe`.
- Iteration contract: candidate=add capability-specific verify/evidence commands; checker=`make verify && make install && make e2e-installed`; repair_budget=2.
- Commands/evidence run: `make verify`, `make install`, `make e2e-installed`, `make e2e-demo-installed`, live `cdp doctor/pages`, and `cdp workflow rendered-extract` against CDP Target docs passed before this plan was created.
- Files changed since last snapshot: `README.md`, `docs/FORMAL_INVARIANTS.md`, `internal/cli/info_commands.go`, `internal/cli/page_commands_test.go`, `internal/cli/root_test.go`, `internal/cli/schema_catalog.go`.
- Stop tag: continuing.
- Exact next action: add `verify_commands`/`evidence_commands` to `doctor --capabilities --json`, update schema/tests/e2e, validate, commit, push, and update PR title/body.

## Phase 1 - Shipped in PR #1

- [x] Document formal browser workflow invariants in `docs/FORMAL_INVARIANTS.md`.
- [x] Link invariants from `README.md`.
- [x] Add ambiguous target-prefix regression test proving structured failure before page attachment.
- [x] Extend `cdp doctor --capabilities --json` with daemon-first `agent_readiness` and safe `next_commands`.
- [x] Push branch and create PR #1.

## Phase 2 - Agent Bootstrap And Verification

- [x] Add capability-specific `verify_commands` and `evidence_commands` to `cdp doctor --capabilities --json`.
- [x] Update `doctor-capabilities` schema and e2e assertions for the new fields.
- [ ] Refresh PR #1 title and body after the verification-command iteration lands.

## Phase 3 - Browser Dogfooding Improvements

- [ ] Use installed `cdp` to inspect the PR, docs, and Chrome DevTools Protocol pages; record one concrete UX gap as a feature request.
- [ ] Implement the smallest safe gap from that request in the same PR.
- [ ] Run `make e2e-demo-installed` for browser-facing changes and include evidence in the PR body.

## Validation Gate

For each iteration:

```bash
make verify
make install
make e2e-installed
```

For browser-facing changes:

```bash
make e2e-demo-installed
cdp doctor --json
cdp pages --json
```

## PR Hygiene

After every committed iteration:

```bash
git push
gh pr edit 1 --title "<current overall title>" --body-file <updated body>
```

The PR title and description should summarize the whole branch, not only the first commit.
