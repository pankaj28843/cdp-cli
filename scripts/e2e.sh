#!/usr/bin/env bash
set -euo pipefail

binary="${1:-./bin/cdp}"

if [[ ! -x "$binary" ]]; then
  echo "missing executable: $binary" >&2
  exit 2
fi

state_dir="$(mktemp -d)"
trap 'rm -rf "$state_dir"' EXIT

"$binary" --help >/tmp/cdp-cli-help.txt
"$binary" version --json | jq -e '.version and .commit and .date' >/dev/null
"$binary" describe --json | jq -e '.ok == true and (.commands.children | length > 5)' >/dev/null
"$binary" describe --jq '.globals | index("--json")' >/dev/null
"$binary" describe --command "daemon status" --json | jq -e '.ok == true and .commands.name == "status" and (.commands.examples | length > 0)' >/dev/null
"$binary" doctor --state-dir "$state_dir" --json | jq -e '.ok == true and (.checks | length >= 3)' >/dev/null
"$binary" explain-error not_implemented --json | jq -e '.ok == true and .error.exit_code == 8' >/dev/null
"$binary" exit-codes --json | jq -e '.ok == true and (.exit_codes | map(.name) | index("not_implemented"))' >/dev/null
"$binary" schema error-envelope --json | jq -e '.ok == true and .schema.name == "error-envelope"' >/dev/null

"$binary" connection add default --auto-connect --state-dir "$state_dir" --json | jq -e '.ok == true and .connection.mode == "auto_connect"' >/dev/null
"$binary" connection current --state-dir "$state_dir" --json | jq -e '.ok == true and .connection.name == "default"' >/dev/null
"$binary" connection list --state-dir "$state_dir" --json | jq -e '.ok == true and (.connections | length == 1)' >/dev/null

if [[ "${CDP_E2E_AUTO_CONNECT:-}" == "1" || "${CDP_E2E_AUTO_CONNECT:-}" == "true" ]]; then
  "$binary" connection add default --auto-connect --json | jq -e '.ok == true and .connection.mode == "auto_connect"' >/dev/null
  "$binary" connection current --json | jq -e '.ok == true and .connection.mode == "auto_connect"' >/dev/null
  "$binary" doctor --json | jq -e '.checks[] | select(.name == "daemon" and (.state == "passive" or .state == "permission_pending"))' >/dev/null
  "$binary" daemon status --json | jq -e '.daemon.connection_mode == "auto_connect" and (.daemon.state == "passive" or .daemon.state == "permission_pending")' >/dev/null
  if [[ "${CDP_E2E_ACTIVE_BROWSER:-}" == "1" || "${CDP_E2E_ACTIVE_BROWSER:-}" == "true" ]]; then
    set +e
    live_protocol_output="$("$binary" --active-browser-probe --timeout 5s protocol metadata --json 2>/tmp/cdp-cli-live-protocol.err)"
    live_protocol_code=$?
    set -e
    if [[ "$live_protocol_code" -eq 0 ]]; then
      printf '%s\n' "$live_protocol_output" | jq -e '.ok == true and (.protocol.domain_count | type == "number")' >/dev/null
    else
      printf '%s\n' "$live_protocol_output" | jq -e '.ok == false and (.code == "connection_failed" or .code == "connection_not_configured")' >/dev/null
    fi
    set +e
    live_domains_output="$("$binary" --active-browser-probe --timeout 5s protocol domains --json 2>/tmp/cdp-cli-live-domains.err)"
    live_domains_code=$?
    set -e
    if [[ "$live_domains_code" -eq 0 ]]; then
      printf '%s\n' "$live_domains_output" | jq -e '.ok == true and (.domains | type == "array")' >/dev/null
    else
      printf '%s\n' "$live_domains_output" | jq -e '.ok == false and (.code == "connection_failed" or .code == "connection_not_configured")' >/dev/null
    fi
    set +e
    live_search_output="$("$binary" --active-browser-probe --timeout 5s protocol search screenshot --json 2>/tmp/cdp-cli-live-search.err)"
    live_search_code=$?
    set -e
    if [[ "$live_search_code" -eq 0 ]]; then
      printf '%s\n' "$live_search_output" | jq -e '.ok == true and (.matches | type == "array")' >/dev/null
    else
      printf '%s\n' "$live_search_output" | jq -e '.ok == false and (.code == "connection_failed" or .code == "connection_not_configured")' >/dev/null
    fi
    set +e
    live_describe_output="$("$binary" --active-browser-probe --timeout 5s protocol describe Page.captureScreenshot --json 2>/tmp/cdp-cli-live-describe.err)"
    live_describe_code=$?
    set -e
    if [[ "$live_describe_code" -eq 0 ]]; then
      printf '%s\n' "$live_describe_output" | jq -e '.ok == true and .entity.path == "Page.captureScreenshot"' >/dev/null
    else
      printf '%s\n' "$live_describe_output" | jq -e '.ok == false and (.code == "connection_failed" or .code == "connection_not_configured" or .code == "unknown_protocol_entity")' >/dev/null
    fi
    set +e
    live_exec_output="$("$binary" --active-browser-probe --timeout 5s protocol exec Browser.getVersion --params '{}' --json 2>/tmp/cdp-cli-live-exec.err)"
    live_exec_code=$?
    set -e
    if [[ "$live_exec_code" -eq 0 ]]; then
      printf '%s\n' "$live_exec_output" | jq -e '.ok == true and .method == "Browser.getVersion"' >/dev/null
    else
      printf '%s\n' "$live_exec_output" | jq -e '.ok == false and (.code == "connection_failed" or .code == "connection_not_configured")' >/dev/null
    fi
    set +e
    live_targets_output="$("$binary" --active-browser-probe --timeout 5s targets --json 2>/tmp/cdp-cli-live-targets.err)"
    live_targets_code=$?
    set -e
    if [[ "$live_targets_code" -eq 0 ]]; then
      printf '%s\n' "$live_targets_output" | jq -e '.ok == true and (.targets | type == "array")' >/dev/null
    else
      printf '%s\n' "$live_targets_output" | jq -e '.ok == false and (.code == "connection_failed" or .code == "connection_not_configured")' >/dev/null
    fi
    set +e
    live_pages_output="$("$binary" --active-browser-probe --timeout 5s pages --json 2>/tmp/cdp-cli-live-pages.err)"
    live_pages_code=$?
    set -e
    if [[ "$live_pages_code" -eq 0 ]]; then
      printf '%s\n' "$live_pages_output" | jq -e '.ok == true and (.pages | type == "array")' >/dev/null
    else
      printf '%s\n' "$live_pages_output" | jq -e '.ok == false and (.code == "connection_failed" or .code == "connection_not_configured")' >/dev/null
    fi
  fi
elif [[ -n "${CDP_E2E_BROWSER_URL:-}" ]]; then
  "$binary" doctor --browser-url "$CDP_E2E_BROWSER_URL" --json \
    | jq -e '.checks[] | select(.name == "browser_debug_endpoint" and .connection_mode == "browser_url" and (.status == "pass" or .status == "warn"))' >/dev/null
fi

"$binary" daemon status --state-dir "$state_dir" --json | jq -e '.ok == true and .daemon.state' >/dev/null

set +e
snapshot_output="$("$binary" snapshot --json 2>/tmp/cdp-cli-snapshot.err)"
snapshot_code=$?
set -e

if [[ "$snapshot_code" -ne 8 ]]; then
  echo "snapshot exit code = $snapshot_code, want 8 while snapshot is planned" >&2
  exit 1
fi

printf '%s\n' "$snapshot_output" | jq -e '.ok == false and .code == "not_implemented"' >/dev/null
