#!/usr/bin/env bash
set -euo pipefail

binary="${1:-./bin/cdp}"

if [[ ! -x "$binary" ]]; then
  echo "missing executable: $binary" >&2
  exit 2
fi

state_dir="$(mktemp -d)"
config_dir="$state_dir/config"
config_path="$state_dir/config.json"
export XDG_CONFIG_HOME="$config_dir"
printf '{}\n' >"$config_path"
health_state_dir=""
forced_restart_state_dir=""
orphan_hold_pid=""
hold_blackhole_pid=""
guide_path=""
guide_path_source=""

cleanup_daemon_state() {
  local dir="${1:-}"
  if [[ -z "$dir" ]]; then
    return
  fi
  "$binary" --browser-mode headless daemon stop --state-dir "$dir" --json >/dev/null 2>&1 || true
  "$binary" --browser-mode headed daemon stop --state-dir "$dir" --json >/dev/null 2>&1 || true
}

cleanup() {
  if [[ -n "$orphan_hold_pid" ]]; then
    kill -TERM "$orphan_hold_pid" >/dev/null 2>&1 || true
    wait "$orphan_hold_pid" >/dev/null 2>&1 || true
  fi
  if [[ -n "$hold_blackhole_pid" ]]; then
    kill -TERM "$hold_blackhole_pid" >/dev/null 2>&1 || true
    wait "$hold_blackhole_pid" >/dev/null 2>&1 || true
  fi
  if [[ "$guide_path_source" == "materialized" && -n "$guide_path" ]]; then
    rm -f -- "$guide_path"
  fi
  cleanup_daemon_state "$state_dir"
  cleanup_daemon_state "${state_dir}-copy-default"
  cleanup_daemon_state "$state_dir/live-browser"
  cleanup_daemon_state "$health_state_dir"
  cleanup_daemon_state "$forced_restart_state_dir"
  rm -rf "$state_dir" "${state_dir}-copy-default"
  if [[ -n "$health_state_dir" ]]; then
    rm -rf "$health_state_dir"
  fi
  if [[ -n "$forced_restart_state_dir" ]]; then
    rm -rf "$forced_restart_state_dir"
  fi
}
trap cleanup EXIT

"$binary" --help >/tmp/cdp-cli-help.txt
grep -Fq 'jq process tree' /tmp/cdp-cli-help.txt
transcription_help="$("$binary" transcription --help)"
grep -Fq 'local REST, SSE, and realtime WebSocket boundary' <<<"$transcription_help"
grep -Fq 'Browser-backed provider converters use the same owned process-group boundary' <<<"$transcription_help"
"$binary" transcription spec | jq -e '.openapi == "3.1.0" and (.paths["/v1/audio/transcriptions"].post != null) and (.paths["/v1/realtime"].get != null)' >/dev/null
"$binary" schema transcription-server --json | jq -e '.ok == true and .schema.name == "transcription-server" and (.schema.description | contains("owned provider conversion process groups")) and (.schema.fields | map(.name) | index("providers"))' >/dev/null
source_head="$(git rev-parse HEAD)"
source_dirty=false
if test -n "$(git status --porcelain --untracked-files=normal)"; then
  source_dirty=true
fi
version_json="$("$binary" version --json)"
jq -e --arg head "$source_head" --arg dirty "$source_dirty" '
  (.version | test("^v?[0-9]+\\.[0-9]+\\.[0-9]+([-+][0-9A-Za-z.-]+)?$")) and
  .commit == $head and (.commit | test("^[0-9a-fA-F]{40}$")) and
  (.date | test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(Z|[+-][0-9]{2}:[0-9]{2})$")) and
  .verified == true and .provenance == "managed" and
  ((.dirty | tostring) == $dirty) and .source_state == (if $dirty == "true" then "dirty" else "clean" end)
' >/dev/null <<<"$version_json"
"$binary" version --json --compact | jq -e --arg head "$source_head" '.verified == true and .commit == $head and .provenance == "managed"' >/dev/null
"$binary" version | grep -q 'managed build; commit '
"$binary" describe --json | jq -e '.ok == true and (.commands.children | length > 5)' >/dev/null
"$binary" describe --jq '.globals | index("--json")' >/dev/null
"$binary" describe --jq '.globals | index("--compact")' >/dev/null
"$binary" describe --jq '.globals | index("--connection")' >/dev/null
"$binary" describe --jq '.globals | index("--browser-mode")' >/dev/null
"$binary" describe --jq '.globals | index("--browserMode")' >/dev/null
"$binary" describe --jq '.globals | index("--max-tabs")' >/dev/null
"$binary" describe --jq '.globals | index("--max-renderer-processes")' >/dev/null
"$binary" describe --command "version" --json | jq -e '.ok == true and .commands.name == "version" and (.commands.examples | any(contains("version --json")))' >/dev/null
"$binary" describe --command "guide" --json | jq -e '.ok == true and .commands.name == "guide" and (.commands.examples | any(contains("guide --path"))) and (.commands.flags[] | select(.name == "path"))' >/dev/null
"$binary" guide --json | jq -e '.ok == true and .schema_version == "guide/v1" and .mode == "content" and (.bytes > 0) and (.content | contains("# cdp-cli Agent Guide"))' >/dev/null
guide_path_json="$("$binary" guide --path --json)"
guide_path="$(jq -r 'if .ok == true and .mode == "path" then .path else empty end' <<<"$guide_path_json")"
guide_path_source="$(jq -r '.source' <<<"$guide_path_json")"
test -n "$guide_path"
test -s "$guide_path"
"$binary" describe --command "targets" --json | jq -e '.ok == true and .commands.name == "targets" and (.commands.flags[] | select(.name == "retry" and .type == "string")) and (.commands.flags[] | select(.name == "max-attempts"))' >/dev/null
"$binary" describe --command "pages" --json | jq -e '.ok == true and .commands.name == "pages" and (.commands.flags[] | select(.name == "title-contains" and .type == "string")) and (.commands.flags[] | select(.name == "retry" and .type == "string")) and (.commands.flags[] | select(.name == "max-attempts"))' >/dev/null
"$binary" describe --command "daemon start" --json | jq -e '.ok == true and .commands.name == "start" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "daemon status" --json | jq -e '.ok == true and .commands.name == "status" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "daemon stop" --json | jq -e '.ok == true and .commands.name == "stop" and (.commands.examples | any(contains("--force-managed") and contains("--stale-lock-after"))) and (.commands.flags[] | select(.name == "force-managed")) and (.commands.flags[] | select(.name == "stale-lock-after"))' >/dev/null
"$binary" describe --command "daemon restart" --json | jq -e '.ok == true and .commands.name == "restart" and (.commands.examples | any(contains("--autoConnect"))) and (.commands.examples | any(contains("--force-managed") and contains("--stale-lock-after"))) and (.commands.flags[] | select(.name == "force-managed")) and (.commands.flags[] | select(.name == "stale-lock-after"))' >/dev/null
"$binary" describe --command "daemon keepalive" --json | jq -e '.ok == true and .commands.name == "keepalive" and (.commands.examples | any(contains("--browser-mode headed"))) and (.commands.examples | any(contains("--browser-mode headless"))) and (.commands.examples | any(. == "cdp cron install --json"))' >/dev/null
keepalive_help="$("$binary" daemon keepalive --help)"
grep -Fq 'superseded hold generations' <<<"$keepalive_help"
grep -Fq 'transient endpoint failures remain retryable' <<<"$keepalive_help"
"$binary" describe --command "daemon maintenance" --json | jq -e '.ok == true and .commands.name == "maintenance" and (.commands.examples | any(contains("--browser-mode headless"))) and (.commands.examples | any(contains("--dry-run"))) and (.commands.flags[] | select(.name == "dry-run")) and (.commands.flags[] | select(.name == "profile-seed-strategy")) and (.commands.flags[] | select(.name == "cleanup-close"))' >/dev/null
"$binary" describe --command "daemon health-check" --json | jq -e '.ok == true and .commands.name == "health-check" and (.commands.examples | any(contains("--browser-mode headless"))) and (.commands.examples | any(contains("--repair"))) and (.commands.examples | any(contains("--require-healthy"))) and (.commands.flags[] | select(.name == "repair")) and (.commands.flags[] | select(.name == "require-healthy")) and (.commands.flags[] | select(.name == "out-dir"))' >/dev/null
health_check_help="$("$binary" daemon health-check --help)"
grep -Fq 'target-gone' <<<"$health_check_help"
"$binary" describe --command "daemon logs" --json | jq -e '.ok == true and .commands.name == "logs" and (.commands.examples | any(contains("--tail")))' >/dev/null
"$binary" describe --command "cron install" --json | jq -e '.ok == true and .commands.name == "install" and (.commands.examples | any(. == "cdp cron install --json")) and (.commands.examples | any(contains("--dry-run"))) and (.commands.flags[] | select(.name == "dry-run")) and (.commands.flags[] | select(.name == "artifact-retention")) and (.commands.flags[] | select(.name == "max-log-size"))' >/dev/null
cron_help="$("$binary" cron --help)"
grep -Fq 'Crontab manager output is bounded' <<<"$cron_help"
grep -Fq 'managed task children use the same owned process-tree boundary' <<<"$cron_help"
transcription_service_help="$("$binary" transcription service --help)"
grep -Fq 'Service-manager diagnostics are bounded' <<<"$transcription_service_help"
"$binary" describe --command "cron run" --json | jq -e '.ok == true and .commands.name == "run" and (.commands.examples | any(contains("headed-daemon-keepalive"))) and (.commands.examples | any(contains("headless-maintenance"))) and (.commands.examples | any(contains("artifact-prune"))) and (.commands.flags[] | select(.name == "display")) and (.commands.flags[] | select(.name == "max-log-size"))' >/dev/null
"$binary" describe --command "artifacts prune" --json | jq -e '.ok == true and .commands.name == "prune" and (.commands.examples | any(contains("--dry-run"))) and (.commands.examples | any(contains("--apply"))) and (.commands.flags[] | select(.name == "older-than")) and (.commands.flags[] | select(.name == "max-log-size"))' >/dev/null
"$binary" describe --command "artifacts run-managed" --json | jq -e '.ok == true and .commands.name == "run-managed" and (.commands.flags[] | select(.name == "task")) and (.commands.flags[] | select(.name == "log")) and (.commands.flags[] | select(.name == "max-log-size"))' >/dev/null
managed_artifacts_help="$("$binary" artifacts run-managed --help)"
grep -Fq 'owned child process tree' <<<"$managed_artifacts_help"
"$binary" describe --command "cron migrate pages-polling" --json | jq -e '.ok == true and .commands.name == "pages-polling" and (.commands.examples | any(contains("migrate pages-polling --json"))) and (.commands.examples | any(contains("--apply"))) and (.commands.flags[] | select(.name == "apply"))' >/dev/null
"$binary" describe --command "cron heal headed" --json | jq -e '.ok == true and .commands.name == "headed" and (.commands.examples | any(contains("--reconnect 30s")))' >/dev/null
"$binary" describe --command "doctor" --json | jq -e '.ok == true and .commands.name == "doctor" and (.commands.examples | any(contains("scheduled-tasks")))' >/dev/null
"$binary" describe --command "browser mode get" --json | jq -e '.ok == true and .commands.name == "get" and (.commands.examples | any(contains("--browser-mode headless")))' >/dev/null
"$binary" describe --command "browser preflight" --json | jq -e '.ok == true and .commands.name == "preflight" and (.commands.examples | any(contains("--repair"))) and (.commands.examples | any(contains("--cleanup"))) and (.commands.flags[] | select(.name == "profile-seed")) and (.commands.flags[] | select(.name == "cleanup-close")) and (.commands.flags[] | select(.name == "open-readiness"))' >/dev/null
browser_preflight_help="$("$binary" browser preflight --help)"
grep -Fq 'target-gone' <<<"$browser_preflight_help"
"$binary" describe --command "browser profile status" --json | jq -e '.ok == true and .commands.name == "status" and .commands.short == "Show managed headless browser profile status" and (.commands.examples | any(contains("--browser-mode headless")))' >/dev/null
"$binary" describe --command "browser profile seed" --json | jq -e '.ok == true and .commands.name == "seed" and .commands.short == "Create managed headless browser profile metadata" and (.commands.examples | any(contains("--browser-mode headless"))) and (.commands.examples | any(contains("--strategy managed"))) and (.commands.examples | any(contains("--strategy copy-default"))) and (.commands.flags[] | select(.name == "strategy"))' >/dev/null
"$binary" describe --command "connection" --json | jq -e '.ok == true and .commands.name == "connection" and (.commands.examples | any(contains("connection list")))' >/dev/null
"$binary" describe --command "connection add" --json | jq -e '.ok == true and .commands.name == "add" and (.commands.examples | any(contains("--auto-connect")))' >/dev/null
"$binary" describe --command "connection select" --json | jq -e '.ok == true and .commands.name == "select" and (.commands.examples | any(contains("connection select")))' >/dev/null
"$binary" describe --command "connection current" --json | jq -e '.ok == true and .commands.name == "current" and (.commands.examples | any(contains("connection current")))' >/dev/null
events_stream_help="$("$binary" events stream --help)"
grep -Fq 'read-only Runtime.evaluate heartbeat' <<<"$events_stream_help"
grep -Fq 'daemon runtime registration' <<<"$events_stream_help"
"$binary" describe --command "events stream" --json | jq -e '.ok == true and .commands.name == "stream" and (.commands.examples | any(contains("events stream"))) and (.commands.examples | any(contains("--target-index"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int")) and (.commands.flags[] | select(.name == "max-events" and .type == "int"))' >/dev/null
"$binary" describe --command "events stream" --json | jq -e '(.commands.flags[] | select(.name == "enable").usage | contains("DOM") and contains("Performance"))' >/dev/null
"$binary" describe --command "events tap" --json | jq -e '.ok == true and .commands.name == "tap" and (.commands.examples | any(contains("--target-index"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int")) and (.commands.flags[] | select(.name == "enable").usage | contains("DOM") and contains("Performance"))' >/dev/null
"$binary" describe --command "events wait" --json | jq -e '.ok == true and .commands.name == "wait" and (.commands.examples | any(contains("--file"))) and (.commands.flags[] | select(.name == "file")) and (.commands.flags[] | select(.name == "method")) and (.commands.flags[] | select(.name == "contains")) and (.commands.flags[] | select(.name == "from-offset")) and (.commands.flags[] | select(.name == "print-offset"))' >/dev/null
"$binary" schema events-wait --json | jq -e '.ok == true and .schema.name == "events-wait" and (.schema.fields | map(.name) | index("record")) and (.schema.fields | map(.name) | index("event")) and (.schema.fields | map(.name) | index("offset")) and (.schema.fields[] | select(.name == "offset").description | contains("--from-offset")) and (.schema.fields[] | select(.name == "wait").description | contains("any-of")) and (.schema.fields[] | select(.name == "wait").description | contains("all-of"))' >/dev/null
"$binary" schema events-tap --json | jq -e '.ok == true and .schema.name == "events-tap" and (.schema.description | contains("target-index")) and (.schema.fields[] | select(.name == "tap").description | contains("target_index"))' >/dev/null
"$binary" describe --command "events interactions" --json | jq -e '.ok == true and .commands.name == "interactions" and (.commands.examples | any(contains("--match click,scroll"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int")) and (.commands.flags[] | select(.name == "match")) and (.commands.flags[] | select(.name == "ready-file"))' >/dev/null
"$binary" schema events-interactions --json | jq -e '.ok == true and .schema.name == "events-interactions" and (.schema.fields | map(.name) | index("interaction")) and (.schema.fields | map(.name) | index("cleanup")) and (.schema.fields[] | select(.name == "interaction").description | contains("key values"))' >/dev/null
event_wait_file="$state_dir/events-wait.jsonl"
cat >"$event_wait_file" <<'JSON_EVENT_WAIT'
{"ok":true,"type":"ready"}
{"ok":true,"type":"event","event":{"method":"Page.loadEventFired","params":{"marker":"e2e-history"}}}
{"ok":true,"type":"event","event":{"method":"Runtime.consoleAPICalled","params":{"marker":"e2e-next"}}}
JSON_EVENT_WAIT
event_wait_history="$("$binary" events wait --file "$event_wait_file" --method Page.loadEventFired --contains e2e-history --json)"
printf '%s\n' "$event_wait_history" | jq -e '.ok == true and .event.method == "Page.loadEventFired" and .wait.matched_method == "Page.loadEventFired" and (.offset | type == "number")' >/dev/null
event_wait_offset="$(jq -r '.offset' <<<"$event_wait_history")"
test "$event_wait_offset" -gt 0
"$binary" events wait --file "$event_wait_file" --from-offset "$event_wait_offset" --method Runtime.consoleAPICalled --contains e2e-next --print-offset --json >"$state_dir/events-wait-next.json" 2>"$state_dir/events-wait-next.offset"
jq -e '.ok == true and .event.method == "Runtime.consoleAPICalled" and .event.params.marker == "e2e-next" and .wait.from_offset > 0' "$state_dir/events-wait-next.json" >/dev/null
grep -Fxq "offset=$(jq -r '.offset' "$state_dir/events-wait-next.json")" "$state_dir/events-wait-next.offset"
event_wait_future_file="$state_dir/events-wait-future.jsonl"
: >"$event_wait_future_file"
event_wait_future_output="$state_dir/events-wait-future.json"
"$binary" events wait --file "$event_wait_future_file" --method Runtime.consoleAPICalled --contains e2e-future --timeout 5s --json >"$event_wait_future_output" &
event_wait_future_pid=$!
sleep 0.1
printf '%s' '{"ok":true,"type":"event","event":{"method":"Runtime.consoleAPICalled","params":{"marker":"e2e-future"}}}' >"$event_wait_future_file"
sleep 0.1
printf '\n' >>"$event_wait_future_file"
wait "$event_wait_future_pid"
jq -e '.ok == true and .event.method == "Runtime.consoleAPICalled" and .event.params.marker == "e2e-future"' "$event_wait_future_output" >/dev/null
set +e
event_wait_timeout_output="$("$binary" events wait --file "$event_wait_file" --method Network.loadingFailed --timeout 50ms --json 2>"$state_dir/events-wait-timeout.err")"
event_wait_timeout_code=$?
set -e
test "$event_wait_timeout_code" -eq 5
printf '%s\n' "$event_wait_timeout_output" | jq -e '.ok == false and .code == "event_wait_timeout" and .data.wait.offset >= 0' >/dev/null
"$binary" describe --command "click" --json | jq -e '.ok == true and .commands.name == "click" and (.commands.examples | any(contains("--target-index 2"))) and (.commands.examples | any(contains("--strategy dom"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "browser marker" --json | jq -e '.ok == true and .commands.name == "marker" and (.commands.children | map(.name) | index("enable")) and (.commands.children | map(.name) | index("disable")) and (.commands.children | map(.name) | index("status"))' >/dev/null
"$binary" describe --command "browser marker enable" --json | jq -e '.ok == true and .commands.name == "enable" and (.commands.flags[] | select(.name == "name" and .type == "string")) and (.commands.examples | any(contains("browser marker enable")))' >/dev/null
set +e
marker_headless_output="$("$binary" --browser-mode headless browser marker enable --name e2e-marker --state-dir "$state_dir" --json)"
marker_headless_code=$?
click_zero_output="$("$binary" click main --target-index 0 --state-dir "$state_dir" --json)"
click_zero_code=$?
set -e
if [[ "$marker_headless_code" -ne 2 || "$click_zero_code" -ne 2 ]]; then
  echo "installed marker/click validation exit codes: marker=$marker_headless_code click=$click_zero_code" >&2
  printf '%s\n' "$marker_headless_output" "$click_zero_output" >&2
  exit 1
fi
jq -e '.ok == false and .code == "invalid_browser_mode" and (.message | contains("headed"))' <<<"$marker_headless_output" >/dev/null
jq -e '.ok == false and .code == "invalid_target_index" and (.message | contains("greater than zero"))' <<<"$click_zero_output" >/dev/null
e2e_upload_path="$state_dir/e2e-upload.txt"
printf '%s\n' 'synthetic upload' >"$e2e_upload_path"
for input_command in fill type insert-text press focus clear check uncheck select hover drag scroll file; do
  case "$input_command" in
    fill|type)
      input_command_args=("$input_command" "input#q" hello)
      ;;
    insert-text)
      input_command_args=("insert-text" "[contenteditable=true]" hello)
      ;;
    press)
      input_command_args=(press Enter "input#q")
      ;;
    focus)
      input_command_args=(focus "input#q")
      ;;
    clear)
      input_command_args=(clear "input#q")
      ;;
    check)
      input_command_args=(check "input#subscribe" --trial)
      ;;
    uncheck)
      input_command_args=(uncheck "input#subscribe" --trial)
      ;;
    select)
      input_command_args=(select "select#plan" pro --trial)
      ;;
    hover)
      input_command_args=(hover "button#submit")
      ;;
    drag)
      input_command_args=(drag "div#drag-target" 8 12)
      ;;
    scroll)
      input_command_args=(scroll "div#scroll-target")
      ;;
    file)
      input_command_args=(file "input#upload" "$e2e_upload_path")
      ;;
  esac
  "$binary" describe --command "$input_command" --json \
    | jq -e '.ok == true and (.commands.flags[] | select(.name == "target-index" and .type == "int")) and (.commands.examples | any(contains("--target-index 2")))' >/dev/null
  "$binary" schema "$input_command" --json \
    | jq -e '.ok == true and (.schema.description | contains("target-index")) and (.schema.fields | map(.name) | index("target_index"))' >/dev/null
  set +e
  input_index_output="$("$binary" "${input_command_args[@]}" --target-index 0 --state-dir "$state_dir" --json)"
  input_index_code=$?
  set -e
  test "$input_index_code" -eq 2
  jq -e '.ok == false and .code == "invalid_target_index"' <<<"$input_index_output" >/dev/null
done
set +e
file_chooser_index_output="$("$binary" file chooser 247 "$e2e_upload_path" --target-index 0 --state-dir "$state_dir" --json)"
file_chooser_index_code=$?
set -e
test "$file_chooser_index_code" -eq 2
jq -e '.ok == false and .code == "invalid_target_index"' <<<"$file_chooser_index_output" >/dev/null
for form_command in values get; do
  case "$form_command" in
    values)
      form_command_args=(form values)
      form_schema=form-values
      ;;
    get)
      form_command_args=(form get '#out')
      form_schema=form-get
      ;;
  esac
  "$binary" describe --command "form $form_command" --json \
    | jq -e '.ok == true and (.commands.flags[] | select(.name == "target-index" and .type == "int")) and (.commands.examples | any(contains("--target-index 2")))' >/dev/null
  "$binary" schema "$form_schema" --json \
    | jq -e '.ok == true and (.schema.description | contains("target-index")) and (.schema.fields | map(.name) | index("target_index"))' >/dev/null
  set +e
  form_index_output="$("$binary" "${form_command_args[@]}" --target-index 0 --state-dir "$state_dir" --json)"
  form_index_code=$?
  set -e
  test "$form_index_code" -eq 2
  jq -e '.ok == false and .code == "invalid_target_index"' <<<"$form_index_output" >/dev/null
done
agent_help="$("$binary" workflow agent --help)"
grep -q 'agents.google.exclusive_ai_mode' <<<"$agent_help"
grep -q -- '--google-ai auto|mode|off' <<<"$agent_help"
chatgpt_attachment_help="$("$binary" workflow agent chatgpt conversations download-attachments --help)"
grep -q 'download-attachments CONVERSATION_ID' <<<"$chatgpt_attachment_help"
grep -q -- '--output-dir string' <<<"$chatgpt_attachment_help"
"$binary" describe --command "workflow agent" --json | jq -e '.ok == true and .commands.name == "agent" and (.commands.examples | any(contains("workflow agent auth refresh"))) and (.commands.examples | any(contains("workflow agent capabilities refresh"))) and (.commands.examples | any(contains("workflow agent claude capabilities"))) and (.commands.examples | any(contains("workflow agent gemini capabilities refresh"))) and (.commands.examples | any(contains("agents.google.exclusive_ai_mode"))) and (.commands.examples | any(contains("--google-ai auto")))' >/dev/null
"$binary" describe --command "workflow agent chatgpt conversations download-attachments" --json | jq -e '.ok == true and .commands.name == "download-attachments" and (.commands.use | contains("download-attachments CONVERSATION_ID")) and (.commands.flags[] | select(.name == "output-dir" and .type == "string"))' >/dev/null
"$binary" describe --command "workflow youtube cookies" --json | jq -e '.ok == true and .commands.name == "cookies" and (.commands.examples | any(contains("--browser-mode headed") and contains("workflow youtube cookies"))) and (.commands.flags[] | select(.name == "url")) and (.commands.flags[] | select(.name == "out")) and (.commands.flags[] | select(.name == "settle"))' >/dev/null
"$binary" --config "$config_path" --state-dir "$state_dir" workflow agent providers --json | jq -e '.ok == true and .schema_version == "webagent-operation/v1" and .data.schema_version == "webagent-capabilities/v1" and (.data.providers | length == 8) and ([.data.providers[] | select(.provider == "claude" or .provider == "gemini") | .operations[] | select(.supported)] | length == 16)' >/dev/null
"$binary" --state-dir "$state_dir" workflow agent chatgpt capabilities --json | jq -e '.ok == true and .provider == "chatgpt" and (.data.operations[] | select(.operation == "conversations.download_attachments" and .supported == true and .status == "implemented" and .side_effect == "local_file_write" and .browser == "provider_defined" and (.command | endswith("conversations download-attachments"))))' >/dev/null
"$binary" --state-dir "$state_dir" workflow agent gemini capabilities --json | jq -e '.ok == true and .provider == "gemini" and .operation == "capabilities" and .data.runtime.state == "missing" and ([.data.operations[] | select(.supported)] | length == 8)' >/dev/null
"$binary" doctor --state-dir "$state_dir" --json | jq -e '.ok == true and (.checks | length >= 3)' >/dev/null
"$binary" doctor --check daemon --state-dir "$state_dir" --json | jq -e '.ok == true and (.checks | length == 1) and .checks[0].name == "daemon"' >/dev/null
"$binary" doctor --check scheduled-tasks --state-dir "$state_dir" --json | jq -e '.checks | length == 1 and .[0].name == "scheduled-tasks" and .[0].details.source == "crontab -l" and (.[0].details.has_headed_daemon_keepalive | type == "boolean") and (.[0].details.has_headless_daemon_keepalive | type == "boolean") and (.[0].details.has_pages_polling_keepalive | type == "boolean") and (.[0].details.pages_polling_count | type == "number") and (.[0].details.has_ambiguous_page_cleanup | type == "boolean") and (.[0].details.has_unflocked_cdp_task | type == "boolean") and (.[0].details.tasks | type == "array") and (.[0].details.last_run_artifacts | type == "object") and (.[0].details.artifact_policy.max_log_size_bytes == 67108864) and (.[0].details.last_cleanup | type == "object") and (.[0].details.managed_processes.checked | type == "boolean") and (.[0].next_commands | index("cdp cron status --json")) and (.[0].next_commands | index("cdp cron diff --json")) and (.[0].next_commands | index("cdp cron install --json")) and (.[0].next_commands | index("cdp --browser-mode headless daemon maintenance --dry-run --json"))' >/dev/null
"$binary" doctor --check scheduled-tasks --state-dir "$state_dir" --json | jq -e '.checks[0].details.command_output_truncated == false' >/dev/null
"$binary" doctor --capabilities --json | jq -e '.ok == true and (.capabilities | map(.name) | index("raw_protocol"))' >/dev/null
"$binary" doctor --capabilities --json | jq -e '.ok == true and (.capabilities[] | select(.name == "advanced_storage" and .status == "implemented"))' >/dev/null
"$binary" doctor --capabilities --json | jq -e '.ok == true and (.capabilities[] | select(.name == "raw_protocol" and (.verify_commands | index("cdp protocol metadata --json"))))' >/dev/null
"$binary" doctor --capabilities --json | jq -e '.ok == true and (.capabilities[] | select(.name == "artifacts" and (.evidence_commands | index("cdp workflow debug-bundle --out-dir tmp/debug-bundle --json"))))' >/dev/null
"$binary" doctor --capabilities --json | jq -e '.ok == true and (.capabilities[] | select(.name == "accessibility" and .status == "implemented" and (.verify_commands | index("cdp a11y tree --json"))))' >/dev/null
"$binary" doctor --capabilities --json | jq -e '.ok == true and (.capabilities[] | select(.name == "performance" and .status == "implemented" and (.evidence_commands | index("cdp workflow perf '\''https://example.com'\'' --wait 1s --trace tmp/perf.local.json --json"))))' >/dev/null
"$binary" doctor --capabilities --json | jq -e '.ok == true and (.capabilities[] | select(.name == "memory" and .status == "implemented" and (.verify_commands | index("cdp memory counters --json"))))' >/dev/null
"$binary" doctor --capabilities --json | jq -e '.ok == true and (.capabilities[] | select(.name == "emulation" and .status == "implemented" and (.verify_commands | index("cdp emulate user-agent --help")) and (.verify_commands | index("cdp emulate timezone --help")) and (.verify_commands | index("cdp emulate locale --help")) and (.verify_commands | index("cdp emulate color-scheme --help")) and (.verify_commands | index("cdp permissions grant --help")) and (.verify_commands | index("cdp permissions set --help")) and (.verify_commands | index("cdp emulate cpu --help")) and (.verify_commands | index("cdp emulate network --help"))))' >/dev/null
"$binary" doctor --capabilities --json | jq -e '.ok == true and ([.capabilities[] | select(.name == "network_throttling")] | length == 0)' >/dev/null
"$binary" doctor --capabilities --json | jq -e '.ok == true and (.bootstrap_path.validate_commands | index("cdp daemon health --json")) and (.bootstrap_path.validate_commands | index("cdp cron status --json")) and (.bootstrap_path.recover_commands | index("cdp daemon logs --tail 50 --json")) and (.bootstrap_path.recover_commands | index("cdp cron diff --json")) and (.bootstrap_path.stop_signals | index("human_required"))' >/dev/null
"$binary" doctor --capabilities --json | jq -e '.ok == true and (.bootstrap_path.validate_commands | index("cdp doctor --check scheduled-tasks --json"))' >/dev/null
"$binary" doctor --capabilities --json | jq -e '.ok == true and (.bootstrap_path.validate_commands | index("cdp doctor --check headless-security --json"))' >/dev/null
"$binary" explain-error not_implemented --json | jq -e '.ok == true and .error.exit_code == 8' >/dev/null
"$binary" exit-codes --json | jq -e '.ok == true and (.exit_codes | map(.name) | index("not_implemented"))' >/dev/null
"$binary" schema error-envelope --json | jq -e '.ok == true and .schema.name == "error-envelope"' >/dev/null
"$binary" schema events-stream --json | jq -e '.ok == true and .schema.name == "events-stream" and (.schema.description | contains("validated target-domain enablement")) and (.schema.fields | map(.name) | index("event")) and (.schema.fields | map(.name) | index("foreign_events_dropped")) and (.schema.fields[] | select(.name == "stream").description | contains("liveness")) and (.schema.fields[] | select(.name == "liveness").description | contains("runtime-registration"))' >/dev/null
"$binary" schema browser-window-marker --json | jq -e '.ok == true and .schema.name == "browser-window-marker" and (.schema.fields | map(.name) | index("marker"))' >/dev/null
"$binary" schema window-marker --json | jq -e '.ok == true and .schema.name == "window-marker" and (.schema.fields | map(.name) | index("active_session_count")) and (.schema.fields | map(.name) | index("host_id_present")) and ([.schema.fields[] | select(.name == "host_id")] | length == 0)' >/dev/null
"$binary" schema webagent-operation --json | jq -e '.ok == true and .schema.name == "webagent-operation" and (.schema.fields | map(.name) | index("cleanup")) and (.schema.fields | map(.name) | index("evidence"))' >/dev/null
"$binary" schema webagent-capabilities --json | jq -e '.ok == true and .schema.name == "webagent-capabilities" and (.schema.fields | map(.name) | index("operations"))' >/dev/null
"$binary" schema webagent-cleanup --json | jq -e '.ok == true and .schema.name == "webagent-cleanup" and (.schema.fields[] | select(.name == "identity_omitted" and .type == "boolean" and (.description | contains("privacy-safe") and contains("lifecycle state"))))' >/dev/null
"$binary" schema chatgpt-attachment-batch --json | jq -e '.ok == true and .schema.name == "chatgpt-attachment-batch" and (.schema.fields | map(.name) | index("manifest_path")) and (.schema.fields | map(.name) | index("items")) and (.schema.fields[] | select(.name == "status").description | contains("complete or partial"))' >/dev/null
"$binary" schema chatgpt-attachment-manifest --json | jq -e '.ok == true and .schema.name == "chatgpt-attachment-manifest" and (.schema.fields | map(.name) | index("total_bytes")) and (.schema.fields | map(.name) | index("items"))' >/dev/null
"$binary" schema describe --json | jq -e '.ok == true and .schema.name == "describe" and (.schema.fields | map(.name) | index("commands"))' >/dev/null
"$binary" schema doctor --json | jq -e '.ok == true and .schema.name == "doctor" and (.schema.fields | map(.name) | index("checks"))' >/dev/null
"$binary" schema doctor-capabilities --json | jq -e '.ok == true and .schema.name == "doctor-capabilities" and (.schema.fields | map(.name) | index("capabilities")) and (.schema.fields | map(.name) | index("bootstrap_path"))' >/dev/null
"$binary" schema scheduled-tasks --json | jq -e '.ok == true and .schema.name == "scheduled-tasks" and (.schema.fields | map(.name) | index("details")) and (.schema.fields[] | select(.name == "details").description | contains("legacy pages polling")) and (.schema.fields | map(.name) | index("next_commands"))' >/dev/null
"$binary" schema scheduled-tasks-details --json | jq -e '.ok == true and .schema.name == "scheduled-tasks-details" and (.schema.fields | map(.name) | index("expected_managed_task_ids")) and (.schema.fields | map(.name) | index("tasks")) and (.schema.fields | map(.name) | index("has_managed_process_sweep")) and (.schema.fields | map(.name) | index("has_headless_launch_without_managed_process_sweep")) and (.schema.fields | map(.name) | index("command_output_truncated")) and (.schema.fields | map(.name) | index("last_run_artifacts")) and (.schema.fields | map(.name) | index("artifact_policy")) and (.schema.fields | map(.name) | index("last_cleanup")) and (.schema.fields | map(.name) | index("managed_processes"))' >/dev/null
"$binary" schema cron --json | jq -e '.ok == true and .schema.name == "cron" and (.schema.fields | map(.name) | index("next_commands")) and (.schema.fields | map(.name) | index("state")) and (.schema.fields | map(.name) | index("health")) and (.schema.fields | map(.name) | index("browser_mode")) and (.schema.fields | map(.name) | index("profile_seed")) and (.schema.fields | map(.name) | index("artifact_policy")) and (.schema.fields | map(.name) | index("tasks")) and (.schema.fields | map(.name) | index("managed_processes")) and (.schema.fields | map(.name) | index("last_run_artifacts")) and (.schema.fields | map(.name) | index("last_cleanup")) and (.schema.fields | map(.name) | index("dry_run"))' >/dev/null
"$binary" schema artifacts-prune --json | jq -e '.ok == true and .schema.name == "artifacts-prune" and (.schema.fields | map(.name) | index("policy")) and (.schema.fields | map(.name) | index("items")) and (.schema.fields | map(.name) | index("bytes_reclaimed")) and (.schema.fields | map(.name) | index("failed_count"))' >/dev/null
"$binary" schema artifacts-run-managed --json | jq -e '.ok == true and .schema.name == "artifacts-run-managed" and (.schema.description | contains("owned process-tree cancellation")) and (.schema.fields | map(.name) | index("task")) and (.schema.fields | map(.name) | index("log")) and (.schema.fields[] | select(.name == "log").description | contains("synchronized hard bound"))' >/dev/null
"$binary" schema cron-profile-seed --json | jq -e '.ok == true and .schema.name == "cron-profile-seed" and (.schema.fields | map(.name) | index("strategy")) and (.schema.fields | map(.name) | index("if_older_than_seconds")) and (.schema.fields | map(.name) | index("last_seed"))' >/dev/null
"$binary" schema cron-task --json | jq -e '.ok == true and .schema.name == "cron-task" and (.schema.fields | map(.name) | index("id")) and (.schema.fields | map(.name) | index("requires_managed_process_sweep")) and (.schema.fields | map(.name) | index("managed_process_sweep_installed")) and (.schema.fields | map(.name) | index("status"))' >/dev/null
"$binary" schema managed-process-reconcile --json | jq -e '.ok == true and .schema.name == "managed-process-reconcile" and (.schema.fields | map(.name) | index("live_count")) and (.schema.fields | map(.name) | index("stale_count")) and (.schema.fields | map(.name) | index("reaped_count")) and (.schema.fields | map(.name) | index("compacted_count")) and (.schema.fields | map(.name) | index("historical_processes")) and (.schema.fields | map(.name) | index("records")) and (.schema.fields | map(.name) | index("signal_failures"))' >/dev/null
"$binary" schema managed-process-history --json | jq -e '.ok == true and (.schema.fields | map(.name) | index("live_probes_attempted")) and (.schema.fields | map(.name) | index("skip_reasons"))' >/dev/null
"$binary" schema collector-readiness --json | jq -e '.ok == true and (.schema.fields | map(.name) | index("session_bound")) and (.schema.fields | map(.name) | index("ready_monotonic_ns")) and (.schema.fields | map(.name) | index("collector_pid"))' >/dev/null
"$binary" schema resource-preflight --json | jq -e '.ok == true and .schema.name == "resource-preflight" and (.schema.fields | map(.name) | index("heavy_work_allowed")) and (.schema.fields | map(.name) | index("policy")) and (.schema.fields | map(.name) | index("checks")) and (.schema.fields | map(.name) | index("reasons"))' >/dev/null
"$binary" schema resource-preflight-check --json | jq -e '.ok == true and .schema.name == "resource-preflight-check" and (.schema.fields | map(.name) | index("retryable")) and (.schema.fields | map(.name) | index("live_count")) and (.schema.fields | map(.name) | index("tab_count")) and (.schema.fields | map(.name) | index("window_count"))' >/dev/null
"$binary" schema workflow-youtube-cookies --json | jq -e '.ok == true and .schema.name == "workflow-youtube-cookies" and (.schema.fields | map(.name) | index("cookie_file")) and (.schema.fields | map(.name) | index("cookie_count")) and (.schema.fields | map(.name) | index("auth_cookie_names")) and (.schema.fields | map(.name) | index("security")) and (.schema.fields | map(.name) | index("cleanup"))' >/dev/null
"$binary" schema cron-migrate-pages-polling --json | jq -e '.ok == true and .schema.name == "cron-migrate-pages-polling" and (.schema.fields | map(.name) | index("candidate_count")) and (.schema.fields | map(.name) | index("managed_keepalive_installed")) and (.schema.fields | map(.name) | index("next_commands"))' >/dev/null
"$binary" schema headless-security --json | jq -e '.ok == true and .schema.name == "headless-security" and (.schema.fields | map(.name) | index("browser_mode")) and (.schema.fields | map(.name) | index("details")) and (.schema.fields | map(.name) | index("next_commands"))' >/dev/null
"$binary" schema version --json | jq -e '.ok == true and .schema.name == "version" and (.schema.fields | map(.name) | index("version"))' >/dev/null
"$binary" schema guide --json | jq -e '.ok == true and .schema.name == "guide" and (.schema.fields | map(.name) | index("schema_version")) and (.schema.fields | map(.name) | index("content")) and (.schema.fields | map(.name) | index("path"))' >/dev/null
"$binary" schema pages --json | jq -e '.ok == true and .schema.name == "pages" and (.schema.fields | map(.name) | index("pages")) and (.schema.fields | map(.name) | index("budget")) and (.schema.fields | map(.name) | index("retry_policy")) and (.schema.fields | map(.name) | index("attempt_count"))' >/dev/null
"$binary" schema targets --json | jq -e '.ok == true and .schema.name == "targets" and (.schema.fields | map(.name) | index("targets")) and (.schema.fields | map(.name) | index("retry_policy")) and (.schema.fields | map(.name) | index("attempt_count"))' >/dev/null
"$binary" schema open --json | jq -e '.ok == true and .schema.name == "open" and (.schema.description | contains("target-index")) and (.schema.fields | map(.name) | index("target_index")) and (.schema.fields | map(.name) | index("page")) and (.schema.fields | map(.name) | index("created")) and (.schema.fields | map(.name) | index("reused")) and (.schema.fields | map(.name) | index("reuse")) and (.schema.fields | map(.name) | index("tab_budget")) and (.schema.fields | map(.name) | index("attempts")) and (.schema.fields | map(.name) | index("retry_policy")) and (.schema.fields | map(.name) | index("run_id")) and (.schema.fields | map(.name) | index("task_id")) and (.schema.fields | map(.name) | index("target_task_ids"))' >/dev/null
"$binary" schema stop-state-classify --json | jq -e '.ok == true and .schema.name == "stop-state-classify" and (.schema.description | contains("target-index")) and (.schema.fields | map(.name) | index("target")) and (.schema.fields | map(.name) | index("target_index")) and (.schema.fields[] | select(.name == "input").description | contains("text byte count"))' >/dev/null
"$binary" schema eval --json | jq -e '.ok == true and .schema.name == "eval" and (.schema.description | contains("target-index")) and (.schema.fields | map(.name) | index("result")) and (.schema.fields | map(.name) | index("target_index")) and (.schema.fields[] | select(.name == "target").description | contains("target-index")) and (.schema.fields | map(.name) | index("attempt_count")) and (.schema.fields | map(.name) | index("last_observed_target"))' >/dev/null
"$binary" schema page-action --json | jq -e '.ok == true and .schema.name == "page-action" and (.schema.description | contains("target-index")) and (.schema.fields | map(.name) | index("action"))' >/dev/null
"$binary" schema snapshot --json | jq -e '.ok == true and .schema.name == "snapshot" and (.schema.description | contains("target-index")) and (.schema.fields | map(.name) | index("target_index")) and (.schema.fields | map(.name) | index("warnings")) and (.schema.fields | map(.name) | index("diagnostics"))' >/dev/null
"$binary" schema connection-add --json | jq -e '.ok == true and .schema.name == "connection-add" and (.schema.fields | map(.name) | index("connection"))' >/dev/null
"$binary" schema connection-list --json | jq -e '.ok == true and .schema.name == "connection-list" and (.schema.fields | map(.name) | index("connections"))' >/dev/null
"$binary" schema connection-select --json | jq -e '.ok == true and .schema.name == "connection-select" and (.schema.fields | map(.name) | index("connection"))' >/dev/null
"$binary" schema connection-current --json | jq -e '.ok == true and .schema.name == "connection-current" and (.schema.fields | map(.name) | index("connection")) and (.schema.fields | map(.name) | index("effective_connection")) and (.schema.fields | map(.name) | index("connection_matches_effective"))' >/dev/null
"$binary" schema connection-remove --json | jq -e '.ok == true and .schema.name == "connection-remove" and (.schema.fields | map(.name) | index("removed"))' >/dev/null
"$binary" schema connection-prune --json | jq -e '.ok == true and .schema.name == "connection-prune" and (.schema.fields | map(.name) | index("removed"))' >/dev/null
"$binary" schema browser-mode --json | jq -e '.ok == true and .schema.name == "browser-mode" and (.schema.fields | map(.name) | index("browser_mode")) and (.schema.fields | map(.name) | index("browser_mode_source"))' >/dev/null
"$binary" schema browser-preflight --json | jq -e '.ok == true and .schema.name == "browser-preflight" and (.schema.fields | map(.name) | index("health")) and (.schema.fields | map(.name) | index("resource_preflight")) and (.schema.fields | map(.name) | index("budget")) and (.schema.fields | map(.name) | index("repair_actions")) and (.schema.fields | map(.name) | index("readiness")) and (.schema.fields | map(.name) | index("cleanup")) and (.schema.fields | map(.name) | index("next_commands"))' >/dev/null
"$binary" schema browser-preflight-readiness --json | jq -e '.ok == true and .schema.name == "browser-preflight-readiness" and (.schema.fields | map(.name) | index("target")) and (.schema.fields | map(.name) | index("cleanup")) and (.schema.fields[] | select(.name == "cleanup" and .type == "workflow_page_cleanup" and (.description | contains("target_gone")) and (.description | contains("keep-open"))))' >/dev/null
"$binary" schema browser-profile-status --json | jq -e '.ok == true and .schema.name == "browser-profile-status" and (.schema.fields | map(.name) | index("managed_browser")) and (.schema.fields | map(.name) | index("profile_perm")) and (.schema.fields | map(.name) | index("metadata_perm")) and (.schema.fields | map(.name) | index("seed_status_path")) and (.schema.fields | map(.name) | index("last_seed")) and (.schema.fields | map(.name) | index("next_commands"))' >/dev/null
"$binary" schema browser-profile-seed --json | jq -e '.ok == true and .schema.name == "browser-profile-seed" and (.schema.fields | map(.name) | index("browser_mode")) and (.schema.fields | map(.name) | index("seed_strategy")) and (.schema.fields | map(.name) | index("managed_browser")) and (.schema.fields | map(.name) | index("maintenance")) and (.schema.fields | map(.name) | index("resource_preflight")) and (.schema.fields | map(.name) | index("seed_status_path")) and (.schema.fields | map(.name) | index("last_seed")) and (.schema.fields | map(.name) | index("default_profile_copied"))' >/dev/null
"$binary" schema browser-resource-budget --json | jq -e '.ok == true and .schema.name == "browser-resource-budget" and (.schema.fields | map(.name) | index("renderer_process_count")) and (.schema.fields | map(.name) | index("max_renderer_processes")) and (.schema.fields | map(.name) | index("target_resource_attribution"))' >/dev/null
"$binary" schema browser-health --json | jq -e '.ok == true and .schema.name == "browser-health" and (.schema.fields | map(.name) | index("resource_budget")) and (.schema.fields | map(.name) | index("target_resource_attribution")) and (.schema.fields | map(.name) | index("process_info"))' >/dev/null
"$binary" schema profile-seed-status --json | jq -e '.ok == true and .schema.name == "profile-seed-status" and (.schema.fields | map(.name) | index("schema_version")) and (.schema.fields | map(.name) | index("seed_action")) and (.schema.fields | map(.name) | index("checked_at")) and (.schema.fields | map(.name) | index("fresh")) and (.schema.fields | map(.name) | index("resource_preflight")) and (.schema.fields | map(.name) | index("maintenance"))' >/dev/null
"$binary" schema profile-seed-maintenance --json | jq -e '.ok == true and .schema.name == "profile-seed-maintenance" and (.schema.fields | map(.name) | index("was_running")) and (.schema.fields | map(.name) | index("managed_process_sweep")) and (.schema.fields | map(.name) | index("managed_stop")) and (.schema.fields | map(.name) | index("healed"))' >/dev/null
"$binary" schema managed-browser --json | jq -e '.ok == true and .schema.name == "managed-browser" and (.schema.fields | map(.name) | index("user_data_dir")) and (.schema.fields | map(.name) | index("profile_seed_strategy"))' >/dev/null
"$binary" schema managed-stop --json | jq -e '.ok == true and .schema.name == "managed-stop" and (.schema.fields | map(.name) | index("remaining_pids")) and (.schema.fields | map(.name) | index("safety_checks"))' >/dev/null
"$binary" schema connection-resolve --json | jq -e '.ok == true and .schema.name == "connection-resolve" and (.schema.fields | map(.name) | index("source")) and (.schema.fields | map(.name) | index("browser_mode")) and (.schema.fields | map(.name) | index("browser_mode_source"))' >/dev/null
"$binary" schema protocol-exec --json | jq -e '.ok == true and .schema.name == "protocol-exec" and (.schema.description | contains("1-based target index")) and (.schema.fields | map(.name) | index("scope")) and (.schema.fields | map(.name) | index("artifact")) and (.schema.fields[] | select(.name == "target" and .type == "target" and (.description | contains("target-index"))))' >/dev/null
"$binary" schema protocol-examples --json | jq -e '.ok == true and .schema.name == "protocol-examples" and (.schema.fields[] | select(.name == "examples").description | contains("required/optional param names"))' >/dev/null
"$binary" schema protocol-metadata --json | jq -e '.ok == true and .schema.name == "protocol-metadata"' >/dev/null
"$binary" schema protocol-domains --json | jq -e '.ok == true and .schema.name == "protocol-domains"' >/dev/null
"$binary" schema protocol-search --json | jq -e '.ok == true and .schema.name == "protocol-search"' >/dev/null
"$binary" schema protocol-describe --json | jq -e '.ok == true and .schema.name == "protocol-describe"' >/dev/null
"$binary" schema daemon-restart --json | jq -e '.ok == true and .schema.name == "daemon-restart" and (.schema.fields | map(.name) | index("restart"))' >/dev/null
"$binary" schema daemon-keepalive --json | jq -e '.ok == true and .schema.name == "daemon-keepalive" and (.schema.fields | map(.name) | index("browser_mode")) and (.schema.fields | map(.name) | index("environment")) and (.schema.fields | map(.name) | index("lock"))' >/dev/null
"$binary" schema auto-heal-environment --json | jq -e '.ok == true and .schema.name == "auto-heal-environment" and (.schema.fields | map(.name) | index("allowed")) and (.schema.fields | map(.name) | index("network")) and (.schema.fields | map(.name) | index("sleep_gap_detected")) and (.schema.fields | map(.name) | index("retry_after_seconds")) and (.schema.fields | map(.name) | index("reason"))' >/dev/null
"$binary" schema daemon-maintenance --json | jq -e '.ok == true and .schema.name == "daemon-maintenance" and (.schema.fields | map(.name) | index("schema_version")) and (.schema.fields | map(.name) | index("run_id")) and (.schema.fields | map(.name) | index("lock")) and (.schema.fields | map(.name) | index("environment")) and (.schema.fields | map(.name) | index("phases")) and (.schema.fields | map(.name) | index("artifacts")) and (.schema.fields | map(.name) | index("warnings")) and ([.schema.fields[] | select(.name == "resource_preflight" or .name == "managed_process_sweep" or .name == "profile_seed" or .name == "health_check" or .name == "cleanup")] | length == 0) and (.schema.fields | map(.name) | index("next_commands"))' >/dev/null
"$binary" schema daemon-maintenance-options --json | jq -e '.ok == true and .schema.name == "daemon-maintenance-options" and (.schema.fields | map(.name) | index("profile_seed_strategy")) and (.schema.fields | map(.name) | index("profile_seed_if_older_than_seconds")) and (.schema.fields | map(.name) | index("cleanup_close")) and (.schema.fields | map(.name) | index("lock_timeout"))' >/dev/null
"$binary" schema daemon-maintenance-phase --json | jq -e '.ok == true and .schema.name == "daemon-maintenance-phase" and (.schema.fields | map(.name) | index("name")) and (.schema.fields | map(.name) | index("resource_gated")) and (.schema.fields | map(.name) | index("started_at")) and (.schema.fields | map(.name) | index("result"))' >/dev/null
"$binary" schema daemon-health-check --json | jq -e '.ok == true and .schema.name == "daemon-health-check" and (.schema.fields | map(.name) | index("status")) and (.schema.fields | map(.name) | index("usable")) and (.schema.fields | map(.name) | index("degraded_reasons")) and (.schema.fields | map(.name) | index("recommended_action")) and (.schema.fields | map(.name) | index("steps")) and (.schema.fields | map(.name) | index("repair")) and (.schema.fields | map(.name) | index("environment")) and (.schema.fields | map(.name) | index("cleanup")) and (.schema.fields[] | select(.name == "cleanup" and .type == "workflow_page_cleanup" and (.description | contains("target_gone")) and (.description | contains("recovery")))) and (.schema.fields | map(.name) | index("artifacts")) and (.schema.fields | map(.name) | index("failure_count"))' >/dev/null
"$binary" schema daemon-status --json | jq -e '.ok == true and .schema.name == "daemon-status" and (.schema.fields | map(.name) | index("daemon"))' >/dev/null
"$binary" schema daemon-logs --json | jq -e '.ok == true and .schema.name == "daemon-logs" and (.schema.fields | map(.name) | index("browser_mode")) and (.schema.fields | map(.name) | index("entries"))' >/dev/null
"$binary" --browser-mode headless --state-dir "$state_dir" daemon maintenance --dry-run --json | jq -e '.ok == true and .schema_version == "cdp-headless-maintenance/v1" and .state == "planned" and .dry_run == true and (.phases | map(.name) | index("managed_process_sweep")) and (.phases | map(.name) | index("daemon_health_check")) and .artifacts.summary' >/dev/null
health_state_dir="$(mktemp -d)"
health_log_dir="$state_dir/health-log"
mkdir -p "$health_log_dir"
set +e
CDP_BIN="$binary" CDP_STATE_DIR="$health_state_dir" CDP_LOG_DIR="$health_log_dir" CDP_ARTIFACT_DIR="$health_log_dir/artifacts" CDP_LOCK_PATH="$health_log_dir/locks/headless-health.lock" CDP_FAILURE_THRESHOLD=1 bash scripts/cdp-headless-healthcheck.sh >"$state_dir/headless-healthcheck.json" 2>"$state_dir/headless-healthcheck.stderr"
healthcheck_code=$?
set -e
if [[ "$healthcheck_code" -eq 0 ]]; then
  jq -e '.ok == true and .state == "healthy" and .artifacts.run_dir' "$state_dir/headless-healthcheck.json" >/dev/null
else
  jq -e '.ok == false and .state == "failed" and (.failure | type == "string") and .artifacts.run_dir and .failure_count >= 1 and (.next_commands | any(contains("daemon health")))' "$state_dir/headless-healthcheck.json" >/dev/null
  test -s "$health_log_dir/artifacts/feature-request-candidate.md"
fi
test -s "$health_log_dir/artifacts/latest.json"

artifact_state_dir="$(mktemp -d)"
mkdir -p "$artifact_state_dir/headless-health/20200101T000000Z" "$artifact_state_dir/browser"
printf 'old run\n' >"$artifact_state_dir/headless-health/20200101T000000Z/summary.json"
printf '{"protected":true}\n' >"$artifact_state_dir/connections.json"
head -c 128 /dev/zero >"$artifact_state_dir/keepalive-headed.log"
"$binary" artifacts prune --state-dir "$artifact_state_dir" --older-than 168h --max-log-size 64B --dry-run --json | jq -e '.ok == true and .dry_run == true and .applied == false and .policy.allowlist_enforced == true and .policy.max_log_size_bytes == 64 and .eligible_count == 2 and (.items | any(.artifact_class == "headless_health_run" and .action == "delete")) and (.items | any(.artifact_class == "managed_task_log" and .action == "bound_log")) and (.items | any((.path | endswith("connections.json")) and .action == "retain"))' >/dev/null
test -d "$artifact_state_dir/headless-health/20200101T000000Z"
test "$(wc -c <"$artifact_state_dir/keepalive-headed.log" | tr -d ' ')" -eq 128
"$binary" artifacts prune --state-dir "$artifact_state_dir" --older-than 168h --max-log-size 64B --apply --json | jq -e '.ok == true and .dry_run == false and .applied == true and .deleted_count == 1 and .bounded_count == 1 and .failed_count == 0 and .bytes_reclaimed > 0' >/dev/null
test ! -e "$artifact_state_dir/headless-health/20200101T000000Z"
test -f "$artifact_state_dir/connections.json"
test "$(wc -c <"$artifact_state_dir/keepalive-headed.log" | tr -d ' ')" -le 64
test -s "$artifact_state_dir/artifact-prune/latest.json"
"$binary" artifacts prune --state-dir "$artifact_state_dir" --older-than 168h --max-log-size 64B --apply --json | jq -e '.ok == true and .action == "unchanged" and .eligible_count == 0 and .deleted_count == 0 and .bounded_count == 0' >/dev/null
"$binary" artifacts run-managed --state-dir "$artifact_state_dir" --task e2e --log headless-maintenance.log --max-log-size 32B --json -- /bin/sh -c 'printf 0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ' | jq -e '.ok == true and .task == "e2e" and .log.max_size_bytes == 32 and .log.size_bytes == 32 and .log.dropped_bytes > 0' >/dev/null
test "$(wc -c <"$artifact_state_dir/headless-maintenance.log" | tr -d ' ')" -eq 32

fake_crontab_store="$state_dir/fake-crontab.txt"
fake_crontab_bin="$state_dir/fake-crontab"
cat >"$fake_crontab_store" <<'EOF_CRONTAB'
SHELL=/bin/sh
0 0 * * * /usr/local/bin/backup
EOF_CRONTAB
cat >"$fake_crontab_bin" <<'EOF_CRONTAB_BIN'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$#" -eq 1 && "$1" == "-l" ]]; then
  cat "$CDP_FAKE_CRONTAB"
  exit 0
fi
if [[ "$#" -eq 1 ]]; then
  cat "$1" >"$CDP_FAKE_CRONTAB"
  exit 0
fi
exit 2
EOF_CRONTAB_BIN
chmod +x "$fake_crontab_bin"
cat >"$fake_crontab_store" <<'EOF_CRONTAB'
SHELL=/bin/sh
0 0 * * * /usr/local/bin/backup
* * * * * /bin/sh -c 'for i in 1 2 3 4 5 6 7 8 9 10 11 12; do nohup $HOME/.local/bin/cdp pages --browser-mode headed >/dev/null 2>&1 & sleep 5; done'
EOF_CRONTAB
CDP_FAKE_CRONTAB="$fake_crontab_store" CDP_CRONTAB_BIN="$fake_crontab_bin" "$binary" doctor --check scheduled-tasks --state-dir "$state_dir" --json | jq -e '.checks | length == 1 and .[0].status == "warn" and (.[0].message | contains("cdp pages polling")) and .[0].details.has_pages_polling_keepalive == true and .[0].details.has_headed_pages_polling == true and .[0].details.pages_polling_count == 1' >/dev/null
CDP_FAKE_CRONTAB="$fake_crontab_store" CDP_CRONTAB_BIN="$fake_crontab_bin" "$binary" cron install --dry-run --state-dir "$state_dir" --json | jq -e '.ok == true and .dry_run == true and .profile_seed.strategy == "managed" and .profile_seed.if_older_than == "6h" and .profile_seed.schedule == "0 * * * *" and (.warnings | any(contains("unmanaged cdp pages polling")))' >/dev/null
CDP_FAKE_CRONTAB="$fake_crontab_store" CDP_CRONTAB_BIN="$fake_crontab_bin" "$binary" cron migrate pages-polling --state-dir "$state_dir" --json | jq -e '.ok == true and .action == "would_remove" and .dry_run == true and .applied == false and .candidate_count == 1 and .removed_count == 0 and .managed_keepalive_installed == false and (.warnings | any(contains("managed daemon keepalive is not installed")))' >/dev/null
CDP_FAKE_CRONTAB="$fake_crontab_store" CDP_CRONTAB_BIN="$fake_crontab_bin" "$binary" cron install --state-dir "$state_dir" --json | jq -e '.ok == true and .changed == true and .artifact_policy.retention_seconds == 604800 and .artifact_policy.max_log_size_bytes == 67108864 and (.warnings | any(contains("unmanaged cdp pages polling"))) and (.managed_block.entries | length == 3) and (.tasks | map(.id) | index("artifact-prune"))' >/dev/null
CDP_FAKE_CRONTAB="$fake_crontab_store" CDP_CRONTAB_BIN="$fake_crontab_bin" "$binary" cron migrate pages-polling --apply --state-dir "$state_dir" --json | jq -e '.ok == true and .action == "removed" and .dry_run == false and .applied == true and .candidate_count == 1 and .removed_count == 1 and .managed_keepalive_installed == true and (.removed_entries | length == 1)' >/dev/null
rg -q '^0 0 \* \* \* /usr/local/bin/backup$' "$fake_crontab_store"
rg -q 'cdp-cli managed browser runtime tasks' "$fake_crontab_store"
rg -q -- 'cron run headed-daemon-keepalive' "$fake_crontab_store"
rg -q -- 'cron run headless-maintenance --profile-seed-strategy managed --profile-seed-if-older-than 6h' "$fake_crontab_store"
rg -q -- 'cron run artifact-prune --artifact-retention 168h --max-log-size 64MiB' "$fake_crontab_store"
! rg -q -e 'flock' -e 'sh -c' -e 'artifacts run-managed' "$fake_crontab_store"
awk 'length($0) > 998 { exit 1 }' "$fake_crontab_store"
! rg -q '>> ' "$fake_crontab_store"
! rg -q 'cdp pages --browser-mode headed' "$fake_crontab_store"
"$binary" --state-dir "$state_dir" cron run artifact-prune --json | jq -e '.ok == true and .task == "artifact-prune" and .state == "completed" and .executed == true' >/dev/null
cat >"$fake_crontab_store" <<'EOF_CRONTAB'
SHELL=/bin/sh
0 0 * * * /usr/local/bin/backup
EOF_CRONTAB
CDP_FAKE_CRONTAB="$fake_crontab_store" CDP_CRONTAB_BIN="$fake_crontab_bin" "$binary" cron status --state-dir "$state_dir" --json | jq -e '.ok == true and .state == "not_installed" and .health.state == "not_installed" and .health.status == "warn" and .health.recommended_command == "cdp cron install --json" and .installed == false and .profile_seed.strategy == "managed" and .profile_seed.if_older_than == "6h" and .profile_seed.schedule == "0 * * * *" and .artifact_policy.retention_seconds == 604800 and .artifact_policy.max_log_size_bytes == 67108864 and (.last_cleanup | type == "object") and (.intended_block.entries | length == 3) and (.locks | type == "object") and (.daemon_locks | type == "object")' >/dev/null
CDP_FAKE_CRONTAB="$fake_crontab_store" CDP_CRONTAB_BIN="$fake_crontab_bin" "$binary" cron diff --state-dir "$state_dir" --json | jq -e '.ok == true and .installed == false and .actions[0].action == "append_managed_block"' >/dev/null
cat >"$fake_crontab_store" <<'EOF_CRONTAB'
SHELL=/bin/sh
# cdp-cli managed browser runtime tasks
* * * * * $HOME/.local/bin/cdp --browser-mode headed cron heal headed --json
# End cdp-cli managed browser runtime tasks
EOF_CRONTAB
mkdir -p "$state_dir/locks"
(sh -c 'exit 0') &
dead_lock_pid=$!
wait "$dead_lock_pid" || true
cat >"$state_dir/locks/keepalive-headless.lock" <<EOF_LOCK
{"name":"keepalive-headless","pid":$dead_lock_pid,"started_at":"2020-01-01T00:00:00Z","phase":"checking"}
EOF_LOCK
touch -d '20 minutes ago' "$state_dir/locks/keepalive-headless.lock" 2>/dev/null || touch -t 202001010000 "$state_dir/locks/keepalive-headless.lock"
CDP_FAKE_CRONTAB="$fake_crontab_store" CDP_CRONTAB_BIN="$fake_crontab_bin" "$binary" cron status --state-dir "$state_dir" --json | jq -e '.ok == true and .state == "needs_update" and .health.state == "needs_update" and .health.status == "warn" and .health.recommended_command == "cdp cron install --json" and .matches_intended == false and .health.stale_lock_count == 1 and (.health.stale_locks | index("keepalive-headless")) and (.health.issues | any(.state == "stale_locks" and .recommended_command == "cdp --browser-mode headless daemon maintenance --stale-lock-after 1s --json"))' >/dev/null
rm -f "$state_dir/locks/keepalive-headless.lock"
: >"$state_dir/locks/keepalive-headless.lock"
touch -d '20 minutes ago' "$state_dir/locks/keepalive-headless.lock" 2>/dev/null || touch -t 202001010000 "$state_dir/locks/keepalive-headless.lock"
CDP_FAKE_CRONTAB="$fake_crontab_store" CDP_CRONTAB_BIN="$fake_crontab_bin" "$binary" cron status --state-dir "$state_dir" --json | jq -e '.ok == true and .matches_intended == false and .health.stale_lock_count == 0 and .locks["keepalive-headless"].exists == true and .locks["keepalive-headless"].stale == false and .locks["keepalive-headless"].marker == "flock_lockfile"' >/dev/null
rm -f "$state_dir/locks/keepalive-headless.lock"
cat >"$fake_crontab_store" <<'EOF_CRONTAB'
SHELL=/bin/sh
0 0 * * * /usr/local/bin/backup
EOF_CRONTAB
CDP_FAKE_CRONTAB="$fake_crontab_store" CDP_CRONTAB_BIN="$fake_crontab_bin" "$binary" --browser-mode headed cron install --dry-run --state-dir "$state_dir" --json | jq -e '.ok == true and .dry_run == true and .changed == true and .installed == false and (.intended_block.entries | length == 2) and (.intended_block.entries | any(contains("cron run headed-daemon-keepalive"))) and (.intended_block.entries | any(contains("cron run artifact-prune")))' >/dev/null
cron_seed_config="$state_dir/cron-seed-config.json"
cat >"$cron_seed_config" <<'EOF_CRON_SEED_CONFIG'
{"browser":{"headless":{"profile_seed_strategy":"copy-default","profile_refresh_after":"30m"}},"artifacts":{"retention":"336h","max_log_size":"8MiB"}}
EOF_CRON_SEED_CONFIG
CDP_FAKE_CRONTAB="$fake_crontab_store" CDP_CRONTAB_BIN="$fake_crontab_bin" "$binary" --config "$cron_seed_config" cron install --dry-run --state-dir "$state_dir" --json | jq -e '.ok == true and .dry_run == true and .profile_seed.strategy == "copy-default" and .profile_seed.if_older_than == "30m" and .profile_seed.if_older_than_seconds == 1800 and .profile_seed.schedule == "*/15 * * * *" and .artifact_policy.retention_seconds == 1209600 and .artifact_policy.max_log_size_bytes == 8388608 and (.intended_block.entries | any(contains("cron run headless-maintenance --profile-seed-strategy copy-default --profile-seed-if-older-than 30m"))) and (.intended_block.entries | any(contains("cron run artifact-prune --artifact-retention 336h --max-log-size 8MiB")))' >/dev/null
CDP_FAKE_CRONTAB="$fake_crontab_store" CDP_CRONTAB_BIN="$fake_crontab_bin" "$binary" cron install --state-dir "$state_dir" --json | jq -e '.ok == true and .changed == true and (.managed_block.entries | length == 3)' >/dev/null
CDP_FAKE_CRONTAB="$fake_crontab_store" CDP_CRONTAB_BIN="$fake_crontab_bin" "$binary" cron install --state-dir "$state_dir" --json | jq -e '.ok == true and .changed == false and .action == "unchanged"' >/dev/null
rg -q '^SHELL=/bin/sh$' "$fake_crontab_store"
rg -q -- 'cron run headed-daemon-keepalive' "$fake_crontab_store"
! rg -q 'cron heal headed' "$fake_crontab_store"
! rg -q -e 'flock' -e 'sh -c' "$fake_crontab_store"
rg -q -- '--profile-seed-strategy managed' "$fake_crontab_store"
rg -q -- '--profile-seed-if-older-than 6h' "$fake_crontab_store"
! rg -q -e '/usr/bin/flock -n' -e '--strategy copy-default' "$fake_crontab_store"
CDP_FAKE_CRONTAB="$fake_crontab_store" CDP_CRONTAB_BIN="$fake_crontab_bin" "$binary" cron remove --state-dir "$state_dir" --json | jq -e '.ok == true and .changed == true and .removed == true' >/dev/null
! rg -q 'cdp-cli managed browser runtime tasks' "$fake_crontab_store"
rg -q '^0 0 \* \* \* /usr/local/bin/backup$' "$fake_crontab_store"

"$binary" schema daemon-health --json | jq -e '.ok == true and .schema.name == "daemon-health" and (.schema.fields | map(.name) | index("health"))' >/dev/null
"$binary" schema daemon-hold-reconcile --json | jq -e '.ok == true and .schema.name == "daemon-hold-reconcile" and (.schema.fields | map(.name) | index("reclaimed_pids")) and (.schema.fields | map(.name) | index("candidates")) and (.schema.fields | map(.name) | index("safety_checks"))' >/dev/null
"$binary" schema daemon-keepalive --json | jq -e '.ok == true and .schema.name == "daemon-keepalive" and (.schema.fields | map(.name) | index("daemon_hold_reconciliation"))' >/dev/null
"$binary" schema browser-health --json | jq -e '.ok == true and .schema.name == "browser-health" and (.schema.fields | map(.name) | index("retired_hold_pids"))' >/dev/null
"$binary" describe --command "open" --json | jq -e '.ok == true and .commands.name == "open" and (.commands.examples | any(contains("--retry transient"))) and (.commands.examples | any(contains("--task-id"))) and (.commands.examples | any(contains("--reuse"))) and (.commands.examples | any(contains("--target-index 2"))) and (.commands.examples | any(contains("--budget-summary"))) and (.commands.flags[] | select(.name == "retry")) and (.commands.flags[] | select(.name == "max-attempts")) and (.commands.flags[] | select(.name == "reuse")) and (.commands.flags[] | select(.name == "budget-summary")) and (.commands.flags[] | select(.name == "target-index" and .type == "int")) and (.commands.flags[] | select(.name == "run-id")) and (.commands.flags[] | select(.name == "task-id")) and (.commands.flags[] | select(.name == "root-task-id")) and (.commands.flags[] | select(.name == "parent-task-id")) and (.commands.flags[] | select(.name == "created-by"))' >/dev/null
"$binary" describe --command "stop-state classify" --json | jq -e '.ok == true and .commands.name == "classify" and (.commands.flags[] | select(.name == "target-index" and .type == "int")) and (.commands.examples | any(contains("--target-index 2"))) and (.commands.examples | any(contains("--text")))' >/dev/null
set +e
stop_state_index_guard_output=$("$binary" stop-state classify --target-index 0 --json 2>/dev/null)
stop_state_index_guard_code=$?
set -e
test "$stop_state_index_guard_code" -eq 2
printf '%s\n' "$stop_state_index_guard_output" | jq -e '.ok == false and .code == "invalid_target_index"' >/dev/null
set +e
stop_state_offline_index_output=$("$binary" stop-state classify --text 'Sign in to continue' --target-index 1 --json 2>/dev/null)
stop_state_offline_index_code=$?
set -e
test "$stop_state_offline_index_code" -eq 2
printf '%s\n' "$stop_state_offline_index_output" | jq -e '.ok == false and .code == "invalid_target_selector"' >/dev/null
"$binary" describe --command "click" --json | jq -e '.ok == true and .commands.name == "click" and (.commands.examples | any(contains("--by role"))) and (.commands.examples | any(contains("--trial"))) and (.commands.examples | any(contains("--force"))) and (.commands.examples | any(contains("--wait-popup"))) and (.commands.examples | any(contains("--wait-download"))) and (.commands.examples | any(contains("--wait-dialog"))) and (.commands.examples | any(contains("--wait-file-chooser"))) and (.commands.examples | any(contains("--wait-request"))) and (.commands.examples | any(contains("--wait-response"))) and (.commands.examples | any(contains("--wait-url-contains"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "trial")) and (.commands.flags[] | select(.name == "force")) and (.commands.flags[] | select(.name == "wait-popup")) and (.commands.flags[] | select(.name == "wait-popup-url")) and (.commands.flags[] | select(.name == "wait-popup-title")) and (.commands.flags[] | select(.name == "wait-download")) and (.commands.flags[] | select(.name == "wait-download-url")) and (.commands.flags[] | select(.name == "wait-download-filename")) and (.commands.flags[] | select(.name == "download-dir")) and (.commands.flags[] | select(.name == "wait-dialog")) and (.commands.flags[] | select(.name == "wait-dialog-action")) and (.commands.flags[] | select(.name == "wait-dialog-message-contains")) and (.commands.flags[] | select(.name == "wait-file-chooser")) and (.commands.flags[] | select(.name == "wait-file-chooser-mode")) and (.commands.flags[] | select(.name == "wait-request")) and (.commands.flags[] | select(.name == "wait-request-match-url")) and (.commands.flags[] | select(.name == "wait-request-method")) and (.commands.flags[] | select(.name == "wait-response")) and (.commands.flags[] | select(.name == "wait-response-match-url")) and (.commands.flags[] | select(.name == "wait-response-status")) and (.commands.flags[] | select(.name == "wait-url")) and (.commands.flags[] | select(.name == "wait-url-contains"))' >/dev/null
"$binary" describe --command "fill" --json | jq -e '.ok == true and .commands.name == "fill" and (.commands.examples | any(contains("--by label"))) and (.commands.examples | any(contains("--trial"))) and (.commands.examples | any(contains("--force"))) and (.commands.examples | any(contains("--wait-selector"))) and (.commands.examples | any(contains("--wait-url-contains"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "trial")) and (.commands.flags[] | select(.name == "force")) and (.commands.flags[] | select(.name == "wait-text")) and (.commands.flags[] | select(.name == "wait-selector")) and (.commands.flags[] | select(.name == "wait-url")) and (.commands.flags[] | select(.name == "wait-url-contains")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "type" --json | jq -e '.ok == true and .commands.name == "type" and (.commands.examples | any(contains("--by label"))) and (.commands.examples | any(contains("--trial"))) and (.commands.examples | any(contains("--force"))) and (.commands.examples | any(contains("--wait-text"))) and (.commands.examples | any(contains("--wait-url-contains"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "trial")) and (.commands.flags[] | select(.name == "force")) and (.commands.flags[] | select(.name == "wait-text")) and (.commands.flags[] | select(.name == "wait-selector")) and (.commands.flags[] | select(.name == "wait-url")) and (.commands.flags[] | select(.name == "wait-url-contains")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "press" --json | jq -e '.ok == true and .commands.name == "press" and (.commands.examples | any(contains("--by label"))) and (.commands.examples | any(contains("--trial"))) and (.commands.examples | any(contains("--wait-text"))) and (.commands.examples | any(contains("--wait-url-contains"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "trial")) and (.commands.flags[] | select(.name == "wait-text")) and (.commands.flags[] | select(.name == "wait-selector")) and (.commands.flags[] | select(.name == "wait-url")) and (.commands.flags[] | select(.name == "wait-url-contains")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "hover" --json | jq -e '.ok == true and .commands.name == "hover" and (.commands.examples | any(contains("--by role"))) and (.commands.examples | any(contains("--target-index 2"))) and (.commands.examples | any(contains("--trial"))) and (.commands.examples | any(contains("--force"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "target-index" and .type == "int")) and (.commands.flags[] | select(.name == "trial")) and (.commands.flags[] | select(.name == "force"))' >/dev/null
"$binary" describe --command "drag" --json | jq -e '.ok == true and .commands.name == "drag" and (.commands.examples | any(contains("--by test-id"))) and (.commands.examples | any(contains("--target-index 2"))) and (.commands.examples | any(contains("--trial"))) and (.commands.examples | any(contains("--force"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "target-index" and .type == "int")) and (.commands.flags[] | select(.name == "trial")) and (.commands.flags[] | select(.name == "force"))' >/dev/null
"$binary" describe --command "select" --json | jq -e '.ok == true and .commands.name == "select" and (.commands.examples | any(contains("--by label"))) and (.commands.examples | any(contains("--trial"))) and (.commands.examples | any(contains("--force"))) and (.commands.examples | any(contains("--wait-text"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "trial")) and (.commands.flags[] | select(.name == "force")) and (.commands.flags[] | select(.name == "wait-text")) and (.commands.flags[] | select(.name == "wait-selector")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "check" --json | jq -e '.ok == true and .commands.name == "check" and (.commands.examples | any(contains("--by label"))) and (.commands.examples | any(contains("--by role"))) and (.commands.examples | any(contains("--trial"))) and (.commands.examples | any(contains("--force"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "trial")) and (.commands.flags[] | select(.name == "force"))' >/dev/null
"$binary" describe --command "uncheck" --json | jq -e '.ok == true and .commands.name == "uncheck" and (.commands.examples | any(contains("--by label"))) and (.commands.examples | any(contains("--by role"))) and (.commands.examples | any(contains("--trial"))) and (.commands.examples | any(contains("--force"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "trial")) and (.commands.flags[] | select(.name == "force"))' >/dev/null
"$binary" describe --command "file" --json | jq -e '.ok == true and .commands.name == "file" and (.commands.examples | any(contains("--by label"))) and (.commands.examples | any(contains("--target-index 2"))) and (.commands.examples | any(contains("--trial"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "target-index" and .type == "int")) and (.commands.flags[] | select(.name == "trial"))' >/dev/null
"$binary" schema file --json | jq -e '.ok == true and .schema.name == "file" and (.schema.description | contains("target-index")) and (.schema.fields | map(.name) | index("file")) and (.schema.fields | map(.name) | index("target_index")) and (.schema.fields | map(.name) | index("actionability")) and (.schema.fields | map(.name) | index("resolved_selector"))' >/dev/null
"$binary" describe --command "file chooser" --json | jq -e '.ok == true and .commands.name == "chooser" and (.commands.examples | any(contains("--target <target-id>"))) and (.commands.examples | any(contains("--target-index 2"))) and (.commands.examples | any(contains("--trial"))) and (.commands.examples | any(contains("first.epub") and contains("second.epub"))) and (.commands.flags[] | select(.name == "target")) and (.commands.flags[] | select(.name == "target-index" and .type == "int")) and (.commands.flags[] | select(.name == "trial"))' >/dev/null
"$binary" schema file-chooser --json | jq -e '.ok == true and .schema.name == "file-chooser" and (.schema.description | contains("target-index")) and (.schema.fields | map(.name) | index("file_chooser")) and (.schema.fields | map(.name) | index("target")) and (.schema.fields | map(.name) | index("target_index"))' >/dev/null
"$binary" describe --command "scroll" --json | jq -e '.ok == true and .commands.name == "scroll" and (.commands.examples | any(contains("--by role"))) and (.commands.examples | any(contains("--target-index 2"))) and (.commands.examples | any(contains("--trial"))) and (.commands.examples | any(contains("--block"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "target-index" and .type == "int")) and (.commands.flags[] | select(.name == "trial")) and (.commands.flags[] | select(.name == "block")) and (.commands.flags[] | select(.name == "inline"))' >/dev/null
"$binary" schema scroll --json | jq -e '.ok == true and .schema.name == "scroll" and (.schema.description | contains("indexed page")) and (.schema.fields | map(.name) | index("scroll")) and (.schema.fields | map(.name) | index("target_index")) and (.schema.fields | map(.name) | index("actionability")) and (.schema.fields | map(.name) | index("resolved_selector"))' >/dev/null
"$binary" describe --command "frames" --json | jq -e '.ok == true and .commands.name == "frames" and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "targets" --json | jq -e '.ok == true and .commands.name == "targets" and (.commands.examples | any(contains("--retry transient"))) and (.commands.flags[] | select(.name == "retry")) and (.commands.flags[] | select(.name == "max-attempts"))' >/dev/null
"$binary" describe --command "pages" --json | jq -e '.ok == true and .commands.name == "pages" and (.commands.examples | any(contains("--title-contains"))) and (.commands.examples | any(contains("--retry transient"))) and (.commands.flags[] | select(.name == "retry")) and (.commands.flags[] | select(.name == "max-attempts"))' >/dev/null
"$binary" describe --command "eval" --json | jq -e '.ok == true and .commands.name == "eval" and (.commands.examples | any(contains("--title-contains"))) and (.commands.examples | any(contains("--target-index 2"))) and (.commands.examples | any(contains("--retry transient"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int")) and (.commands.flags[] | select(.name == "retry")) and (.commands.flags[] | select(.name == "max-attempts"))' >/dev/null
"$binary" describe --command "observe" --json | jq -e '.ok == true and .commands.name == "observe" and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" schema observe --json | jq -e '.ok == true and .schema.name == "observe" and (.schema.description | contains("target-index")) and (.schema.fields | map(.name) | index("target_index"))' >/dev/null
"$binary" describe --command "text" --json | jq -e '.ok == true and .commands.name == "text" and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "html" --json | jq -e '.ok == true and .commands.name == "html" and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "snapshot" --json | jq -e '.ok == true and .commands.name == "snapshot" and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "page select" --json | jq -e '.ok == true and .commands.name == "select" and (.commands.examples | any(contains("--url-contains"))) and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "page reload" --json | jq -e '.ok == true and .commands.name == "reload" and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "page back" --json | jq -e '.ok == true and .commands.name == "back" and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "page forward" --json | jq -e '.ok == true and .commands.name == "forward" and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "page activate" --json | jq -e '.ok == true and .commands.name == "activate" and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "page close" --json | jq -e '.ok == true and .commands.name == "close" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "page cleanup" --json | jq -e '.ok == true and .commands.name == "cleanup" and (.commands.examples | any(contains("--close"))) and (.commands.examples | any(contains("--root-task-id"))) and (.commands.flags[] | select(.name == "run-id")) and (.commands.flags[] | select(.name == "task-id")) and (.commands.flags[] | select(.name == "root-task-id"))' >/dev/null
"$binary" schema page-cleanup --json | jq -e '.ok == true and .schema.name == "page-cleanup" and (.schema.fields | map(.name) | index("candidates")) and (.schema.fields[] | select(.name == "cleanup").description | contains("target-task map"))' >/dev/null
"$binary" describe --json | jq -e '.commands.children[] | select(.name == "page") | .children[] | select(.name == "cleanup")' >/dev/null
help_output="$("$binary" --help)"
rg -q "cleanup routine|page cleanup|clean" <<<"$help_output"
page_cleanup_describe="$("$binary" describe --command "page cleanup" --json)"
page_cleanup_examples="$(jq -r '.commands.examples[]' <<<"$page_cleanup_describe")"
rg -q -- '--browser-mode headed page cleanup' <<<"$page_cleanup_examples"
rg -q -- '--browser-mode headless page cleanup --created-by cdp --idle-for 30m --close --force --wait-gone --max-attempts 3 --close-concurrency 4 --max 25' <<<"$page_cleanup_examples"
page_cleanup_short="$(jq -r '.commands.short' <<<"$page_cleanup_describe")"
rg -q 'cron cleanup' <<<"$page_cleanup_short"
"$binary" describe --command "page cleanup" --json | jq -e '.commands.flags[] | select(.name == "max")' >/dev/null
"$binary" describe --command "text" --json | jq -e '.ok == true and .commands.name == "text" and (.commands.examples | any(contains("--retry transient"))) and (.commands.flags[] | select(.name == "retry")) and (.commands.flags[] | select(.name == "max-attempts"))' >/dev/null
"$binary" describe --command "locator find" --json | jq -e '.ok == true and .commands.name == "find" and (.commands.examples | any(contains("--by role"))) and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "html" --json | jq -e '.ok == true and .commands.name == "html" and (.commands.examples | any(contains("--diagnose-empty"))) and (.commands.flags[] | select(.name == "diagnose-empty"))' >/dev/null
"$binary" describe --command "dom query" --json | jq -e '.ok == true and .commands.name == "query" and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "css inspect" --json | jq -e '.ok == true and .commands.name == "inspect" and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "layout overflow" --json | jq -e '.ok == true and .commands.name == "overflow" and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "wait text" --json | jq -e '.ok == true and .commands.name == "text" and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "wait selector" --json | jq -e '.ok == true and .commands.name == "selector" and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "wait url" --json | jq -e '.ok == true and .commands.name == "url" and (.commands.examples | any(contains("--mode contains"))) and (.commands.examples | any(contains("--mode exact"))) and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "mode")) and (.commands.flags[] | select(.name == "poll")) and (.commands.flags[] | select(.name == "url-contains")) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "wait locator" --json | jq -e '.ok == true and .commands.name == "locator" and (.commands.examples | any(contains("--by role"))) and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "strict")) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "wait eval" --json | jq -e '.ok == true and .commands.name == "eval" and (.commands.examples | any(contains("__rendered"))) and (.commands.examples | any(contains("--ready-expr"))) and (.commands.examples | any(contains("--retry transient"))) and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "ready-expr")) and (.commands.flags[] | select(.name == "ready-field")) and (.commands.flags[] | select(.name == "out-dir")) and (.commands.flags[] | select(.name == "retry")) and (.commands.flags[] | select(.name == "max-attempts")) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "wait load-state" --json | jq -e '.ok == true and .commands.name == "load-state" and (.commands.examples | any(contains("domcontentloaded"))) and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "poll")) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "wait request" --json | jq -e '.ok == true and .commands.name == "request" and (.commands.examples | any(contains("--match-url"))) and (.commands.flags[] | select(.name == "method")) and (.commands.flags[] | select(.name == "resource-type")) and (.commands.flags[] | select(.name == "redact"))' >/dev/null
"$binary" describe --command "wait response" --json | jq -e '.ok == true and .commands.name == "response" and (.commands.examples | any(contains("--status"))) and (.commands.flags[] | select(.name == "status")) and (.commands.flags[] | select(.name == "status-min")) and (.commands.flags[] | select(.name == "status-max")) and (.commands.flags[] | select(.name == "redact"))' >/dev/null
"$binary" describe --command "wait network-idle" --json | jq -e '.ok == true and .commands.name == "network-idle" and (.commands.examples | any(contains("--idle"))) and (.commands.examples | any(contains("--ignore-url-contains"))) and (.commands.flags[] | select(.name == "idle")) and (.commands.flags[] | select(.name == "max-inflight")) and (.commands.flags[] | select(.name == "ignore-url-contains")) and (.commands.flags[] | select(.name == "redact"))' >/dev/null
"$binary" describe --command "wait dialog" --json | jq -e '.ok == true and .commands.name == "dialog" and (.commands.examples | any(contains("--action dismiss"))) and (.commands.flags[] | select(.name == "type")) and (.commands.flags[] | select(.name == "message-contains")) and (.commands.flags[] | select(.name == "action")) and (.commands.flags[] | select(.name == "prompt-text")) and (.commands.flags[] | select(.name == "redact"))' >/dev/null
"$binary" describe --command "wait file-chooser" --json | jq -e '.ok == true and .commands.name == "file-chooser" and (.commands.examples | any(contains("--mode single"))) and (.commands.flags[] | select(.name == "mode"))' >/dev/null
"$binary" describe --command "wait popup" --json | jq -e '.ok == true and .commands.name == "popup" and (.commands.examples | any(contains("--match-url"))) and (.commands.flags[] | select(.name == "target")) and (.commands.flags[] | select(.name == "match-url")) and (.commands.flags[] | select(.name == "match-title"))' >/dev/null
"$binary" describe --command "wait download" --json | jq -e '.ok == true and .commands.name == "download" and (.commands.examples | any(contains("--download-dir"))) and (.commands.flags[] | select(.name == "match-url")) and (.commands.flags[] | select(.name == "filename-contains")) and (.commands.flags[] | select(.name == "download-dir")) and (.commands.flags[] | select(.name == "state")) and (.commands.flags[] | select(.name == "redact"))' >/dev/null
"$binary" describe --command "assert value" --json | jq -e '.ok == true and .commands.name == "value" and (.commands.examples | any(contains("--by label"))) and (.commands.examples | any(contains("--poll"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "assert text" --json | jq -e '.ok == true and .commands.name == "text" and (.commands.examples | any(contains("--by role"))) and (.commands.examples | any(contains("--poll"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "assert url" --json | jq -e '.ok == true and .commands.name == "url" and (.commands.examples | any(contains("--mode contains"))) and (.commands.examples | any(contains("--poll"))) and (.commands.flags[] | select(.name == "mode")) and (.commands.flags[] | select(.name == "poll")) and (.commands.flags[] | select(.name == "url-contains"))' >/dev/null
"$binary" describe --command "assert title" --json | jq -e '.ok == true and .commands.name == "title" and (.commands.examples | any(contains("--mode exact"))) and (.commands.examples | any(contains("--poll"))) and (.commands.flags[] | select(.name == "mode")) and (.commands.flags[] | select(.name == "poll")) and (.commands.flags[] | select(.name == "title-contains"))' >/dev/null
"$binary" describe --command "assert count" --json | jq -e '.ok == true and .commands.name == "count" and (.commands.examples | any(contains("--by role"))) and (.commands.examples | any(contains("--poll"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "assert attribute" --json | jq -e '.ok == true and .commands.name == "attribute" and (.commands.examples | any(contains("--mode exact"))) and (.commands.examples | any(contains("--by role"))) and (.commands.flags[] | select(.name == "mode")) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "assert class" --json | jq -e '.ok == true and .commands.name == "class" and (.commands.examples | any(contains("--by role"))) and (.commands.examples | any(contains("--poll"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "assert focused" --json | jq -e '.ok == true and .commands.name == "focused" and (.commands.examples | any(contains("--by label"))) and (.commands.examples | any(contains("--poll"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "assert css" --json | jq -e '.ok == true and .commands.name == "css" and (.commands.examples | any(contains("background-color"))) and (.commands.examples | any(contains("--by role"))) and (.commands.flags[] | select(.name == "mode")) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "assert role" --json | jq -e '.ok == true and .commands.name == "role" and (.commands.examples | any(contains("--by role"))) and (.commands.examples | any(contains("--poll"))) and (.commands.flags[] | select(.name == "mode")) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "assert name" --json | jq -e '.ok == true and .commands.name == "name" and (.commands.examples | any(contains("--mode exact"))) and (.commands.examples | any(contains("--by role"))) and (.commands.flags[] | select(.name == "mode")) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "assert aria-snapshot" --json | jq -e '.ok == true and .commands.name == "aria-snapshot" and (.commands.examples | any(contains("--expected"))) and (.commands.examples | any(contains("--selector body"))) and (.commands.examples | any(contains("--file"))) and (.commands.flags[] | select(.name == "selector")) and (.commands.flags[] | select(.name == "depth")) and (.commands.flags[] | select(.name == "limit")) and (.commands.flags[] | select(.name == "include-ignored")) and (.commands.flags[] | select(.name == "mode")) and (.commands.flags[] | select(.name == "expected")) and (.commands.flags[] | select(.name == "file")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "assert attached" --json | jq -e '.ok == true and .commands.name == "attached" and (.commands.examples | any(contains("--by role"))) and (.commands.examples | any(contains("--poll"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "assert detached" --json | jq -e '.ok == true and .commands.name == "detached" and (.commands.examples | any(contains("--by text"))) and (.commands.examples | any(contains("--poll"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "assert visible" --json | jq -e '.ok == true and .commands.name == "visible" and (.commands.examples | any(contains("--by role"))) and (.commands.examples | any(contains("--poll"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "assert hidden" --json | jq -e '.ok == true and .commands.name == "hidden" and (.commands.examples | any(contains("--by role"))) and (.commands.examples | any(contains("--poll"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "assert in-viewport" --json | jq -e '.ok == true and .commands.name == "in-viewport" and (.commands.examples | any(contains("--by role"))) and (.commands.examples | any(contains("--poll"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "assert enabled" --json | jq -e '.ok == true and .commands.name == "enabled" and (.commands.examples | any(contains("--by role"))) and (.commands.examples | any(contains("--poll"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "assert disabled" --json | jq -e '.ok == true and .commands.name == "disabled" and (.commands.examples | any(contains("--by role"))) and (.commands.examples | any(contains("--poll"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "assert editable" --json | jq -e '.ok == true and .commands.name == "editable" and (.commands.examples | any(contains("--by label"))) and (.commands.examples | any(contains("--poll"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "assert readonly" --json | jq -e '.ok == true and .commands.name == "readonly" and (.commands.examples | any(contains("--by label"))) and (.commands.examples | any(contains("--poll"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "assert checked" --json | jq -e '.ok == true and .commands.name == "checked" and (.commands.examples | any(contains("--by label"))) and (.commands.examples | any(contains("--poll"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "assert unchecked" --json | jq -e '.ok == true and .commands.name == "unchecked" and (.commands.examples | any(contains("--by label"))) and (.commands.examples | any(contains("--poll"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "assert indeterminate" --json | jq -e '.ok == true and .commands.name == "indeterminate" and (.commands.examples | any(contains("--by role"))) and (.commands.examples | any(contains("--poll"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
for assertion_command in value text url title count attribute class focused css role name aria-snapshot attached detached visible hidden in-viewport enabled disabled editable readonly checked unchecked indeterminate; do
  "$binary" describe --command "assert $assertion_command" --json | jq -e '.ok == true and (.commands.flags[] | select(.name == "target-index" and .type == "int")) and (.commands.examples | any(contains("--target-index 2")))' >/dev/null
done
"$binary" describe --command "snapshot" --json | jq -e '.ok == true and .commands.name == "snapshot" and (.commands.examples | any(contains("--diagnose-empty"))) and (.commands.flags[] | select(.name == "debug-empty"))' >/dev/null
"$binary" describe --command "screenshot" --json | jq -e '.ok == true and .commands.name == "screenshot" and (.commands.examples | any(contains("--preset mobile"))) and (.commands.examples | any(contains("--tile-full-page"))) and (.commands.examples | any(contains("--element"))) and (.commands.flags[] | select(.name == "crop")) and (.commands.flags[] | select(.name == "navigate")) and (.commands.flags[] | select(.name == "preset")) and (.commands.flags[] | select(.name == "tile-full-page")) and (.commands.flags[] | select(.name == "out-dir"))' >/dev/null
"$binary" describe --command "screenshot render" --json | jq -e '.ok == true and .commands.name == "render" and (.commands.examples | any(contains("--serve"))) and (.commands.examples | any(contains("--wait-for"))) and (.commands.flags[] | select(.name == "wait-for"))' >/dev/null
"$binary" describe --command "console" --json | jq -e '.ok == true and .commands.name == "console" and (.commands.examples | any(contains("--errors")))' >/dev/null
"$binary" describe --command "network" --json | jq -e '.ok == true and .commands.name == "network" and (.commands.examples | any(contains("--failed")))' >/dev/null
"$binary" describe --command "network capture" --json | jq -e '.ok == true and .commands.name == "capture" and (.commands.examples | any(contains("--redact"))) and (.commands.examples | any(contains("--har-out"))) and (.commands.flags[] | select(.name == "har-out"))' >/dev/null
"$binary" describe --command "wait request" --json | jq -e '.ok == true and .commands.name == "request" and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "wait response" --json | jq -e '.ok == true and .commands.name == "response" and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "wait network-idle" --json | jq -e '.ok == true and .commands.name == "network-idle" and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "wait dialog" --json | jq -e '.ok == true and .commands.name == "dialog" and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "wait file-chooser" --json | jq -e '.ok == true and .commands.name == "file-chooser" and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "wait popup" --json | jq -e '.ok == true and .commands.name == "popup" and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "wait download" --json | jq -e '.ok == true and .commands.name == "download" and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "storage" --json | jq -e '.ok == true and .commands.name == "storage" and (.commands.children | map(.name) | index("snapshot"))' >/dev/null
"$binary" describe --command "storage cookies set" --json | jq -e '.ok == true and .commands.name == "set" and (.commands.examples | any(contains("--name")))' >/dev/null
"$binary" describe --command "storage indexeddb" --json | jq -e '.ok == true and .commands.name == "indexeddb" and (.commands.children | map(.name) | index("put"))' >/dev/null
"$binary" describe --command "storage indexeddb put" --json | jq -e '.ok == true and .commands.name == "put" and (.commands.examples | any(contains("@tmp/value.json")))' >/dev/null
"$binary" describe --command "storage cache" --json | jq -e '.ok == true and .commands.name == "cache" and (.commands.children | map(.name) | index("put"))' >/dev/null
"$binary" describe --command "storage cache put" --json | jq -e '.ok == true and .commands.name == "put" and (.commands.examples | any(contains("--content-type")))' >/dev/null
"$binary" describe --command "storage service-workers" --json | jq -e '.ok == true and .commands.name == "service-workers" and (.commands.children | map(.name) | index("unregister"))' >/dev/null
"$binary" describe --command "storage service-workers unregister" --json | jq -e '.ok == true and .commands.name == "unregister" and (.commands.examples | any(contains("--scope")))' >/dev/null
for storage_command in \
  "storage list" "storage get" "storage set" "storage delete" "storage clear" "storage snapshot" \
  "storage cookies list" "storage cookies set" "storage cookies delete" \
  "storage indexeddb list" "storage indexeddb get" "storage indexeddb put" "storage indexeddb dump" "storage indexeddb delete" "storage indexeddb clear" \
  "storage cache list" "storage cache get" "storage cache put" "storage cache delete" "storage cache clear" \
  "storage service-workers list" "storage service-workers unregister"; do
  "$binary" describe --command "$storage_command" --json \
    | jq -e '.ok == true and (.commands.flags[] | select(.name == "target-index" and .type == "int")) and (.commands.flags[] | select(.name == "title-contains" and .type == "string")) and (.commands.examples | any(contains("--target-index 2")))' >/dev/null
done
for storage_schema in storage storage-cache storage-indexeddb storage-service-workers storage-snapshot; do
  "$binary" schema "$storage_schema" --json \
    | jq -e '.ok == true and (.schema.description | contains("target-index")) and (.schema.fields | map(.name) | index("target_index")) and (.schema.fields[] | select(.name == "target_index" and .type == "integer"))' >/dev/null
done
"$binary" schema storage-diff --json \
  | jq -e '.ok == true and ([.schema.fields[] | select(.name == "target_index")] | length == 0)' >/dev/null
storage_guard_commands=(
  "storage list --include localStorage"
  "storage get localStorage feature"
  "storage set localStorage feature disabled"
  "storage delete localStorage feature"
  "storage clear localStorage"
  "storage snapshot --include localStorage"
  "storage cookies list"
  "storage cookies set --name feature --value enabled"
  "storage cookies delete --name feature"
  "storage indexeddb list"
  "storage indexeddb get app settings feature"
  "storage indexeddb put app settings feature {\"enabled\":true}"
  "storage indexeddb dump app settings --page-size 2"
  "storage indexeddb delete app settings feature"
  "storage indexeddb clear app settings"
  "storage cache list"
  "storage cache get app https://example.com/api"
  "storage cache put app https://example.com/api {\"ok\":true}"
  "storage cache delete app https://example.com/api"
  "storage cache clear app-cache"
  "storage service-workers list"
  "storage service-workers unregister --all"
)
for storage_guard in "${storage_guard_commands[@]}"; do
  read -r -a storage_guard_args <<<"$storage_guard"
  set +e
  storage_guard_output="$("$binary" "${storage_guard_args[@]}" --target-index 0 --json 2>/dev/null)"
  storage_guard_code=$?
  set -e
  test "$storage_guard_code" -eq 2
  printf '%s\n' "$storage_guard_output" | jq -e '.ok == false and .code == "invalid_target_index"' >/dev/null
done
"$binary" describe --command "page close" --json | jq -e '.ok == true and .commands.name == "close" and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "protocol exec" --json | jq -e '.ok == true and .commands.name == "exec" and (.commands.examples | any(contains("--target"))) and (.commands.examples | any(contains("--target-index 2"))) and (.commands.examples | any(contains("--target-type service_worker"))) and (.commands.flags[] | select(.name == "target-type" and .type == "string")) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "protocol examples" --json | jq -e '.ok == true and .commands.name == "examples" and (.commands.examples | any(contains("Page.captureScreenshot")))' >/dev/null
"$binary" describe --command "workflow visible-posts" --json | jq -e '.ok == true and .commands.name == "visible-posts" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "workflow hacker-news" --json | jq -e '.ok == true and .commands.name == "hacker-news" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "workflow google-maps-directions" --json | jq -e '.ok == true and .commands.name == "google-maps-directions" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "workflow a11y" --json | jq -e '.ok == true and .commands.name == "a11y" and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "workflow responsive-audit" --json | jq -e '.ok == true and .commands.name == "responsive-audit" and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" workflow responsive-audit --help | grep -F 'exact-target cleanup' >/dev/null
"$binary" describe --command "workflow console-errors" --json | jq -e '.ok == true and .commands.name == "console-errors" and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "workflow network-failures" --json | jq -e '.ok == true and .commands.name == "network-failures" and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
workflow_target_index_guards=(
  "workflow action-capture --action press:Enter --selector body"
  "workflow console-errors"
  "workflow network-failures"
  "workflow submit-search Search query --submit none"
  "workflow page-load"
  "workflow rendered-extract"
  "workflow debug-bundle"
  "workflow verify"
  "workflow perf"
  "workflow a11y"
  "workflow responsive-audit"
  "network block"
  "network mock"
)
for workflow_target_index_guard in "${workflow_target_index_guards[@]}"; do
  read -r -a workflow_target_index_args <<<"$workflow_target_index_guard"
  set +e
  workflow_target_index_output="$("$binary" "${workflow_target_index_args[@]}" --target-index 0 --json 2>/dev/null)"
  workflow_target_index_code=$?
  set -e
  test "$workflow_target_index_code" -eq 2
  printf '%s\n' "$workflow_target_index_output" | jq -e '.ok == false and .code == "invalid_target_index"' >/dev/null
done
"$binary" describe --command "network capture" --json | jq -e '.ok == true and .commands.name == "capture" and (.commands.examples | any(contains("--body-out-dir") and contains("--redact safe"))) and (.commands.flags[] | select(.name == "body-artifact-limit"))' >/dev/null
"$binary" describe --command "network block" --json | jq -e '.ok == true and .commands.name == "block" and (.commands.examples | any(contains("--pattern") and contains("--duration"))) and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "network mock" --json | jq -e '.ok == true and .commands.name == "mock" and (.commands.examples | any(contains("--rule") and contains("max_matches"))) and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "workflow page-load" --json | jq -e '.ok == true and .commands.name == "page-load" and (.commands.examples | any(contains("--reload"))) and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int")) and (.commands.flags[] | select(.name == "ready-file"))' >/dev/null
for diagnostic_schema in workflow-verify workflow-perf workflow-a11y workflow-page-load; do
  "$binary" schema "$diagnostic_schema" --json | jq -e '.ok == true and (.schema.fields | map(.name) | index("cleanup")) and (.schema.fields[] | select(.name == "cleanup" and .type == "workflow_page_cleanup" and (.description | contains("target_gone")))) and (.schema.description | contains("caller-owned"))' >/dev/null
done
"$binary" schema workflow-page-cleanup --json | jq -e '.ok == true and .schema.name == "workflow-page-cleanup" and (.schema.fields | map(.name) | index("target_gone")) and (.schema.fields | map(.name) | index("recovery_command")) and (.schema.description | contains("lease-target-policy-failure")) and (.schema.fields[] | select(.name == "closed").description | contains("confirmed gone"))' >/dev/null
"$binary" schema lease-target-policy-failure --json | jq -e '.ok == true and .schema.name == "lease-target-policy-failure" and (.schema.fields | map(.name) | index("target_id")) and (.schema.fields | map(.name) | index("policy_error")) and (.schema.fields | map(.name) | index("primary_error")) and (.schema.fields | map(.name) | index("close")) and (.schema.fields | map(.name) | index("recovery_command")) and (.schema.fields[] | select(.name == "close").type == "page_close_report")' >/dev/null
"$binary" describe --command "workflow rendered-extract" --json | jq -e '.ok == true and .commands.name == "rendered-extract" and (.commands.examples | any(contains("--serp google"))) and (.commands.examples | any(contains("--target-index 2"))) and (.commands.examples | any(contains("arxiv.org/pdf/") and contains("--content-extractor auto"))) and (.commands.examples | any(contains("news.ycombinator.com/item") and contains("--content-extractor auto"))) and (.commands.examples | any(contains("--target") and contains("--reload"))) and (.commands.examples | any(contains("--wait 20s") and contains("--settle 2s"))) and (.commands.flags[] | select(.name == "target")) and (.commands.flags[] | select(.name == "target-index" and .type == "int")) and (.commands.flags[] | select(.name == "url-contains")) and (.commands.flags[] | select(.name == "title-contains")) and (.commands.flags[] | select(.name == "reload")) and (.commands.flags[] | select(.name == "settle" and .default == "2s")) and (.commands.flags[] | select(.name == "selector" and (.usage | contains("generic capture/fallback")) and (.usage | contains("Hacker News")))) and (.commands.flags[] | select(.name == "content-extractor" and .default == "auto" and (.usage | contains("native source profiles")))) and (.commands.flags[] | select(.name == "min-visible-words" and (.usage | contains("readiness")) and (.usage | contains("0 to disable")))) and (.commands.flags[] | select(.name == "out-dir"))' >/dev/null
"$binary" describe --command "workflow pdf-to-markdown" --json | jq -e '.ok == true and .commands.name == "pdf-to-markdown" and (.commands.examples | length > 0) and (.commands.flags[] | select(.name == "out-dir" and ((has("default") | not) or .default == "")))' >/dev/null
pdf_text_help="$("$binary" workflow pdf-to-markdown --help)"
grep -Fq 'bounded diagnostics' <<<"$pdf_text_help"
grep -Fq 'process-group cancellation' <<<"$pdf_text_help"
"$binary" describe --command "workflow google-translate" --json | jq -e '.ok == true and .commands.name == "google-translate" and (.commands.examples | any(contains("--source"))) and (.commands.examples | any(contains("--file"))) and (.commands.flags[] | select(.name == "mode")) and (.commands.flags[] | select(.name == "wait")) and (.commands.flags[] | select(.name == "chunk-size"))' >/dev/null
google_translate_help="$("$binary" workflow google-translate --help)"
grep -Fq 'Poppler diagnostics' <<<"$google_translate_help"
grep -Fq 'process group' <<<"$google_translate_help"
grep -Fq 'regular non-empty file' <<<"$google_translate_help"
"$binary" explain-error pdf_burst_failed --json | jq -e '.ok == true and .error.code == "pdf_burst_failed" and .error.exit_code == 1' >/dev/null
"$binary" explain-error pdf_burst_invalid_page --json | jq -e '.ok == true and .error.code == "pdf_burst_invalid_page" and .error.exit_code == 1' >/dev/null
"$binary" describe --command "workflow web-research" --json | jq -e '.ok == true and .commands.name == "web-research" and (.commands.children | map(.name) | index("extract"))' >/dev/null
"$binary" describe --command "workflow web-research serp" --json | jq -e '.ok == true and .commands.name == "serp" and (.commands.examples | any(contains("--serp google") and contains("cdr:1,cd_min:07/01/2026,cd_max:07/01/2026"))) and (.commands.examples | any(contains("--google-ai auto"))) and (.commands.examples | any(contains("--google-ai mode"))) and (.commands.examples | any(contains("--navigation-delay 30s") and contains("--parallel 1") and contains("--blocked-failure-threshold 1"))) and (.commands.examples | any(contains("--serp all"))) and (.commands.examples | any(contains("--parallel-engines"))) and (.commands.examples | any(contains("--serp duckduckgo"))) and (.commands.examples | any(contains("--fallback-serp google"))) and (.commands.examples | any(contains("--result-pages 3") and contains("--settle 3s"))) and (.commands.examples | any(contains("--fast-fail-blocked"))) and (.commands.examples | any(contains("--progress stderr"))) and (.commands.flags[] | select(.name == "query-file" and (.usage | contains("query<TAB>Google tbs time filter")) and (.usage | contains("# comment rows ignored")))) and (.commands.flags[] | select(.name == "serp" and (.usage | contains("comma-separated")))) and (.commands.flags[] | select(.name == "google-ai" and (.usage | contains("auto")) and (.usage | contains("mode")) and (.usage | contains("off")) and .default == "auto")) and (.commands.flags[] | select(.name == "parallel-engines")) and (.commands.flags[] | select(.name == "fallback-serp" and (.usage | contains("auto")))) and (.commands.flags[] | select(.name == "candidate-out")) and (.commands.flags[] | select(.name == "settle" and .default == "3s")) and (.commands.flags[] | select(.name == "navigation-delay" and .default == "0s" and (.usage | contains("minimum delay")) and (.usage | contains("engine lane")))) and (.commands.flags[] | select(.name == "result-pages")) and (.commands.flags[] | select(.name == "fast-fail-blocked")) and (.commands.flags[] | select(.name == "blocked-failure-threshold")) and (.commands.flags[] | select(.name == "progress"))' >/dev/null
"$binary" describe --command "workflow web-research extract" --json | jq -e '.ok == true and .commands.name == "extract" and (.commands.examples | any(contains("--parallel 4") and contains("--settle 2s"))) and (.commands.examples | any(contains("--content-extractor auto"))) and (.commands.examples | any(contains("--parallel 10"))) and (.commands.flags[] | select(.name == "url-file")) and (.commands.flags[] | select(.name == "settle" and .default == "2s")) and (.commands.flags[] | select(.name == "selector" and (.usage | contains("generic capture/fallback")) and (.usage | contains("Hacker News")))) and (.commands.flags[] | select(.name == "content-extractor" and .default == "auto" and (.usage | contains("native source profiles")))) and (.commands.flags[] | select(.name == "min-html-chars" and (.usage | contains("quality")) and (.usage | contains("0 to disable"))))' >/dev/null
"$binary" describe --command "workflow verify" --json | jq -e '.ok == true and .commands.name == "verify" and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "workflow perf" --json | jq -e '.ok == true and .commands.name == "perf" and (.commands.examples | any(contains("--trace-max-bytes") and contains("--redact safe"))) and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "trace-max-bytes")) and (.commands.flags[] | select(.name == "redact")) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
lighthouse_help="$("$binary" workflow lighthouse --help)"
grep -Fq 'bounded' <<<"$lighthouse_help"
grep -Fq 'process group' <<<"$lighthouse_help"
grep -Fq 'validated non-empty JSON and HTML' <<<"$lighthouse_help"
"$binary" describe --command "workflow lighthouse" --json | jq -e '.ok == true and .commands.name == "lighthouse" and (.commands.examples | any(contains("--browser-mode headless") and contains("--redact safe"))) and (.commands.flags[] | select(.name == "redact"))' >/dev/null
"$binary" describe --command "workflow debug-bundle" --json | jq -e '.ok == true and .commands.name == "debug-bundle" and (.commands.examples | any(contains("--task-id"))) and (.commands.examples | any(contains("--target-index 2"))) and (.commands.examples | any(contains("--inline-payloads"))) and (.commands.examples | any(contains("--reload=false") and contains("--ignore-cache=false"))) and (.commands.flags[] | select(.name == "redact")) and (.commands.flags[] | select(.name == "target-index" and .type == "int")) and (.commands.flags[] | select(.name == "inline-payloads")) and (.commands.flags[] | select(.name == "reload" and .default == "true")) and (.commands.flags[] | select(.name == "ignore-cache" and .default == "true")) and (.commands.flags[] | select(.name == "run-id")) and (.commands.flags[] | select(.name == "task-id")) and (.commands.flags[] | select(.name == "stage"))' >/dev/null
"$binary" describe --command "workflow action-capture" --json | jq -e '.ok == true and .commands.name == "action-capture" and (.commands.examples | any(contains("--include network,console,dom,text,a11y,screenshot"))) and (.commands.examples | any(contains("--include-bodies json,text") and contains("--body-url-contains"))) and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int")) and (.commands.flags[] | select(.name == "evidence-out-dir" and (.usage | contains("accessibility")) and (.usage | contains("screenshot")) and (.usage | contains("manifest")))) and (.commands.flags[] | select(.name == "include" and (.usage | contains("a11y")) and (.usage | contains("screenshot")))) and (.commands.flags[] | select(.name == "include-bodies" and .default == "none")) and (.commands.flags[] | select(.name == "body-limit")) and (.commands.flags[] | select(.name == "body-url-contains" and (.usage | contains("--include-bodies")))) and (.commands.flags[] | select(.name == "screenshot-full-page")) and (.commands.flags[] | select(.name == "a11y-depth")) and (.commands.flags[] | select(.name == "a11y-limit"))' >/dev/null
"$binary" describe --command "workflow submit-search" --json | jq -e '.ok == true and .commands.name == "submit-search" and (.commands.examples | any(contains("--wait-url-contains"))) and (.commands.examples | any(contains("--suggestion"))) and (.commands.examples | any(contains("--wait-load-state"))) and (.commands.examples | any(contains("--wait-response"))) and (.commands.examples | any(contains("--submit none"))) and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int")) and (.commands.flags[] | select(.name == "input-mode")) and (.commands.flags[] | select(.name == "submit")) and (.commands.flags[] | select(.name == "suggestion")) and (.commands.flags[] | select(.name == "suggestion-by")) and (.commands.flags[] | select(.name == "wait-url-contains")) and (.commands.flags[] | select(.name == "wait-load-state")) and (.commands.flags[] | select(.name == "wait-response")) and (.commands.flags[] | select(.name == "wait-response-match-url")) and (.commands.flags[] | select(.name == "wait-response-status"))' >/dev/null
"$binary" schema screenshot --json | jq -e '.ok == true and .schema.name == "screenshot" and (.schema.fields | map(.name) | index("cleanup")) and (.schema.fields[] | select(.name == "cleanup").description | contains("target_gone"))' >/dev/null
"$binary" schema console --json | jq -e '.ok == true and .schema.name == "console"' >/dev/null
"$binary" schema network --json | jq -e '.ok == true and .schema.name == "network"' >/dev/null
"$binary" schema workflow-action-capture --json | jq -e '.ok == true and .schema.name == "workflow-action-capture" and (.schema.fields | map(.name) | index("target_index"))' >/dev/null
"$binary" schema workflow-console-errors --json | jq -e '.ok == true and .schema.name == "workflow-console-errors" and (.schema.fields | map(.name) | index("target_index"))' >/dev/null
"$binary" schema workflow-network-failures --json | jq -e '.ok == true and .schema.name == "workflow-network-failures" and (.schema.fields | map(.name) | index("target_index"))' >/dev/null
"$binary" schema workflow-submit-search --json | jq -e '.ok == true and .schema.name == "workflow-submit-search" and (.schema.fields | map(.name) | index("target_index"))' >/dev/null
"$binary" schema network-capture --json | jq -e '.ok == true and .schema.name == "network-capture" and (.schema.description | contains("artifact-only manifest")) and (.schema.fields | map(.name) | index("output_mode")) and (.schema.fields | map(.name) | index("capture")) and (.schema.fields | map(.name) | index("capture.artifact_safety")) and (.schema.fields | map(.name) | index("har")) and (.schema.fields | map(.name) | index("body_artifacts")) and (.schema.fields | map(.name) | index("body_artifact_count"))' >/dev/null
"$binary" schema network-websocket --json | jq -e '.ok == true and .schema.name == "network-websocket" and (.schema.description | contains("artifact-only manifest")) and (.schema.fields | map(.name) | index("output_mode"))' >/dev/null
"$binary" schema network-block --json | jq -e '.ok == true and .schema.name == "network-block" and (.schema.fields | map(.name) | index("target_index")) and (.schema.fields[] | select(.name == "target_index" and .type == "integer")) and (.schema.fields | map(.name) | index("matched_count")) and (.schema.fields | map(.name) | index("cleanup"))' >/dev/null
"$binary" schema network-mock --json | jq -e '.ok == true and .schema.name == "network-mock" and (.schema.fields | map(.name) | index("target_index")) and (.schema.fields[] | select(.name == "target_index" and .type == "integer")) and (.schema.fields | map(.name) | index("actions")) and (.schema.fields | map(.name) | index("cleanup"))' >/dev/null
"$binary" schema storage --json | jq -e '.ok == true and .schema.name == "storage" and (.schema.fields | map(.name) | index("storage")) and (.schema.fields | map(.name) | index("target_index"))' >/dev/null
"$binary" schema storage-cache --json | jq -e '.ok == true and .schema.name == "storage-cache" and (.schema.fields | map(.name) | index("storage")) and (.schema.fields | map(.name) | index("target_index"))' >/dev/null
"$binary" schema storage-indexeddb --json | jq -e '.ok == true and .schema.name == "storage-indexeddb" and (.schema.fields | map(.name) | index("storage")) and (.schema.fields | map(.name) | index("target_index"))' >/dev/null
"$binary" schema storage-service-workers --json | jq -e '.ok == true and .schema.name == "storage-service-workers" and (.schema.fields | map(.name) | index("storage")) and (.schema.fields | map(.name) | index("target_index"))' >/dev/null
"$binary" schema storage-snapshot --json | jq -e '.ok == true and .schema.name == "storage-snapshot" and (.schema.fields | map(.name) | index("snapshot")) and (.schema.fields | map(.name) | index("storage.artifact_safety")) and (.schema.fields | map(.name) | index("target_index")) and (.schema.fields[] | select(.name == "snapshot").description | contains("--redact safe"))' >/dev/null
"$binary" schema storage-diff --json | jq -e '.ok == true and .schema.name == "storage-diff" and (.schema.fields | map(.name) | index("diff"))' >/dev/null
"$binary" schema page-select --json | jq -e '.ok == true and .schema.name == "page-select" and (.schema.description | contains("target-index")) and (.schema.fields | map(.name) | index("selected_page"))' >/dev/null
"$binary" schema text --json | jq -e '.ok == true and .schema.name == "text" and (.schema.description | contains("target-index")) and (.schema.fields | map(.name) | index("target_index")) and (.schema.fields | map(.name) | index("attempts")) and (.schema.fields | map(.name) | index("retry_policy"))' >/dev/null
"$binary" schema locator-find --json | jq -e '.ok == true and .schema.name == "locator-find" and (.schema.description | contains("target-index")) and (.schema.fields | map(.name) | index("target_index")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("matches")) and (.schema.fields | map(.name) | index("next_commands"))' >/dev/null
"$binary" schema html --json | jq -e '.ok == true and .schema.name == "html" and (.schema.description | contains("target-index")) and (.schema.fields | map(.name) | index("target_index")) and (.schema.fields | map(.name) | index("diagnostics"))' >/dev/null
"$binary" schema dom-query --json | jq -e '.ok == true and .schema.name == "dom-query" and (.schema.description | contains("target-index")) and (.schema.fields | map(.name) | index("target_index"))' >/dev/null
"$binary" schema css-inspect --json | jq -e '.ok == true and .schema.name == "css-inspect" and (.schema.description | contains("target-index")) and (.schema.fields | map(.name) | index("target_index"))' >/dev/null
"$binary" schema layout-overflow --json | jq -e '.ok == true and .schema.name == "layout-overflow" and (.schema.description | contains("target-index")) and (.schema.fields | map(.name) | index("target_index"))' >/dev/null
"$binary" schema wait --json | jq -e '.ok == true and .schema.name == "wait" and (.schema.description | contains("target-index")) and (.schema.fields | map(.name) | index("target_index")) and (.schema.fields[] | select(.name == "wait").description | contains("URL condition")) and (.schema.fields[] | select(.name == "wait").description | contains("load state")) and (.schema.fields[] | select(.name == "wait").description | contains("eval ready predicate")) and (.schema.fields[] | select(.name == "wait").description | contains("last_value")) and (.schema.fields[] | select(.name == "wait").description | contains("attempt_count")) and (.schema.fields[] | select(.name == "wait").description | contains("bounded evidence")) and (.schema.fields[] | select(.name == "wait").description | contains("observed event counts")) and (.schema.fields[] | select(.name == "wait").description | contains("network-idle")) and (.schema.fields[] | select(.name == "wait").description | contains("dialog")) and (.schema.fields[] | select(.name == "wait").description | contains("file-chooser")) and (.schema.fields[] | select(.name == "wait").description | contains("popup")) and (.schema.fields[] | select(.name == "wait").description | contains("download")) and (.schema.fields | map(.name) | index("artifacts")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("matches")) and (.schema.fields | map(.name) | index("event")) and (.schema.fields | map(.name) | index("dialog")) and (.schema.fields | map(.name) | index("file_chooser")) and (.schema.fields | map(.name) | index("popup")) and (.schema.fields | map(.name) | index("download")) and (.schema.fields | map(.name) | index("last_event")) and (.schema.fields | map(.name) | index("next_commands")) and (.schema.fields | map(.name) | index("retry_policy")) and (.schema.fields | map(.name) | index("attempt_count")) and (.schema.fields | map(.name) | index("last_observed_target"))' >/dev/null
"$binary" schema wait-url --json | jq -e '.ok == true and .schema.name == "wait-url" and (.schema.description | contains("target-index")) and (.schema.fields | map(.name) | index("target_index")) and (.schema.fields | map(.name) | index("wait")) and (.schema.fields[] | select(.name == "wait").description | contains("condition exact or contains")) and (.schema.fields[] | select(.name == "wait").description | contains("final observed URL"))' >/dev/null
"$binary" schema wait-request --json | jq -e '.ok == true and .schema.name == "wait-request" and (.schema.description | contains("target-index")) and (.schema.fields | map(.name) | index("target_index")) and (.schema.fields | map(.name) | index("wait")) and (.schema.fields | map(.name) | index("event")) and (.schema.fields | map(.name) | index("last_event")) and (.schema.fields[] | select(.name == "wait").description | contains("observed_count")) and (.schema.fields[] | select(.name == "wait").description | contains("headers and bodies are omitted"))' >/dev/null
"$binary" schema wait-response --json | jq -e '.ok == true and .schema.name == "wait-response" and (.schema.description | contains("target-index")) and (.schema.fields | map(.name) | index("target_index")) and (.schema.fields | map(.name) | index("wait")) and (.schema.fields | map(.name) | index("event")) and (.schema.fields | map(.name) | index("last_event")) and (.schema.fields[] | select(.name == "wait").description | contains("status")) and (.schema.fields[] | select(.name == "wait").description | contains("headers and bodies are omitted"))' >/dev/null
"$binary" schema wait-network-idle --json | jq -e '.ok == true and .schema.name == "wait-network-idle" and (.schema.description | contains("target-index")) and (.schema.fields | map(.name) | index("target_index")) and (.schema.fields | map(.name) | index("wait")) and (.schema.fields | map(.name) | index("last_event")) and (.schema.fields[] | select(.name == "wait").description | contains("max_inflight")) and (.schema.fields[] | select(.name == "wait").description | contains("in_flight evidence")) and (.schema.fields[] | select(.name == "wait").description | contains("headers and bodies are omitted"))' >/dev/null
"$binary" schema wait-dialog --json | jq -e '.ok == true and .schema.name == "wait-dialog" and (.schema.description | contains("target-index")) and (.schema.fields | map(.name) | index("target_index")) and (.schema.fields | map(.name) | index("wait")) and (.schema.fields | map(.name) | index("dialog")) and (.schema.fields | map(.name) | index("last_event")) and (.schema.fields | map(.name) | index("next_commands")) and (.schema.fields[] | select(.name == "wait").description | contains("handling action")) and (.schema.fields[] | select(.name == "dialog").description | contains("accept/dismiss"))' >/dev/null
"$binary" schema wait-file-chooser --json | jq -e '.ok == true and .schema.name == "wait-file-chooser" and (.schema.description | contains("target-index")) and (.schema.fields | map(.name) | index("target_index")) and (.schema.fields | map(.name) | index("wait")) and (.schema.fields | map(.name) | index("file_chooser")) and (.schema.fields | map(.name) | index("last_event")) and (.schema.fields | map(.name) | index("next_commands")) and (.schema.fields[] | select(.name == "wait").description | contains("interception")) and (.schema.fields[] | select(.name == "file_chooser").description | contains("backend node id"))' >/dev/null
"$binary" schema wait-popup --json | jq -e '.ok == true and .schema.name == "wait-popup" and (.schema.description | contains("target-index")) and (.schema.fields | map(.name) | index("target_index")) and (.schema.fields | map(.name) | index("wait")) and (.schema.fields | map(.name) | index("opener")) and (.schema.fields | map(.name) | index("popup")) and (.schema.fields | map(.name) | index("last_event")) and (.schema.fields | map(.name) | index("next_commands")) and (.schema.fields[] | select(.name == "wait").description | contains("baseline_count")) and (.schema.fields[] | select(.name == "popup").description | contains("opener_id"))' >/dev/null
"$binary" schema wait-download --json | jq -e '.ok == true and .schema.name == "wait-download" and (.schema.description | contains("target-index")) and (.schema.fields | map(.name) | index("target_index")) and (.schema.fields | map(.name) | index("wait")) and (.schema.fields | map(.name) | index("download")) and (.schema.fields | map(.name) | index("event")) and (.schema.fields | map(.name) | index("progress")) and (.schema.fields | map(.name) | index("last_event")) and (.schema.fields | map(.name) | index("next_commands")) and (.schema.fields[] | select(.name == "wait").description | contains("download_dir")) and (.schema.fields[] | select(.name == "download").description | contains("suggested filename"))' >/dev/null
"$binary" schema assert-value --json | jq -e '.ok == true and .schema.name == "assert-value" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields[] | select(.name == "assertion").description | contains("diff")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms"))' >/dev/null
"$binary" schema assert-text --json | jq -e '.ok == true and .schema.name == "assert-text" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields[] | select(.name == "assertion").description | contains("diff")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms"))' >/dev/null
"$binary" schema assert-url --json | jq -e '.ok == true and .schema.name == "assert-url" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields[] | select(.name == "assertion").description | contains("diff")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms")) and (.schema.fields[] | select(.name == "target").description | contains("final observed URL"))' >/dev/null
"$binary" schema assert-title --json | jq -e '.ok == true and .schema.name == "assert-title" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields[] | select(.name == "assertion").description | contains("diff")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms")) and (.schema.fields[] | select(.name == "target").description | contains("final observed URL"))' >/dev/null
"$binary" schema assert-count --json | jq -e '.ok == true and .schema.name == "assert-count" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields[] | select(.name == "assertion").description | contains("diff")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms")) and (.schema.fields[] | select(.name == "locator").description | contains("multiple matches"))' >/dev/null
"$binary" schema assert-attribute --json | jq -e '.ok == true and .schema.name == "assert-attribute" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields[] | select(.name == "assertion").description | contains("diff")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms"))' >/dev/null
"$binary" schema assert-class --json | jq -e '.ok == true and .schema.name == "assert-class" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields[] | select(.name == "assertion").description | contains("class token")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms"))' >/dev/null
"$binary" schema assert-focused --json | jq -e '.ok == true and .schema.name == "assert-focused" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields[] | select(.name == "assertion").description | contains("active element")) and (.schema.fields[] | select(.name == "assertion").description | contains("diff")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms"))' >/dev/null
"$binary" schema assert-css --json | jq -e '.ok == true and .schema.name == "assert-css" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields[] | select(.name == "assertion").description | contains("computed value")) and (.schema.fields[] | select(.name == "assertion").description | contains("diff")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms"))' >/dev/null
"$binary" schema assert-role --json | jq -e '.ok == true and .schema.name == "assert-role" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields[] | select(.name == "assertion").description | contains("actual role")) and (.schema.fields[] | select(.name == "assertion").description | contains("diff")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms"))' >/dev/null
"$binary" schema assert-name --json | jq -e '.ok == true and .schema.name == "assert-name" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields[] | select(.name == "assertion").description | contains("actual accessible name")) and (.schema.fields[] | select(.name == "assertion").description | contains("diff")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms"))' >/dev/null
"$binary" schema assert-aria-snapshot --json | jq -e '.ok == true and .schema.name == "assert-aria-snapshot" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields | map(.name) | index("snapshot")) and (.schema.fields[] | select(.name == "assertion").description | contains("expected_lines")) and (.schema.fields[] | select(.name == "assertion").description | contains("actual_lines")) and (.schema.fields[] | select(.name == "assertion").description | contains("diff")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms")) and (.schema.fields[] | select(.name == "snapshot").description | contains("include_ignored"))' >/dev/null
"$binary" schema assert-attached --json | jq -e '.ok == true and .schema.name == "assert-attached" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields[] | select(.name == "assertion").description | contains("attached")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms"))' >/dev/null
"$binary" schema assert-detached --json | jq -e '.ok == true and .schema.name == "assert-detached" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields[] | select(.name == "assertion").description | contains("detached")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms"))' >/dev/null
"$binary" schema assert-visible --json | jq -e '.ok == true and .schema.name == "assert-visible" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields[] | select(.name == "assertion").description | contains("diff")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms"))' >/dev/null
"$binary" schema assert-hidden --json | jq -e '.ok == true and .schema.name == "assert-hidden" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields[] | select(.name == "assertion").description | contains("diff")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms"))' >/dev/null
"$binary" schema assert-in-viewport --json | jq -e '.ok == true and .schema.name == "assert-in-viewport" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields[] | select(.name == "assertion").description | contains("in-viewport")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms"))' >/dev/null
"$binary" schema assert-enabled --json | jq -e '.ok == true and .schema.name == "assert-enabled" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields[] | select(.name == "assertion").description | contains("diff")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms"))' >/dev/null
"$binary" schema assert-disabled --json | jq -e '.ok == true and .schema.name == "assert-disabled" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields[] | select(.name == "assertion").description | contains("diff")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms"))' >/dev/null
"$binary" schema assert-editable --json | jq -e '.ok == true and .schema.name == "assert-editable" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields[] | select(.name == "assertion").description | contains("diff")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms"))' >/dev/null
"$binary" schema assert-readonly --json | jq -e '.ok == true and .schema.name == "assert-readonly" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields[] | select(.name == "assertion").description | contains("diff")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms"))' >/dev/null
"$binary" schema assert-checked --json | jq -e '.ok == true and .schema.name == "assert-checked" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields[] | select(.name == "assertion").description | contains("diff")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms"))' >/dev/null
"$binary" schema assert-unchecked --json | jq -e '.ok == true and .schema.name == "assert-unchecked" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields[] | select(.name == "assertion").description | contains("diff")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms"))' >/dev/null
"$binary" schema assert-indeterminate --json | jq -e '.ok == true and .schema.name == "assert-indeterminate" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields[] | select(.name == "assertion").description | contains("indeterminate")) and (.schema.fields[] | select(.name == "assertion").description | contains("diff")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms"))' >/dev/null
for assertion_schema in assert-value assert-text assert-url assert-title assert-count assert-attribute assert-class assert-focused assert-css assert-role assert-name assert-aria-snapshot assert-attached assert-detached assert-visible assert-hidden assert-in-viewport assert-enabled assert-disabled assert-editable assert-readonly assert-checked assert-unchecked assert-indeterminate; do
  "$binary" schema "$assertion_schema" --json | jq -e '.ok == true and (.schema.description | contains("target-index")) and (.schema.fields[] | select(.name == "target_index" and .type == "integer"))' >/dev/null
done
"$binary" schema workflow-hacker-news --json | jq -e '.ok == true and .schema.name == "workflow-hacker-news" and (.schema.fields | map(.name) | index("organization"))' >/dev/null
"$binary" schema workflow-reddit-posts --json | jq -e '.ok == true and .schema.name == "workflow-reddit-posts" and (.schema.fields | map(.name) | index("request")) and (.schema.fields | map(.name) | index("threads")) and (.schema.fields[] | select(.name == "threads").description | contains("same-subreddit") and contains("t3")) and (.schema.fields[] | select(.name == "next_cursor").description | contains("never proves"))' >/dev/null
"$binary" schema workflow-reddit-collect --json | jq -e '.ok == true and .schema.name == "workflow-reddit-collect" and (.schema.fields | map(.name) | index("kind")) and (.schema.fields | map(.name) | index("records")) and (.schema.fields[] | select(.name == "records").description | contains("root-thread") and contains("author membership")) and (.schema.fields[] | select(.name == "workflow").description | contains("hard 500"))' >/dev/null
"$binary" schema workflow-x-collect --json | jq -e '.ok == true and .schema.name == "workflow-x-collect" and (.schema.fields | map(.name) | index("kind")) and (.schema.fields | map(.name) | index("records")) and (.schema.fields | map(.name) | index("coverage")) and (.schema.fields[] | select(.name == "records").description | contains("canonical status") and contains("root status")) and (.schema.fields[] | select(.name == "coverage").description | contains("termination evidence")) and (.schema.fields[] | select(.name == "workflow").description | contains("hard 500"))' >/dev/null
"$binary" schema workflow-linkedin-collect --json | jq -e '.ok == true and .schema.name == "workflow-linkedin-collect" and (.schema.fields | map(.name) | index("kind")) and (.schema.fields | map(.name) | index("records")) and (.schema.fields | map(.name) | index("coverage")) and (.schema.fields[] | select(.name == "records").description | contains("activity linkage") and contains("discovery surface")) and (.schema.fields[] | select(.name == "coverage").description | contains("termination evidence")) and (.schema.fields[] | select(.name == "workflow").description | contains("hard 500"))' >/dev/null
"$binary" schema workflow-hacker-news-collect --json | jq -e '.ok == true and .schema.name == "workflow-hacker-news-collect" and (.schema.fields | map(.name) | index("kind")) and (.schema.fields | map(.name) | index("records")) and (.schema.fields | map(.name) | index("coverage")) and (.schema.fields[] | select(.name == "records").description | contains("story") and contains("comment")) and (.schema.fields[] | select(.name == "coverage").description | contains("termination evidence")) and (.schema.fields[] | select(.name == "workflow").description | contains("hard 500"))' >/dev/null
"$binary" schema workflow-arxiv-collect --json | jq -e '.ok == true and .schema.name == "workflow-arxiv-collect" and (.schema.fields | map(.name) | index("paper")) and (.schema.fields | map(.name) | index("references")) and (.schema.fields | map(.name) | index("coverage")) and (.schema.fields[] | select(.name == "paper").description | contains("version-pinned")) and (.schema.fields[] | select(.name == "coverage").description | contains("termination evidence")) and (.schema.fields[] | select(.name == "workflow").description | contains("hard 500"))' >/dev/null
"$binary" schema workflow-pdf-to-markdown --json | jq -e '.ok == true and .schema.name == "workflow-pdf-to-markdown" and (.schema.description | contains("bounded diagnostics")) and (.schema.fields[] | select(.name == "source" and .type == "pdf_source").description | contains("SHA-256")) and (.schema.fields[] | select(.name == "extraction" and .type == "pdf_text_extraction").description | contains("pdftotext") and contains("never OCR") and contains("process-group cancellation")) and (.schema.fields[] | select(.name == "coverage" and .type == "pdf_text_coverage").description | contains("threshold")) and (.schema.fields | map(.name) | index("pages")) and (.schema.fields | map(.name) | index("artifacts"))' >/dev/null
"$binary" explain-error pdf_text_extraction_canceled --json | jq -e '.ok == true and .error.code == "pdf_text_extraction_canceled" and .error.exit_code == 5' >/dev/null
"$binary" explain-error pdf_text_extraction_failed --json | jq -e '.ok == true and .error.code == "pdf_text_extraction_failed" and .error.exit_code == 1' >/dev/null
"$binary" explain-error pdf_text_output_too_large --json | jq -e '.ok == true and .error.code == "pdf_text_output_too_large" and .error.exit_code == 1' >/dev/null
"$binary" schema workflow-google-translate --json | jq -e '.ok == true and .schema.name == "workflow-google-translate" and (.schema.fields | map(.name) | index("input")) and (.schema.fields | map(.name) | index("chunks")) and (.schema.fields | map(.name) | index("pages")) and (.schema.fields[] | select(.name == "cleanup" and .type == "google_translate_cleanup" and (.description | contains("newly discovered")) and (.description | contains("recovery command")))) and (.schema.fields[] | select(.name == "mode").description | contains("image-only scans") and contains("Poppler")) and (.schema.fields[] | select(.name == "pages").description | contains("regular non-empty") and contains("source pages"))' >/dev/null
"$binary" schema google-translate-cleanup --json | jq -e '.ok == true and .schema.name == "google-translate-cleanup" and (.schema.fields | map(.name) | index("target_ids")) and (.schema.fields | map(.name) | index("reports")) and (.schema.fields | map(.name) | index("recovery_command"))' >/dev/null
"$binary" schema workflow-reddit-collect --json | jq -e '.ok == true and .schema.name == "workflow-reddit-collect" and (.schema.fields | map(.name) | index("coverage")) and (.schema.fields[] | select(.name == "coverage").description | contains("termination evidence"))' >/dev/null
"$binary" schema source-collection-coverage --json | jq -e '.ok == true and .schema.name == "source-collection-coverage" and (.schema.fields | map(.name) | index("observed_record_kinds")) and (.schema.fields | map(.name) | index("possibly_missing_record_kinds")) and (.schema.fields | map(.name) | index("continuation")) and (.schema.fields | map(.name) | index("unresolved_controls")) and (.schema.fields | map(.name) | index("termination_evidence"))' >/dev/null
"$binary" schema workflow-google-maps-directions --json | jq -e '.ok == true and .schema.name == "workflow-google-maps-directions" and (.schema.fields | map(.name) | index("trust")) and (.schema.fields | map(.name) | index("cleanup"))' >/dev/null
"$binary" schema workflow-console-errors --json | jq -e '.ok == true and .schema.name == "workflow-console-errors"' >/dev/null
"$binary" schema workflow-network-failures --json | jq -e '.ok == true and .schema.name == "workflow-network-failures"' >/dev/null
"$binary" schema workflow-a11y --json | jq -e '.ok == true and .schema.name == "workflow-a11y" and (.schema.fields | map(.name) | index("a11y")) and (.schema.fields[] | select(.name == "target_index" and .type == "integer"))' >/dev/null
"$binary" schema workflow-page-load --json | jq -e '.ok == true and .schema.name == "workflow-page-load" and (.schema.fields | map(.name) | index("target_index")) and (.schema.fields | map(.name) | index("content_state")) and (.schema.fields | map(.name) | index("storage"))' >/dev/null
"$binary" schema workflow-rendered-extract --json | jq -e '.ok == true and .schema.name == "workflow-rendered-extract" and (.schema.fields | map(.name) | index("content")) and (.schema.fields | map(.name) | index("quality")) and (.schema.fields | map(.name) | index("artifacts")) and (.schema.fields[] | select(.name == "target_index" and .type == "integer"))' >/dev/null
"$binary" schema rendered-extract-content --json | jq -e '.ok == true and .schema.name == "rendered-extract-content" and (.schema.fields | map(.name) | index("profile")) and (.schema.fields[] | select(.name == "profile").description | contains("x") and contains("linkedin") and contains("reddit")) and (.schema.fields | map(.name) | index("planned_strategy")) and (.schema.fields | map(.name) | index("strategy")) and (.schema.fields | map(.name) | index("planned_representation")) and (.schema.fields | map(.name) | index("representation_rewritten")) and (.schema.fields | map(.name) | index("native_succeeded")) and (.schema.fields | map(.name) | index("fallback_reason")) and (.schema.fields | map(.name) | index("representations"))' >/dev/null
"$binary" schema rendered-extract-readiness --json | jq -e '.ok == true and .schema.name == "rendered-extract-readiness" and (.schema.fields[] | select(.name == "outcome").description | contains("wait_expired")) and (.schema.fields | map(.name) | index("thresholds_met")) and (.schema.fields | map(.name) | index("capture_consistent")) and (.schema.fields[] | select(.name == "network_idle_seen").description | contains("Always false"))' >/dev/null
"$binary" schema rendered-extract-quality --json | jq -e '.ok == true and .schema.name == "rendered-extract-quality" and (.schema.fields | map(.name) | index("passed")) and (.schema.fields[] | select(.name == "thresholds").description | contains("zero disables"))' >/dev/null
"$binary" schema workflow-rendered-extract-cleanup --json | jq -e '.ok == true and .schema.name == "workflow-rendered-extract-cleanup" and (.schema.fields | map(.name) | index("target_id")) and (.schema.fields | map(.name) | index("recovery_command")) and (.schema.fields | map(.name) | index("error"))' >/dev/null
"$binary" schema workflow-web-research-serp --json | jq -e '.ok == true and .schema.name == "workflow-web-research-serp" and (.schema.fields[] | select(.name == "queries" and .type == "array<web_research_query>").description | contains("query<TAB>") and contains("applied only to Google") and contains("cdr:1,cd_min:07/01/2026,cd_max:07/01/2026")) and (.schema.fields | map(.name) | index("candidates")) and (.schema.fields | map(.name) | index("query_coverage")) and (.schema.fields | map(.name) | index("warnings")) and (.schema.fields | map(.name) | index("failures")) and (.schema.fields | map(.name) | index("artifacts")) and (.schema.fields[] | select(.name == "serps").description | contains("google_ai")) and (.schema.fields[] | select(.name == "workflow").description | contains("policy source") and contains("exclusive-mode") and contains("agents.google.exclusive_ai_mode") and contains("reusable engine lanes"))' >/dev/null
"$binary" schema web-research-google-ai-response --json | jq -e '.ok == true and .schema.name == "web-research-google-ai-response" and (.schema.fields | map(.name) | index("status")) and (.schema.fields | map(.name) | index("text")) and (.schema.fields | map(.name) | index("sources")) and (.schema.fields | map(.name) | index("expanded")) and (.schema.fields | map(.name) | index("artifacts")) and (.schema.fields[] | select(.name == "status").description | contains("not_present") and contains("unavailable")) and (.schema.fields[] | select(.name == "text").description | contains("text_truncated"))' >/dev/null
"$binary" schema web-research-query --json | jq -e '.ok == true and .schema.name == "web-research-query" and (.schema.fields[] | select(.name == "query" and .type == "string" and .required == true).description | contains("non-empty")) and (.schema.fields[] | select(.name == "time_filter" and .type == "string" and .required == false).description | contains("Google tbs") and contains("ignored by other engines") and contains("cdr:1,cd_min:07/01/2026,cd_max:07/01/2026"))' >/dev/null
"$binary" schema web-research-query-coverage --json | jq -e '.ok == true and .schema.name == "web-research-query-coverage" and (.schema.fields[] | select(.name == "produced_candidates").description | contains("before global")) and (.schema.fields[] | select(.name == "duplicate_candidates").description | contains("earlier query")) and (.schema.fields[] | select(.name == "omitted_by_cap").description | contains("global candidate cap"))' >/dev/null
"$binary" schema workflow-web-research-extract --json | jq -e '.ok == true and .schema.name == "workflow-web-research-extract" and (.schema.fields | map(.name) | index("quality")) and (.schema.fields | map(.name) | index("failures")) and (.schema.fields[] | select(.name == "workflow").description | contains("backpressure"))' >/dev/null
"$binary" schema workflow-verify --json | jq -e '.ok == true and .schema.name == "workflow-verify" and (.schema.fields | map(.name) | index("requests")) and (.schema.fields[] | select(.name == "target_index" and .type == "integer"))' >/dev/null
"$binary" schema workflow-perf --json | jq -e '.ok == true and .schema.name == "workflow-perf" and (.schema.fields | map(.name) | index("performance")) and (.schema.fields | map(.name) | index("trace")) and (.schema.fields | map(.name) | index("insights")) and (.schema.fields[] | select(.name == "trace").description | contains("artifact-safety")) and (.schema.fields[] | select(.name == "target_index" and .type == "integer"))' >/dev/null
"$binary" schema workflow-lighthouse --json | jq -e '.ok == true and .schema.name == "workflow-lighthouse" and (.schema.description | contains("bounded") and contains("validated") and contains("cancellation")) and (.schema.fields | map(.name) | index("artifact_list")) and (.schema.fields | map(.name) | index("artifact_safety")) and (.schema.fields[] | select(.name == "ok").description | contains("regular non-empty") and contains("JSON") and contains("HTML")) and (.schema.fields[] | select(.name == "artifact_list").description | contains("Validated") and contains("byte counts"))' >/dev/null
"$binary" schema workflow-debug-bundle --json | jq -e '.ok == true and .schema.name == "workflow-debug-bundle" and (.schema.fields | map(.name) | index("bundle")) and (.schema.fields | map(.name) | index("artifacts")) and (.schema.fields | map(.name) | index("artifact_list")) and (.schema.fields[] | select(.name == "target_index" and .type == "integer")) and (.schema.fields[] | select(.name == "bundle").description | contains("public-safe")) and (.schema.fields[] | select(.name == "requests").description | contains("--inline-payloads")) and (.schema.fields[] | select(.name == "artifacts").description | contains("local_only"))' >/dev/null
"$binary" schema workflow-action-capture --json | jq -e '.ok == true and .schema.name == "workflow-action-capture" and (.schema.fields | map(.name) | index("evidence")) and (.schema.fields | map(.name) | index("local_capture_warning")) and (.schema.fields[] | select(.name == "workflow").description | contains("URL substring filter")) and (.schema.fields[] | select(.name == "requests").description | contains("--include-bodies")) and (.schema.fields[] | select(.name == "evidence").description | contains("--evidence-out-dir")) and (.schema.fields[] | select(.name == "evidence").description | contains("accessibility")) and (.schema.fields[] | select(.name == "evidence").description | contains("screenshot")) and (.schema.fields[] | select(.name == "evidence").description | contains("action-window event")) and (.schema.fields[] | select(.name == "evidence").description | contains("manifest")) and (.schema.fields[] | select(.name == "artifacts").description | contains("screenshot evidence")) and (.schema.fields[] | select(.name == "artifacts").description | contains("manifest"))' >/dev/null
"$binary" schema workflow-submit-search --json | jq -e '.ok == true and .schema.name == "workflow-submit-search" and (.schema.fields | map(.name) | index("fill")) and (.schema.fields | map(.name) | index("press")) and (.schema.fields | map(.name) | index("suggestion")) and (.schema.fields | map(.name) | index("suggestion_click")) and (.schema.fields | map(.name) | index("verification")) and (.schema.fields | map(.name) | index("response_wait")) and (.schema.fields | map(.name) | index("response")) and (.schema.fields | map(.name) | index("before_target")) and (.schema.fields | map(.name) | index("final_target")) and (.schema.fields[] | select(.name == "workflow").description | contains("suggestion_requested")) and (.schema.fields[] | select(.name == "suggestion").description | contains("strictness")) and (.schema.fields[] | select(.name == "verification").description | contains("--wait-load-state")) and (.schema.fields[] | select(.name == "response_wait").description | contains("without headers or bodies"))' >/dev/null
"$binary" schema click --json | jq -e '.ok == true and .schema.name == "click" and (.schema.fields | map(.name) | index("click")) and (.schema.fields | map(.name) | index("target_index")) and (.schema.fields | map(.name) | index("actionability")) and (.schema.fields | map(.name) | index("auto_scroll")) and (.schema.fields | map(.name) | index("before_target")) and (.schema.fields | map(.name) | index("after_target")) and (.schema.fields | map(.name) | index("final_target")) and (.schema.fields | map(.name) | index("page_state")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields | map(.name) | index("popup_wait")) and (.schema.fields | map(.name) | index("popup")) and (.schema.fields | map(.name) | index("download_wait")) and (.schema.fields | map(.name) | index("download")) and (.schema.fields | map(.name) | index("download_event")) and (.schema.fields | map(.name) | index("dialog_wait")) and (.schema.fields | map(.name) | index("dialog")) and (.schema.fields | map(.name) | index("file_chooser_wait")) and (.schema.fields | map(.name) | index("file_chooser")) and (.schema.fields | map(.name) | index("request_wait")) and (.schema.fields | map(.name) | index("request")) and (.schema.fields | map(.name) | index("response_wait")) and (.schema.fields | map(.name) | index("response")) and (.schema.fields | map(.name) | index("next_commands"))' >/dev/null
"$binary" schema fill --json | jq -e '.ok == true and .schema.name == "fill" and (.schema.fields | map(.name) | index("fill")) and (.schema.fields | map(.name) | index("actionability")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields | map(.name) | index("verification")) and (.schema.fields[] | select(.name == "verification").description | contains("--wait-url-contains"))' >/dev/null
"$binary" schema select --json | jq -e '.ok == true and .schema.name == "select" and (.schema.fields | map(.name) | index("select")) and (.schema.fields | map(.name) | index("actionability")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields | map(.name) | index("verification"))' >/dev/null
"$binary" schema check --json | jq -e '.ok == true and .schema.name == "check" and (.schema.fields | map(.name) | index("check")) and (.schema.fields | map(.name) | index("actionability")) and (.schema.fields | map(.name) | index("auto_scroll")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector"))' >/dev/null
"$binary" schema uncheck --json | jq -e '.ok == true and .schema.name == "uncheck" and (.schema.fields | map(.name) | index("uncheck")) and (.schema.fields | map(.name) | index("actionability")) and (.schema.fields | map(.name) | index("auto_scroll")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector"))' >/dev/null
"$binary" schema type --json | jq -e '.ok == true and .schema.name == "type" and (.schema.fields | map(.name) | index("type")) and (.schema.fields | map(.name) | index("actionability")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields | map(.name) | index("verification")) and (.schema.fields[] | select(.name == "verification").description | contains("--wait-url-contains"))' >/dev/null
"$binary" schema press --json | jq -e '.ok == true and .schema.name == "press" and (.schema.fields | map(.name) | index("press")) and (.schema.fields | map(.name) | index("actionability")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("verification")) and (.schema.fields[] | select(.name == "verification").description | contains("--wait-url-contains"))' >/dev/null
"$binary" schema hover --json | jq -e '.ok == true and .schema.name == "hover" and (.schema.description | contains("indexed page")) and (.schema.fields | map(.name) | index("hover")) and (.schema.fields | map(.name) | index("target_index")) and (.schema.fields | map(.name) | index("actionability")) and (.schema.fields | map(.name) | index("auto_scroll")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector"))' >/dev/null
"$binary" schema drag --json | jq -e '.ok == true and .schema.name == "drag" and (.schema.description | contains("indexed page")) and (.schema.fields | map(.name) | index("drag")) and (.schema.fields | map(.name) | index("target_index")) and (.schema.fields | map(.name) | index("actionability")) and (.schema.fields | map(.name) | index("auto_scroll")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector"))' >/dev/null
"$binary" schema frames --json | jq -e '.ok == true and .schema.name == "frames" and (.schema.description | contains("target-index")) and (.schema.fields | map(.name) | index("target_index")) and (.schema.fields | map(.name) | index("frames"))' >/dev/null

"$binary" describe --command "a11y tree" --json | jq -e '.ok == true and .commands.name == "tree" and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "depth")) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "a11y find" --json | jq -e '.ok == true and .commands.name == "find" and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "a11y node" --json | jq -e '.ok == true and .commands.name == "node" and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "a11y snapshot" --json | jq -e '.ok == true and .commands.name == "snapshot" and (.commands.examples | any(contains("--selector body"))) and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "selector")) and (.commands.flags[] | select(.name == "depth")) and (.commands.flags[] | select(.name == "limit")) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "emulate viewport" --json | jq -e '.ok == true and .commands.name == "viewport" and (.commands.examples | any(contains("--preset")))' >/dev/null
"$binary" describe --command "emulate user-agent" --json | jq -e '.ok == true and .commands.name == "user-agent" and (.commands.examples | any(contains("--user-agent")))' >/dev/null
"$binary" describe --command "emulate geolocation" --json | jq -e '.ok == true and .commands.name == "geolocation" and (.commands.examples | any(contains("--latitude")))' >/dev/null
"$binary" describe --command "emulate timezone" --json | jq -e '.ok == true and .commands.name == "timezone" and (.commands.examples | any(contains("--timezone-id"))) and (.commands.flags[] | select(.name == "timezone-id"))' >/dev/null
"$binary" describe --command "emulate locale" --json | jq -e '.ok == true and .commands.name == "locale" and (.commands.examples | any(contains("--locale"))) and (.commands.flags[] | select(.name == "locale"))' >/dev/null
"$binary" describe --command "emulate color-scheme" --json | jq -e '.ok == true and .commands.name == "color-scheme" and (.commands.examples | any(contains("--scheme"))) and (.commands.flags[] | select(.name == "scheme"))' >/dev/null
"$binary" schema emulate-timezone --json | jq -e '.ok == true and .schema.name == "emulate-timezone" and (.schema.fields | map(.name) | index("emulation"))' >/dev/null
"$binary" schema emulate-locale --json | jq -e '.ok == true and .schema.name == "emulate-locale" and (.schema.fields | map(.name) | index("emulation"))' >/dev/null
"$binary" schema emulate-color-scheme --json | jq -e '.ok == true and .schema.name == "emulate-color-scheme" and (.schema.fields | map(.name) | index("emulation"))' >/dev/null
for emulation_command in viewport clear media color-scheme user-agent geolocation timezone locale cpu network; do
  "$binary" describe --command "emulate $emulation_command" --json | jq -e '.ok == true and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
  "$binary" schema "emulate-$emulation_command" --json | jq -e '.ok == true and (.schema.fields | map(.name) | index("target_index")) and (.schema.fields[] | select(.name == "target_index" and .type == "integer"))' >/dev/null
done
"$binary" describe --command "permissions grant" --json | jq -e '.ok == true and .commands.name == "grant" and (.commands.examples | any(contains("--origin"))) and (.commands.flags[] | select(.name == "origin"))' >/dev/null
"$binary" describe --command "permissions set" --json | jq -e '.ok == true and .commands.name == "set" and (.commands.examples | any(contains("--setting"))) and (.commands.flags[] | select(.name == "setting")) and (.commands.flags[] | select(.name == "origin"))' >/dev/null
"$binary" describe --command "permissions reset" --json | jq -e '.ok == true and .commands.name == "reset" and (.commands.examples | any(contains("permissions reset")))' >/dev/null
"$binary" schema permissions --json | jq -e '.ok == true and .schema.name == "permissions" and (.schema.fields | map(.name) | index("permissions")) and (.schema.fields | map(.name) | index("next_commands"))' >/dev/null
"$binary" describe --command "emulate cpu" --json | jq -e '.ok == true and .commands.name == "cpu" and (.commands.examples | any(contains("--rate")))' >/dev/null
"$binary" describe --command "emulate network" --json | jq -e '.ok == true and .commands.name == "network" and (.commands.examples | any(contains("--preset slow-3g"))) and (.commands.flags[] | select(.name == "download-kbps"))' >/dev/null
"$binary" describe --command "dialog accept" --json | jq -e '.ok == true and .commands.name == "accept" and (.commands.examples | any(contains("--wait --type prompt"))) and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "prompt-text")) and (.commands.flags[] | select(.name == "wait" and .type == "bool")) and (.commands.flags[] | select(.name == "type")) and (.commands.flags[] | select(.name == "message")) and (.commands.flags[] | select(.name == "message-contains")) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "dialog dismiss" --json | jq -e '.ok == true and .commands.name == "dismiss" and (.commands.examples | any(contains("--wait --type confirm"))) and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "wait" and .type == "bool")) and (.commands.flags[] | select(.name == "type")) and (.commands.flags[] | select(.name == "message")) and (.commands.flags[] | select(.name == "message-contains")) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" schema dialog --json | jq -e '.ok == true and .schema.name == "dialog" and (.schema.description | contains("same attached session")) and (.schema.fields | map(.name) | index("target_index")) and (.schema.fields | map(.name) | index("wait")) and (.schema.fields | map(.name) | index("dialog")) and (.schema.fields | map(.name) | index("last_event")) and (.schema.fields | map(.name) | index("next_commands"))' >/dev/null
for dialog_action in accept dismiss; do
  set +e
  dialog_index_output="$("$binary" dialog "$dialog_action" --target-index 0 --state-dir "$state_dir" --json)"
  dialog_index_code=$?
  set -e
  test "$dialog_index_code" -eq 2
  jq -e '.ok == false and .code == "invalid_target_index"' <<<"$dialog_index_output" >/dev/null
done
"$binary" describe --command "events tap" --json | jq -e '.ok == true and .commands.name == "tap" and (.commands.examples | any(contains("--target-index"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int")) and (.commands.flags[] | select(.name == "max-events")) and (.commands.flags[] | select(.name == "ready-file"))' >/dev/null
"$binary" describe --command "screenshot" --json | jq -e '.ok == true and .commands.name == "screenshot" and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "console" --json | jq -e '.ok == true and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int")) and (.commands.flags[] | select(.name == "ready-file"))' >/dev/null
"$binary" describe --command "network" --json | jq -e '.ok == true and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int")) and (.commands.flags[] | select(.name == "ready-file"))' >/dev/null
"$binary" describe --command "network capture" --json | jq -e '.ok == true and (.commands.examples | any(contains("--target-index 2"))) and (.commands.examples | any(contains("--out"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "network websocket" --json | jq -e '.ok == true and (.commands.examples | any(contains("--target-index 2"))) and (.commands.examples | any(contains("--out"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
network_capture_help="$("$binary" network capture --help)"
grep -Fq 'privacy-safe artifact-only manifest' <<<"$network_capture_help"
grep -Fq 'Without --out' <<<"$network_capture_help"
network_websocket_help="$("$binary" network websocket --help)"
grep -Fq 'privacy-safe artifact-only manifest' <<<"$network_websocket_help"
"$binary" describe --command "protocol compat" --json | jq -e '.ok == true and .commands.name == "compat" and (.commands.examples | any(contains("--requires")))' >/dev/null
"$binary" describe --command "memory heap-snapshot" --json | jq -e '.ok == true and .commands.name == "heap-snapshot" and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int")) and (.commands.flags[] | select(.name == "out"))' >/dev/null
"$binary" describe --command "memory counters" --json | jq -e '.ok == true and .commands.name == "counters" and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int"))' >/dev/null
"$binary" describe --command "perf summary" --json | jq -e '.ok == true and .commands.name == "summary" and (.commands.examples | any(contains("--target-index 2"))) and (.commands.flags[] | select(.name == "target-index" and .type == "int")) and (.commands.flags[] | select(.name == "duration"))' >/dev/null
"$binary" describe --command "workflow feeds" --json | jq -e '.ok == true and .commands.name == "feeds" and (.commands.examples | any(contains("--wait-load"))) and (.commands.flags[] | select(.name == "keep-open" and (.usage | contains("bounded cleanup")) and (.usage | contains("recovery evidence"))))' >/dev/null
"$binary" describe --command "workflow responsive-audit" --json | jq -e '.ok == true and .commands.name == "responsive-audit"' >/dev/null
"$binary" schema protocol-compat --json | jq -e '.ok == true and .schema.name == "protocol-compat"' >/dev/null
"$binary" schema a11y --json | jq -e '.ok == true and .schema.name == "a11y" and (.schema.description | contains("target-index")) and (.schema.fields | map(.name) | index("target_index"))' >/dev/null
"$binary" schema a11y-snapshot --json | jq -e '.ok == true and .schema.name == "a11y-snapshot" and (.schema.description | contains("target-index")) and (.schema.fields | map(.name) | index("target_index")) and (.schema.fields | map(.name) | index("snapshot")) and (.schema.fields | map(.name) | index("lines")) and (.schema.fields | map(.name) | index("text")) and (.schema.fields[] | select(.name == "snapshot").description | contains("include_ignored"))' >/dev/null
"$binary" schema perf-summary --json | jq -e '.ok == true and .schema.name == "perf-summary" and (.schema.description | contains("target-index")) and (.schema.fields | map(.name) | index("target")) and (.schema.fields | map(.name) | index("target_index")) and (.schema.fields | map(.name) | index("metrics"))' >/dev/null
"$binary" schema memory --json | jq -e '.ok == true and .schema.name == "memory" and (.schema.description | contains("target-index")) and (.schema.fields | map(.name) | index("target")) and (.schema.fields | map(.name) | index("target_index")) and (.schema.fields | map(.name) | index("artifact"))' >/dev/null
"$binary" schema screenshot --json | jq -e '.ok == true and .schema.name == "screenshot" and (.schema.description | contains("target-index")) and (.schema.fields | map(.name) | index("target")) and (.schema.fields | map(.name) | index("target_index")) and (.schema.fields | map(.name) | index("screenshot"))' >/dev/null
"$binary" schema console --json | jq -e '.ok == true and .schema.name == "console" and (.schema.description | contains("target-index")) and (.schema.fields | map(.name) | index("target")) and (.schema.fields | map(.name) | index("target_index")) and (.schema.fields | map(.name) | index("messages"))' >/dev/null
"$binary" schema network --json | jq -e '.ok == true and .schema.name == "network" and (.schema.description | contains("target-index")) and (.schema.fields | map(.name) | index("target")) and (.schema.fields | map(.name) | index("target_index")) and (.schema.fields | map(.name) | index("requests"))' >/dev/null
"$binary" schema network-capture --json | jq -e '.ok == true and .schema.name == "network-capture" and (.schema.description | contains("target-index")) and (.schema.fields | map(.name) | index("target")) and (.schema.fields | map(.name) | index("target_index")) and (.schema.fields | map(.name) | index("capture.artifact_safety"))' >/dev/null
"$binary" schema network-websocket --json | jq -e '.ok == true and .schema.name == "network-websocket" and (.schema.description | contains("target-index")) and (.schema.fields | map(.name) | index("target")) and (.schema.fields | map(.name) | index("target_index")) and (.schema.fields | map(.name) | index("websockets"))' >/dev/null
"$binary" schema workflow-feeds --json | jq -e '.ok == true and .schema.name == "workflow-feeds"' >/dev/null
"$binary" schema workflow-responsive-audit --json | jq -e '.ok == true and .schema.name == "workflow-responsive-audit" and (.schema.description | contains("target-index")) and (.schema.fields | map(.name) | index("target")) and (.schema.fields | map(.name) | index("target_index")) and (.schema.fields | map(.name) | index("cleanup")) and (.schema.fields[] | select(.name == "cleanup" and .type == "workflow_page_cleanup" and (.description | contains("target_gone")))) and (.schema.fields | map(.name) | index("workflow")) and (.schema.fields | map(.name) | index("results")) and (.schema.fields | map(.name) | index("artifacts"))' >/dev/null

mkdir -p "$state_dir/user-data"
set +e
daemon_start_output="$("$binary" daemon start --autoConnect --user-data-dir "$state_dir/user-data" --state-dir "$state_dir" --json 2>/tmp/cdp-cli-daemon-start.err)"
daemon_start_code=$?
set -e
if [[ "$daemon_start_code" -ne 4 ]]; then
  echo "daemon start exit code = $daemon_start_code, want 4 while auto-connect permission is pending" >&2
  exit 1
fi
printf '%s\n' "$daemon_start_output" | jq -e '.ok == false and .code == "permission_pending" and .human_required == true and .agent_should_stop == true and (.remediation_commands | index("open chrome://inspect/#remote-debugging")) and (.safe_diagnostics | index("cdp daemon status --json"))' >/dev/null
"$binary" connection current --state-dir "$state_dir" --json | jq -e '.ok == true and .browser_mode == "headed" and .connection.name == "default" and .connection.mode == "auto_connect" and .effective_connection.name == "default" and .connection_matches_effective == true' >/dev/null

set +e
daemon_restart_output="$("$binary" daemon restart --debug --autoConnect --active-browser-probe --user-data-dir "$state_dir/user-data" --state-dir "$state_dir" --json 2>/tmp/cdp-cli-daemon-restart.err)"
daemon_restart_code=$?
set -e
if [[ "$daemon_restart_code" -ne 4 ]]; then
  echo "daemon restart exit code = $daemon_restart_code, want 4 while auto-connect permission is pending" >&2
  exit 1
fi
printf '%s\n' "$daemon_restart_output" | jq -e '.ok == false and .code == "permission_pending" and .human_required == true and .agent_should_stop == true and (.remediation_commands | index("open chrome://inspect/#remote-debugging")) and (.safe_diagnostics | index("cdp daemon status --json"))' >/dev/null

"$binary" browser mode get --state-dir "$state_dir" --json | jq -e '.ok == true and .browser_mode == "headed" and .browser_mode_source == "default" and (.next_commands | length > 0)' >/dev/null
CDP_BROWSER_MODE=headless "$binary" browser mode get --state-dir "$state_dir" --json | jq -e '.ok == true and .browser_mode == "headless" and .browser_mode_source == "env" and (.next_commands | length > 0)' >/dev/null
"$binary" --state-dir "$state_dir" browser profile status --json | jq -e '.ok == true and .browser_mode == "headless" and .state == "missing" and .seeded == false and (.next_commands | index("cdp browser profile seed --strategy managed --json"))' >/dev/null
"$binary" --state-dir "$state_dir" browser profile seed --strategy managed --json | jq -e '.ok == true and .seeded == true and .exists == true and .seed_strategy == "managed" and .managed_browser.browser_mode == "headless" and (.managed_browser | has("ownership_token") | not) and (.managed_browser | has("process_start_time") | not)' >/dev/null
profile_copy_home="$state_dir/profile-copy-home"
profile_copy_config_dir="$profile_copy_home/xdg-config"
profile_copy_source="$profile_copy_config_dir/google-chrome"
if [[ "$(uname -s)" == "Darwin" ]]; then
  profile_copy_source="$profile_copy_home/Library/Application Support/Google/Chrome"
fi
mkdir -p "$profile_copy_source/Default/Local Storage/leveldb"
mkdir -p "$profile_copy_source/Default/IndexedDB/https_example_0.indexeddb.leveldb"
mkdir -p "$profile_copy_source/Default/Extensions/abcdefghijklmnop/1.0.0"
mkdir -p "$profile_copy_source/Default/Cache/Cache_Data"
printf 'local-state' > "$profile_copy_source/Local State"
printf 'cookie-db' > "$profile_copy_source/Default/Cookies"
printf 'local-storage-token' > "$profile_copy_source/Default/Local Storage/leveldb/token.log"
printf 'indexeddb-state' > "$profile_copy_source/Default/IndexedDB/https_example_0.indexeddb.leveldb/000003.log"
printf '{"name":"synthetic-extension"}' > "$profile_copy_source/Default/Extensions/abcdefghijklmnop/1.0.0/manifest.json"
printf 'cache-bytes' > "$profile_copy_source/Default/Cache/Cache_Data/f_000001"
printf 'runtime-artifact' > "$profile_copy_source/SingletonLock"
# This fixture proves copy semantics, not ambient host-load policy. Keep it
# deterministic when the development machine is busy running the full suite.
profile_seed_config="$state_dir/profile-seed-config.json"
printf '%s\n' '{"browser":{"resource_budget":{"min_free_memory_mb":1,"min_free_disk_mb":1,"max_load_per_cpu":1000}}}' > "$profile_seed_config"
profile_seed_json="$state_dir/profile-seed-copy-default.json"
HOME="$profile_copy_home" XDG_CONFIG_HOME="$profile_copy_config_dir" "$binary" --config "$profile_seed_config" --state-dir "$state_dir-copy-default" browser profile seed --strategy copy-default --json >"$profile_seed_json"
jq -e '.ok == true and .seeded == true and .exists == true and .seed_action == "seeded" and .seed_strategy == "copy-default" and .seed_status_path and .last_seed.schema_version == "cdp-profile-seed-status/v1" and .last_seed.seed_strategy == "copy-default" and .last_seed.seed_action == "seeded" and .managed_browser.browser_mode == "headless" and .managed_browser.profile_seed_strategy == "copy-default" and .managed_browser.default_profile_copied == true and .managed_browser.copied_file_count >= 6 and .resource_preflight.heavy_work_allowed == true and .maintenance.managed_process_sweep.checked == true and (.managed_browser | has("ownership_token") | not) and (.managed_browser | has("process_start_time") | not)' "$profile_seed_json" >/dev/null
profile_seed_summary="$(jq -r '.seed_status_path' "$profile_seed_json")"
test -s "$profile_seed_summary"
grep -q 'cookie-db' "$state_dir-copy-default/browser/headless-profile/Default/Cookies"
grep -q 'local-storage-token' "$state_dir-copy-default/browser/headless-profile/Default/Local Storage/leveldb/token.log"
grep -q 'indexeddb-state' "$state_dir-copy-default/browser/headless-profile/Default/IndexedDB/https_example_0.indexeddb.leveldb/000003.log"
grep -q 'synthetic-extension' "$state_dir-copy-default/browser/headless-profile/Default/Extensions/abcdefghijklmnop/1.0.0/manifest.json"
grep -q 'cache-bytes' "$state_dir-copy-default/browser/headless-profile/Default/Cache/Cache_Data/f_000001"
test ! -e "$state_dir-copy-default/browser/headless-profile/SingletonLock"
printf 'managed-cookie-db' > "$state_dir-copy-default/browser/headless-profile/Default/Cookies"
HOME="$profile_copy_home" XDG_CONFIG_HOME="$profile_copy_config_dir" "$binary" --state-dir "$state_dir-copy-default" browser profile seed --strategy copy-default --if-older-than 6h --json | jq -e '.ok == true and .seed_action == "skipped" and .seed_strategy == "copy-default" and .seed_interval_seconds == 21600 and .managed_browser.profile_seed_strategy == "copy-default"' >/dev/null
grep -q 'managed-cookie-db' "$state_dir-copy-default/browser/headless-profile/Default/Cookies"
"$binary" --state-dir "$state_dir" browser profile status --json | jq -e '.ok == true and .state == "ready" and .profile_perm == "700" and .metadata_perm == "600" and (.next_commands | index("cdp --browser-mode headless daemon keepalive --repair --json"))' >/dev/null
"$binary" doctor --check headless-security --state-dir "$state_dir" --json | jq -e '.ok == true and (.checks | length == 1) and .checks[0].name == "headless-security" and .checks[0].status == "pass" and .checks[0].details.profile_owner_only == true and .checks[0].details.metadata_owner_only == true and .checks[0].details.managed_profile_selected == true and .checks[0].details.seed_strategy == "managed" and (.. | objects | has("ownership_token") | not) and (.. | objects | has("process_start_time") | not)' >/dev/null
set +e
invalid_mode_output="$(CDP_BROWSER_MODE=hidden "$binary" browser mode get --state-dir "$state_dir" --json 2>/tmp/cdp-cli-browser-mode.err)"
invalid_mode_code=$?
set -e
if [[ "$invalid_mode_code" -ne 2 ]]; then
  echo "browser mode invalid exit code = $invalid_mode_code, want 2" >&2
  exit 1
fi
printf '%s
' "$invalid_mode_output" | jq -e '.ok == false and .code == "invalid_browser_mode" and .err_class == "usage"' >/dev/null
"$binary" connection add default --auto-connect --state-dir "$state_dir" --json | jq -e '.ok == true and .connection.mode == "auto_connect"' >/dev/null
"$binary" connection current --state-dir "$state_dir" --json | jq -e '.ok == true and .connection.name == "default" and .effective_connection.name == "default" and .connection_matches_effective == true' >/dev/null
"$binary" connection resolve --state-dir "$state_dir" --json | jq -e '.ok == true and .source == "browser_mode" and .browser_mode == "headed" and .browser_mode_source == "default" and .connection.name == "default" and .connection.mode == "auto_connect" and .connection.browser_mode == "headed"' >/dev/null
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
    live_examples_output="$("$binary" --timeout 5s protocol examples Browser.getVersion --json 2>/tmp/cdp-cli-live-examples.err)"
    live_examples_code=$?
    set -e
    if [[ "$live_examples_code" -eq 0 ]]; then
      printf '%s\n' "$live_examples_output" | jq -e '.ok == true and .examples[0].scope == "browser" and (.examples[0].command | contains("--target") | not) and (.examples[0] | has("required_params")) and (.examples[0] | has("scope_note"))' >/dev/null
    else
      printf '%s\n' "$live_examples_output" | jq -e '.ok == false and (.code == "connection_failed" or .code == "connection_not_configured" or .code == "unknown_protocol_entity")' >/dev/null
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

if [[ "${CDP_E2E_MANAGED_HEADLESS_RECOVERY:-}" == "1" || "${CDP_E2E_MANAGED_HEADLESS_RECOVERY:-}" == "true" ]]; then
  forced_restart_state_dir="$(mktemp -d)"
  set +e
  forced_keepalive_output="$("$binary" --browser-mode headless --timeout 60s daemon keepalive --repair --force --state-dir "$forced_restart_state_dir" --json 2>&1)"
  forced_keepalive_code=$?
  set -e
  if [[ "$forced_keepalive_code" -ne 0 ]] || ! printf '%s\n' "$forced_keepalive_output" \
    | jq -e '.ok == true and (.state == "started" or .state == "repaired" or .state == "healthy")' >/dev/null; then
    printf 'managed recovery keepalive failed (exit %s):\n%s\n' "$forced_keepalive_code" "$forced_keepalive_output" >&2
    exit 1
  fi
  managed_metadata="$forced_restart_state_dir/browser/managed-browser.json"
  old_managed_pid="$(jq -r '.chrome_pid' "$managed_metadata")"
  test "$old_managed_pid" -gt 0

  # Exercise the installed repair path against an adopted hold from a
  # superseded generation. The runtime file is hidden only in this disposable
  # fixture so the hold can enter its bounded endpoint dial before the current
  # generation is restored. The blackhole accepts the TCP connection but never
  # completes a WebSocket handshake, keeping the candidate alive without a
  # second browser owner.
  orphan_runtime="$forced_restart_state_dir/headless/daemon.json"
  orphan_runtime_snapshot="$forced_restart_state_dir/headless/daemon.current.json"
  orphan_runtime_hidden="$forced_restart_state_dir/headless/daemon.hidden.json"
  orphan_hold_pid_file="$forced_restart_state_dir/orphan-hold.pid"
  hold_blackhole_port_file="$forced_restart_state_dir/hold-blackhole.port"
  hold_blackhole_accepted_file="$forced_restart_state_dir/hold-blackhole.accepted"
  cp "$orphan_runtime" "$orphan_runtime_snapshot"
  hold_connection_mode="$(jq -r '.connection_mode // empty' "$orphan_runtime_snapshot")"
  hold_socket="$(jq -r '.socket_path // empty' "$orphan_runtime_snapshot")"
  if [[ -z "$hold_socket" ]]; then
    hold_socket="$forced_restart_state_dir/headless/daemon.sock"
  fi
  hold_profile="$(jq -r '.user_data_dir // empty' "$orphan_runtime_snapshot")"
  hold_managed_profile="$(jq -r '.managed_profile_path // empty' "$orphan_runtime_snapshot")"
  hold_seed_strategy="$(jq -r '.profile_seed_strategy // empty' "$orphan_runtime_snapshot")"
  orphan_pages_before="$forced_restart_state_dir/orphan-pages-before.json"
  orphan_pages_after="$forced_restart_state_dir/orphan-pages-after.json"
  "$binary" --browser-mode headless pages --state-dir "$forced_restart_state_dir" --json >"$orphan_pages_before"
  python3 -c 'import socket,sys,time; s=socket.socket(); s.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1); s.bind(("127.0.0.1",0)); s.listen(1); f=open(sys.argv[1],"w"); f.write(str(s.getsockname()[1])); f.close(); s.accept(); f=open(sys.argv[2],"w"); f.write("accepted"); f.close(); time.sleep(300)' "$hold_blackhole_port_file" "$hold_blackhole_accepted_file" >/dev/null 2>&1 &
  hold_blackhole_pid="$!"
  for _ in $(seq 1 40); do
    if [[ -s "$hold_blackhole_port_file" ]]; then
      break
    fi
    sleep 0.05
  done
  test -s "$hold_blackhole_port_file"
  hold_blackhole_port="$(<"$hold_blackhole_port_file")"
  mv "$orphan_runtime" "$orphan_runtime_hidden"
  (
    CDP_DAEMON_STATE_DIR="$forced_restart_state_dir" \
    CDP_DAEMON_BROWSER_MODE=headless \
    CDP_DAEMON_CONNECTION_MODE="$hold_connection_mode" \
    CDP_DAEMON_SOCKET="$hold_socket" \
    CDP_DAEMON_HOLD_ENDPOINT="ws://127.0.0.1:${hold_blackhole_port}/devtools/browser/orphan" \
    CDP_DAEMON_RECONNECT=1h \
    CDP_DAEMON_USER_DATA_DIR="$hold_profile" \
    CDP_DAEMON_MANAGED_PROFILE_PATH="$hold_managed_profile" \
    CDP_DAEMON_PROFILE_SEED_STRATEGY="$hold_seed_strategy" \
    python3 -c 'import os,sys; os.setsid(); os.execvpe(sys.argv[1],sys.argv[1:],os.environ)' "$binary" daemon hold >/dev/null 2>&1 &
    printf '%s\n' "$!" >"$orphan_hold_pid_file"
  )
  orphan_hold_pid="$(<"$orphan_hold_pid_file")"
  test "$orphan_hold_pid" -gt 0
  for _ in $(seq 1 40); do
    if ! kill -0 "$orphan_hold_pid" >/dev/null 2>&1; then
      break
    fi
    orphan_hold_parent="$(ps -o ppid= -p "$orphan_hold_pid" 2>/dev/null | tr -d ' ' || true)"
    if [[ "$orphan_hold_parent" == "1" ]]; then
      break
    fi
    sleep 0.05
  done
  test "$(ps -o ppid= -p "$orphan_hold_pid" 2>/dev/null | tr -d ' ')" = "1"
  for _ in $(seq 1 40); do
    if [[ -s "$hold_blackhole_accepted_file" ]]; then
      break
    fi
    sleep 0.05
  done
  test -s "$hold_blackhole_accepted_file"
  mv "$orphan_runtime_hidden" "$orphan_runtime"
  kill -0 "$orphan_hold_pid" >/dev/null 2>&1
  orphan_keepalive="$forced_restart_state_dir/orphan-keepalive.json"
  set +e
  "$binary" --browser-mode headless daemon keepalive --repair --state-dir "$forced_restart_state_dir" --json >"$orphan_keepalive" 2>"$orphan_keepalive.err"
  orphan_keepalive_code="$?"
  set -e
  if [[ "$orphan_keepalive_code" -ne 0 ]]; then
    jq '{ok, code, message, state, action, health: {daemon_hold_reconciliation: .health.daemon_hold_reconciliation}}' "$orphan_keepalive" >&2 || true
    sed -n '1,80p' "$orphan_keepalive.err" >&2 || true
    exit "$orphan_keepalive_code"
  fi
  if ! jq -e --argjson orphan_pid "$orphan_hold_pid" '
    .ok == true and (.state == "healthy" or .state == "reconciled") and
    (.health.daemon_hold_reconciliation.reclaimed_pids | index($orphan_pid)) and
    (.health.daemon_hold_reconciliation.candidates | any(.pid == $orphan_pid and .state == "reclaimed" and .generation_state == "superseded"))
  ' "$orphan_keepalive" >/dev/null; then
    jq '{ok, state, action, health: {daemon_hold_reconciliation: .health.daemon_hold_reconciliation}}' "$orphan_keepalive" >&2
    exit 1
  fi
  for _ in $(seq 1 40); do
    if ! kill -0 "$orphan_hold_pid" >/dev/null 2>&1; then
      break
    fi
    sleep 0.05
  done
  if kill -0 "$orphan_hold_pid" >/dev/null 2>&1; then
    echo "installed orphaned daemon hold was not reclaimed" >&2
    exit 1
  fi
  "$binary" --browser-mode headless pages --state-dir "$forced_restart_state_dir" --json >"$orphan_pages_after"
  jq -e --arg before "$(jq -c '.pages' "$orphan_pages_before")" '
    .ok == true and (.pages | tostring) == $before
  ' "$orphan_pages_after" >/dev/null
  jq -e --arg daemon_pid "$(jq -r '.pid' "$orphan_runtime_snapshot")" \
    --arg chrome_pid "$(jq -r '.chrome_pid' "$orphan_runtime_snapshot")" \
    --arg profile "$(jq -r '.user_data_dir' "$orphan_runtime_snapshot")" \
    --arg socket "$(jq -r '.socket_path' "$orphan_runtime_snapshot")" '
    (.pid | tostring) == $daemon_pid and (.chrome_pid | tostring) == $chrome_pid and
    .user_data_dir == $profile and .socket_path == $socket
  ' "$orphan_runtime" >/dev/null
  kill -TERM "$hold_blackhole_pid" >/dev/null 2>&1 || true
  wait "$hold_blackhole_pid" >/dev/null 2>&1 || true
  hold_blackhole_pid=""

  jq 'del(.ownership_token, .process_start_time)' "$managed_metadata" >"$managed_metadata.corrupt"
  mv "$managed_metadata.corrupt" "$managed_metadata"
  "$binary" --browser-mode headless daemon stop --state-dir "$forced_restart_state_dir" --json \
    | jq -e '.ok == true and .daemon_stopped == true and .managed_browser_stopped == false and .managed_browser.reason == "managed ownership metadata incomplete"' >/dev/null
  forced_restart_json="$forced_restart_state_dir/forced-restart.json"
  "$binary" --browser-mode headless daemon restart --force-managed --stale-lock-after 1s --state-dir "$forced_restart_state_dir" --json >"$forced_restart_json"
  jq -e --argjson old_pid "$old_managed_pid" '
    .ok == true and .daemon.state == "running" and .restart.managed_restart == true and
    .restart.managed_browser_stopped == true and
    (.restart.managed_browser.pids | index($old_pid)) and
    (.restart.managed_browser.safety_checks | index("managed_profile_path_matches_state_dir")) and
    (.restart.managed_browser.safety_checks | index("process_tree_inspected")) and
    (.restart.managed_browser.process_evidence | any(.pid == $old_pid and .role == "root" and .debugging_port_match == true)) and
    .restart.recovery_state.runtime_artifacts_cleared == true
  ' "$forced_restart_json" >/dev/null
  "$binary" --browser-mode headless daemon health --state-dir "$forced_restart_state_dir" --json \
    | jq -e '.ok == true and .health.daemon_process_running == true and .health.daemon_rpc_ready == true and .health.browser_endpoint_reachable == true and .health.managed_chrome_owned == true' >/dev/null
  "$binary" --browser-mode headless pages --state-dir "$forced_restart_state_dir" --json \
    | jq -e '.ok == true and (.pages | type == "array")' >/dev/null
fi

mode_status_dir="$state_dir/mode-status"
"$binary" daemon status --state-dir "$mode_status_dir" --json | jq -e '.ok == true and .daemon.browser_mode == "headed" and .daemon.state' >/dev/null
"$binary" --browser-mode headless daemon status --state-dir "$mode_status_dir" --json | jq -e '.ok == true and .daemon.browser_mode == "headless" and (.daemon.next_commands | index("cdp --browser-mode headless browser profile status --json"))' >/dev/null
"$binary" daemon logs --state-dir "$mode_status_dir" --json | jq -e '.ok == true and .browser_mode == "headed" and .log.count == 0 and (.entries | length == 0)' >/dev/null
"$binary" --browser-mode headless daemon logs --state-dir "$mode_status_dir" --json | jq -e '.ok == true and .browser_mode == "headless" and (.log.path | contains("headless/daemon.log")) and .log.count == 0 and (.entries | length == 0)' >/dev/null

socket_unready_dir="$state_dir/socket-unready"
mkdir -p "$socket_unready_dir/headless"
cat > "$socket_unready_dir/headless/daemon.json" <<JSON
{
  "pid": $$,
  "started_at": "2026-06-05T00:00:00Z",
  "browser_mode": "headless",
  "connection_mode": "browser_url",
  "socket_path": "$socket_unready_dir/headless/missing.sock"
}
JSON
set +e
socket_unready_health="$("$binary" --browser-mode headless daemon health --state-dir "$socket_unready_dir" --json 2>/tmp/cdp-cli-socket-unready-health.err)"
socket_unready_code=$?
set -e
if [[ "$socket_unready_code" -ne 1 ]]; then
  echo "headless daemon health exit code = $socket_unready_code, want 1 for socket-unready runtime" >&2
  cat /tmp/cdp-cli-socket-unready-health.err >&2
  exit 1
fi
printf '%s\n' "$socket_unready_health" | jq -e '.ok == false and .code == "headless_daemon_rpc_not_ready" and .state == "degraded" and .daemon.state == "runtime_socket_unready" and .daemon.process_running == true and .daemon.runtime_socket_ready == false and .health.state == "degraded" and .health.daemon_rpc_ready == false and (.health.reasons | index("daemon_socket_unready")) and (.next_commands | index("cdp --browser-mode headless daemon keepalive --repair --json"))' >/dev/null

crash_log_dir="$state_dir/crash-log"
mkdir -p "$crash_log_dir/headless"
cat > "$crash_log_dir/headless/daemon.log" <<'JSONL'
{"time":"2026-06-05T00:00:00Z","level":"info","event":"runtime_saved","message":"daemon runtime state saved","pid":101}
{"time":"2026-06-05T00:00:01Z","level":"warn","event":"hold_connection_ended","message":"failed to get reader: failed to read frame header: EOF","pid":101}
{"time":"2026-06-05T00:00:02Z","level":"error","event":"rpc_listen_failed","message":"listen daemon rpc socket: bind failed","pid":102}
JSONL
set +e
crash_health="$("$binary" --browser-mode headless daemon health --state-dir "$crash_log_dir" --json 2>/tmp/cdp-cli-crash-health.err)"
crash_health_code=$?
set -e
if [[ "$crash_health_code" -ne 1 ]]; then
  echo "headless daemon crash health exit code = $crash_health_code, want 1 for degraded runtime" >&2
  cat /tmp/cdp-cli-crash-health.err >&2
  exit 1
fi
printf '%s\n' "$crash_health" | jq -e '.ok == false and .code == "headless_daemon_not_running" and .health.crash_capture == "daemon_logs" and .health.recent_log_warnings == 1 and .health.recent_log_errors == 1 and (.health.recent_crashes | length == 2) and .health.recent_crashes[0].type == "browser_connection_ended" and .health.recent_crashes[1].type == "daemon_rpc_listen_failed" and (.health.last_browser_keepalive_error | contains("hold_connection_ended"))' >/dev/null

retired_hold_log_dir="$state_dir/retired-hold-log"
mkdir -p "$retired_hold_log_dir/headless"
cat > "$retired_hold_log_dir/headless/daemon.log" <<'JSONL'
{"time":"2026-06-05T00:00:00Z","level":"warn","event":"hold_connection_ended","message":"failed to get reader: retired generation","pid":101}
{"time":"2026-06-05T00:00:01Z","level":"info","event":"hold_reclaimed","message":"orphaned daemon hold reclaimed after exact ownership verification","pid":101}
{"time":"2026-06-05T00:00:02Z","level":"warn","event":"hold_connection_ended","message":"failed to get reader: active generation one","pid":202}
{"time":"2026-06-05T00:00:03Z","level":"error","event":"browser_dial_failed","message":"active generation two","pid":202}
JSONL
set +e
retired_hold_health="$("$binary" --browser-mode headless daemon health --state-dir "$retired_hold_log_dir" --json 2>/tmp/cdp-cli-retired-hold-health.err)"
retired_hold_health_code=$?
set -e
if [[ "$retired_hold_health_code" -ne 1 ]]; then
  echo "headless daemon retired-hold health exit code = $retired_hold_health_code, want 1 for degraded runtime" >&2
  cat /tmp/cdp-cli-retired-hold-health.err >&2
  exit 1
fi
printf '%s\n' "$retired_hold_health" | jq -e '.ok == false and .code == "headless_daemon_not_running" and .health.recent_log_warnings == 1 and .health.recent_log_errors == 1 and (.health.retired_hold_pids | . == [101]) and (.health.recent_crashes | length == 2) and .health.recent_crashes[0].pid == 202 and .health.recent_crashes[1].pid == 202 and (.health.degraded_reasons | index("repeated_connection_churn"))' >/dev/null

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
storage_output="$("$binary" storage list --state-dir "$state_dir" --json 2>/tmp/cdp-cli-storage.err)"
storage_code=$?
set -e

if [[ "$storage_code" -ne 3 ]]; then
  echo "storage exit code = $storage_code, want 3 without a browser connection" >&2
  cat /tmp/cdp-cli-storage.err >&2
  exit 1
fi

printf '%s\n' "$storage_output" | jq -e '.ok == false and .code == "connection_not_configured"' >/dev/null

set +e
indexeddb_output="$("$binary" storage indexeddb list --state-dir "$state_dir" --json 2>/tmp/cdp-cli-indexeddb.err)"
indexeddb_code=$?
set -e

if [[ "$indexeddb_code" -ne 3 ]]; then
  echo "storage indexeddb exit code = $indexeddb_code, want 3 without a browser connection" >&2
  cat /tmp/cdp-cli-indexeddb.err >&2
  exit 1
fi

printf '%s\n' "$indexeddb_output" | jq -e '.ok == false and .code == "connection_not_configured"' >/dev/null

set +e
cache_output="$("$binary" storage cache list --state-dir "$state_dir" --json 2>/tmp/cdp-cli-storage-cache.err)"
cache_code=$?
set -e

if [[ "$cache_code" -ne 3 ]]; then
  echo "storage cache exit code = $cache_code, want 3 without a browser connection" >&2
  cat /tmp/cdp-cli-storage-cache.err >&2
  exit 1
fi

printf '%s\n' "$cache_output" | jq -e '.ok == false and .code == "connection_not_configured"' >/dev/null

set +e
service_worker_output="$("$binary" storage service-workers list --state-dir "$state_dir" --json 2>/tmp/cdp-cli-service-workers.err)"
service_worker_code=$?
set -e

if [[ "$service_worker_code" -ne 3 ]]; then
  echo "storage service-workers exit code = $service_worker_code, want 3 without a browser connection" >&2
  cat /tmp/cdp-cli-service-workers.err >&2
  exit 1
fi

printf '%s\n' "$service_worker_output" | jq -e '.ok == false and .code == "connection_not_configured"' >/dev/null

cat >"$state_dir/storage-left.json" <<'JSON'
{"snapshot":{"local_storage":{"entries":[{"key":"feature","value":"enabled"}]},"session_storage":{"entries":[]},"cookies":[]}}
JSON
cat >"$state_dir/storage-right.json" <<'JSON'
{"snapshot":{"local_storage":{"entries":[{"key":"feature","value":"disabled"},{"key":"new","value":"yes"}]},"session_storage":{"entries":[]},"cookies":[]}}
JSON
"$binary" storage diff --left "$state_dir/storage-left.json" --right "$state_dir/storage-right.json" --json \
  | jq -e '.ok == true and .has_diff == true and .diff.summary.added == 1 and .diff.summary.changed == 1' >/dev/null

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

CDP_E2E_VALIDATE_ONLY=1 bash scripts/e2e_web_research_live.sh "$binary" >/dev/null
