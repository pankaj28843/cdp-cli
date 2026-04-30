#!/usr/bin/env bash
set -euo pipefail

binary="${1:-$(command -v cdp)}"
chrome="${CDP_E2E_CHROME:-$(command -v google-chrome || command -v chromium || command -v chromium-browser || true)}"

if [[ ! -x "$binary" ]]; then
  echo "missing executable: $binary" >&2
  exit 2
fi
if [[ -z "$chrome" || ! -x "$chrome" ]]; then
  echo "missing chrome executable; set CDP_E2E_CHROME" >&2
  exit 2
fi

state_dir="$(mktemp -d)"
app_log="$state_dir/demo-app.log"
chrome_log="$state_dir/chrome.log"
app_pid=""
chrome_pid=""
app_url=""

require_artifact() {
  local path=$1
  if [[ ! -e "$path" ]]; then
    echo "missing artifact: $path" >&2
    return 2
  fi
  if [[ ! -s "$path" ]]; then
    echo "empty artifact: $path" >&2
    return 2
  fi
}

extract_demo_url() {
  local source_file=$1
  local line
  while IFS= read -r line; do
    line="${line//$'\r'/}"
    if [[ "$line" =~ ^[[:space:]]*(https?://[^[:space:]]+)[[:space:]]*$ ]]; then
      printf '%s\n' "${BASH_REMATCH[1]}"
      return 0
    fi
  done <"$source_file"
  return 1
}

cleanup() {
  if [[ -n "$chrome_pid" ]]; then
    "$binary" daemon stop --state-dir "$state_dir/cdp-state" --json >/dev/null 2>&1 || true
    kill "$chrome_pid" 2>/dev/null || true
    wait "$chrome_pid" 2>/dev/null || true
  fi
  if [[ -n "$app_pid" ]]; then
    kill "$app_pid" 2>/dev/null || true
    wait "$app_pid" 2>/dev/null || true
  fi
  for _ in {1..20}; do
    rm -rf "$state_dir" 2>/dev/null && return
    sleep 0.1
  done
  rm -rf "$state_dir" 2>/dev/null || true
}
trap cleanup EXIT

python3 scripts/demo_app.py 0 >"$app_log" 2>&1 &
app_pid=$!
for _ in {1..60}; do
  if app_url="$(extract_demo_url "$app_log")"; then
    break
  fi
  if ! kill -0 "$app_pid" 2>/dev/null; then
    echo "demo app exited before publishing URL" >&2
    sed -n '1,80p' "$app_log" >&2
    exit 1
  fi
  sleep 0.1
done
if [[ -z "$app_url" ]]; then
  echo "demo app did not start" >&2
  sed -n '1,80p' "$app_log" >&2
  exit 1
fi

"$chrome" \
  --headless=new \
  --disable-gpu \
  --no-first-run \
  --no-default-browser-check \
  --user-data-dir="$state_dir/chrome-profile" \
  --remote-debugging-port=0 \
  --remote-debugging-address=127.0.0.1 \
  "$app_url" >"$chrome_log" 2>&1 &
chrome_pid=$!

browser_url=""
for _ in {1..100}; do
  if [[ -f "$state_dir/chrome-profile/DevToolsActivePort" ]]; then
    read -r port < "$state_dir/chrome-profile/DevToolsActivePort"
    port="${port//$'\r'/}"
    if [[ "$port" =~ ^[0-9]+$ ]]; then
      browser_url="http://127.0.0.1:$port"
      break
    fi
  fi
  if ! kill -0 "$chrome_pid" 2>/dev/null; then
    echo "chrome exited before DevToolsActivePort became available" >&2
    sed -n '1,80p' "$chrome_log" >&2
    exit 1
  fi
  sleep 0.1
done
if [[ -z "$browser_url" ]]; then
  echo "Chrome did not expose DevToolsActivePort" >&2
  exit 1
fi

"$binary" doctor --browser-url "$browser_url" --json \
  | jq -e '.checks[] | select(.name == "browser_debug_endpoint" and .status == "pass")' >/dev/null
"$binary" daemon start --browser-url "$browser_url" --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .daemon.state == "running"' >/dev/null
"$binary" daemon keepalive --browser-url "$browser_url" --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .state == "healthy" and .action == "none"' >/dev/null
"$binary" daemon logs --state-dir "$state_dir/cdp-state" --tail 20 --json \
  | jq -e '.ok == true and (.entries[] | select(.event == "rpc_listening"))' >/dev/null
"$binary" pages --state-dir "$state_dir/cdp-state" --json \
  | jq -e --arg url "$app_url/" '.ok == true and (.pages[] | select(.url == $url))' >/dev/null
"$binary" page select --url-contains "$app_url" --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .selected_page.target_id == .target.id' >/dev/null
"$binary" wait text "Ready from demo app" --state-dir "$state_dir/cdp-state" --timeout 5s --json \
  | jq -e '.ok == true and .wait.matched == true' >/dev/null
"$binary" workflow page-load --url-contains "$app_url" --reload --state-dir "$state_dir/cdp-state" --wait 1s --out "$state_dir/page-load.local.json" --json \
  | jq -e --arg path "$state_dir/page-load.local.json" '.ok == true and .workflow.name == "page-load" and .workflow.trigger == "reload" and .artifact.path == $path and (.storage.local_storage_keys | type == "array") and (.performance.count | type == "number")' >/dev/null
require_artifact "$state_dir/page-load.local.json"
"$binary" text main --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and (.text.text | contains("CDP CLI Demo Ready"))' >/dev/null
rendered_dir="$state_dir/rendered-extract"
"$binary" workflow rendered-extract "$app_url" --state-dir "$state_dir/cdp-state" --out-dir "$rendered_dir" --wait 5s --json \
  | jq -e --arg dir "$rendered_dir" '.ok == true and .workflow.name == "rendered-extract" and .readiness.navigated_from_about_blank == true and .target.url != "about:blank" and .quality.visible_word_count > 5 and .quality.html_length > 64 and .artifacts.visible_txt == ($dir + "/visible.txt") and .artifacts.markdown == ($dir + "/page.md") and .artifacts.links_json == ($dir + "/links.json")' >/dev/null
require_artifact "$rendered_dir/visible.txt"
require_artifact "$rendered_dir/html.json"
require_artifact "$rendered_dir/page.md"
require_artifact "$rendered_dir/links.json"
"$binary" snapshot --selector article --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .snapshot.selector == "article" and (.snapshot.items | length >= 1)' >/dev/null
"$binary" snapshot --selector "#missing-empty-fixture" --diagnose-empty --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .snapshot.count == 0 and (.warnings | length >= 1) and .diagnostics.selector_matched == false and .diagnostics.document_ready_state != "" and (.diagnostics.suggested_commands | length >= 1)' >/dev/null
"$binary" html "#missing-empty-fixture" --diagnose-empty --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .html.count == 0 and (.warnings | length >= 1) and .diagnostics.selector_match_count == 0 and (.diagnostics.possible_causes | index("selector_matched_zero"))' >/dev/null
"$binary" click "#action" --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "clicked" and .click.clicked == true and .click.selector == "#action"' >/dev/null
"$binary" fill "#agent-input" "filled value" --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "filled" and .fill.filled == true and .fill.value == "filled value"' >/dev/null
"$binary" type "#agent-input" " plus typed" --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "typed" and .type.typing == true and .type.typed == " plus typed"' >/dev/null
"$binary" press Enter --selector "#agent-input" --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "pressed" and .press.dispatched == true and .press.key == "Enter"' >/dev/null
"$binary" hover "#action" --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "hovered" and .hover.hovered == true and .hover.count >= 1' >/dev/null
"$binary" drag "#drag-target" 8 12 --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "dragged" and .drag.dragged == true and .drag.delta_x == 8 and .drag.delta_y == 12' >/dev/null
"$binary" frames --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and (.frames | length >= 1)' >/dev/null
"$binary" dom query button --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and (.nodes | length >= 1)' >/dev/null
"$binary" css inspect main --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .inspect.found == true' >/dev/null
"$binary" layout overflow --state-dir "$state_dir/cdp-state" --selector '.overflow' --json \
  | jq -e '.ok == true and (.items | length >= 1)' >/dev/null
"$binary" console --state-dir "$state_dir/cdp-state" --errors --wait 250ms --json \
  | jq -e '.ok == true and (.messages[] | select(.text | contains("synthetic demo error")))' >/dev/null
probe_id="$(date +%s%N)"
network_output="$state_dir/network.json"
"$binary" network --state-dir "$state_dir/cdp-state" --failed --wait 2s --json >"$network_output" &
network_pid=$!
sleep 0.2
"$binary" eval "fetch('$app_url/api/fail?probe=$probe_id').then(r => r.status)" --state-dir "$state_dir/cdp-state" --await-promise --json \
  | jq -e '.ok == true and .result.value == 503' >/dev/null
wait "$network_pid"
require_artifact "$network_output"
jq -e --arg probe "$probe_id" '.ok == true and (.requests[] | select((.url | contains($probe)) and .status == 503))' "$network_output" >/dev/null
capture_output="$state_dir/network-capture.json"
"$binary" network capture --state-dir "$state_dir/cdp-state" --url-contains "$app_url" --reload --wait 2s --redact safe --out "$state_dir/network-capture.local.json" --json >"$capture_output"
require_artifact "$capture_output"
jq -e --arg path "$state_dir/network-capture.local.json" '.ok == true and .artifact.path == $path and .capture.trigger == "reload" and (.requests[] | select((.url | contains("/api/ok")) and .body.text and (.body.text | contains("\"ok\""))))' "$capture_output" >/dev/null
"$binary" storage list --state-dir "$state_dir/cdp-state" --url-contains "$app_url" --json \
  | jq -e '.ok == true and (.storage.local_storage.entries[] | select(.key == "feature" and .value == "enabled")) and (.storage.session_storage.keys | index("nonce")) and (.storage.cookies | length >= 1)' >/dev/null
"$binary" storage set localStorage feature disabled --state-dir "$state_dir/cdp-state" --url-contains "$app_url" --json \
  | jq -e '.ok == true and .storage.backend == "localStorage" and .storage.value == "disabled"' >/dev/null
"$binary" storage get localStorage feature --state-dir "$state_dir/cdp-state" --url-contains "$app_url" --json \
  | jq -e '.ok == true and .storage.found == true and .storage.value == "disabled"' >/dev/null
"$binary" storage delete sessionStorage nonce --state-dir "$state_dir/cdp-state" --url-contains "$app_url" --json \
  | jq -e '.ok == true and .storage.backend == "sessionStorage" and .storage.found == true' >/dev/null
"$binary" storage cookies set --state-dir "$state_dir/cdp-state" --url "$app_url" --name cdp_demo --value enabled --json \
  | jq -e '.ok == true and .cookie.name == "cdp_demo"' >/dev/null
"$binary" storage cookies delete --state-dir "$state_dir/cdp-state" --url "$app_url" --name cdp_demo --json \
  | jq -e '.ok == true and .cookie.name == "cdp_demo"' >/dev/null
"$binary" storage snapshot --state-dir "$state_dir/cdp-state" --url-contains "$app_url" --include localStorage,sessionStorage,cookies,indexeddb,cache,serviceWorkers,quota --redact safe --out "$state_dir/storage.local.json" --json \
  | jq -e --arg path "$state_dir/storage.local.json" --arg scope "$app_url/" '.ok == true and .artifact.path == $path and .storage.redact == "safe" and (.snapshot.local_storage.entries[] | select(.key == "feature" and .value == "<redacted>")) and (.snapshot.indexeddb[] | select(.name == "cdp-demo-db" and (.stores[] | select(.name == "settings")))) and (.snapshot.cache_storage[] | select(.name == "cdp-demo-cache")) and (.snapshot.service_workers[] | select(.scope_url == $scope))' >/dev/null
require_artifact "$state_dir/storage.local.json"
"$binary" storage indexeddb list --state-dir "$state_dir/cdp-state" --url-contains "$app_url" --json \
  | jq -e '.ok == true and (.storage.databases[] | select(.name == "cdp-demo-db" and (.stores[] | select(.name == "settings" and .count >= 1))))' >/dev/null
"$binary" storage indexeddb get cdp-demo-db settings feature --state-dir "$state_dir/cdp-state" --url-contains "$app_url" --json \
  | jq -e '.ok == true and .storage.found == true and .storage.value.enabled == true' >/dev/null
"$binary" storage indexeddb put cdp-demo-db settings agent '{"from":"cdp"}' --state-dir "$state_dir/cdp-state" --url-contains "$app_url" --json \
  | jq -e '.ok == true and .storage.created == true and .storage.value_source == "inline"' >/dev/null
"$binary" storage indexeddb delete cdp-demo-db settings agent --state-dir "$state_dir/cdp-state" --url-contains "$app_url" --json \
  | jq -e '.ok == true and .storage.deleted == true' >/dev/null
"$binary" storage indexeddb clear cdp-demo-db settings --state-dir "$state_dir/cdp-state" --url-contains "$app_url" --json \
  | jq -e '.ok == true and .storage.cleared >= 1' >/dev/null
"$binary" storage cache list --state-dir "$state_dir/cdp-state" --url-contains "$app_url" --json \
  | jq -e '.ok == true and (.storage.caches[] | select(.name == "cdp-demo-cache" and (.requests[] | select(.url | contains("/api/cached")))))' >/dev/null
"$binary" storage cache get cdp-demo-cache "$app_url/api/cached" --state-dir "$state_dir/cdp-state" --url-contains "$app_url" --json \
  | jq -e '.ok == true and .storage.found == true and .storage.response.content_type == "application/json" and (.storage.body.text | contains("\"cached\":true"))' >/dev/null
"$binary" storage cache put cdp-demo-cache "$app_url/api/agent" '{"agent":true}' --content-type application/json --state-dir "$state_dir/cdp-state" --url-contains "$app_url" --json \
  | jq -e '.ok == true and .storage.found == true and .storage.created == true and .storage.body_source == "inline"' >/dev/null
"$binary" storage cache delete cdp-demo-cache "$app_url/api/agent" --state-dir "$state_dir/cdp-state" --url-contains "$app_url" --json \
  | jq -e '.ok == true and .storage.deleted == true' >/dev/null
"$binary" storage cache clear cdp-demo-cache --state-dir "$state_dir/cdp-state" --url-contains "$app_url" --json \
  | jq -e '.ok == true and (.storage.cleared | index("cdp-demo-cache"))' >/dev/null
"$binary" storage service-workers list --state-dir "$state_dir/cdp-state" --url-contains "$app_url" --json \
  | jq -e --arg scope "$app_url/" '.ok == true and (.storage.registrations[] | select(.scope_url == $scope))' >/dev/null
"$binary" storage service-workers unregister --scope "$app_url/" --state-dir "$state_dir/cdp-state" --url-contains "$app_url" --json \
  | jq -e --arg scope "$app_url/" '.ok == true and .storage.found == true and (.storage.unregistered[] | select(.scope_url == $scope and .result == true))' >/dev/null
"$binary" screenshot --state-dir "$state_dir/cdp-state" --out "$state_dir/demo.png" --json \
  | jq -e --arg path "$state_dir/demo.png" '.ok == true and .screenshot.path == $path and .screenshot.bytes > 0' >/dev/null
require_artifact "$state_dir/demo.png"
mkdir -p "$state_dir/debug-bundle"
"$binary" workflow debug-bundle --state-dir "$state_dir/cdp-state" --url "$app_url" --since 2s --out-dir "$state_dir/debug-bundle" --json \
  | jq -e --arg path "$state_dir/debug-bundle/debug-bundle.bundle.json" '.ok == true and .artifact.path == $path and .workflow.name == "debug-bundle" and .workflow.request_count >= 1 and .workflow.message_count >= 1 and (.artifacts | length >= 6)' >/dev/null
require_artifact "$state_dir/debug-bundle/debug-bundle.bundle.json"
"$binary" protocol exec Page.captureScreenshot --url-contains "$app_url" --params '{"format":"png"}' --save "$state_dir/protocol-shot.png" --state-dir "$state_dir/cdp-state" --json \
  | jq -e --arg path "$state_dir/protocol-shot.png" '.ok == true and .artifact.path == $path and .artifact.bytes > 0 and .result.data.omitted == true' >/dev/null
require_artifact "$state_dir/protocol-shot.png"

if [[ -n "${CDP_E2E_REAL_BUNDLE_URL:-}" ]]; then
  real_bundle_dir="$state_dir/real-bundle"
  real_bundle_path="$real_bundle_dir/debug-bundle.bundle.json"
  mkdir -p "$real_bundle_dir"
  "$binary" workflow debug-bundle --state-dir "$state_dir/cdp-state" --url "${CDP_E2E_REAL_BUNDLE_URL}" --since 2s --out-dir "$real_bundle_dir" --json \
    | jq -e --arg path "$real_bundle_path" '.ok == true and .artifact.path == $path and .workflow.name == "debug-bundle"' >/dev/null
  require_artifact "$real_bundle_path"
fi

printf 'demo e2e passed: %s\n' "$app_url"
