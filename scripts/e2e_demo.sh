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
config_dir="$state_dir/config"
export XDG_CONFIG_HOME="$config_dir"
app_log="$state_dir/demo-app.log"
chrome_log="$state_dir/chrome.log"
app_pid=""
chrome_pid=""
app_url=""
managed_state=""
managed_stop_verification_state=""
stream_pid=""
generic_stream_pid=""
interaction_pid=""
future_interaction_pid=""

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
  if [[ ! -e "$source_file" ]]; then
    return 1
  fi
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
	if [[ -n "$managed_state" ]]; then
		"$binary" --browser-mode headless daemon stop --force-managed --state-dir "$managed_state" --json >/dev/null 2>&1 || true
	fi
	if [[ -n "$managed_stop_verification_state" ]]; then
		"$binary" --browser-mode headless daemon stop --force-managed --state-dir "$managed_stop_verification_state" --json >/dev/null 2>&1 || true
	fi
  if [[ -n "$stream_pid" ]]; then
    kill "$stream_pid" 2>/dev/null || true
    wait "$stream_pid" 2>/dev/null || true
  fi
  if [[ -n "$generic_stream_pid" ]]; then
    kill "$generic_stream_pid" 2>/dev/null || true
    wait "$generic_stream_pid" 2>/dev/null || true
  fi
  if [[ -n "$interaction_pid" ]]; then
    kill "$interaction_pid" 2>/dev/null || true
    wait "$interaction_pid" 2>/dev/null || true
  fi
  if [[ -n "$future_interaction_pid" ]]; then
    kill "$future_interaction_pid" 2>/dev/null || true
    wait "$future_interaction_pid" 2>/dev/null || true
  fi
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

managed_stop_verification_state="$state_dir/stop-check"
"$binary" --browser-mode headless daemon keepalive --repair --force --chrome-command "$chrome" --state-dir "$managed_stop_verification_state" --json \
  | jq -e '.ok == true and (.state == "started" or .state == "repaired" or .state == "healthy")' >/dev/null
managed_stop_verification_output="$("$binary" --browser-mode headless daemon stop --state-dir "$managed_stop_verification_state" --json)"
jq -e '
  .ok == true and
  .managed_browser_stopped == true and
  .managed_browser.stopped == true and
  ((.managed_browser.remaining_pids // []) | length == 0) and
  (.managed_browser.safety_checks | index("shutdown_process_tree_verified")) and
  (.managed_browser.safety_checks | index("debugging_endpoint_unreachable"))
' <<<"$managed_stop_verification_output" >/dev/null

managed_state="$state_dir/managed-cdp-state"
for _ in {1..10}; do
  "$binary" --browser-mode headless daemon keepalive --repair --force --chrome-command "$chrome" --state-dir "$managed_state" --json \
    | jq -e '.ok == true and (.state == "started" or .state == "repaired" or .state == "healthy")' >/dev/null
  "$binary" --browser-mode headless daemon stop --force-managed --state-dir "$managed_state" --json \
    | jq -e '.ok == true and .stopped == true' >/dev/null
done
"$binary" --browser-mode headless daemon keepalive --repair --force --managed-process-sweep --chrome-command "$chrome" --state-dir "$managed_state" --json \
  | jq -e '.ok == true and (.state == "started" or .state == "repaired" or .state == "healthy") and .health.managed_process_sweep.historical_processes.live_probes_attempted == 0 and .health.managed_process_sweep.compacted_count >= 1 and .health.managed_process_sweep.registered_count == 8' >/dev/null
"$binary" --browser-mode headless daemon health --state-dir "$managed_state" --json \
  | jq -e '.ok == true and .health.state == "healthy" and .health.usable == true and .health.managed_processes.historical_processes.live_probes_attempted == 0' >/dev/null
"$binary" --browser-mode headless daemon health-check --managed-process-sweep --require-healthy --chrome-command "$chrome" --state-dir "$managed_state" --json \
  | jq -e '.ok == true and .state == "healthy" and .usable == true and .managed_process_sweep.state == "healthy"' >/dev/null
"$binary" --browser-mode headless daemon stop --force-managed --state-dir "$managed_state" --json >/dev/null

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
set +e
marker_headless_output="$("$binary" --browser-mode headless browser marker enable --name demo-marker --state-dir "$state_dir/cdp-state" --json)"
marker_headless_code=$?
set -e
if [[ "$marker_headless_code" -ne 2 ]]; then
  echo "installed headless marker guard exit code: $marker_headless_code" >&2
  printf '%s\n' "$marker_headless_output" >&2
  exit 1
fi
jq -e '.ok == false and .code == "invalid_browser_mode" and (.message | contains("headed"))' <<<"$marker_headless_output" >/dev/null
"$binary" daemon keepalive --browser-url "$browser_url" --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .state == "healthy" and .action == "none"' >/dev/null
"$binary" daemon logs --state-dir "$state_dir/cdp-state" --tail 20 --json \
  | jq -e '.ok == true and (.entries[] | select(.event == "rpc_listening"))' >/dev/null
"$binary" targets --retry transient --max-attempts 2 --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .retry_policy == "transient" and .attempt_count == 1 and .attempts[0].ok == true and (.targets | type == "array")' >/dev/null
"$binary" pages --state-dir "$state_dir/cdp-state" --json \
  | jq -e --arg url "$app_url/" '.ok == true and (.pages[] | select(.url == $url))' >/dev/null
"$binary" pages --retry transient --max-attempts 2 --state-dir "$state_dir/cdp-state" --json \
  | jq -e --arg url "$app_url/" '.ok == true and .retry_policy == "transient" and .attempt_count == 1 and .attempts[0].ok == true and (.pages[] | select(.url == $url))' >/dev/null
set +e
eval_timeout_output="$("$binary" eval 'new Promise(resolve => setTimeout(() => resolve(document.title), 1500))' --await-promise --timeout 150ms --state-dir "$state_dir/cdp-state" --json)"
eval_timeout_code=$?
set -e
if [[ "$eval_timeout_code" -ne 5 ]]; then
  echo "installed eval timeout exit code: $eval_timeout_code" >&2
  printf '%s\n' "$eval_timeout_output" >&2
  exit 1
fi
jq -e '.ok == false and .code == "timeout" and .err_class == "timeout"' <<<"$eval_timeout_output" >/dev/null
"$binary" eval 'document.title' --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .result.value == "cdp-cli demo app"' >/dev/null
"$binary" pages --state-dir "$state_dir/cdp-state" --json \
  | jq -e --arg url "$app_url/" '.ok == true and (.pages[] | select(.url == $url))' >/dev/null
"$binary" page select --url-contains "$app_url" --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .selected_page.target_id == .target.id' >/dev/null
collector_target_id="$("$binary" pages --state-dir "$state_dir/cdp-state" --json | jq -r --arg url "$app_url/" '.pages[] | select(.url == $url) | .id' | head -n 1)"
collector_ready_root="$state_dir/collector-ready"
collector_ready_file="$collector_ready_root/console.ready.json"
collector_output="$state_dir/collector-console.json"
mkdir -m 700 "$collector_ready_root"
"$binary" console --target "$collector_target_id" --wait 3s --ready-file "$collector_ready_file" --state-dir "$state_dir/cdp-state" --json >"$collector_output" &
collector_pid=$!
for _ in {1..100}; do
  [[ -s "$collector_ready_file" ]] && break
  if ! kill -0 "$collector_pid" 2>/dev/null; then
    echo "collector exited before readiness" >&2
    wait "$collector_pid"
    exit 1
  fi
  sleep 0.05
done
jq -e --arg target "$collector_target_id" '.schema_version == "cdp-collector-readiness/v1" and .state == "ready" and .target_id == $target and .session_bound == true and .collector_pid > 0 and .ready_monotonic_ns > 0 and (.enabled_domains | sort == ["Log","Runtime"]) and (has("url") | not) and (has("headers") | not) and (has("cookies") | not)' "$collector_ready_file" >/dev/null
"$binary" eval "window.setTimeout(() => { throw new Error('ready-collector-exception') }, 0); true" --target "$collector_target_id" --state-dir "$state_dir/cdp-state" --json >/dev/null
wait "$collector_pid"
jq -e '.ok == true and (.messages[] | select(.type == "exception" and (.text | contains("ready-collector-exception"))))' "$collector_output" >/dev/null
if [[ -e "$collector_ready_file" ]]; then
  echo "collector readiness artifact remained after collector exit" >&2
  exit 1
fi
stream_ready_root="$state_dir/stream-ready"
stream_ready_file="$stream_ready_root/events.ready.json"
stream_output="$state_dir/events-stream.jsonl"
mkdir -m 700 "$stream_ready_root"
"$binary" events stream --target "$collector_target_id" --enable runtime --match Runtime.consoleAPICalled --duration 2s --ready-file "$stream_ready_file" --state-dir "$state_dir/cdp-state" --json < <(sleep 5) >"$stream_output" &
stream_pid=$!
for _ in {1..100}; do
  [[ -s "$stream_ready_file" ]] && break
  if ! kill -0 "$stream_pid" 2>/dev/null; then
    echo "event stream exited before readiness" >&2
    sed -n '1,80p' "$stream_output" >&2 || true
    wait "$stream_pid"
    exit 1
  fi
  sleep 0.05
done
jq -e --arg target "$collector_target_id" '.schema_version == "cdp-collector-readiness/v1" and .state == "ready" and .target_id == $target and .session_bound == true and (.enabled_domains | sort == ["runtime"])' "$stream_ready_file" >/dev/null
"$binary" eval "console.error('stream-synthetic-marker')" --target "$collector_target_id" --state-dir "$state_dir/cdp-state" --json >/dev/null
wait "$stream_pid"
stream_pid=""
jq -s -e --arg target "$collector_target_id" '
  ([.[] | select(.type == "ready" and .target.id == $target and .stream.session_bound == true)] | length) == 1 and
  ([.[] | select(.type == "event" and .event.method == "Runtime.consoleAPICalled" and (.event.sessionId | type == "string" and length > 0) and (.event.params.args[0].value == "stream-synthetic-marker"))] | length) == 1 and
  ([.[] | select(.type == "stopped" and .reason == "duration" and .event_count >= 1 and .truncated == false)] | length) == 1
' "$stream_output" >/dev/null
if [[ -e "$stream_ready_file" ]]; then
  echo "event stream readiness artifact remained after stream exit" >&2
  exit 1
fi
generic_stream_ready_root="$state_dir/generic-stream-ready"
generic_stream_ready_file="$generic_stream_ready_root/events.ready.json"
generic_stream_output="$state_dir/events-stream-generic-domain.jsonl"
mkdir -m 700 "$generic_stream_ready_root"
"$binary" events stream --target "$collector_target_id" --enable DOM --match DOM.documentUpdated --duration 3s --max-events 1 --ready-file "$generic_stream_ready_file" --state-dir "$state_dir/cdp-state" --json < <(sleep 5) >"$generic_stream_output" &
generic_stream_pid=$!
for _ in {1..100}; do
  [[ -s "$generic_stream_ready_file" ]] && break
  if ! kill -0 "$generic_stream_pid" 2>/dev/null; then
    echo "generic-domain event stream exited before readiness" >&2
    sed -n '1,80p' "$generic_stream_output" >&2 || true
    wait "$generic_stream_pid"
    exit 1
  fi
  sleep 0.05
done
jq -e --arg target "$collector_target_id" '.schema_version == "cdp-collector-readiness/v1" and .state == "ready" and .target_id == $target and .session_bound == true and (.enabled_domains | sort == ["DOM"])' "$generic_stream_ready_file" >/dev/null
"$binary" page reload --target "$collector_target_id" --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "reloaded"' >/dev/null
wait "$generic_stream_pid"
generic_stream_pid=""
jq -s -e --arg target "$collector_target_id" '
  ([.[] | select(.type == "ready" and .target.id == $target and .stream.session_bound == true and (.stream.enabled_domains | sort == ["DOM"]))] | length) == 1 and
  ([.[] | select(.type == "event" and .event.method == "DOM.documentUpdated" and (.event.sessionId | type == "string" and length > 0))] | length) >= 1 and
  ([.[] | select(.type == "stopped" and .reason == "max_events" and .event_count == 1 and .truncated == true)] | length) == 1
' "$generic_stream_output" >/dev/null
if [[ -e "$generic_stream_ready_file" ]]; then
  echo "generic-domain event stream readiness artifact remained after stream exit" >&2
  exit 1
fi
interaction_ready_root="$state_dir/interaction-ready"
interaction_ready_file="$interaction_ready_root/interactions.ready.json"
interaction_output="$state_dir/events-interactions.jsonl"
mkdir -m 700 "$interaction_ready_root"
"$binary" events interactions --target "$collector_target_id" --match click --max-events 1 --duration 5s --ready-file "$interaction_ready_file" --state-dir "$state_dir/cdp-state" --json >"$interaction_output" &
interaction_pid=$!
for _ in {1..100}; do
  [[ -s "$interaction_ready_file" ]] && break
  if ! kill -0 "$interaction_pid" 2>/dev/null; then
    echo "interaction observer exited before readiness" >&2
    sed -n '1,80p' "$interaction_output" >&2 || true
    wait "$interaction_pid"
    exit 1
  fi
  sleep 0.05
done
jq -e --arg target "$collector_target_id" '.schema_version == "cdp-collector-readiness/v1" and .state == "ready" and .target_id == $target and .session_bound == true and (.enabled_domains | sort == ["page","runtime"]) and (has("url") | not) and (has("headers") | not) and (has("cookies") | not)' "$interaction_ready_file" >/dev/null
"$binary" click "Click target" --by role --role button --strategy raw-input --force --target "$collector_target_id" --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "clicked"' >/dev/null
wait "$interaction_pid"
interaction_pid=""
jq -s -e --arg target "$collector_target_id" '
  ([.[] | select(.type == "ready" and .target.id == $target and .observer.sanitized_payload == true and .observer.current_document_installed == true and .observer.future_documents_installed == true)] | length) == 1 and
  ([.[] | select(.type == "interaction" and .interaction.type == "click" and .event.method == "Runtime.bindingCalled" and ((.interaction | has("text")) | not) and ((.interaction | has("value")) | not) and ((.interaction | has("key")) | not))] | length) == 1 and
  ([.[] | select(.type == "stopped" and .reason == "max_events" and .cleanup.current_document_removed == true and .cleanup.future_document_removed == true and .cleanup.binding_removed == true)] | length) == 1
' "$interaction_output" >/dev/null
if [[ -e "$interaction_ready_file" ]]; then
  echo "interaction observer readiness artifact remained after observer exit" >&2
  exit 1
fi
future_interaction_ready_root="$state_dir/future-interaction-ready"
future_interaction_ready_file="$future_interaction_ready_root/interactions.ready.json"
future_interaction_output="$state_dir/events-interactions-future.jsonl"
mkdir -m 700 "$future_interaction_ready_root"
"$binary" events interactions --target "$collector_target_id" --match click --max-events 1 --duration 8s --ready-file "$future_interaction_ready_file" --state-dir "$state_dir/cdp-state" --json >"$future_interaction_output" &
future_interaction_pid=$!
for _ in {1..100}; do
  [[ -s "$future_interaction_ready_file" ]] && break
  if ! kill -0 "$future_interaction_pid" 2>/dev/null; then
    echo "future-document interaction observer exited before readiness" >&2
    sed -n '1,80p' "$future_interaction_output" >&2 || true
    wait "$future_interaction_pid"
    exit 1
  fi
  sleep 0.05
done
"$binary" page reload --target "$collector_target_id" --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "reloaded"' >/dev/null
"$binary" click "Click target" --by role --role button --strategy raw-input --force --target "$collector_target_id" --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "clicked"' >/dev/null
wait "$future_interaction_pid"
future_interaction_pid=""
jq -s -e --arg target "$collector_target_id" '
  ([.[] | select(.type == "ready" and .target.id == $target and .observer.future_documents_installed == true)] | length) == 1 and
  ([.[] | select(.type == "interaction" and .interaction.type == "click" and .event.method == "Runtime.bindingCalled")] | length) == 1 and
  ([.[] | select(.type == "stopped" and .reason == "max_events" and .cleanup.current_document_removed == true and .cleanup.future_document_removed == true and .cleanup.binding_removed == true)] | length) == 1
' "$future_interaction_output" >/dev/null
if [[ -e "$future_interaction_ready_file" ]]; then
  echo "future-document interaction readiness artifact remained after observer exit" >&2
  exit 1
fi
reuse_open_output="$("$binary" open "$app_url?cdp_reused=1" --reuse --url-contains "$app_url" --budget-summary --state-dir "$state_dir/cdp-state" --json)"
jq -e '.ok == true and .action == "reused" and .reused == true and .created == false and .reuse.matched == true and .tab_budget.policy == "reuse_url_contains" and .tab_budget.cleanup_status == "skipped_reused_tab" and .tab_budget.managed_tab_created == false and (.tab_budget.before.tab_count >= 1) and (.tab_budget.after.tab_count == .tab_budget.before.tab_count)' <<<"$reuse_open_output" >/dev/null
task_open_output="$("$binary" open "$app_url?cdp_task_owned=1" --run-id demo-run --task-id demo-child --root-task-id demo-root --parent-task-id demo-root --state-dir "$state_dir/cdp-state" --json)"
jq -e '.ok == true and .run_id == "demo-run" and .task_id == "demo-child" and .root_task_id == "demo-root" and .parent_task_id == "demo-root" and .created_by == "cdp" and (.target_task_ids[.page.id] == "demo-child")' <<<"$task_open_output" >/dev/null
task_target_id="$(jq -r '.page.id' <<<"$task_open_output")"
"$binary" page cleanup --root-task-id demo-root --idle-for 0s --close --force --state-dir "$state_dir/cdp-state" --json \
  | jq -e --arg id "$task_target_id" '.ok == true and .cleanup.task_scope == true and .cleanup.root_task_id == "demo-root" and .cleanup.closed_count == 1 and (.cleanup.target_task_ids[$id] == "demo-child") and (.closed[] | select(.target.targetId == $id and .task_id == "demo-child"))' >/dev/null
"$binary" pages --state-dir "$state_dir/cdp-state" --json \
  | jq -e --arg id "$task_target_id" '.ok == true and all(.pages[]; .id != $id)' >/dev/null
"$binary" wait text "Ready from demo app" --state-dir "$state_dir/cdp-state" --timeout 5s --json \
  | jq -e '.ok == true and .wait.matched == true' >/dev/null
"$binary" eval 'window.__cdpDemoStartSemanticDelay(600)' --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .result.value.terminalCondition == "loading"' >/dev/null
semantic_dir="$state_dir/semantic-readiness"
"$binary" wait eval 'window.__cdpDemoSemanticState' --ready-expr 'value.terminalCondition === "fare_rows"' --retry transient --max-attempts 2 --poll 50ms --timeout 3s --out-dir "$semantic_dir" --artifact-prefix demo-stage --state-dir "$state_dir/cdp-state" --json \
  | jq -e --arg dir "$semantic_dir" '.ok == true and .retry_policy == "transient" and .attempt_count == 1 and .attempts[0].ok == true and .wait.kind == "eval" and .wait.ready == true and .wait.matched == true and .wait.ready_expression == "value.terminalCondition === \"fare_rows\"" and .wait.last_value.terminalCondition == "fare_rows" and .wait.last_value.rowCount == 3 and .wait.attempt_count >= 2 and (.wait.attempts | length) == .wait.attempt_count and (.wait.artifacts | length) == .wait.attempt_count and (.artifacts | length) == .wait.attempt_count and (.wait.artifacts[0].path | startswith($dir))' >/dev/null
require_artifact "$semantic_dir/demo-stage-attempt-01.json"
set +e
semantic_timeout_output="$("$binary" wait eval 'window.__cdpDemoNeverReady()' --ready-expr 'value.terminalCondition === "fare_rows"' --poll 100ms --timeout 300ms --state-dir "$state_dir/cdp-state" --json)"
semantic_timeout_code=$?
set -e
if [[ "$semantic_timeout_code" -ne 5 ]]; then
  echo "wait eval semantic timeout exit code: $semantic_timeout_code" >&2
  printf '%s\n' "$semantic_timeout_output" >&2
  exit 1
fi
jq -e '.ok == false and .code == "timeout" and .data.wait.kind == "eval" and .data.wait.ready == false and .data.wait.matched == false and .data.wait.last_value.terminalCondition == "loading" and .data.wait.attempt_count >= 1 and .data.wait.evidence.ready == false' <<<"$semantic_timeout_output" >/dev/null
stop_url="$app_url/stop-login"
"$binary" open "$stop_url" --reuse --url-contains "$app_url" --state-dir "$state_dir/cdp-state" --json \
  | jq -e --arg url "$stop_url" '.ok == true and .reused == true and .created == false and (.page.url | contains($url))' >/dev/null
set +e
stop_state_wait_output="$("$binary" wait eval '({ready:false})' --ready-field ready --classify-stop-state --poll 100ms --timeout 2s --state-dir "$state_dir/cdp-state" --json)"
stop_state_wait_code=$?
set -e
if [[ "$stop_state_wait_code" -ne 1 ]]; then
  echo "wait eval stop-state exit code: $stop_state_wait_code" >&2
  printf '%s\n' "$stop_state_wait_output" >&2
  exit 1
fi
jq -e '.ok == false and .code == "stop_state" and .stop_state == "login_required" and .stop_state_class == "auth" and .agent_should_stop == true and .human_required == true and .data.wait.kind == "eval" and .data.wait.matched == false and .data.stop_state_result.stop_state == "login_required" and (.next_commands | any(contains("daemon status"))) and (.remediation_commands | any(contains("daemon status")))' <<<"$stop_state_wait_output" >/dev/null
"$binary" open "$app_url" --reuse --url-contains "$app_url" --state-dir "$state_dir/cdp-state" --json \
  | jq -e --arg url "$app_url" '.ok == true and .reused == true and .created == false and (.page.url | contains($url))' >/dev/null
"$binary" assert url "$app_url" --mode contains --state-dir "$state_dir/cdp-state" --timeout 2s --poll 100ms --json \
  | jq -e --arg url "$app_url" '.ok == true and .assertion.field == "url" and .assertion.passed == true and (.assertion.actual | contains($url)) and (.assertion.url | contains($url)) and .assertion.title == "cdp-cli demo app" and .assertion.attempts >= 1 and .assertion.poll_interval == "100ms" and (.target.url | contains($url))' >/dev/null
"$binary" wait url "$app_url" --mode contains --state-dir "$state_dir/cdp-state" --timeout 2s --poll 100ms --json \
  | jq -e --arg url "$app_url" '.ok == true and .wait.kind == "url" and .wait.condition == "contains" and .wait.matched == true and .wait.needle == $url and (.wait.url | contains($url)) and .wait.title == "cdp-cli demo app" and .wait.poll_interval == "100ms" and .wait.evidence.condition == "contains" and (.target.url | contains($url))' >/dev/null
"$binary" assert title "cdp-cli demo app" --mode exact --state-dir "$state_dir/cdp-state" --timeout 2s --poll 100ms --json \
  | jq -e --arg url "$app_url" '.ok == true and .assertion.field == "title" and .assertion.passed == true and .assertion.actual == "cdp-cli demo app" and .assertion.title == "cdp-cli demo app" and (.assertion.url | contains($url)) and .assertion.attempts >= 1 and .assertion.poll_interval == "100ms" and .target.title == "cdp-cli demo app"' >/dev/null
"$binary" emulate timezone --timezone-id UTC --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .emulation.timezone.timezone_id == "UTC" and .emulation.timezone.observed_timezone == "UTC" and .emulation.timezone.verified == true and (.emulation.cleanup_command | contains("cdp emulate clear"))' >/dev/null
"$binary" emulate locale --locale de-DE --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .emulation.locale.locale == "de-DE" and .emulation.locale.observed_locale == "de-DE" and .emulation.locale.verified == true and (.emulation.cleanup_command | contains("cdp emulate clear"))' >/dev/null
"$binary" emulate color-scheme --scheme dark --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .emulation.color_scheme.scheme == "dark" and .emulation.color_scheme.observed_scheme == "dark" and .emulation.color_scheme.verified == true and (.emulation.cleanup_command | contains("cdp emulate clear"))' >/dev/null
"$binary" emulate clear --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and (.emulation.cleared_overrides | index("timezone")) and (.emulation.cleared_overrides | index("locale")) and (.emulation.cleared_overrides | index("media"))' >/dev/null
app_origin="${app_url%/}"
"$binary" permissions grant notifications --origin "$app_origin" --state-dir "$state_dir/cdp-state" --json \
  | jq -e --arg origin "$app_origin" '.ok == true and .permissions.action == "grant" and .permissions.origin == $origin and .permissions.browser_scoped == true and (.permissions.permissions[] | select(.name == "notifications" and .setting == "granted" and .method == "Browser.setPermission")) and (.permissions.reset_command | contains("permissions reset"))' >/dev/null
"$binary" permissions set notifications --setting denied --origin "$app_origin" --state-dir "$state_dir/cdp-state" --json \
  | jq -e --arg origin "$app_origin" '.ok == true and .permissions.action == "set" and .permissions.origin == $origin and .permissions.setting == "denied" and (.permissions.permissions[] | select(.name == "notifications" and .setting == "denied" and .method == "Browser.setPermission")) and (.permissions.reset_command | contains("permissions reset"))' >/dev/null
"$binary" permissions reset --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .permissions.action == "reset" and .permissions.method == "Browser.resetPermissions" and .permissions.browser_scoped == true and .permissions.reset_all_origins == true' >/dev/null
"$binary" workflow page-load --url-contains "$app_url" --reload --state-dir "$state_dir/cdp-state" --wait 1s --out "$state_dir/page-load.local.json" --json \
  | jq -e --arg path "$state_dir/page-load.local.json" '.ok == true and .workflow.name == "page-load" and .workflow.trigger == "reload" and .artifact.path == $path and .content_state.class == "content" and .content_state.actionable == true and (.storage.local_storage_keys | type == "array") and (.performance.count | type == "number")' >/dev/null
require_artifact "$state_dir/page-load.local.json"
"$binary" text main --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and (.text.text | contains("CDP CLI Demo Ready"))' >/dev/null
"$binary" a11y snapshot --selector body --depth 5 --limit 80 --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .snapshot.selector == "body" and .snapshot.source == "cdp-accessibility-tree" and .snapshot.line_count >= 1 and (.snapshot.lines | length) == .snapshot.line_count and (.snapshot.text | contains("Click target")) and (.text | contains("button"))' >/dev/null
"$binary" assert aria-snapshot --expected '- button "Click target"' --selector body --depth 5 --limit 80 --timeout 2s --poll 100ms --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .assertion.passed == true and .assertion.selector == "body" and .assertion.mode == "contains" and .assertion.expected == "- button \"Click target\"" and (.assertion.actual_lines | index("- button \"Click target\"")) and .assertion.line_count == .snapshot.line_count and .assertion.attempts >= 1 and .assertion.poll_interval == "100ms"' >/dev/null
"$binary" locator find "Agent input" --by label --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .locator.strict == true and .locator.matches[0].selector_hint == "input#agent-input" and any(.next_commands[]; contains("cdp fill"))' >/dev/null
"$binary" locator find "Click target" --by role --role button --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .locator.strict == true and .locator.matches[0].role == "button" and .locator.matches[0].selector_hint == "button#action"' >/dev/null
"$binary" assert count "Click target" 1 --by role --role button --timeout 2s --poll 100ms --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .assertion.passed == true and .assertion.expected == 1 and .assertion.actual == 1 and .assertion.count == 1 and .assertion.attempts >= 1 and .assertion.poll_interval == "100ms" and .locator.count == 1 and .locator.strict == true' >/dev/null
"$binary" assert attribute "Click target" id action --by role --role button --timeout 2s --poll 100ms --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .assertion.passed == true and .assertion.selector == "button#action" and .assertion.attribute == "id" and .assertion.attribute_present == true and .assertion.expected == "action" and .assertion.actual == "action" and .assertion.mode == "exact" and .assertion.attempts >= 1 and .assertion.poll_interval == "100ms" and .locator.strict == true and .resolved_selector == "button#action"' >/dev/null
"$binary" assert class article card --timeout 2s --poll 100ms --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .assertion.passed == true and .assertion.selector == "article" and .assertion.class_name == "card" and .assertion.expected == "card" and .assertion.has_class == true and .assertion.matching_count == 1 and .assertion.count == 1 and (.assertion.items[0].class_list | index("card")) and .assertion.poll_interval == "100ms"' >/dev/null
"$binary" wait locator "Agent input" --by label --strict --state-dir "$state_dir/cdp-state" --timeout 5s --json \
  | jq -e '.ok == true and .wait.kind == "locator" and .wait.matched == true and .wait.strict == true and .locator.strict == true and .wait.resolved_selector == "input#agent-input"' >/dev/null
"$binary" fill "Agent input" "locator filled value" --by label --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "filled" and .locator.strict == true and .resolved_selector == "input#agent-input" and .fill.selector == "input#agent-input" and .fill.value == "locator filled value" and .actionability.actionable == true and .actionability.checks.editable.passed == true' >/dev/null
"$binary" assert value "Agent input" "locator filled value" --by label --timeout 2s --poll 100ms --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .assertion.passed == true and .assertion.attempts >= 1 and .assertion.poll_interval == "100ms" and .locator.strict == true and .resolved_selector == "input#agent-input" and .assertion.selector == "input#agent-input"' >/dev/null
"$binary" assert focused "Agent input" --by label --timeout 2s --poll 100ms --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .assertion.passed == true and .assertion.focused == true and .assertion.focused_count == 1 and .assertion.active_selector == "input#agent-input" and .assertion.attempts >= 1 and .assertion.poll_interval == "100ms" and .locator.strict == true and .resolved_selector == "input#agent-input" and .assertion.selector == "input#agent-input"' >/dev/null
"$binary" assert css "Click target" background-color "rgb(20, 92, 160)" --by role --role button --timeout 2s --poll 100ms --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .assertion.passed == true and .assertion.selector == "button#action" and .assertion.property == "background-color" and .assertion.expected == "rgb(20, 92, 160)" and .assertion.actual == "rgb(20, 92, 160)" and .assertion.mode == "exact" and .assertion.count == 1 and .assertion.attempts >= 1 and .assertion.poll_interval == "100ms" and .locator.strict == true and .resolved_selector == "button#action"' >/dev/null
"$binary" assert role "Click target" button --by role --role button --timeout 2s --poll 100ms --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .assertion.passed == true and .assertion.field == "role" and .assertion.expected == "button" and .assertion.actual == "button" and .assertion.count == 1 and .assertion.attempts >= 1 and .assertion.poll_interval == "100ms" and .locator.strict == true and .resolved_selector == "button#action"' >/dev/null
"$binary" assert name "Click target" "Click target" --by role --role button --timeout 2s --poll 100ms --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .assertion.passed == true and .assertion.field == "name" and .assertion.expected == "Click target" and .assertion.actual == "Click target" and .assertion.mode == "exact" and .assertion.count == 1 and .assertion.attempts >= 1 and .assertion.poll_interval == "100ms" and .locator.strict == true and .resolved_selector == "button#action"' >/dev/null
"$binary" fill "Agent input" "trial should not write" --by label --trial --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "trial" and .fill.trial == true and .fill.filled == false and .locator.strict == true and .resolved_selector == "input#agent-input" and .actionability.trial == true and .actionability.actionable == true and .actionability.checks.visible.passed == true and .actionability.checks.enabled.passed == true and .actionability.checks.editable.passed == true' >/dev/null
"$binary" assert value "Agent input" "locator filled value" --by label --timeout 2s --poll 100ms --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .assertion.passed == true and .assertion.attempts >= 1 and .assertion.poll_interval == "100ms" and .assertion.actual == "locator filled value"' >/dev/null
"$binary" fill "#hidden-agent-input" "forced hidden value" --force --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "filled" and .fill.force == true and .fill.filled == true and .actionability.force == true and .actionability.actionable == true and (.actionability.skipped_checks | index("visible")) and .actionability.checks.visible.required == false and .actionability.checks.visible.skipped == true and .actionability.checks.visible.passed == false and .actionability.checks.editable.passed == true' >/dev/null
"$binary" assert value "#hidden-agent-input" "forced hidden value" --timeout 2s --poll 100ms --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .assertion.passed == true and .assertion.attempts >= 1 and .assertion.poll_interval == "100ms" and .assertion.actual == "forced hidden value"' >/dev/null
"$binary" select "Plan" pro --by label --trial --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "trial" and .select.trial == true and .select.selected == false and .select.value == "pro" and .locator.strict == true and .resolved_selector == "select#plan" and .actionability.trial == true and .actionability.actionable == true and .actionability.checks.visible.passed == true and .actionability.checks.enabled.passed == true and ((.actionability.required_checks | index("stable")) == null) and ((.actionability.required_checks | index("receives_events")) == null)' >/dev/null
"$binary" select "Plan" pro --by label --wait-text "Plan selected: pro" --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "selected" and .select.selected == true and .select.verified == true and .select.value == "pro" and .select.previous == "free" and .select.matched_by == "value" and (.select.selected_values | index("pro")) and .verification.kind == "text" and .verification.needle == "Plan selected: pro" and .verification.matched == true and .locator.strict == true and .resolved_selector == "select#plan" and .actionability.actionable == true and .actionability.checks.visible.passed == true and .actionability.checks.enabled.passed == true' >/dev/null
"$binary" assert value "Plan" pro --by label --timeout 2s --poll 100ms --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .assertion.passed == true and .assertion.attempts >= 1 and .assertion.poll_interval == "100ms" and .locator.strict == true and .resolved_selector == "select#plan" and .assertion.actual == "pro"' >/dev/null
"$binary" select "#hidden-plan" pro --force --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "selected" and .select.force == true and .select.selected == true and .select.value == "pro" and .actionability.force == true and .actionability.actionable == true and (.actionability.skipped_checks | index("visible")) and .actionability.checks.visible.required == false and .actionability.checks.visible.skipped == true and .actionability.checks.visible.passed == false and .actionability.checks.enabled.passed == true' >/dev/null
"$binary" eval 'document.querySelector("#subscribe").scrollIntoView({block:"center"})' --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true' >/dev/null
if ! check_trial_output="$("$binary" check "Subscribe" --by role --role checkbox --trial --state-dir "$state_dir/cdp-state" --json)"; then
  printf '%s\n' "$check_trial_output" >&2
  exit 1
fi
if ! jq -e '.ok == true and .action == "trial" and .check.trial == true and .check.checked == false and .check.desired_checked == true and .check.changed == false and ((.check.already // false) == false) and .locator.strict == true and .resolved_selector == "input#subscribe" and .actionability.trial == true and .actionability.actionable == true and .actionability.checks.visible.passed == true and .actionability.checks.stable.passed == true and .actionability.checks.receives_events.passed == true and .actionability.checks.enabled.passed == true' <<<"$check_trial_output" >/dev/null; then
  printf '%s\n' "$check_trial_output" >&2
  exit 1
fi
"$binary" check "Subscribe to newsletter" --by label --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "checked" and .check.checked == true and .check.desired_checked == true and .check.previous_checked == false and .check.changed == true and .check.type == "checkbox" and .check.role == "checkbox" and .locator.strict == true and .resolved_selector == "input#subscribe" and .actionability.actionable == true and .actionability.checks.receives_events.passed == true and .actionability.checks.enabled.passed == true' >/dev/null
"$binary" assert checked "Subscribe to newsletter" --by label --timeout 2s --poll 100ms --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .assertion.passed == true and .assertion.checked == true and .assertion.unchecked == false and .assertion.checked_count == 1 and .assertion.unchecked_count == 0 and .assertion.unsupported_count == 0 and .assertion.attempts >= 1 and .assertion.poll_interval == "100ms" and .locator.strict == true and .resolved_selector == "input#subscribe" and .assertion.items[0].role == "checkbox"' >/dev/null
"$binary" uncheck "Subscribe to newsletter" --by label --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "unchecked" and .uncheck.checked == false and .uncheck.desired_checked == false and .uncheck.previous_checked == true and .uncheck.changed == true and .uncheck.type == "checkbox" and .uncheck.role == "checkbox" and .locator.strict == true and .resolved_selector == "input#subscribe" and .actionability.actionable == true and .actionability.checks.receives_events.passed == true and .actionability.checks.enabled.passed == true' >/dev/null
"$binary" assert unchecked "Subscribe to newsletter" --by label --timeout 2s --poll 100ms --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .assertion.passed == true and .assertion.checked == false and .assertion.unchecked == true and .assertion.checked_count == 0 and .assertion.unchecked_count == 1 and .assertion.attempts >= 1 and .assertion.poll_interval == "100ms" and .locator.strict == true and .resolved_selector == "input#subscribe"' >/dev/null
"$binary" assert indeterminate "Partial selection" --by label --timeout 2s --poll 100ms --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .assertion.passed == true and .assertion.indeterminate == true and .assertion.indeterminate_count == 1 and .assertion.checked_count == 0 and .assertion.unchecked_count == 0 and .assertion.unsupported_count == 0 and .assertion.attempts >= 1 and .assertion.poll_interval == "100ms" and .locator.strict == true and .resolved_selector == "input#partial-selection" and .assertion.items[0].indeterminate == true' >/dev/null
"$binary" check "#covered-checkbox" --force --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "checked" and .check.checked == true and .check.force == true and .check.changed == true and .actionability.force == true and .actionability.actionable == true and (.actionability.skipped_checks | index("receives_events")) and .actionability.checks.receives_events.required == false and .actionability.checks.receives_events.skipped == true and .actionability.checks.receives_events.passed == false and .actionability.checks.enabled.passed == true' >/dev/null
"$binary" assert checked "#covered-checkbox" --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .assertion.passed == true and .assertion.checked == true and .assertion.checked_count == 1 and .assertion.unsupported_count == 0 and .assertion.items[0].id == "covered-checkbox" and .assertion.items[0].type == "checkbox"' >/dev/null
"$binary" eval 'window.scrollTo(0, 0)' --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true' >/dev/null
"$binary" check "Below fold checkbox" --by label --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "checked" and .check.checked == true and .check.desired_checked == true and .check.previous_checked == false and .check.changed == true and .locator.strict == true and .resolved_selector == "input#below-fold-checkbox" and .auto_scroll.before.in_viewport == false and .auto_scroll.after.in_viewport == true and .auto_scroll.changed == true and .actionability.actionable == true and .actionability.checks.receives_events.passed == true and .actionability.checks.enabled.passed == true' >/dev/null
"$binary" eval 'window.scrollTo(0, 0)' --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true' >/dev/null
"$binary" uncheck "Below fold checkbox" --by label --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "unchecked" and .uncheck.checked == false and .uncheck.desired_checked == false and .uncheck.previous_checked == true and .uncheck.changed == true and .locator.strict == true and .resolved_selector == "input#below-fold-checkbox" and .auto_scroll.before.in_viewport == false and .auto_scroll.after.in_viewport == true and .auto_scroll.changed == true and .actionability.actionable == true and .actionability.checks.receives_events.passed == true and .actionability.checks.enabled.passed == true' >/dev/null
"$binary" eval 'window.scrollTo(0, 0)' --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true' >/dev/null
"$binary" assert text "Click target" "Click target" --by role --role button --timeout 2s --poll 100ms --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .assertion.passed == true and .assertion.attempts >= 1 and .assertion.poll_interval == "100ms" and .locator.strict == true and .resolved_selector == "button#action" and .assertion.selector == "button#action" and .assertion.actual == "Click target"' >/dev/null
"$binary" assert visible "Click target" --by role --role button --timeout 2s --poll 100ms --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .assertion.passed == true and .assertion.attempts >= 1 and .assertion.poll_interval == "100ms" and .locator.strict == true and .resolved_selector == "button#action" and .assertion.selector == "button#action" and .assertion.visible == true and .assertion.visible_count == 1' >/dev/null
"$binary" assert hidden "Dismiss" --by role --role button --timeout 2s --poll 100ms --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .assertion.passed == true and .assertion.attempts >= 1 and .assertion.poll_interval == "100ms" and .locator.strict == true and .resolved_selector == "button#dismiss" and .assertion.selector == "button#dismiss" and .assertion.hidden == true and .assertion.visible_count == 0 and .assertion.hidden_count == 1' >/dev/null
"$binary" assert attached "Click target" --by role --role button --timeout 2s --poll 100ms --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .assertion.passed == true and .assertion.attempts >= 1 and .assertion.poll_interval == "100ms" and .locator.strict == true and .resolved_selector == "button#action" and .assertion.selector == "button#action" and .assertion.attached == true and .assertion.detached == false and .assertion.count == 1 and .assertion.items[0].role == "button"' >/dev/null
"$binary" assert detached "#not-present" --timeout 2s --poll 100ms --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .assertion.passed == true and .assertion.attempts >= 1 and .assertion.poll_interval == "100ms" and .assertion.selector == "#not-present" and .assertion.attached == false and .assertion.detached == true and .assertion.count == 0' >/dev/null
"$binary" assert enabled "Click target" --by role --role button --timeout 2s --poll 100ms --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .assertion.passed == true and .assertion.attempts >= 1 and .assertion.poll_interval == "100ms" and .locator.strict == true and .resolved_selector == "button#action" and .assertion.selector == "button#action" and .assertion.enabled == true and .assertion.enabled_count == 1 and .assertion.disabled_count == 0' >/dev/null
"$binary" assert disabled "Disabled target" --by role --role button --timeout 2s --poll 100ms --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .assertion.passed == true and .assertion.attempts >= 1 and .assertion.poll_interval == "100ms" and .locator.strict == true and .resolved_selector == "button#disabled-action" and .assertion.selector == "button#disabled-action" and .assertion.disabled == true and .assertion.enabled_count == 0 and .assertion.disabled_count == 1 and (.assertion.items[0].disabled_reason | index("native_disabled"))' >/dev/null
"$binary" assert editable "Agent input" --by label --timeout 2s --poll 100ms --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .assertion.passed == true and .assertion.attempts >= 1 and .assertion.poll_interval == "100ms" and .locator.strict == true and .resolved_selector == "input#agent-input" and .assertion.selector == "input#agent-input" and .assertion.editable == true and .assertion.editable_count == 1 and .assertion.read_only_count == 0 and .assertion.disabled_count == 0' >/dev/null
"$binary" assert readonly "Read-only notes" --by label --timeout 2s --poll 100ms --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .assertion.passed == true and .assertion.attempts >= 1 and .assertion.poll_interval == "100ms" and .locator.strict == true and .resolved_selector == "textarea#readonly-notes" and .assertion.selector == "textarea#readonly-notes" and .assertion.read_only == true and .assertion.editable_count == 0 and .assertion.read_only_count == 1 and (.assertion.items[0].read_only_reason | index("native_readonly"))' >/dev/null
"$binary" eval 'window.scrollTo(0, 0)' --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true' >/dev/null
"$binary" click "Click target" --by role --role button --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "clicked" and .locator.strict == true and .resolved_selector == "button#action" and .click.selector == "button#action" and .page_state.same_target == true and .actionability.actionable == true and .actionability.checks.receives_events.passed == true' >/dev/null
"$binary" click "Click target" --by role --role button --trial --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "trial" and .click.trial == true and .click.clicked == false and .locator.strict == true and .resolved_selector == "button#action" and .actionability.trial == true and .actionability.actionable == true and .actionability.checks.visible.passed == true and .actionability.checks.stable.passed == true and .actionability.checks.receives_events.passed == true and .actionability.checks.enabled.passed == true' >/dev/null
"$binary" click "#covered-action" --force --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "clicked" and .click.force == true and .click.clicked == true and .actionability.force == true and .actionability.actionable == true and (.actionability.skipped_checks | index("receives_events")) and .actionability.checks.receives_events.required == false and .actionability.checks.receives_events.skipped == true and .actionability.checks.receives_events.passed == false and .actionability.checks.enabled.passed == true' >/dev/null
hydrated_dir="$state_dir/rendered-extract-hydrated"
hydrated_report="$state_dir/rendered-extract-hydrated.json"
"$binary" eval '(() => { const main = document.querySelector("main"); main.innerHTML = "<p>Loading</p>"; setTimeout(() => { main.innerHTML = "<article><h1>Hydrated extraction marker</h1><p>The delayed application content now contains enough meaningful words to satisfy every configured extraction quality threshold.</p><p>This second sentence proves the workflow waited for hydrated content and then observed a full stable interval before capture.</p></article>"; }, 800); return true; })()' --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .result.value == true' >/dev/null
"$binary" workflow rendered-extract --url-contains "$app_url" --selector main --state-dir "$state_dir/cdp-state" --out-dir "$hydrated_dir" --min-visible-words 12 --min-markdown-words 12 --min-html-chars 64 --wait 5s --settle 1s --json >"$hydrated_report"
jq -e --arg dir "$hydrated_dir" '
  .ok == true and
  .workflow.created_page == false and
  .workflow.reused_page == true and
  .workflow.closed == false and
  .readiness.thresholds_met == true and
  .readiness.content_settled_seen == true and
  .readiness.outcome == "settled" and
  .readiness.settle == "1s" and
  (.readiness.settled_for | type == "string" and . != "0s") and
  .readiness.poll_count >= 3 and
  .readiness.capture_consistency_checked == true and
  .readiness.capture_consistent == true and
  .quality.passed == true and
  .artifacts.visible_txt == ($dir + "/visible.txt")
' "$hydrated_report" >/dev/null
require_artifact "$hydrated_dir/visible.txt"
grep -F "Hydrated extraction marker" "$hydrated_dir/visible.txt" >/dev/null
rendered_dir="$state_dir/rendered-extract"
"$binary" workflow rendered-extract "$app_url" --state-dir "$state_dir/cdp-state" --out-dir "$rendered_dir" --wait 5s --json \
  | jq -e --arg dir "$rendered_dir" '.ok == true and .workflow.name == "rendered-extract" and .readiness.navigated_from_about_blank == true and .readiness.thresholds_met == true and .readiness.content_settled_seen == true and .readiness.outcome == "settled" and .readiness.capture_consistency_checked == true and .readiness.capture_consistent == true and .target.url != "about:blank" and .quality.passed == true and .quality.visible_word_count > 5 and .quality.html_length > 64 and .artifacts.visible_txt == ($dir + "/visible.txt") and .artifacts.markdown == ($dir + "/page.md") and .artifacts.links_json == ($dir + "/links.json")' >/dev/null
require_artifact "$rendered_dir/visible.txt"
require_artifact "$rendered_dir/html.json"
require_artifact "$rendered_dir/page.md"
require_artifact "$rendered_dir/links.json"
rendered_baseline_tabs="$("$binary" pages --state-dir "$state_dir/cdp-state" --json | jq '.pages | length')"
rendered_reuse_dir="$state_dir/rendered-extract-reuse"
"$binary" workflow rendered-extract --url-contains "$app_url" --reload --state-dir "$state_dir/cdp-state" --out-dir "$rendered_reuse_dir" --wait 5s --json \
  | jq -e '.ok == true and .workflow.trigger == "reload" and .workflow.created_page == false and .workflow.reused_page == true and .workflow.reloaded == true and .workflow.closed == false and .workflow.cleanup.skipped == true and .workflow.cleanup.reason == "caller_owned"' >/dev/null
rendered_after_reuse_tabs="$("$binary" pages --state-dir "$state_dir/cdp-state" --json | jq '.pages | length')"
test "$rendered_after_reuse_tabs" -eq "$rendered_baseline_tabs"
rendered_blocked_out="$state_dir/rendered-extract-not-a-directory"
printf 'synthetic fixture\n' >"$rendered_blocked_out"
rendered_failure_json="$state_dir/rendered-extract-failure.json"
set +e
"$binary" workflow rendered-extract "$app_url" --state-dir "$state_dir/cdp-state" --out-dir "$rendered_blocked_out" --wait 1s --settle 0s --json >"$rendered_failure_json"
rendered_failure_code=$?
set -e
test "$rendered_failure_code" -eq 10
jq -e '.ok == false and .code == "artifact_write_failed"' "$rendered_failure_json" >/dev/null
rendered_after_failure_tabs="$("$binary" pages --state-dir "$state_dir/cdp-state" --json | jq '.pages | length')"
test "$rendered_after_failure_tabs" -eq "$rendered_baseline_tabs"
"$binary" snapshot --selector article --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .snapshot.selector == "article" and (.snapshot.items | length >= 1)' >/dev/null
"$binary" snapshot --selector "#missing-empty-fixture" --diagnose-empty --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .snapshot.count == 0 and (.warnings | length >= 1) and .diagnostics.selector_matched == false and .diagnostics.document_ready_state != "" and (.diagnostics.suggested_commands | length >= 1)' >/dev/null
"$binary" html "#missing-empty-fixture" --diagnose-empty --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .html.count == 0 and (.warnings | length >= 1) and .diagnostics.selector_match_count == 0 and (.diagnostics.possible_causes | index("selector_matched_zero"))' >/dev/null
"$binary" click "#action" --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "clicked" and .click.clicked == true and .click.selector == "#action" and .target.url == .after_target.url and .final_target.url == .after_target.url and .page_state.same_target == true and .page_state.url_changed == false and .actionability.actionable == true' >/dev/null
action_capture_dir="$state_dir/action-capture"
mkdir -p "$action_capture_dir"
"$binary" workflow action-capture --action click:#action --state-dir "$state_dir/cdp-state" --wait-before 0s --wait-after 250ms --include network,console,dom,text,a11y --evidence-out-dir "$action_capture_dir" --json \
  | jq -e --arg before "$action_capture_dir/action-capture.before.text.json" --arg after "$action_capture_dir/action-capture.after.dom.json" --arg before_a11y "$action_capture_dir/action-capture.before.a11y.json" --arg after_a11y "$action_capture_dir/action-capture.after.a11y.json" --arg network "$action_capture_dir/action-capture.action.network.json" --arg console "$action_capture_dir/action-capture.action.console.json" --arg manifest "$action_capture_dir/action-capture.manifest.json" '.ok == true and .workflow.name == "action-capture" and .action.type == "click" and .evidence.artifact_count == 9 and .evidence.before.text.artifact.path == $before and .evidence.after.dom.artifact.path == $after and .evidence.before.a11y.artifact.path == $before_a11y and .evidence.after.a11y.artifact.path == $after_a11y and .evidence.events.network.artifact.path == $network and .evidence.events.console.artifact.path == $console and .evidence.manifest.artifact.path == $manifest and .evidence.manifest.referenced_artifact_count == 8 and (.artifacts | length >= 9)' >/dev/null
require_artifact "$action_capture_dir/action-capture.before.text.json"
require_artifact "$action_capture_dir/action-capture.before.dom.json"
require_artifact "$action_capture_dir/action-capture.before.a11y.json"
require_artifact "$action_capture_dir/action-capture.after.text.json"
require_artifact "$action_capture_dir/action-capture.after.dom.json"
require_artifact "$action_capture_dir/action-capture.after.a11y.json"
require_artifact "$action_capture_dir/action-capture.action.network.json"
require_artifact "$action_capture_dir/action-capture.action.console.json"
require_artifact "$action_capture_dir/action-capture.manifest.json"
"$binary" fill "#agent-input" "filled value" --wait-text "Suggestion ready: filled value" --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "filled" and .fill.filled == true and .fill.verified == true and .fill.value == "filled value" and .verification.kind == "text" and .verification.needle == "Suggestion ready: filled value" and .verification.matched == true and .actionability.actionable == true' >/dev/null
"$binary" type "#agent-input" " plus typed" --wait-text "Suggestion ready: filled value plus typed" --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "typed" and .type.typing == true and .type.verified == true and .type.typed == " plus typed" and .verification.kind == "text" and .verification.needle == "Suggestion ready: filled value plus typed" and .verification.matched == true' >/dev/null
"$binary" press Enter "Agent input" --by label --trial --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "trial" and .press.trial == true and .press.dispatched == false and .locator.strict == true and .resolved_selector == "input#agent-input" and .actionability.actionable == true and .actionability.required_checks == ["attached"] and .actionability.checks.attached.passed == true and .actionability.checks.visible.required == false' >/dev/null
"$binary" press Enter "Agent input" --by label --wait-text "Submitted from press" --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "pressed" and .press.dispatched == true and .press.key == "Enter" and .press.verified == true and .press.selector == "input#agent-input" and .verification.kind == "text" and .verification.needle == "Submitted from press" and .verification.matched == true and .locator.strict == true and .resolved_selector == "input#agent-input" and .actionability.actionable == true and .actionability.required_checks == ["attached"]' >/dev/null
"$binary" press Enter --selector "#agent-input" --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "pressed" and .press.dispatched == true and .press.key == "Enter"' >/dev/null
"$binary" hover "Click target" --by role --role button --trial --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "trial" and .resolved_selector == "button#action" and .hover.trial == true and .hover.hovered == false and .actionability.actionable == true and ((.actionability.required_checks | index("enabled")) == null)' >/dev/null
"$binary" hover "Click target" --by role --role button --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "hovered" and .resolved_selector == "button#action" and .hover.hovered == true and .hover.count >= 1 and .actionability.actionable == true' >/dev/null
"$binary" hover "#covered-action" --force --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "hovered" and .hover.hovered == true and .hover.force == true and .actionability.force == true and (.actionability.skipped_checks | index("receives_events")) and .actionability.checks.receives_events.skipped == true' >/dev/null
"$binary" eval 'window.scrollTo(0, 0)' --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true' >/dev/null
"$binary" hover "scroll-target" --by test-id --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "hovered" and .resolved_selector == "button#scroll-target" and .hover.hovered == true and .auto_scroll.before.in_viewport == false and .auto_scroll.after.in_viewport == true and .auto_scroll.changed == true and .actionability.actionable == true and .actionability.checks.receives_events.passed == true' >/dev/null
"$binary" eval 'window.scrollTo(0, 0)' --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true' >/dev/null
"$binary" drag "scroll-target" 8 12 --by test-id --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "dragged" and .resolved_selector == "button#scroll-target" and .drag.dragged == true and .drag.delta_x == 8 and .drag.delta_y == 12 and .auto_scroll.before.in_viewport == false and .auto_scroll.after.in_viewport == true and .auto_scroll.changed == true and .actionability.actionable == true and .actionability.checks.receives_events.passed == true' >/dev/null
"$binary" eval 'document.querySelector("#drag-target").scrollIntoView({block:"center"})' --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true' >/dev/null
"$binary" drag "drag-target" 8 12 --by test-id --trial --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "trial" and .resolved_selector == "div#drag-target" and .drag.trial == true and .drag.dragged == false and .drag.delta_x == 8 and .drag.delta_y == 12 and .actionability.actionable == true and ((.actionability.required_checks | index("enabled")) == null)' >/dev/null
"$binary" drag "drag-target" 8 12 --by test-id --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "dragged" and .resolved_selector == "div#drag-target" and .drag.dragged == true and .drag.delta_x == 8 and .drag.delta_y == 12 and .actionability.actionable == true' >/dev/null
"$binary" drag "#covered-action" 8 12 --force --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "dragged" and .drag.dragged == true and .drag.force == true and .actionability.force == true and (.actionability.skipped_checks | index("receives_events")) and .actionability.checks.receives_events.skipped == true' >/dev/null
upload_file="$state_dir/upload.txt"
printf 'synthetic upload\n' >"$upload_file"
"$binary" file "Upload file" "$upload_file" --by label --trial --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "trial" and .file.accepted == true and .file.file_set == false and .file.trial == true and .file.file_name == "upload.txt" and .file.content_omitted == true and .locator.strict == true and .resolved_selector == "input#upload-file" and .actionability.trial == true and .actionability.actionable == true and .actionability.required_checks == ["attached"] and .actionability.checks.attached.passed == true and .actionability.checks.visible.required == false' >/dev/null
"$binary" file "Upload file" "$upload_file" --by label --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "file_set" and .file.accepted == true and .file.file_set == true and ((.file.trial // false) == false) and .file.file_name == "upload.txt" and .file.content_omitted == true and .locator.strict == true and .resolved_selector == "input#upload-file" and .actionability.actionable == true and .actionability.required_checks == ["attached"]' >/dev/null
"$binary" file "#hidden-upload" "$upload_file" --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "file_set" and .file.file_set == true and .actionability.actionable == true and .actionability.checks.visible.required == false and .actionability.checks.visible.skipped == true' >/dev/null
"$binary" eval 'window.scrollTo(0, 0)' --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true' >/dev/null
"$binary" click "scroll-target" --by test-id --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "clicked" and .resolved_selector == "button#scroll-target" and .click.clicked == true and .auto_scroll.before.in_viewport == false and .auto_scroll.after.in_viewport == true and .auto_scroll.changed == true and .actionability.actionable == true and .actionability.checks.receives_events.passed == true' >/dev/null
"$binary" eval 'window.scrollTo(0, 0)' --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true' >/dev/null
"$binary" scroll "scroll-target" --by test-id --trial --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "trial" and .scroll.trial == true and .scroll.scrolled == false and .scroll.changed == false and .scroll.before.in_viewport == false and .scroll.after.in_viewport == false and .locator.strict == true and .resolved_selector == "button#scroll-target" and .actionability.actionable == true and .actionability.required_checks == ["attached","stable"] and .actionability.checks.stable.passed == true and .actionability.checks.visible.required == false and .actionability.checks.in_viewport.required == false' >/dev/null
"$binary" scroll "scroll-target" --by test-id --block center --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "scrolled" and .scroll.scrolled == true and .scroll.changed == true and .scroll.before.in_viewport == false and .scroll.after.in_viewport == true and .scroll.block == "center" and .scroll.inline == "nearest" and .locator.strict == true and .resolved_selector == "button#scroll-target" and .actionability.actionable == true and .actionability.required_checks == ["attached","stable"]' >/dev/null
"$binary" assert in-viewport "scroll-target" --by test-id --timeout 2s --poll 100ms --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .assertion.passed == true and .assertion.in_viewport == true and .assertion.in_viewport_count == 1 and .assertion.out_of_viewport_count == 0 and .assertion.attempts >= 1 and .assertion.poll_interval == "100ms" and .locator.strict == true and .resolved_selector == "button#scroll-target"' >/dev/null
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
"$binary" network --state-dir "$state_dir/cdp-state" --failed --wait 5s --json >"$network_output" &
network_pid=$!
sleep 0.5
"$binary" eval "fetch('$app_url/api/fail?probe=$probe_id').then(r => r.status)" --state-dir "$state_dir/cdp-state" --await-promise --json \
  | jq -e '.ok == true and .result.value == 503' >/dev/null
wait "$network_pid"
require_artifact "$network_output"
jq -e --arg probe "$probe_id" '.ok == true and (.requests[] | select((.url | contains($probe)) and .status == 503))' "$network_output" >/dev/null
request_probe="$(date +%s%N)"
wait_request_output="$state_dir/wait-request.json"
"$binary" wait request --match-url "$request_probe" --method GET --resource-type Fetch --timeout 5s --state-dir "$state_dir/cdp-state" --json >"$wait_request_output" &
wait_request_pid=$!
sleep 0.2
"$binary" eval "fetch('$app_url/api/ok?wait_request=$request_probe').then(r => r.status)" --state-dir "$state_dir/cdp-state" --await-promise --json \
  | jq -e '.ok == true and .result.value == 200' >/dev/null
wait "$wait_request_pid"
require_artifact "$wait_request_output"
jq -e --arg probe "$request_probe" '.ok == true and .wait.kind == "request" and .wait.matched == true and .wait.criteria.url_contains == $probe and .wait.criteria.method == "GET" and .event.cdp_method == "Network.requestWillBeSent" and .event.method == "GET" and (.event.url | contains($probe)) and .event.resource_type == "Fetch" and .wait.evidence.bounded == true and .wait.evidence.headers == false and .wait.evidence.bodies == false' "$wait_request_output" >/dev/null
response_probe="$(date +%s%N)"
wait_response_output="$state_dir/wait-response.json"
"$binary" wait response --match-url "$response_probe" --method GET --status 200 --resource-type Fetch --timeout 5s --state-dir "$state_dir/cdp-state" --json >"$wait_response_output" &
wait_response_pid=$!
sleep 0.2
"$binary" eval "fetch('$app_url/api/ok?wait_response=$response_probe').then(r => r.status)" --state-dir "$state_dir/cdp-state" --await-promise --json \
  | jq -e '.ok == true and .result.value == 200' >/dev/null
wait "$wait_response_pid"
require_artifact "$wait_response_output"
jq -e --arg probe "$response_probe" '.ok == true and .wait.kind == "response" and .wait.matched == true and .wait.criteria.url_contains == $probe and .wait.criteria.method == "GET" and .wait.criteria.status == 200 and .event.cdp_method == "Network.responseReceived" and .event.method == "GET" and .event.status == 200 and (.event.url | contains($probe)) and .event.resource_type == "Fetch" and .wait.evidence.bounded == true and .wait.evidence.headers == false and .wait.evidence.bodies == false' "$wait_response_output" >/dev/null
click_request_probe="$(date +%s%N)"
"$binary" eval "window.__cdpClickRequestProbe = '$click_request_probe'" --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true' >/dev/null
"$binary" click "Send request" --by role --role button --wait-request --wait-request-match-url "$click_request_probe" --wait-request-method GET --wait-request-resource-type Fetch --state-dir "$state_dir/cdp-state" --json \
  | jq -e --arg probe "$click_request_probe" '.ok == true and .action == "clicked" and .click.clicked == true and .click.strategy == "raw-input" and .click.verified == true and .request_wait.kind == "request" and .request_wait.matched == true and .request_wait.criteria.url_contains == $probe and .request_wait.criteria.method == "GET" and .request.cdp_method == "Network.requestWillBeSent" and .request.method == "GET" and (.request.url | contains($probe)) and .request.resource_type == "Fetch" and .request_wait.evidence.bounded == true and .request_wait.evidence.headers == false and .request_wait.evidence.bodies == false' >/dev/null
click_response_probe="$(date +%s%N)"
"$binary" eval "window.__cdpClickResponseProbe = '$click_response_probe'" --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true' >/dev/null
"$binary" click "Save via API" --by role --role button --wait-response --wait-response-match-url "$click_response_probe" --wait-response-method GET --wait-response-status 200 --wait-response-resource-type Fetch --state-dir "$state_dir/cdp-state" --json \
  | jq -e --arg probe "$click_response_probe" '.ok == true and .action == "clicked" and .click.clicked == true and .click.strategy == "raw-input" and .click.verified == true and .response_wait.kind == "response" and .response_wait.matched == true and .response_wait.criteria.url_contains == $probe and .response_wait.criteria.method == "GET" and .response_wait.criteria.status == 200 and .response.cdp_method == "Network.responseReceived" and .response.method == "GET" and .response.status == 200 and (.response.url | contains($probe)) and .response.resource_type == "Fetch" and .response_wait.evidence.bounded == true and .response_wait.evidence.headers == false and .response_wait.evidence.bodies == false' >/dev/null
submit_search_response_probe="$(date +%s%N)"
"$binary" eval "window.__cdpClickResponseProbe = '$submit_search_response_probe'" --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true' >/dev/null
"$binary" workflow submit-search "Agent input" "workflow-response" --by label --suggestion "Save via API" --suggestion-by role --suggestion-role button --submit none --wait-response --wait-response-match-url "$submit_search_response_probe" --wait-response-method GET --wait-response-status 200 --wait-response-resource-type Fetch --state-dir "$state_dir/cdp-state" --json \
  | jq -e --arg probe "$submit_search_response_probe" '.ok == true and .action == "submit_search" and .workflow.name == "submit-search" and .workflow.suggestion_requested == true and .workflow.suggestion_selected == true and .workflow.submit == "none" and .workflow.wait_requested == true and .workflow.verified == true and .fill.filled == true and .fill.verified == true and .suggestion.strict == true and .suggestion.matches[0].selector_hint == "button#response-action" and .suggestion_selector == "button#response-action" and .suggestion_click.clicked == true and .suggestion_click.verified == true and .response_wait.kind == "response" and .response_wait.matched == true and .response_wait.criteria.url_contains == $probe and .response_wait.criteria.method == "GET" and .response_wait.criteria.status == 200 and .response.cdp_method == "Network.responseReceived" and .response.method == "GET" and .response.status == 200 and (.response.url | contains($probe)) and .response.resource_type == "Fetch" and .response_wait.evidence.bounded == true and .response_wait.evidence.headers == false and .response_wait.evidence.bodies == false' >/dev/null
click_url_probe="$(date +%s%N)"
"$binary" eval "window.__cdpClickURLProbe = '$click_url_probe'" --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true' >/dev/null
"$binary" click "Change URL" --by role --role button --wait-url-contains "$click_url_probe" --state-dir "$state_dir/cdp-state" --json \
  | jq -e --arg probe "$click_url_probe" '.ok == true and .action == "clicked" and .click.clicked == true and .click.verified == true and .verification.kind == "url" and .verification.condition == "contains" and .verification.needle == $probe and (.verification.url | contains($probe)) and .page_state.url_changed == true and (.final_target.url | contains($probe))' >/dev/null
press_url_probe="$(date +%s%N)"
"$binary" eval "window.__cdpPressURLProbe = '$press_url_probe'; document.querySelector('#agent-input').addEventListener('keydown', event => { if (event.key === 'Enter') history.pushState({}, '', '/press-url?press_wait_url=' + window.__cdpPressURLProbe); }, { once: true })" --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true' >/dev/null
"$binary" press Enter "Agent input" --by label --wait-url-contains "$press_url_probe" --state-dir "$state_dir/cdp-state" --json \
  | jq -e --arg probe "$press_url_probe" '.ok == true and .action == "pressed" and .press.dispatched == true and .press.verified == true and .verification.kind == "url" and .verification.condition == "contains" and .verification.needle == $probe and (.verification.url | contains($probe)) and (.target.url | contains($probe)) and (.press.url | contains($probe))' >/dev/null
type_url_probe="$(date +%s%N)"
"$binary" eval "window.__cdpTypeURLProbe = '$type_url_probe'; document.querySelector('#agent-input').addEventListener('input', () => { history.pushState({}, '', '/type-url?type_wait_url=' + window.__cdpTypeURLProbe); }, { once: true })" --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true' >/dev/null
"$binary" type "Agent input" "url" --by label --wait-url-contains "$type_url_probe" --state-dir "$state_dir/cdp-state" --json \
  | jq -e --arg probe "$type_url_probe" '.ok == true and .action == "typed" and .type.typing == true and .type.verified == true and .verification.kind == "url" and .verification.condition == "contains" and .verification.needle == $probe and (.verification.url | contains($probe)) and (.target.url | contains($probe)) and (.type.url | contains($probe))' >/dev/null
fill_url_probe="$(date +%s%N)"
"$binary" eval "window.__cdpFillURLProbe = '$fill_url_probe'; document.querySelector('#agent-input').addEventListener('input', () => { history.pushState({}, '', '/fill-url?fill_wait_url=' + window.__cdpFillURLProbe); }, { once: true })" --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true' >/dev/null
"$binary" fill "Agent input" "fill-url" --by label --wait-url-contains "$fill_url_probe" --state-dir "$state_dir/cdp-state" --json \
  | jq -e --arg probe "$fill_url_probe" '.ok == true and .action == "filled" and .fill.filled == true and .fill.verified == true and .verification.kind == "url" and .verification.condition == "contains" and .verification.needle == $probe and (.verification.url | contains($probe)) and (.target.url | contains($probe)) and (.fill.url | contains($probe))' >/dev/null
submit_search_probe="$(date +%s%N)"
"$binary" eval "window.__cdpSubmitSearchProbe = '$submit_search_probe'; document.querySelector('#agent-input').addEventListener('keydown', event => { if (event.key === 'Enter') history.pushState({}, '', '/submit-search?submit_search=' + window.__cdpSubmitSearchProbe); }, { once: true })" --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true' >/dev/null
"$binary" workflow submit-search "Agent input" "workflow-query" --by label --wait-url-contains "$submit_search_probe" --state-dir "$state_dir/cdp-state" --json \
  | jq -e --arg probe "$submit_search_probe" '.ok == true and .action == "submit_search" and .workflow.name == "submit-search" and .workflow.input_mode == "fill" and .workflow.submit == "enter" and .workflow.verified == true and .input.selector == "input#agent-input" and .input.query == "workflow-query" and .fill.filled == true and .fill.verified == true and .press.dispatched == true and .press.verified == true and .verification.kind == "url" and .verification.condition == "contains" and .verification.needle == $probe and (.verification.url | contains($probe)) and .page_state.same_target == true and .page_state.url_changed == true and (.final_target.url | contains($probe))' >/dev/null
suggestion_probe="$(date +%s%N)"
"$binary" eval "window.__cdpSuggestionProbe = '$suggestion_probe'; document.querySelector('#action').addEventListener('click', () => { history.pushState({}, '', '/suggestion?selected=' + window.__cdpSuggestionProbe); }, { once: true })" --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true' >/dev/null
"$binary" workflow submit-search "Agent input" "workflow-suggestion" --by label --suggestion "Click target" --suggestion-by role --suggestion-role button --submit none --wait-url-contains "$suggestion_probe" --state-dir "$state_dir/cdp-state" --json \
  | jq -e --arg probe "$suggestion_probe" '.ok == true and .action == "submit_search" and .workflow.name == "submit-search" and .workflow.suggestion_requested == true and .workflow.suggestion_selected == true and .workflow.submit == "none" and .suggestion.strict == true and .suggestion.matches[0].selector_hint == "button#action" and .suggestion_selector == "button#action" and .suggestion_click.clicked == true and .suggestion_click.verified == true and .verification.kind == "url" and .verification.needle == $probe and (.final_target.url | contains($probe))' >/dev/null
"$binary" workflow submit-search "Agent input" "workflow-load-state" --by label --submit none --wait-load-state domcontentloaded --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .workflow.name == "submit-search" and .workflow.wait_requested == true and .workflow.verified == true and .fill.filled == true and .fill.verified == true and .verification.kind == "load-state" and .verification.state == "domcontentloaded" and (.verification.ready_state == "interactive" or .verification.ready_state == "complete") and .verification.matched == true' >/dev/null
idle_probe="$(date +%s%N)"
wait_idle_output="$state_dir/wait-network-idle.json"
"$binary" wait network-idle --idle 500ms --timeout 5s --state-dir "$state_dir/cdp-state" --json >"$wait_idle_output" &
wait_idle_pid=$!
sleep 0.05
"$binary" eval "fetch('$app_url/api/ok?wait_idle=$idle_probe').then(r => r.status)" --state-dir "$state_dir/cdp-state" --await-promise --json \
  | jq -e '.ok == true and .result.value == 200' >/dev/null
wait "$wait_idle_pid"
require_artifact "$wait_idle_output"
jq -e '.ok == true and .wait.kind == "network-idle" and .wait.matched == true and .wait.idle == "500ms" and .wait.in_flight_count == 0 and .wait.request_count >= 1 and .wait.completed_count >= 1 and .wait.evidence.bounded == true and .wait.evidence.headers == false and .wait.evidence.bodies == false and (.wait.warnings[] | contains("quiescence signal"))' "$wait_idle_output" >/dev/null
wait_file_chooser_output="$state_dir/wait-file-chooser.json"
"$binary" wait file-chooser --mode single --timeout 5s --state-dir "$state_dir/cdp-state" --json >"$wait_file_chooser_output" &
wait_file_chooser_pid=$!
sleep 0.2
"$binary" click "Upload file" --by label --strategy raw-input --state-dir "$state_dir/cdp-state" --force --json \
  | jq -e '.ok == true and .action == "clicked" and .click.strategy == "raw-input"' >/dev/null
wait "$wait_file_chooser_pid"
require_artifact "$wait_file_chooser_output"
jq -e '.ok == true and .wait.kind == "file-chooser" and .wait.matched == true and .wait.criteria.mode == "selectSingle" and .wait.intercepted == true and .wait.evidence.bounded == true and .wait.evidence.headers == false and .wait.evidence.bodies == false and .file_chooser.cdp_method == "Page.fileChooserOpened" and .file_chooser.mode == "selectSingle" and .file_chooser.multiple == false and (.next_commands | any(contains("cdp file")))' "$wait_file_chooser_output" >/dev/null
wait_popup_output="$state_dir/wait-popup.json"
"$binary" wait popup --match-url /popup --timeout 5s --state-dir "$state_dir/cdp-state" --json >"$wait_popup_output" &
wait_popup_pid=$!
sleep 0.2
"$binary" click "Open popup" --by role --role button --strategy raw-input --state-dir "$state_dir/cdp-state" --force --json \
  | jq -e '.ok == true and .action == "clicked" and .click.strategy == "raw-input"' >/dev/null
wait "$wait_popup_pid"
require_artifact "$wait_popup_output"
jq -e '.ok == true and .wait.kind == "popup" and .wait.matched == true and .wait.criteria.url_contains == "/popup" and .wait.evidence.bounded == true and .wait.evidence.headers == false and .wait.evidence.bodies == false and (.popup.target.url | contains("/popup")) and (.popup.cdp_method == "Target.targetCreated" or .popup.cdp_method == "Target.targetInfoChanged") and (.next_commands | any(contains("cdp page select --target")))' "$wait_popup_output" >/dev/null
popup_target="$(jq -r '.popup.target.id' "$wait_popup_output")"
"$binary" page close --target "$popup_target" --state-dir "$state_dir/cdp-state" --json \
  | jq -e '.ok == true and .action == "closed"' >/dev/null
wait_download_output="$state_dir/wait-download.json"
"$binary" wait download --match-url /download/report.txt --filename-contains report.txt --download-dir "$state_dir/downloads" --timeout 5s --state-dir "$state_dir/cdp-state" --json >"$wait_download_output" &
wait_download_pid=$!
sleep 0.2
"$binary" click "Download report" --by role --role button --strategy raw-input --state-dir "$state_dir/cdp-state" --force --json \
  | jq -e '.ok == true and .action == "clicked" and .click.strategy == "raw-input"' >/dev/null
wait "$wait_download_pid"
require_artifact "$wait_download_output"
jq -e '.ok == true and .wait.kind == "download" and .wait.matched == true and .wait.criteria.url_contains == "/download/report.txt" and .wait.criteria.filename_contains == "report.txt" and .wait.criteria.state == "completed" and .wait.evidence.bounded == true and .wait.evidence.headers == false and .wait.evidence.bodies == false and .download.suggested_filename == "report.txt" and .download.completed == true and .download.received_bytes > 0 and .event.cdp_method == "Browser.downloadWillBegin" and .progress.cdp_method == "Browser.downloadProgress" and (.next_commands | any(contains("ls -lah")))' "$wait_download_output" >/dev/null
download_file="$(jq -r '.download.file_path // empty' "$wait_download_output")"
if [[ -n "$download_file" ]]; then
  require_artifact "$download_file"
fi
capture_output="$state_dir/network-capture.json"
capture_artifact="$state_dir/network-capture.local.json"
capture_har="$state_dir/network-capture.har"
capture_body_dir="$state_dir/network-bodies"
"$binary" network capture --state-dir "$state_dir/cdp-state" --url-contains "$app_url" --reload --wait 2s --redact safe --out "$capture_artifact" --har-out "$capture_har" --body-out-dir "$capture_body_dir" --body-artifact-limit 10 --json >"$capture_output"
require_artifact "$capture_output"
require_artifact "$capture_artifact"
require_artifact "$capture_har"
test -d "$capture_body_dir"
test -n "$(find "$capture_body_dir" -type f -print -quit)"
jq -e --arg path "$capture_artifact" --arg har "$capture_har" '.ok == true and .artifact.path == $path and .har.path == $har and .capture.trigger == "reload" and .capture.artifact_safety.shareable == true and (.body_artifacts | length > 0) and (.requests[] | select((.url | contains("/api/ok")) and .body.text and (.body.text | contains("\"ok\"")) and (.body.text | contains("demo-network-secret") | not)))' "$capture_output" >/dev/null
if rg -q 'demo-network-secret' "$capture_output" "$capture_artifact" "$capture_har" "$capture_body_dir"; then
  echo "safe network evidence leaked the synthetic secret" >&2
  exit 1
fi
"$binary" storage list --state-dir "$state_dir/cdp-state" --url-contains "$app_url" --json \
  | jq -e '.ok == true and (.storage.local_storage.entries[] | select(.key == "feature" and .value == "enabled")) and (.storage.session_storage.keys | index("nonce")) and (.storage.cookies | length >= 1)' >/dev/null
"$binary" storage set localStorage feature disabled --state-dir "$state_dir/cdp-state" --url-contains "$app_url" --json \
  | jq -e '.ok == true and .storage.backend == "localStorage" and .storage.value == "disabled"' >/dev/null
"$binary" storage get localStorage feature --state-dir "$state_dir/cdp-state" --url-contains "$app_url" --json \
  | jq -e '.ok == true and .storage.found == true and .storage.value == "disabled"' >/dev/null
"$binary" storage set localStorage debug_bundle_sentinel preserved --state-dir "$state_dir/cdp-state" --url-contains "$app_url" --json >/dev/null
"$binary" storage indexeddb put cdp-demo-db settings debug-bundle-sentinel '{"preserved":true}' --state-dir "$state_dir/cdp-state" --url-contains "$app_url" --json >/dev/null
"$binary" storage cookies set --state-dir "$state_dir/cdp-state" --url "$app_url" --name debug_bundle_sentinel --value preserved --json >/dev/null
bootstrap_before="$(python3 -c 'import json,sys,urllib.request; print(json.load(urllib.request.urlopen(sys.argv[1]))["count"])' "$app_url/api/bootstrap-count")"
for run in 1 2; do
  "$binary" workflow debug-bundle --state-dir "$state_dir/cdp-state" --url-contains "$app_url" --since 1s --json \
    | jq -e '.ok == true and .workflow.trigger == "reload" and .workflow.reloaded == true and .workflow.ignore_cache == true and .workflow.cache_policy == "bypass_http_cache"' >/dev/null
done
bootstrap_after="$(python3 -c 'import json,sys,urllib.request; print(json.load(urllib.request.urlopen(sys.argv[1]))["count"])' "$app_url/api/bootstrap-count")"
if [[ "$bootstrap_after" -ne $((bootstrap_before + 2)) ]]; then
  echo "debug-bundle cache bypass did not refetch bootstrap: before=$bootstrap_before after=$bootstrap_after" >&2
  exit 1
fi
"$binary" storage get localStorage debug_bundle_sentinel --state-dir "$state_dir/cdp-state" --url-contains "$app_url" --json \
  | jq -e '.ok == true and .storage.value == "preserved"' >/dev/null
"$binary" storage indexeddb get cdp-demo-db settings debug-bundle-sentinel --state-dir "$state_dir/cdp-state" --url-contains "$app_url" --json \
  | jq -e '.ok == true and .storage.value.preserved == true' >/dev/null
"$binary" storage list --state-dir "$state_dir/cdp-state" --url-contains "$app_url" --json \
  | jq -e '.ok == true and (.storage.cookies[] | select(.name == "debug_bundle_sentinel" and .value == "preserved"))' >/dev/null
"$binary" storage cookies delete --state-dir "$state_dir/cdp-state" --url "$app_url" --name debug_bundle_sentinel --json >/dev/null
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
debug_bundle_report="$state_dir/debug-bundle.json"
"$binary" workflow debug-bundle --state-dir "$state_dir/cdp-state" --url "$app_url?token=demo-secret" --since 2s --out-dir "$state_dir/debug-bundle" --run-id demo-run --task-id demo-debug-bundle --stage demo-debug --keep-open --json >"$debug_bundle_report"
jq -e --arg path "$state_dir/debug-bundle/debug-bundle.bundle.json" '.ok == true and .artifact.path == $path and .workflow.name == "debug-bundle" and .workflow.trigger == "navigate" and .workflow.reloaded == false and .workflow.ignore_cache == true and .workflow.cache_policy == "bypass_http_cache" and .workflow.request_count >= 1 and .workflow.message_count >= 1 and (.bundle.schema_version == "cdp-evidence-bundle/v1") and .bundle.default_json == "artifact_references" and (.bundle.public_safe_artifacts >= 1) and (.bundle.local_only_artifacts >= 1) and (.bundle.commands[0].task_id == "demo-debug-bundle") and (.bundle.commands[0].artifact_path == $path) and (.bundle.stages[0].name == "demo-debug") and (has("requests") | not) and (.artifacts | length >= 8) and (.artifact_list[] | select(.type == "workflow-debug-bundle-command-log" and .classification == "public_safe"))' "$debug_bundle_report" >/dev/null
debug_bundle_target_id="$(jq -er '.target.id | select(length > 0)' "$debug_bundle_report")"
require_artifact "$state_dir/debug-bundle/debug-bundle.bundle.json"
require_artifact "$state_dir/debug-bundle/debug-bundle.command-log.jsonl"
require_artifact "$state_dir/debug-bundle/debug-bundle.stage-log.json"
protocol_shot_report="$state_dir/protocol-shot.json"
if ! "$binary" protocol exec Page.captureScreenshot --target "$debug_bundle_target_id" --params '{"format":"png"}' --save "$state_dir/protocol-shot.png" --state-dir "$state_dir/cdp-state" --json >"$protocol_shot_report"; then
  echo "protocol screenshot command failed:" >&2
  sed -n '1,120p' "$protocol_shot_report" >&2
  exit 1
fi
if ! jq -e --arg path "$state_dir/protocol-shot.png" '.ok == true and .artifact.path == $path and .artifact.bytes > 0 and .result.data.omitted == true' "$protocol_shot_report" >/dev/null; then
  echo "protocol screenshot assertion failed:" >&2
  sed -n '1,120p' "$protocol_shot_report" >&2
  exit 1
fi
require_artifact "$state_dir/protocol-shot.png"
protocol_index_open_output="$("$binary" open "$app_url?protocol-index=1" --new-tab --run-id demo-run --task-id protocol-index --root-task-id protocol-index --state-dir "$state_dir/cdp-state" --json)"
protocol_index_target_id="$(jq -er '.page.id | select(length > 0)' <<<"$protocol_index_open_output")"
protocol_index="$("$binary" pages --state-dir "$state_dir/cdp-state" --json | jq -er --arg id "$protocol_index_target_id" '([.pages[].id] | index($id)) as $index | if $index == null then empty else $index + 1 end')"
test "$protocol_index" -gt 0
protocol_index_report="$state_dir/protocol-target-index.json"
"$binary" protocol exec Runtime.evaluate --target-index "$protocol_index" --params '{"expression":"document.title","returnByValue":true}' --state-dir "$state_dir/cdp-state" --json >"$protocol_index_report"
jq -e --arg id "$protocol_index_target_id" '.ok == true and .scope == "target" and .target.id == $id and (.session_id | type == "string" and length > 0) and .method == "Runtime.evaluate"' "$protocol_index_report" >/dev/null
event_tap_index_report="$state_dir/event-tap-target-index.json"
"$binary" events tap --target-index "$protocol_index" --enable page --match Page.loadEventFired --duration 1s --max-events 1 --state-dir "$state_dir/cdp-state" --json >"$event_tap_index_report"
jq -e --arg id "$protocol_index_target_id" --argjson index "$protocol_index" '.ok == true and .target.id == $id and .tap.target_index == $index and .tap.session_bound == true' "$event_tap_index_report" >/dev/null
"$binary" page close --target-index "$protocol_index" --state-dir "$state_dir/cdp-state" --json \
  | jq -e --arg id "$protocol_index_target_id" '.ok == true and .action == "closed" and .target.id == $id and .target_gone == true' >/dev/null

if [[ -n "${CDP_E2E_REAL_BUNDLE_URL:-}" ]]; then
  real_bundle_dir="$state_dir/real-bundle"
  real_bundle_path="$real_bundle_dir/debug-bundle.bundle.json"
  mkdir -p "$real_bundle_dir"
  "$binary" workflow debug-bundle --state-dir "$state_dir/cdp-state" --url "${CDP_E2E_REAL_BUNDLE_URL}" --since 2s --out-dir "$real_bundle_dir" --json \
    | jq -e --arg path "$real_bundle_path" '.ok == true and .artifact.path == $path and .workflow.name == "debug-bundle"' >/dev/null
  require_artifact "$real_bundle_path"
fi

printf 'demo e2e passed: %s\n' "$app_url"
