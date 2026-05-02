# cdp-cli Agent Notes

This is the canonical repo instruction file. `CLAUDE.md` is a compatibility
symlink for Claude Code, and repo-local skills live under `.agents/skills`.

This is a public, machine-agnostic repository. Do not commit local machine details:

- No absolute home paths, usernames, hostnames, local ports from a private setup, or local MCP/Claude/Codex config dumps.
- No browser profile paths, cookies, tokens, request headers, screenshots, traces, logs, or page content unless they are synthetic fixtures.
- Keep installation-specific notes outside the repo, or document them generically with placeholders.

## Build

```bash
go test ./...
go vet ./...
go build ./cmd/cdp
```

Before committing feature work, run the full project loop:

```bash
make verify
make install
make e2e-installed
```

For browser-facing changes, also run the synthetic live-site loop:

```bash
make e2e-demo-installed
```

When validating a CLI behavior that users or agents will exercise through the installed `cdp` binary, run `make install` before the live/manual validation and again after final code changes. Run the validation with `cdp ...` from `PATH`, not `go run`, so the improved command is actually available to everyone using the local install.

## Design

- The CLI is for agents first: strong `--help`, `--json`, `--jq`, `--debug`, `--timeout`, concise defaults, and stable error envelopes.
- Prefer small packages under `internal/`; keep `cmd/cdp` as the composition root.
- Use `context.Context` as the first parameter for cancelable work.
- Return wrapped errors; log or return, never both.
- Large browser artifacts should be written to files and referenced by path, not embedded in JSON.
- Browser-facing commands must run through the local cdp daemon runtime. The daemon is the product boundary that holds the user-approved Chrome/default-profile session after one "Allow" click; do not add direct per-command browser WebSocket dialing as a fallback.
- Disk-backed connection memory may select or start a daemon-backed connection, but it must not make browser commands bypass the daemon.
- Keep tab discovery lazy and scoped. Listing pages must not attach to every page or wake discarded/background tabs.
- Prefer raw CDP escape hatches plus focused high-level workflows over broad shallow wrappers.
- New command JSON needs a schema entry, help examples, and E2E coverage in `scripts/e2e.sh`.
- See `ARCHITECTURE.md` for package boundaries and feature-shaping rules.

## Cross-Agent Layout

- `AGENTS.md` is canonical for repo instructions.
- `CLAUDE.md` is compatibility-only and should point to `AGENTS.md`.
- `.agents/skills` is canonical for repo-local skills.
- `.claude/skills`, `.codex/skills`, `.opencode/skills`, and `.github/skills` are compatibility symlinks to `.agents/skills`.
- `.github/copilot-instructions.md` points GitHub Copilot custom-instruction flows at `AGENTS.md`.
