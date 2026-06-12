#!/usr/bin/env bash
set -euo pipefail

cdp_bin="${CDP_BIN:-cdp}"
log_dir="${CDP_LOG_DIR:-$HOME/.cdp-cli}"
artifact_dir="${CDP_ARTIFACT_DIR:-$log_dir/headless-health}"
failure_threshold="${CDP_FAILURE_THRESHOLD:-3}"
health_url="${CDP_HEALTH_URL:-data:text/html,%3Cmain%20data-cdp-health%3D%22ok%22%3Ecdp-headless-health%3C%2Fmain%3E}"
state_dir_arg=()
if [[ -n "${CDP_STATE_DIR:-}" ]]; then
  state_dir_arg=(--state-dir "$CDP_STATE_DIR")
fi

mkdir -p "$artifact_dir"
run_id="$(date -u +%Y%m%dT%H%M%SZ)"
run_dir="$artifact_dir/$run_id"
maintenance_dir="$run_dir/maintenance"
mkdir -p "$maintenance_dir"
log_file="$artifact_dir/headless-health.log"
summary_file="$artifact_dir/latest.json"
failure_count_file="$artifact_dir/failure-count"
stderr_file="$run_dir/maintenance.stderr"
stdout_file="$run_dir/maintenance.json"

write_escalation() {
  local count="$1"
  cat >"$artifact_dir/feature-request-candidate.md" <<EOF
---
title: "Investigate repeated headless maintenance failures"
priority: P1
domain: daemon
status: proposed
created: $(date -u +%F)
---

# Investigate repeated headless maintenance failures

## Problem

The cron-compatible headless maintenance wrapper failed $count consecutive times.

## Current behaviour

Run artifacts: $run_dir
Summary: $summary_file
Log: $log_file

## Proposed solution

Inspect the maintenance report, identify whether managed-process sweep, resource
preflight, profile seed, daemon repair, synthetic health-check, page cleanup, or
artifact writing failed, then convert this candidate into a managed feature
request.

## Impact

Repeated failures block unattended headless cdp automation, browser-grounded
research, and agent live-site validation.
EOF
}

{
  printf '[%s] daemon maintenance\n' "$(date -u +%FT%TZ)"
  printf 'command:'
  printf ' %q' "$cdp_bin" --browser-mode headless "${state_dir_arg[@]}" daemon maintenance --out-dir "$maintenance_dir" --health-url "$health_url" --json
  printf '\n'
} >>"$log_file"

set +e
maintenance_output="$("$cdp_bin" --browser-mode headless "${state_dir_arg[@]}" daemon maintenance --out-dir "$maintenance_dir" --health-url "$health_url" --json 2>"$stderr_file")"
maintenance_exit=$?
set -e
printf '%s\n' "$maintenance_output" >"$stdout_file"

if jq -e . "$stdout_file" >/dev/null 2>&1; then
  maintenance_json="$(jq -c . "$stdout_file")"
else
  maintenance_json="$(jq -n --arg stderr "$(cat "$stderr_file" 2>/dev/null || true)" --argjson exit_code "$maintenance_exit" '{ok: false, state: "command_failed", status: "fail", action: "failed", exit_code: $exit_code, stderr: $stderr}')"
fi

maintenance_ok="$(jq -r '.ok == true' <<<"$maintenance_json")"
maintenance_state="$(jq -r '.state // "unknown"' <<<"$maintenance_json")"
state="failed"
action="diagnosed"
failure="$maintenance_state"
if [[ "$maintenance_ok" == "true" && "$maintenance_state" == "healthy" ]]; then
  state="healthy"
  action="validated"
  failure=""
elif [[ "$maintenance_ok" == "true" && "$maintenance_state" == "locked" ]]; then
  state="locked"
  action="skipped"
  failure=""
fi

if [[ "$state" == "failed" ]]; then
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
  --arg maintenance_summary "$maintenance_dir/latest.json" \
  --arg maintenance_stdout "$stdout_file" \
  --arg maintenance_stderr "$stderr_file" \
  --argjson maintenance "$maintenance_json" \
  --argjson failure_count "$failure_count" \
  '{
    ok: ($state == "healthy" or $state == "locked"),
    state: $state,
    action: $action,
    failure: (if $failure == "" then null else $failure end),
    run_id: $run_id,
    maintenance: $maintenance,
    artifacts: {
      run_dir: $run_dir,
      log: $log_file,
      summary: $summary_file,
      maintenance_summary: $maintenance_summary,
      maintenance_stdout: $maintenance_stdout,
      maintenance_stderr: $maintenance_stderr
    },
    failure_count: $failure_count,
    next_commands: [
      "cdp --browser-mode headless daemon maintenance --json",
      "cdp --browser-mode headless daemon health --json",
      "cdp --browser-mode headless daemon logs --tail 50 --json"
    ]
  }' | tee "$summary_file"

[[ "$state" == "healthy" || "$state" == "locked" ]]
