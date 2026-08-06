#!/usr/bin/env bash
set -euo pipefail

binary="${1:-$(command -v cdp || true)}"
browser_mode="${CDP_E2E_BROWSER_MODE:-headless}"
keep_artifacts="${CDP_E2E_KEEP_ARTIFACTS:-0}"
validate_only="${CDP_E2E_VALIDATE_ONLY:-0}"
parallel="${CDP_E2E_PARALLEL:-3}"
wait_time="${CDP_E2E_WAIT:-30s}"
settle_time="${CDP_E2E_SETTLE:-2s}"

if [[ ! -x "$binary" ]]; then
  echo "missing installed cdp executable: $binary" >&2
  exit 2
fi
if ! command -v jq >/dev/null 2>&1; then
  echo "jq is required" >&2
  exit 2
fi
case "$browser_mode" in
  headless | headed) ;;
  *)
    echo "CDP_E2E_BROWSER_MODE must be headless or headed" >&2
    exit 2
    ;;
esac
if [[ ! "$parallel" =~ ^[0-9]+$ ]] || ((parallel < 1 || parallel > 10)); then
  echo "CDP_E2E_PARALLEL must be an integer from 1 through 10" >&2
  exit 2
fi

tmp_base="${CDP_E2E_TMP_BASE:-${TMPDIR:-/tmp}}"
tmp_base="${tmp_base%/}"
# macOS limits Unix-domain socket paths to 104 bytes. Keep the isolated
# runtime root short enough for state/headless/daemon.sock even when TMPDIR
# is a long per-process directory.
socket_template="$tmp_base/cdp-web-research-live.XXXXXX/state/headless/daemon.sock"
if (( ${#socket_template} >= 100 )); then
  tmp_base="/tmp"
fi
run_root="$(mktemp -d "$tmp_base/cdp-web-research-live.XXXXXX")"
url_file="$run_root/urls.txt"
expected_json="$run_root/expected-urls.json"
report_json="$run_root/report.json"
report_stderr="$run_root/report.stderr"
metadata_json="$run_root/run-metadata.json"
evidence_json="$run_root/evidence.json"
out_dir="$run_root/pages"
state_dir="$run_root/state"
run_started_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
headless_owned=false
headless_pid=""
headless_started_at=""
headless_socket=""

cleanup() {
  local exit_code=$?
  local cleanup_failed=false
  local status_path="$run_root/headless-cleanup-status.json"
  trap - EXIT

  if [[ "$headless_owned" == true ]]; then
    if "$binary" --browser-mode headless --state-dir "$state_dir" daemon status --json >"$status_path" 2>/dev/null \
      && jq -e --argjson pid "$headless_pid" --arg started "$headless_started_at" --arg socket "$headless_socket" \
        '.ok == true and .daemon.runtime.pid == $pid and .daemon.runtime.started_at == $started and .daemon.runtime.socket_path == $socket' \
        "$status_path" >/dev/null; then
      if ! "$binary" --browser-mode headless --state-dir "$state_dir" daemon stop --force-managed --json >"$run_root/headless-stop.json" 2>"$run_root/headless-stop.stderr"; then
        echo "managed headless cleanup failed; preserving $run_root" >&2
        cleanup_failed=true
      fi
    else
      echo "managed headless identity changed; refusing cleanup and preserving $run_root" >&2
      cleanup_failed=true
    fi
  fi

  if [[ "$cleanup_failed" == true && "$exit_code" -eq 0 ]]; then
    exit_code=1
  fi
  if [[ "$keep_artifacts" == "1" || "$exit_code" -ne 0 || "$cleanup_failed" == true ]]; then
    echo "live web-research E2E artifacts: $run_root" >&2
  elif [[ -d "$run_root" && "$run_root" == "$tmp_base"/cdp-web-research-live.* ]]; then
    rm -rf "$run_root"
  fi
  exit "$exit_code"
}
trap cleanup EXIT

declare -a urls=()
if [[ -n "${CDP_E2E_URL_FILE:-}" ]]; then
  if [[ ! -f "$CDP_E2E_URL_FILE" ]]; then
    echo "CDP_E2E_URL_FILE does not exist: $CDP_E2E_URL_FILE" >&2
    exit 2
  fi
  while IFS= read -r raw_line || [[ -n "$raw_line" ]]; do
    line="${raw_line//$'\r'/}"
    line="${line#"${line%%[![:space:]]*}"}"
    line="${line%"${line##*[![:space:]]}"}"
    [[ -z "$line" || "$line" == \#* ]] && continue
    urls+=("$line")
  done <"$CDP_E2E_URL_FILE"
else
  urls=(
    "https://go.dev/doc/"
    "https://pkg.go.dev/net/http"
    "https://developer.mozilla.org/en-US/docs/Web/API/Document/readyState"
    "https://chromedevtools.github.io/devtools-protocol/"
    "https://www.w3.org/TR/webdriver2/"
    "https://www.rfc-editor.org/rfc/rfc9110.html"
    "https://docs.github.com/en/rest"
    "https://playwright.dev/docs/api/class-page"
    "https://pptr.dev/api/puppeteer.page"
    "https://pkg.go.dev/github.com/go-rod/rod"
    "https://docs.python.org/3/library/asyncio.html"
    "https://git-scm.com/docs/git"
  )
fi

declare -A seen_urls=()
for url in "${urls[@]}"; do
  if [[ ! "$url" =~ ^https?://[^[:space:]#]+$ ]]; then
    echo "live E2E URLs must be fragment-free HTTP(S) URLs: $url" >&2
    exit 2
  fi
  if [[ -n "${seen_urls[$url]:-}" ]]; then
    echo "duplicate live E2E URL: $url" >&2
    exit 2
  fi
  seen_urls[$url]=1
  printf '%s\n' "$url" >>"$url_file"
done

url_count=${#urls[@]}
if ((url_count < 10 || url_count > 20)); then
  echo "live E2E requires 10 through 20 unique URLs; got $url_count" >&2
  exit 2
fi
min_success="${CDP_E2E_MIN_SUCCESS:-$url_count}"
if [[ ! "$min_success" =~ ^[0-9]+$ ]] || ((min_success < 10 || min_success > url_count)); then
  echo "CDP_E2E_MIN_SUCCESS must be an integer from 10 through $url_count" >&2
  exit 2
fi
jq -Rsc 'split("\n") | map(select(length > 0))' <"$url_file" >"$expected_json"
jq -n \
  --arg started_at "$run_started_at" \
  --arg browser_mode "$browser_mode" \
  --arg wait "$wait_time" \
  --arg settle "$settle_time" \
  --argjson parallel "$parallel" \
  --argjson min_success "$min_success" \
  --slurpfile urls "$expected_json" \
  '{
    schema_version: 1,
    started_at: $started_at,
    browser_mode: $browser_mode,
    wait: $wait,
    settle: $settle,
    parallel: $parallel,
    min_success: $min_success,
    urls: $urls[0]
  }' >"$metadata_json"

if [[ "$validate_only" == "1" ]]; then
  echo "validated live web-research E2E corpus: $url_count URLs"
  exit 0
fi

mode_args=(--browser-mode "$browser_mode")
if [[ "$browser_mode" == "headless" ]]; then
  mkdir -p "$run_root/config"
  export XDG_CONFIG_HOME="$run_root/config"
  mode_args+=(--state-dir "$state_dir")
  keepalive_args=(daemon keepalive --repair --json)
  if [[ -n "${CDP_E2E_CHROME:-}" ]]; then
    keepalive_args=(daemon keepalive --repair --chrome-command "$CDP_E2E_CHROME" --json)
  fi
  "$binary" "${mode_args[@]}" "${keepalive_args[@]}" >"$run_root/headless-keepalive.json"
  jq -e '.ok == true and (.state == "started" or .state == "repaired" or .state == "healthy")' "$run_root/headless-keepalive.json" >/dev/null
  "$binary" "${mode_args[@]}" daemon status --json >"$run_root/daemon-before.json"
  jq -e '.ok == true and .daemon.browser_mode == "headless" and .daemon.process_running == true and .daemon.runtime_socket_ready == true and .daemon.health.usable == true' "$run_root/daemon-before.json" >/dev/null
  headless_pid="$(jq -r '.daemon.runtime.pid' "$run_root/daemon-before.json")"
  headless_started_at="$(jq -r '.daemon.runtime.started_at' "$run_root/daemon-before.json")"
  headless_socket="$(jq -r '.daemon.runtime.socket_path' "$run_root/daemon-before.json")"
  if [[ ! "$headless_pid" =~ ^[0-9]+$ || -z "$headless_started_at" || -z "$headless_socket" ]]; then
    echo "headless daemon did not expose stable runtime identity" >&2
    exit 1
  fi
  headless_owned=true
else
  "$binary" "${mode_args[@]}" daemon status --json >"$run_root/daemon-before.json"
  if ! jq -e '.ok == true and .daemon.browser_mode == "headed" and .daemon.process_running == true and .daemon.runtime_socket_ready == true and .daemon.health.usable == true' "$run_root/daemon-before.json" >/dev/null; then
    echo "headed daemon is not already approved and usable; this lane will not repair it" >&2
    echo "inspect with: cdp --browser-mode headed daemon status --json" >&2
    exit 8
  fi
fi

"$binary" "${mode_args[@]}" pages --json >"$run_root/pages-before.json"
jq -e '.ok == true and (.pages | type == "array")' "$run_root/pages-before.json" >/dev/null

set +e
"$binary" "${mode_args[@]}" workflow web-research extract \
  --url-file "$url_file" \
  --out-dir "$out_dir" \
  --max-pages "$url_count" \
  --parallel "$parallel" \
  --wait "$wait_time" \
  --settle "$settle_time" \
  --json >"$report_json" 2>"$report_stderr"
workflow_code=$?
set -e
run_completed_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
if jq -e 'type == "object"' "$report_json" >/dev/null 2>&1; then
  jq -n \
    --arg completed_at "$run_completed_at" \
    --argjson workflow_exit_code "$workflow_code" \
    --slurpfile metadata "$metadata_json" \
    --slurpfile report "$report_json" \
    '$metadata[0] + {
      completed_at: $completed_at,
      workflow_exit_code: $workflow_exit_code,
      terminal_failure_class: (
        if $workflow_exit_code == 0 then null
        else ($report[0].error.code // $report[0].error.class // "workflow_exit_nonzero")
        end
      ),
      results: [
        ($report[0].pages // [])[] |
        {
          requested_url: .url,
          final_url: .report.workflow.final_url,
          quality_passed: .report.quality.passed,
          readiness_outcome: .report.readiness.outcome
        }
      ],
      failures: [
        ($report[0].failures // [])[] |
        {
          requested_url: .url,
          failure_class: .err_class,
          retryable: (.retryable // false),
          error: .error
        }
      ]
    }' >"$evidence_json"
else
  jq -n \
    --arg completed_at "$run_completed_at" \
    --argjson workflow_exit_code "$workflow_code" \
    --slurpfile metadata "$metadata_json" \
    '$metadata[0] + {
      completed_at: $completed_at,
      workflow_exit_code: $workflow_exit_code,
      terminal_failure_class: "invalid_workflow_output",
      results: [],
      failures: []
    }' >"$evidence_json"
fi
if [[ "$workflow_code" -ne 0 ]]; then
  echo "web-research extract exited $workflow_code" >&2
  sed -n '1,80p' "$report_stderr" >&2
  exit "$workflow_code"
fi

jq -e \
  --argjson count "$url_count" \
  --argjson min_success "$min_success" \
  --slurpfile expected "$expected_json" '
    .workflow.name == "web-research-extract" and
    .workflow.url_count == $count and
    .workflow.max_pages == $count and
    .workflow.remaining_url_count == 0 and
    .workflow.page_count == (.pages | length) and
    .workflow.failure_count == (.failures | length) and
    ([.pages[] | select(.report.quality.passed == true)] | length) >= $min_success and
    ([.pages[].url, .failures[].url] | unique | sort) == ($expected[0] | sort) and
    all(.pages[];
      .report.ok == true and
      .report.workflow.created_page == true and
      .report.workflow.reused_page == false and
      .report.workflow.closed == true and
      .report.workflow.close_error == "" and
      .report.workflow.cleanup.closed == true and
      .report.workflow.cleanup.target_id == .report.target.id and
      (.report.target.id | type == "string" and length > 0) and
      (.report.workflow.final_url | type == "string" and length > 0) and
      (.report.quality.passed | type == "boolean") and
      (.report.readiness.capture_consistency_checked | type == "boolean") and
      (.report.readiness.capture_consistent | type == "boolean") and
      (.report.artifacts.visible_json | type == "string" and length > 0) and
      (.report.artifacts.visible_txt | type == "string" and length > 0) and
      (.report.artifacts.html_json | type == "string" and length > 0) and
      (.report.artifacts.markdown | type == "string" and length > 0) and
      (.report.artifacts.links_json | type == "string" and length > 0)
    ) and
    all(.pages[] | select(.report.quality.passed == true);
      (.report.readiness.outcome | IN("settled", "load", "immediate")) and
      .report.readiness.capture_consistency_checked == true and
      .report.readiness.capture_consistent == true
    ) and
    all(.failures[];
      (.url | type == "string" and length > 0) and
      (.err_class | type == "string" and length > 0)
    ) and
    all(.failures[] | select(.err_class == "quality_gate_failed");
      .retryable == true and
      (.artifacts | type == "object") and
      (.artifacts.markdown | type == "string" and length > 0)
    )
  ' "$report_json" >/dev/null

while IFS=$'\t' read -r visible_json visible_txt html_json markdown links_json; do
  for artifact in "$visible_json" "$visible_txt" "$html_json" "$markdown" "$links_json"; do
    if [[ ! -s "$artifact" ]]; then
      echo "missing or empty page artifact: $artifact" >&2
      exit 1
    fi
  done
  jq -e '.snapshot and (.items | type == "array")' "$visible_json" >/dev/null
  jq -e '.html and (.html.items | type == "array")' "$html_json" >/dev/null
  jq -e '(.results | type == "array") and .count == (.results | length)' "$links_json" >/dev/null
done < <(jq -r '.pages[].report.artifacts | [.visible_json, .visible_txt, .html_json, .markdown, .links_json] | @tsv' "$report_json")

for artifact_key in page_quality_json failures_json retry_command; do
  artifact="$(jq -r --arg key "$artifact_key" '.artifacts[$key]' "$report_json")"
  if [[ ! -s "$artifact" ]]; then
    echo "missing or empty workflow artifact $artifact_key: $artifact" >&2
    exit 1
  fi
done
jq -e 'type == "array"' "$(jq -r '.artifacts.page_quality_json' "$report_json")" >/dev/null
jq -e 'type == "array"' "$(jq -r '.artifacts.failures_json' "$report_json")" >/dev/null

"$binary" "${mode_args[@]}" pages --json >"$run_root/pages-after.json"
jq -e --slurpfile before "$run_root/pages-before.json" --slurpfile report "$report_json" '
  ($before[0].pages | map(.id)) as $before_ids |
  (.pages | map(.id)) as $after_ids |
  ($report[0].pages | map(.report.target.id)) as $owned_ids |
  .ok == true and
  (($before_ids - $after_ids) | length) == 0 and
  (($after_ids - $before_ids) | length) == 0 and
  (($owned_ids - $after_ids) | length) == ($owned_ids | length)
' "$run_root/pages-after.json" >/dev/null

"$binary" "${mode_args[@]}" daemon status --json >"$run_root/daemon-after.json"
jq -e --slurpfile before "$run_root/daemon-before.json" '
  .ok == true and
  .daemon.runtime.pid == $before[0].daemon.runtime.pid and
  .daemon.runtime.started_at == $before[0].daemon.runtime.started_at and
  .daemon.runtime.socket_path == $before[0].daemon.runtime.socket_path
' "$run_root/daemon-after.json" >/dev/null

echo "live web-research E2E passed: $url_count URLs, $(jq -r '.workflow.page_count' "$report_json") pages, mode=$browser_mode"
