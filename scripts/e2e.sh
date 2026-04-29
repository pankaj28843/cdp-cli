#!/usr/bin/env bash
set -euo pipefail

binary="${1:-./bin/cdp}"

if [[ ! -x "$binary" ]]; then
  echo "missing executable: $binary" >&2
  exit 2
fi

"$binary" --help >/tmp/cdp-cli-help.txt
"$binary" version --json | jq -e '.version and .commit and .date' >/dev/null
"$binary" describe --json | jq -e '.ok == true and (.commands.children | length > 5)' >/dev/null
"$binary" describe --jq '.globals | index("--json")' >/dev/null
"$binary" describe --command "daemon status" --json | jq -e '.ok == true and .commands.name == "status" and (.commands.examples | length > 0)' >/dev/null
"$binary" doctor --json | jq -e '.ok == true and (.checks | length >= 3)' >/dev/null
"$binary" explain-error not_implemented --json | jq -e '.ok == true and .error.exit_code == 8' >/dev/null
"$binary" exit-codes --json | jq -e '.ok == true and (.exit_codes | map(.name) | index("not_implemented"))' >/dev/null
"$binary" schema error-envelope --json | jq -e '.ok == true and .schema.name == "error-envelope"' >/dev/null

set +e
daemon_output="$("$binary" daemon status --json 2>/tmp/cdp-cli-daemon-status.err)"
daemon_code=$?
set -e

if [[ "$daemon_code" -ne 8 ]]; then
  echo "daemon status exit code = $daemon_code, want 8 while daemon is planned" >&2
  exit 1
fi

printf '%s\n' "$daemon_output" | jq -e '.ok == false and .code == "not_implemented"' >/dev/null
