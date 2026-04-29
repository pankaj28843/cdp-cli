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

## Design

- The CLI is for agents first: strong `--help`, `--json`, `--jq`, `--debug`, `--timeout`, concise defaults, and stable error envelopes.
- Prefer small packages under `internal/`; keep `cmd/cdp` as the composition root.
- Use `context.Context` as the first parameter for cancelable work.
- Return wrapped errors; log or return, never both.
- Large browser artifacts should be written to files and referenced by path, not embedded in JSON.

## Cross-Agent Layout

- `AGENTS.md` is canonical for repo instructions.
- `CLAUDE.md` is compatibility-only and should point to `AGENTS.md`.
- `.agents/skills` is canonical for repo-local skills.
- `.claude/skills`, `.codex/skills`, `.opencode/skills`, and `.github/skills` are compatibility symlinks to `.agents/skills`.
- `.github/copilot-instructions.md` points GitHub Copilot custom-instruction flows at `AGENTS.md`.
