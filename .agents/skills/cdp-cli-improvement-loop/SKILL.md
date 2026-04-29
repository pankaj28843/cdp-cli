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
- Borrow prior-art workflow mechanics only when they apply to a Chrome DevTools
  Protocol CLI. Do not import domain-specific assumptions from unrelated CLIs.
- Every loop ships one complete improvement, validates it with `make verify`,
  validates the installed binary, then commits and pushes.
- Unit tests are not enough. Run the CLI as an agent would use it.
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

6. If the CLI output points to the wrong next command, hides a useful recovery,
   emits invalid JSON, or requires source reading to understand, create a new
   feature request in `~/feature-requests/cdp-cli/`.
7. Move shipped request files to `~/feature-requests/cdp-cli/shipped/`.
8. Commit and push only when the tree is green and leak checks pass.

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
cdp daemon start --auto-connect --prime --reconnect 30s --json
cdp daemon status --json
cdp pages --json
cdp protocol metadata --json
cdp workflow console-errors --json
```

If Chrome is unavailable or permission is pending, the command must return a
classified JSON error and recovery commands; that is still a valid E2E signal.

## Success Criteria

- `make verify` passes.
- `make install` succeeds.
- `make e2e-installed` exercises the installed `cdp` binary.
- `git status --short` is clean after commit.
- The commit is pushed.
- No public-repo hygiene scan findings.

