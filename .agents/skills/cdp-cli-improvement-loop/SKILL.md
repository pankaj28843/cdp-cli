---
name: cdp-cli-improvement-loop
description: >
  Drive the cdp-cli repo improvement loop. Use when asked to iterate on this
  CLI, implement feature requests, improve agent browser-debugging workflows,
  validate the installed cdp binary end-to-end, or keep the public repo green
  while shipping small Go improvements.
compatibility: Requires Go, git, make, jq, and the repo-local feature-request
  backlog for cdp-cli.
---

# cdp-cli Improvement Loop

Use this skill for repo-local cdp-cli development. The CLI is for coding agents:
optimize for inspectable help, stable JSON, jq filtering, explicit recovery
commands, and end-to-end checks against the installed binary.

## Non-negotiables

- Keep the repository public-safe. Do not commit local usernames, absolute home
  paths, hostnames, private browser profile paths, cookies, tokens, request
  headers, screenshots, traces, page content, or local MCP configuration dumps.
- Keep PRP plans outside the code repo under `~/plan-prps/cdp-cli/`. Before
  starting or resuming work, check that directory for the newest plan and update
  that external file as the living sprint state; do not create or update PRP
  plans under `docs/`, `plans/`, or any other tracked repo path.
- Borrow prior-art workflow mechanics only when they apply to a Chrome DevTools
  Protocol CLI. Do not import domain-specific assumptions from unrelated CLIs.
- Every loop ships one complete improvement, validates it with `make verify`,
  validates the installed binary, then commits and pushes.
- Unit tests are not enough. Run the CLI as an agent would use it.
- For browser-facing changes, compare headed and headless behavior with fresh
  live tasks. Generate new search keywords each loop, including a few random or
  unusual queries, and exercise the same workflow in both modes before declaring
  parity.
- Keep reseeding explicit and active when validating headless. Use
  `browser profile seed --strategy copy-default` when the task depends on
  default-profile state, then rerun the headed/headless comparison with the
  installed `cdp` binary.
- Do not let safety wording block the browser-harness goal. Headless is
  self-managed, disposable agent infrastructure: it is acceptable to stop,
  reseed, restart, and force-close stale cdp-created headless tabs when that is
  the clean fix.
- Planned commands may return `not_implemented`, but that behavior must be
  stable, documented, and covered by E2E checks until implemented.

## Main Loop

Default to **30 iterations** unless the user gives a smaller number. Each
iteration should be a distinct improvement with its own validation signal.

1. Use the active requests in `~/feature-requests/cdp-cli/`; pick the highest
   impact actionable P1/P2.
2. If a request is too broad, split it or move it to `backlog/`; do not ship
   shallow placeholders as done.
3. Implement one improvement only.
4. Run verification:

```bash
make verify
make install
make e2e-installed
```

5. Exercise the CLI in an agent workflow. Minimum smoke path:

```bash
cdp --help
cdp version --json
cdp describe --json | jq '.commands.children | map(.name)'
cdp doctor --json
cdp daemon status --json || test "$?" -eq 8
```

6. For browser-harness work, run a headed/headless parity pass:
   - Generate at least five fresh queries for the current loop: two realistic
     developer searches, two random/unusual searches, and one adversarial or
     blocked-SERP probe.
   - Try the same navigation, page discovery, extraction, console/network, and
     workflow commands in `--browser-mode headed` and `--browser-mode headless`.
   - Reseed headless with explicit `copy-default` when authenticated/default
     profile state is relevant. `copy-default` may stop and heal the headless
     daemon. Retry before calling the gap real.
   - Record only public-safe metadata: query text, command, exit code, error
     class, result counts/domains, and recovery commands. Do not record cookies,
     tokens, request headers, screenshots, traces, or page content.
   - Keep fixing until headless is comparable to headed for the exercised
     workflow, or create a concrete feature request for any remaining gap with
     reproduction commands.
7. If the CLI output points to the wrong next command, hides a useful recovery,
   emits invalid JSON, or requires source reading to understand, create a new
   feature request in `~/feature-requests/cdp-cli/`.
8. Move shipped request files to `~/feature-requests/cdp-cli/shipped/`.
9. Commit and push only when the tree is green and leak checks pass.

## Capability Mining

When there are not enough actionable asks, mine for agent-experience gaps:

- Can an agent discover the command surface without reading source?
- Can JSON be filtered before entering model context?
- Do errors include stable classes and safe remediation commands?
- Are heavy browser artifacts returned as paths rather than payloads?
- Can a workflow be replayed, cached, diffed, and handed to another agent?
- Does the CLI make Chrome/default-profile risk explicit before attachment?
- Can raw CDP be discovered and executed without waiting for wrappers?

Convert each useful gap into a concrete feature request before coding.

## Validation Targets

For repo-only changes:

```bash
make verify
make install
make e2e-installed
```

For future browser/CDP changes, add the smallest real check that proves the
behavior:

```bash
cdp daemon start --auto-connect --json
cdp daemon status --json
cdp pages --json
cdp protocol metadata --json
cdp workflow console-errors --json
```

If Chrome is unavailable or permission is pending, the command must return a
classified JSON error and recovery commands; that is still a valid E2E signal.

For headed/headless parity validation, use installed `cdp` and fresh queries
rather than replaying one memorized search. A useful minimum pass exercises:

```bash
cdp --browser-mode headed pages --json
cdp --browser-mode headless pages --json
cdp --browser-mode headless browser profile seed --strategy copy-default --json
cdp --browser-mode headed workflow web-research serp --query "$QUERY" --json
cdp --browser-mode headless workflow web-research serp --query "$QUERY" --json
```

Adjust command names if the workflow surface changes, but keep the comparison:
same intent, same query set, both browser modes, reseed/retry headless before
filing a gap.

## Success Criteria

- `make verify` passes.
- `make install` succeeds.
- `make e2e-installed` exercises the installed `cdp` binary.
- Browser-facing changes have a recorded headed/headless parity pass with fresh
  generated queries, explicit headless reseeding when relevant, and concrete
  follow-up requests for any remaining gaps.
- `git status --short` is clean after commit.
- The commit is pushed.
- No public-repo hygiene scan findings.
