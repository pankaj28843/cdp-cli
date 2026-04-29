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

if [[ -n "${CDP_E2E_BROWSER_URL:-}" ]]; then
  if [[ "${CDP_E2E_AUTO_CONNECT:-}" == "1" || "${CDP_E2E_AUTO_CONNECT:-}" == "true" ]]; then
    "$binary" doctor --auto-connect --browser-url "$CDP_E2E_BROWSER_URL" --json \
      | jq -e '.checks[] | select(.name == "browser_debug_endpoint" and .connection_mode == "auto_connect" and (.status == "pass" or .status == "pending"))' >/dev/null
  else
    "$binary" doctor --browser-url "$CDP_E2E_BROWSER_URL" --json \
      | jq -e '.checks[] | select(.name == "browser_debug_endpoint" and .connection_mode == "browser_url" and (.status == "pass" or .status == "warn"))' >/dev/null
  fi
fi

set +e
daemon_output="$("$binary" daemon status --json 2>/tmp/cdp-cli-daemon-status.err)"
daemon_code=$?
set -e

if [[ "$daemon_code" -ne 8 ]]; then
  echo "daemon status exit code = $daemon_code, want 8 while daemon is planned" >&2
  exit 1
fi

printf '%s\n' "$daemon_output" | jq -e '.ok == false and .code == "not_implemented"' >/dev/null
