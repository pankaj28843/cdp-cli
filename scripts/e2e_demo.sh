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
for _ in {1..50}; do
  if [[ -s "$app_log" ]]; then
    break
  fi
  sleep 0.1
done
app_url="$(head -n 1 "$app_log")"
if [[ -z "$app_url" ]]; then
  echo "demo app did not start" >&2
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
    port="$(head -n 1 "$state_dir/chrome-profile/DevToolsActivePort")"
    browser_url="http://127.0.0.1:$port"
    break
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
"$binary" text main --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and (.text.text | contains("CDP CLI Demo Ready"))' >/dev/null
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
jq -e --arg probe "$probe_id" '.ok == true and (.requests[] | select((.url | contains($probe)) and .status == 503))' "$network_output" >/dev/null
capture_output="$state_dir/network-capture.json"
"$binary" network capture --state-dir "$state_dir/cdp-state" --url-contains "$app_url" --reload --wait 2s --redact safe --out "$state_dir/network-capture.local.json" --json >"$capture_output"
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
"$binary" storage snapshot --state-dir "$state_dir/cdp-state" --url-contains "$app_url" --include localStorage,sessionStorage,cookies,cache,serviceWorkers,quota --redact safe --out "$state_dir/storage.local.json" --json \
  | jq -e --arg path "$state_dir/storage.local.json" --arg scope "$app_url/" '.ok == true and .artifact.path == $path and .storage.redact == "safe" and (.snapshot.local_storage.entries[] | select(.key == "feature" and .value == "disabled")) and (.snapshot.cache_storage[] | select(.name == "cdp-demo-cache")) and (.snapshot.service_workers[] | select(.scope_url == $scope))' >/dev/null
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
"$binary" protocol exec Page.captureScreenshot --url-contains "$app_url" --params '{"format":"png"}' --save "$state_dir/protocol-shot.png" --state-dir "$state_dir/cdp-state" --json \
  | jq -e --arg path "$state_dir/protocol-shot.png" '.ok == true and .artifact.path == $path and .artifact.bytes > 0 and .result.data.omitted == true' >/dev/null

printf 'demo e2e passed: %s\n' "$app_url"
