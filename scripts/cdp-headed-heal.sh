#!/usr/bin/env bash
set -euo pipefail

cdp_bin="${CDP_BIN:-$HOME/.local/bin/cdp}"
log_dir="${CDP_LOG_DIR:-$HOME/.cdp-cli}"
lock_path="${CDP_LOCK_PATH:-$log_dir/locks/heal-headed.lock}"
display_value="${DISPLAY:-:0}"
xdg_runtime_dir="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
reconnect="${CDP_RECONNECT:-30s}"
artifact_dir="${CDP_ARTIFACT_DIR:-$log_dir/headed-heal}"
run_id="$(date -u +%Y%m%dT%H%M%SZ)"
run_dir="$artifact_dir/$run_id"
summary_file="$artifact_dir/latest.json"

state_dir_arg=()
if [[ -n "${CDP_STATE_DIR:-}" ]]; then
  state_dir_arg=(--state-dir "$CDP_STATE_DIR")
fi

mkdir -p "$(dirname "$lock_path")" "$run_dir"
exec 9>"$lock_path"
if ! flock -n 9; then
  jq -n --arg lock "$lock_path" '{ok: true, browser_mode: "headed", state: "locked", action: "skipped", lock: $lock}'
  exit 0
fi

run_step() {
  local name="$1"
  shift
  "$@" >"$run_dir/$name.json" 2>"$run_dir/$name.stderr"
}

run_step status_before env DISPLAY="$display_value" XDG_RUNTIME_DIR="$xdg_runtime_dir" "$cdp_bin" --browser-mode headed "${state_dir_arg[@]}" daemon status --auto-connect --json || true

status_state="$(jq -r '.daemon.state // empty' "$run_dir/status_before.json" 2>/dev/null || true)"
if [[ "$status_state" == "running" ]]; then
  run_step keepalive env DISPLAY="$display_value" XDG_RUNTIME_DIR="$xdg_runtime_dir" "$cdp_bin" --browser-mode headed "${state_dir_arg[@]}" daemon keepalive --auto-connect --repair --reconnect "$reconnect" --display "$display_value" --json
else
  run_step stop env DISPLAY="$display_value" XDG_RUNTIME_DIR="$xdg_runtime_dir" "$cdp_bin" --browser-mode headed "${state_dir_arg[@]}" daemon stop --json || true
  run_step keepalive env DISPLAY="$display_value" XDG_RUNTIME_DIR="$xdg_runtime_dir" "$cdp_bin" --browser-mode headed "${state_dir_arg[@]}" daemon keepalive --auto-connect --repair --reconnect "$reconnect" --display "$display_value" --json
fi

run_step health env DISPLAY="$display_value" XDG_RUNTIME_DIR="$xdg_runtime_dir" "$cdp_bin" --browser-mode headed "${state_dir_arg[@]}" daemon health --json || true

jq -n \
  --arg run_id "$run_id" \
  --arg run_dir "$run_dir" \
  --arg summary "$summary_file" \
  --slurpfile keepalive "$run_dir/keepalive.json" \
  --slurpfile health "$run_dir/health.json" \
  '{ok: (($keepalive[0].ok // false) and (($health[0].health.state // "") == "healthy")), browser_mode: "headed", state: ($health[0].health.state // $keepalive[0].state // "unknown"), action: ($keepalive[0].action // null), run_id: $run_id, artifacts: {run_dir: $run_dir, summary: $summary}, daemon: ($keepalive[0].daemon // null), health: ($health[0].health // null), processes_by_mode: ($health[0].health.daemon_processes_by_mode // null), next_commands: ["cdp --browser-mode headed daemon health --json", "cdp --browser-mode headed daemon logs --tail 50 --json"]}' \
  | tee "$summary_file"
