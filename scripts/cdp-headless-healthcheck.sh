#!/usr/bin/env bash
set -euo pipefail

cdp_bin="${CDP_BIN:-cdp}"
log_dir="${CDP_LOG_DIR:-$HOME/.cdp-cli}"
artifact_dir="${CDP_ARTIFACT_DIR:-$log_dir/headless-health}"
lock_path="${CDP_LOCK_PATH:-$log_dir/locks/headless-health.lock}"
failure_threshold="${CDP_FAILURE_THRESHOLD:-3}"
health_url="${CDP_HEALTH_URL:-data:text/html,%3Cmain%20data-cdp-health%3D%22ok%22%3Ecdp-headless-health%3C%2Fmain%3E}"
state_dir_arg=()
if [[ -n "${CDP_STATE_DIR:-}" ]]; then
  state_dir_arg=(--state-dir "$CDP_STATE_DIR")
fi

mkdir -p "$(dirname "$lock_path")" "$artifact_dir"
exec 9>"$lock_path"
if ! flock -n 9; then
  jq -n --arg lock "$lock_path" '{ok: true, state: "locked", action: "skipped", lock: $lock}'
  exit 0
fi

run_id="$(date -u +%Y%m%dT%H%M%SZ)"
run_dir="$artifact_dir/$run_id"
mkdir -p "$run_dir"
log_file="$artifact_dir/headless-health.log"
summary_file="$artifact_dir/latest.json"
failure_count_file="$artifact_dir/failure-count"

run_step() {
  local name="$1"
  shift
  {
    printf '[%s] %s\n' "$(date -u +%FT%TZ)" "$name"
    printf 'command:'
    printf ' %q' "$@"
    printf '\n'
  } >>"$log_file"
  "$@" >"$run_dir/$name.json" 2>"$run_dir/$name.stderr"
}

collect_diagnostics() {
  "$cdp_bin" --browser-mode headless "${state_dir_arg[@]}" daemon status --json >"$run_dir/daemon-status.json" 2>"$run_dir/daemon-status.stderr" || true
  "$cdp_bin" --browser-mode headless "${state_dir_arg[@]}" doctor --check daemon --json >"$run_dir/doctor-daemon.json" 2>"$run_dir/doctor-daemon.stderr" || true
  "$cdp_bin" --browser-mode headless "${state_dir_arg[@]}" doctor --check browser-health --json >"$run_dir/doctor-browser-health.json" 2>"$run_dir/doctor-browser-health.stderr" || true
  "$cdp_bin" --browser-mode headless "${state_dir_arg[@]}" daemon logs --tail 50 --json >"$run_dir/daemon-logs.json" 2>"$run_dir/daemon-logs.stderr" || true
}

keepalive_lock_failure() {
  if [[ ! -s "$run_dir/keepalive.json" ]]; then
    return 1
  fi
  local locked phase
  locked="$(jq -r '.locked // false' "$run_dir/keepalive.json" 2>/dev/null || printf 'false')"
  phase="$(jq -r '.lock.phase // empty' "$run_dir/keepalive.json" 2>/dev/null || true)"
  [[ "$locked" == "true" && "$phase" == launching_* ]]
}

write_escalation() {
  local count="$1"
  cat >"$artifact_dir/feature-request-candidate.md" <<EOF
---
title: "Investigate repeated headless health-check failures"
priority: P1
domain: daemon
status: proposed
created: $(date -u +%F)
---

# Investigate repeated headless health-check failures

## Problem

The cron-compatible headless health check failed $count consecutive times.

## Current behaviour

Run artifacts: $run_dir
Summary: $summary_file
Log: $log_file

## Proposed solution

Inspect the diagnostics, identify whether launch, daemon RPC, navigation, DOM/JS, or screenshot capture failed, then convert this candidate into a managed feature request.

## Impact

Repeated failures block unattended headless cdp automation, browser-grounded research, and agent live-site validation.
EOF
}

state="healthy"
action="validated"
failure=""
if ! run_step keepalive "$cdp_bin" --browser-mode headless "${state_dir_arg[@]}" daemon keepalive --repair --json; then
  state="failed"
  action="diagnosed"
  failure="keepalive_failed"
elif ! run_step health "$cdp_bin" --browser-mode headless "${state_dir_arg[@]}" daemon health --json; then
  state="failed"
  action="diagnosed"
  failure="health_failed"
elif [[ "$(jq -r '.health.state // .daemon.health.state // empty' "$run_dir/health.json")" != "healthy" ]]; then
  state="failed"
  action="diagnosed"
  if keepalive_lock_failure; then
    failure="launch_lock_blocking_recovery"
  else
    failure="health_not_healthy"
  fi
elif ! run_step open "$cdp_bin" --browser-mode headless "${state_dir_arg[@]}" open "$health_url" --json; then
  state="failed"
  action="diagnosed"
  failure="navigate_failed"
else
  target_id="$(jq -r '.page.id // .page.target_id // .page.targetId // empty' "$run_dir/open.json")"
  if [[ -z "$target_id" ]]; then
    state="failed"
    action="diagnosed"
    failure="target_missing"
  elif ! run_step eval "$cdp_bin" --browser-mode headless "${state_dir_arg[@]}" eval --target "$target_id" 'document.querySelector("[data-cdp-health]")?.textContent' --json; then
    state="failed"
    action="diagnosed"
    failure="javascript_failed"
  elif ! jq -e '.. | strings | select(. == "cdp-headless-health")' "$run_dir/eval.json" >/dev/null; then
    state="failed"
    action="diagnosed"
    failure="javascript_unexpected_result"
  elif ! run_step text "$cdp_bin" --browser-mode headless "${state_dir_arg[@]}" text body --target "$target_id" --json; then
    state="failed"
    action="diagnosed"
    failure="dom_text_failed"
  elif ! run_step screenshot "$cdp_bin" --browser-mode headless "${state_dir_arg[@]}" screenshot --target "$target_id" --out "$run_dir/screenshot.png" --json; then
    state="failed"
    action="diagnosed"
    failure="screenshot_failed"
  fi
fi

if [[ "$state" != "healthy" ]]; then
  collect_diagnostics
  previous_count="$(cat "$failure_count_file" 2>/dev/null || printf '0')"
  if ! [[ "$previous_count" =~ ^[0-9]+$ ]]; then
    previous_count=0
  fi
  failure_count="$((previous_count + 1))"
  printf '%s\n' "$failure_count" >"$failure_count_file"
  if (( failure_count >= failure_threshold )); then
    write_escalation "$failure_count"
  fi
else
  failure_count=0
  printf '0\n' >"$failure_count_file"
fi

jq -n \
  --arg state "$state" \
  --arg action "$action" \
  --arg failure "$failure" \
  --arg run_id "$run_id" \
  --arg run_dir "$run_dir" \
  --arg log_file "$log_file" \
  --arg summary_file "$summary_file" \
  --arg screenshot "$run_dir/screenshot.png" \
  --argjson keepalive_lock "$(jq -c '{locked: (.locked // false), phase: (.lock.phase // null), pid: (.lock.pid // null), started_at: (.lock.started_at // null), name: (.lock.name // null)}' "$run_dir/keepalive.json" 2>/dev/null || printf 'null')" \
  --argjson failure_count "$failure_count" \
  '{ok: ($state == "healthy"), state: $state, action: $action, failure: (if $failure == "" then null else $failure end), run_id: $run_id, keepalive_lock: $keepalive_lock, artifacts: {run_dir: $run_dir, log: $log_file, summary: $summary_file, screenshot: $screenshot}, failure_count: $failure_count, next_commands: ["cdp --browser-mode headless daemon health --json", "cdp --browser-mode headless daemon logs --tail 50 --json"]}' \
  | tee "$summary_file"

[[ "$state" == "healthy" ]]
