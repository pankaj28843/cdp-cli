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
"$binary" version --json --compact | jq -e '.version and .commit and .date' >/dev/null
"$binary" describe --json | jq -e '.ok == true and (.commands.children | length > 5)' >/dev/null
"$binary" describe --jq '.globals | index("--json")' >/dev/null
"$binary" describe --jq '.globals | index("--compact")' >/dev/null
"$binary" describe --jq '.globals | index("--connection")' >/dev/null
"$binary" describe --command "daemon start" --json | jq -e '.ok == true and .commands.name == "start" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "daemon status" --json | jq -e '.ok == true and .commands.name == "status" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "daemon stop" --json | jq -e '.ok == true and .commands.name == "stop" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "daemon restart" --json | jq -e '.ok == true and .commands.name == "restart" and (.commands.examples | any(contains("--autoConnect")))' >/dev/null
"$binary" describe --command "daemon keepalive" --json | jq -e '.ok == true and .commands.name == "keepalive" and (.commands.examples | any(contains("--display")))' >/dev/null
"$binary" doctor --state-dir "$state_dir" --json | jq -e '.ok == true and (.checks | length >= 3)' >/dev/null
"$binary" doctor --check daemon --state-dir "$state_dir" --json | jq -e '.ok == true and (.checks | length == 1) and .checks[0].name == "daemon"' >/dev/null
"$binary" doctor --capabilities --json | jq -e '.ok == true and (.capabilities | map(.name) | index("raw_protocol"))' >/dev/null
"$binary" explain-error not_implemented --json | jq -e '.ok == true and .error.exit_code == 8' >/dev/null
"$binary" exit-codes --json | jq -e '.ok == true and (.exit_codes | map(.name) | index("not_implemented"))' >/dev/null
"$binary" schema error-envelope --json | jq -e '.ok == true and .schema.name == "error-envelope"' >/dev/null
"$binary" schema snapshot --json | jq -e '.ok == true and .schema.name == "snapshot"' >/dev/null
"$binary" schema protocol-exec --json | jq -e '.ok == true and .schema.name == "protocol-exec" and (.schema.fields | map(.name) | index("scope"))' >/dev/null
"$binary" schema protocol-examples --json | jq -e '.ok == true and .schema.name == "protocol-examples" and (.schema.fields | map(.name) | index("examples"))' >/dev/null
"$binary" schema daemon-restart --json | jq -e '.ok == true and .schema.name == "daemon-restart" and (.schema.fields | map(.name) | index("restart"))' >/dev/null
"$binary" schema daemon-keepalive --json | jq -e '.ok == true and .schema.name == "daemon-keepalive" and (.schema.fields | map(.name) | index("lock"))' >/dev/null
"$binary" describe --command "open" --json | jq -e '.ok == true and .commands.name == "open" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "page reload" --json | jq -e '.ok == true and .commands.name == "reload" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "page back" --json | jq -e '.ok == true and .commands.name == "back" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "page forward" --json | jq -e '.ok == true and .commands.name == "forward" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "page activate" --json | jq -e '.ok == true and .commands.name == "activate" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "page close" --json | jq -e '.ok == true and .commands.name == "close" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "text" --json | jq -e '.ok == true and .commands.name == "text" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "html" --json | jq -e '.ok == true and .commands.name == "html" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "dom query" --json | jq -e '.ok == true and .commands.name == "query" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "css inspect" --json | jq -e '.ok == true and .commands.name == "inspect" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "layout overflow" --json | jq -e '.ok == true and .commands.name == "overflow" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "wait text" --json | jq -e '.ok == true and .commands.name == "text" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "wait selector" --json | jq -e '.ok == true and .commands.name == "selector" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "snapshot" --json | jq -e '.ok == true and .commands.name == "snapshot" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "screenshot" --json | jq -e '.ok == true and .commands.name == "screenshot" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "console" --json | jq -e '.ok == true and .commands.name == "console" and (.commands.examples | any(contains("--errors")))' >/dev/null
"$binary" describe --command "network" --json | jq -e '.ok == true and .commands.name == "network" and (.commands.examples | any(contains("--failed")))' >/dev/null
"$binary" describe --command "protocol exec" --json | jq -e '.ok == true and .commands.name == "exec" and (.commands.examples | any(contains("--target")))' >/dev/null
"$binary" describe --command "protocol examples" --json | jq -e '.ok == true and .commands.name == "examples" and (.commands.examples | any(contains("Page.captureScreenshot")))' >/dev/null
"$binary" describe --command "workflow visible-posts" --json | jq -e '.ok == true and .commands.name == "visible-posts" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "workflow hacker-news" --json | jq -e '.ok == true and .commands.name == "hacker-news" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "workflow console-errors" --json | jq -e '.ok == true and .commands.name == "console-errors" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "workflow network-failures" --json | jq -e '.ok == true and .commands.name == "network-failures" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "workflow page-load" --json | jq -e '.ok == true and .commands.name == "page-load" and (.commands.examples | any(contains("--reload")))' >/dev/null
"$binary" schema screenshot --json | jq -e '.ok == true and .schema.name == "screenshot"' >/dev/null
"$binary" schema console --json | jq -e '.ok == true and .schema.name == "console"' >/dev/null
"$binary" schema network --json | jq -e '.ok == true and .schema.name == "network"' >/dev/null
"$binary" schema text --json | jq -e '.ok == true and .schema.name == "text"' >/dev/null
"$binary" schema html --json | jq -e '.ok == true and .schema.name == "html"' >/dev/null
"$binary" schema dom-query --json | jq -e '.ok == true and .schema.name == "dom-query"' >/dev/null
"$binary" schema css-inspect --json | jq -e '.ok == true and .schema.name == "css-inspect"' >/dev/null
"$binary" schema layout-overflow --json | jq -e '.ok == true and .schema.name == "layout-overflow"' >/dev/null
"$binary" schema wait --json | jq -e '.ok == true and .schema.name == "wait"' >/dev/null
"$binary" schema workflow-hacker-news --json | jq -e '.ok == true and .schema.name == "workflow-hacker-news" and (.schema.fields | map(.name) | index("organization"))' >/dev/null
"$binary" schema workflow-console-errors --json | jq -e '.ok == true and .schema.name == "workflow-console-errors"' >/dev/null
"$binary" schema workflow-network-failures --json | jq -e '.ok == true and .schema.name == "workflow-network-failures"' >/dev/null
"$binary" schema workflow-page-load --json | jq -e '.ok == true and .schema.name == "workflow-page-load" and (.schema.fields | map(.name) | index("storage"))' >/dev/null

mkdir -p "$state_dir/user-data"
set +e
daemon_start_output="$("$binary" daemon start --autoConnect --user-data-dir "$state_dir/user-data" --state-dir "$state_dir" --json 2>/tmp/cdp-cli-daemon-start.err)"
daemon_start_code=$?
set -e
if [[ "$daemon_start_code" -ne 4 ]]; then
  echo "daemon start exit code = $daemon_start_code, want 4 while auto-connect permission is pending" >&2
  exit 1
fi
printf '%s\n' "$daemon_start_output" | jq -e '.ok == false and .code == "permission_pending" and (.remediation_commands | index("open chrome://inspect/#remote-debugging"))' >/dev/null
"$binary" connection current --state-dir "$state_dir" --json | jq -e '.ok == true and .connection.name == "default" and .connection.mode == "auto_connect"' >/dev/null

set +e
daemon_restart_output="$("$binary" daemon restart --debug --autoConnect --active-browser-probe --user-data-dir "$state_dir/user-data" --state-dir "$state_dir" --json 2>/tmp/cdp-cli-daemon-restart.err)"
daemon_restart_code=$?
set -e
if [[ "$daemon_restart_code" -ne 4 ]]; then
  echo "daemon restart exit code = $daemon_restart_code, want 4 while auto-connect permission is pending" >&2
  exit 1
fi
printf '%s\n' "$daemon_restart_output" | jq -e '.ok == false and .code == "permission_pending" and (.remediation_commands | index("open chrome://inspect/#remote-debugging"))' >/dev/null

"$binary" connection add default --auto-connect --state-dir "$state_dir" --json | jq -e '.ok == true and .connection.mode == "auto_connect"' >/dev/null
"$binary" connection current --state-dir "$state_dir" --json | jq -e '.ok == true and .connection.name == "default"' >/dev/null
"$binary" connection resolve --state-dir "$state_dir" --json | jq -e '.ok == true and .source == "selected" and .connection.name == "default"' >/dev/null
"$binary" connection list --state-dir "$state_dir" --json | jq -e '.ok == true and (.connections | length == 1)' >/dev/null
"$binary" connection add extra --auto-connect --state-dir "$state_dir" --json | jq -e '.ok == true and .connection.name == "extra"' >/dev/null
"$binary" connection remove extra --state-dir "$state_dir" --json | jq -e '.ok == true and .removed == "extra" and (.connections | length == 1)' >/dev/null
"$binary" connection add stale --browser-url http://example.invalid --project "$state_dir/missing-project" --state-dir "$state_dir" --json | jq -e '.ok == true and .connection.name == "stale"' >/dev/null
"$binary" connection prune --missing-projects --state-dir "$state_dir" --json | jq -e '.ok == true and (.removed | length == 1)' >/dev/null
"$binary" daemon stop --state-dir "$state_dir" --json | jq -e '.ok == true and .stopped == false' >/dev/null

if [[ "${CDP_E2E_AUTO_CONNECT:-}" == "1" || "${CDP_E2E_AUTO_CONNECT:-}" == "true" ]]; then
  "$binary" connection add default --auto-connect --json | jq -e '.ok == true and .connection.mode == "auto_connect"' >/dev/null
  "$binary" connection current --json | jq -e '.ok == true and .connection.mode == "auto_connect"' >/dev/null
  "$binary" doctor --json | jq -e '.checks[] | select(.name == "daemon" and (.state == "passive" or .state == "permission_pending"))' >/dev/null
  "$binary" daemon status --json | jq -e '.daemon.connection_mode == "auto_connect" and (.daemon.state == "passive" or .daemon.state == "permission_pending")' >/dev/null
  if [[ "${CDP_E2E_ACTIVE_BROWSER:-}" == "1" || "${CDP_E2E_ACTIVE_BROWSER:-}" == "true" ]]; then
    set +e
    live_daemon_output="$("$binary" daemon start --auto-connect --timeout 10s --json 2>/tmp/cdp-cli-live-daemon-start.err)"
    live_daemon_code=$?
    set -e
    if [[ "$live_daemon_code" -eq 0 ]]; then
      printf '%s\n' "$live_daemon_output" | jq -e '.ok == true and .daemon.state == "running"' >/dev/null
    else
      printf '%s\n' "$live_daemon_output" | jq -e '.ok == false and (.code == "permission_pending" or .code == "connection_failed" or .code == "connection_not_configured")' >/dev/null
    fi
    set +e
    live_protocol_output="$("$binary" --timeout 5s protocol metadata --json 2>/tmp/cdp-cli-live-protocol.err)"
    live_protocol_code=$?
    set -e
    if [[ "$live_protocol_code" -eq 0 ]]; then
      printf '%s\n' "$live_protocol_output" | jq -e '.ok == true and (.protocol.domain_count | type == "number")' >/dev/null
    else
      printf '%s\n' "$live_protocol_output" | jq -e '.ok == false and (.code == "connection_failed" or .code == "connection_not_configured")' >/dev/null
    fi
    set +e
    live_domains_output="$("$binary" --timeout 5s protocol domains --json 2>/tmp/cdp-cli-live-domains.err)"
    live_domains_code=$?
    set -e
    if [[ "$live_domains_code" -eq 0 ]]; then
      printf '%s\n' "$live_domains_output" | jq -e '.ok == true and (.domains | type == "array")' >/dev/null
    else
      printf '%s\n' "$live_domains_output" | jq -e '.ok == false and (.code == "connection_failed" or .code == "connection_not_configured")' >/dev/null
    fi
    set +e
    live_search_output="$("$binary" --timeout 5s protocol search screenshot --json 2>/tmp/cdp-cli-live-search.err)"
    live_search_code=$?
    set -e
    if [[ "$live_search_code" -eq 0 ]]; then
      printf '%s\n' "$live_search_output" | jq -e '.ok == true and (.matches | type == "array")' >/dev/null
    else
      printf '%s\n' "$live_search_output" | jq -e '.ok == false and (.code == "connection_failed" or .code == "connection_not_configured")' >/dev/null
    fi
    set +e
    live_describe_output="$("$binary" --timeout 5s protocol describe Page.captureScreenshot --json 2>/tmp/cdp-cli-live-describe.err)"
    live_describe_code=$?
    set -e
    if [[ "$live_describe_code" -eq 0 ]]; then
      printf '%s\n' "$live_describe_output" | jq -e '.ok == true and .entity.path == "Page.captureScreenshot"' >/dev/null
    else
      printf '%s\n' "$live_describe_output" | jq -e '.ok == false and (.code == "connection_failed" or .code == "connection_not_configured" or .code == "unknown_protocol_entity")' >/dev/null
    fi
    set +e
    live_exec_output="$("$binary" --timeout 5s protocol exec Browser.getVersion --params '{}' --json 2>/tmp/cdp-cli-live-exec.err)"
    live_exec_code=$?
    set -e
    if [[ "$live_exec_code" -eq 0 ]]; then
      printf '%s\n' "$live_exec_output" | jq -e '.ok == true and .method == "Browser.getVersion"' >/dev/null
    else
      printf '%s\n' "$live_exec_output" | jq -e '.ok == false and (.code == "connection_failed" or .code == "connection_not_configured")' >/dev/null
    fi
    set +e
    live_targets_output="$("$binary" --timeout 5s targets --json 2>/tmp/cdp-cli-live-targets.err)"
    live_targets_code=$?
    set -e
    if [[ "$live_targets_code" -eq 0 ]]; then
      printf '%s\n' "$live_targets_output" | jq -e '.ok == true and (.targets | type == "array")' >/dev/null
    else
      printf '%s\n' "$live_targets_output" | jq -e '.ok == false and (.code == "connection_failed" or .code == "connection_not_configured")' >/dev/null
    fi
    set +e
    live_pages_output="$("$binary" --timeout 5s pages --json 2>/tmp/cdp-cli-live-pages.err)"
    live_pages_code=$?
    set -e
    if [[ "$live_pages_code" -eq 0 ]]; then
      printf '%s\n' "$live_pages_output" | jq -e '.ok == true and (.pages | type == "array")' >/dev/null
    else
      printf '%s\n' "$live_pages_output" | jq -e '.ok == false and (.code == "connection_failed" or .code == "connection_not_configured")' >/dev/null
    fi
    if [[ -n "${CDP_E2E_VISIBLE_POSTS_URL:-}" ]]; then
      set +e
      live_posts_output="$("$binary" --timeout "${CDP_E2E_VISIBLE_POSTS_TIMEOUT:-45s}" workflow visible-posts "$CDP_E2E_VISIBLE_POSTS_URL" --selector "${CDP_E2E_VISIBLE_POSTS_SELECTOR:-article}" --limit "${CDP_E2E_VISIBLE_POSTS_LIMIT:-3}" --json 2>/tmp/cdp-cli-live-posts.err)"
      live_posts_code=$?
      set -e
      if [[ "$live_posts_code" -ne 0 ]]; then
        echo "workflow visible-posts failed for CDP_E2E_VISIBLE_POSTS_URL with exit code $live_posts_code" >&2
        exit 1
      fi
      printf '%s\n' "$live_posts_output" | jq -e '.ok == true and (.items | length > 0)' >/dev/null
    fi
    if [[ -n "${CDP_E2E_HN_URL:-}" ]]; then
      set +e
      live_hn_output="$("$binary" --timeout "${CDP_E2E_HN_TIMEOUT:-45s}" workflow hacker-news "$CDP_E2E_HN_URL" --limit "${CDP_E2E_HN_LIMIT:-3}" --json 2>/tmp/cdp-cli-live-hn.err)"
      live_hn_code=$?
      set -e
      if [[ "$live_hn_code" -ne 0 ]]; then
        echo "workflow hacker-news failed for CDP_E2E_HN_URL with exit code $live_hn_code" >&2
        exit 1
      fi
      printf '%s\n' "$live_hn_output" | jq -e '.ok == true and (.stories | length > 0) and .organization.story_row_selector == "tr.athing"' >/dev/null
    fi
    "$binary" daemon stop --json >/dev/null 2>&1 || true
  fi
elif [[ -n "${CDP_E2E_BROWSER_URL:-}" ]]; then
  "$binary" doctor --browser-url "$CDP_E2E_BROWSER_URL" --json \
    | jq -e '.checks[] | select(.name == "browser_debug_endpoint" and .connection_mode == "browser_url" and (.status == "pass" or .status == "warn"))' >/dev/null
  "$binary" daemon start --browser-url "$CDP_E2E_BROWSER_URL" --state-dir "$state_dir/live-browser" --json \
    | jq -e '.ok == true and .daemon.state == "running"' >/dev/null
  "$binary" pages --state-dir "$state_dir/live-browser" --json \
    | jq -e '.ok == true and (.pages | type == "array")' >/dev/null
  "$binary" daemon stop --state-dir "$state_dir/live-browser" --json >/dev/null
fi

"$binary" daemon status --state-dir "$state_dir" --json | jq -e '.ok == true and .daemon.state' >/dev/null

set +e
snapshot_output="$("$binary" snapshot --state-dir "$state_dir" --json 2>/tmp/cdp-cli-snapshot.err)"
snapshot_code=$?
set -e

if [[ "$snapshot_code" -ne 3 ]]; then
  echo "snapshot exit code = $snapshot_code, want 3 without a browser connection" >&2
  exit 1
fi

printf '%s\n' "$snapshot_output" | jq -e '.ok == false and .code == "connection_not_configured"' >/dev/null

set +e
screenshot_output="$("$binary" screenshot --out "$state_dir/page.png" --state-dir "$state_dir" --json 2>/tmp/cdp-cli-screenshot.err)"
screenshot_code=$?
set -e

if [[ "$screenshot_code" -ne 3 ]]; then
  echo "screenshot exit code = $screenshot_code, want 3 without a browser connection" >&2
  exit 1
fi

printf '%s\n' "$screenshot_output" | jq -e '.ok == false and .code == "connection_not_configured"' >/dev/null

set +e
console_output="$("$binary" console --state-dir "$state_dir" --wait 0s --json 2>/tmp/cdp-cli-console.err)"
console_code=$?
set -e

if [[ "$console_code" -ne 3 ]]; then
  echo "console exit code = $console_code, want 3 without a browser connection" >&2
  exit 1
fi

printf '%s\n' "$console_output" | jq -e '.ok == false and .code == "connection_not_configured"' >/dev/null

set +e
network_output="$("$binary" network --state-dir "$state_dir" --wait 0s --json 2>/tmp/cdp-cli-network.err)"
network_code=$?
set -e

if [[ "$network_code" -ne 3 ]]; then
  echo "network exit code = $network_code, want 3 without a browser connection" >&2
  exit 1
fi

printf '%s\n' "$network_output" | jq -e '.ok == false and .code == "connection_not_configured"' >/dev/null
