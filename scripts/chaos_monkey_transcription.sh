#!/usr/bin/env bash
# Bounded recovery proof for the cdp transcription service on macOS and Linux.
set -Eeuo pipefail

readonly name="$(basename "$0")"
readonly here="$(CDPATH= cd "$(dirname "$0")" && pwd -P)"
readonly launch_label="dev.pankaj.cdp.transcription"
readonly unit="cdp-transcription.service"
readonly model="whisper-1"
readonly old_time="1970-01-01T00:00:00Z"
readonly loopback="127.0.0.1"
readonly manager_wait=30
readonly health_wait_default=180
readonly request_wait_default=45

mode=check
scenario="${CDP_CHAOS_SCENARIO:-process-kill}"
scope="${CDP_CHAOS_SYSTEM_SCOPE:-}"
health_override="${CDP_CHAOS_HEALTH_URL:-}"
fixture_override="${CDP_CHAOS_FIXTURE:-}"
provider_override="${CDP_CHAOS_PROVIDER:-}"
state_provider="${CDP_CHAOS_STATE_PROVIDER:-microsoft-365-web}"
baseline_transcription=true
signal="${CDP_CHAOS_SIGNAL:-KILL}"
health_wait="${CDP_CHAOS_HEALTH_TIMEOUT:-$health_wait_default}"
request_wait="${CDP_CHAOS_REQUEST_TIMEOUT:-$request_wait_default}"
duration_ms="${CDP_CHAOS_DURATION_MS:-1500}"
service_label="${CDP_CHAOS_SERVICE_LABEL:-$launch_label}"
systemd_unit="${CDP_CHAOS_SYSTEMD_UNIT:-$unit}"

platform="" manager="" manager_scope="" manager_ref="" plist="" env_file=""
state_dir="" health_url="" fixture="" smoke_provider="" api_base=""
baseline_pid="" recovered_pid="" temp=""
chaos_active=0 service_stopped=0 state_mutated=0
state_files=() state_backups=()

usage() {
  cat <<EOF
Usage: $name [--check|--chaos] [options]

  --check                    Verify manager, /healthz, and real transcription.
  --chaos                    Run the selected recovery scenario.
  --scenario NAME            process-kill, process-term, service-restart,
                             service-down, state-expired, or all.
  --health-url URL           /healthz URL; required for a tunnel.
  --fixture PATH             WebM fixture for the real smoke request.
  --provider ID              Provider for the real smoke request.
  --state-provider ID        chatgpt-web, claude-web, gemini-web,
                             microsoft-365-web, or both (ChatGPT + M365).
  --skip-baseline-transcription
                             Rely on an outer real-provider gate before chaos.
  --system-scope             Use system systemd.
  --user-scope               Use user systemd as its service user.
  --health-timeout SECONDS   Recovery health budget (default: $health_wait_default).
  --request-timeout SECONDS  Transcription request budget (default: $request_wait_default).
  --duration-ms MILLISECONDS Fixture duration (default: 1500).
  --help                     Show this help.

Environment: CDP_CHAOS_HEALTH_URL, CDP_CHAOS_FIXTURE, CDP_CHAOS_PROVIDER,
CDP_CHAOS_STATE_PROVIDER, CDP_CHAOS_SCENARIO, CDP_CHAOS_SIGNAL,
CDP_CHAOS_SERVICE_LABEL, CDP_CHAOS_SYSTEMD_UNIT, CDP_CHAOS_SYSTEM_SCOPE,
CDP_CHAOS_HEALTH_TIMEOUT, CDP_CHAOS_REQUEST_TIMEOUT, CDP_CHAOS_DURATION_MS.

State-expired backs up the exact provider state required by transcription,
changes only captured_at, and restores it if repair cannot be proven. Unrelated
provider UI capability state is out of scope. No credentials, audio, or
unrelated process is deleted.
EOF
}

die() { printf '%s: %s\n' "$name" "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || die "required command is unavailable: $1"; }
positive() { [[ "$1" =~ ^[1-9][0-9]*$ ]]; }

parse() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --check) mode=check ;;
      --chaos) mode=chaos ;;
      --scenario|--health-url|--fixture|--provider|--state-provider|--health-timeout|--request-timeout|--duration-ms)
        [[ $# -gt 1 ]] || die "$1 needs a value"
        case "$1" in
          --scenario) scenario="$2"; mode=chaos ;;
          --health-url) health_override="$2" ;;
          --fixture) fixture_override="$2" ;;
          --provider) provider_override="$2" ;;
          --state-provider) state_provider="$2" ;;
          --health-timeout) health_wait="$2" ;;
          --request-timeout) request_wait="$2" ;;
          --duration-ms) duration_ms="$2" ;;
        esac
        shift ;;
      --system-scope) scope=system ;;
      --user-scope) scope=user ;;
      --skip-baseline-transcription) baseline_transcription=false ;;
      --help|-h) usage; exit 0 ;;
      *) die "unknown argument: $1 (use --help)" ;;
    esac
    shift
  done
  positive "$health_wait" || die "health timeout must be a positive integer"
  positive "$request_wait" || die "request timeout must be a positive integer"
  positive "$duration_ms" || die "duration-ms must be a positive integer"
  case "$scope" in ""|system|user) ;; *) die "system scope must be system or user" ;; esac
  case "$signal" in KILL|TERM) ;; *) die "CDP_CHAOS_SIGNAL must be KILL or TERM" ;; esac
  case "$scenario" in process-kill|process-term|service-restart|service-down|state-expired|all) ;; *) die "unknown chaos scenario: $scenario" ;; esac
  case "$state_provider" in
    microsoft-365-web|chatgpt-web|claude-web|gemini-web|both) ;;
    *) die "state provider must be chatgpt-web, claude-web, gemini-web, microsoft-365-web, or both" ;;
  esac
  if [[ -n "$provider_override" && ( "$scenario" == state-expired || "$scenario" == all ) && "$provider_override" != "$state_provider" ]]; then
    die "provider and state-provider must match for state expiry"
  fi
}

user_bus() {
  if [[ -z "${XDG_RUNTIME_DIR:-}" && -d "/run/user/$(id -u)" ]]; then
    export XDG_RUNTIME_DIR="/run/user/$(id -u)"
  fi
  if [[ -z "${DBUS_SESSION_BUS_ADDRESS:-}" && -S "${XDG_RUNTIME_DIR:-}/bus" ]]; then
    export DBUS_SESSION_BUS_ADDRESS="unix:path=$XDG_RUNTIME_DIR/bus"
  fi
}
systemd_user() { systemctl --user "$@"; }

setup_manager() {
  platform="$(uname -s)"
  case "$platform" in
    Darwin)
      need launchctl; need plutil; need ps
      manager=launchd; manager_scope=user
      manager_ref="gui/$(id -u)/$service_label"
      plist="$HOME/Library/LaunchAgents/$service_label.plist"
      ;;
    Linux)
      need systemctl; need ps; user_bus
      local system_ok=0 user_ok=0
      systemctl show "$systemd_unit" >/dev/null 2>&1 && system_ok=1
      systemd_user show "$systemd_unit" >/dev/null 2>&1 && user_ok=1
      case "$scope" in
        system) [[ $system_ok == 1 ]] || die "systemd unit is unavailable: $systemd_unit"; manager_scope=system ;;
        user) [[ $user_ok == 1 ]] || die "user systemd unit is unavailable or user bus is missing"; manager_scope=user ;;
        *)
          if [[ $system_ok == 1 && "$(id -u)" == 0 ]]; then manager_scope=system
          elif [[ $user_ok == 1 ]]; then manager_scope=user
          elif [[ $system_ok == 1 ]]; then manager_scope=system
          else die "could not find systemd unit $systemd_unit"; fi
          ;;
      esac
      manager=systemd; manager_ref="$systemd_unit"
      ;;
    *) die "unsupported platform $platform; use macOS or Linux" ;;
  esac
  manager_active || die "declared transcription service is not active before baseline"
}

manager_active() {
  if [[ "$manager" == launchd ]]; then launchctl print "$manager_ref" >/dev/null 2>&1
  elif [[ "$manager_scope" == system ]]; then systemctl is-active --quiet "$manager_ref"
  else systemd_user is-active --quiet "$manager_ref"; fi
}
manager_pid() {
  if [[ "$manager" == launchd ]]; then launchctl print "$manager_ref" 2>/dev/null | awk '$1 == "pid" && $2 == "=" {print $3; exit}'
  elif [[ "$manager_scope" == system ]]; then systemctl show "$manager_ref" -p MainPID --value 2>/dev/null
  else systemd_user show "$manager_ref" -p MainPID --value 2>/dev/null; fi
}
manager_state() {
  if [[ "$manager" == launchd ]]; then launchctl print "$manager_ref" 2>/dev/null | awk '$1 == "state" && $2 == "=" {print $3; exit}'
  elif [[ "$manager_scope" == system ]]; then systemctl show "$manager_ref" -p ActiveState --value 2>/dev/null
  else systemd_user show "$manager_ref" -p ActiveState --value 2>/dev/null; fi
}
launchd_bootstrap() {
  local domain="gui/$(id -u)" registered=false pid=""
  for _ in $(seq 1 10); do
    registered=false
    launchctl bootstrap "$domain" "$plist" >/dev/null 2>&1 && registered=true
    if [[ "$registered" == false ]]; then
      launchctl load "$plist" >/dev/null 2>&1 && registered=true
    fi
    if [[ "$registered" == true ]]; then
      for _ in $(seq 1 5); do
        pid="$(manager_pid || true)"
        if manager_active && positive "$pid" && kill -0 "$pid" 2>/dev/null; then return 0; fi
        sleep 1
      done
    fi
    sleep 1
  done
  return 1
}
manager_ctl() {
  case "$manager:$1" in
    launchd:restart)
      local old_pid=""
      old_pid="$(manager_pid || true)"
      launchctl bootout "$manager_ref"
      if positive "$old_pid"; then wait_stopped "$old_pid"; fi
      launchd_bootstrap
      ;;
    launchd:stop) launchctl bootout "$manager_ref" ;;
    launchd:start) launchd_bootstrap ;;
    systemd:*)
      if [[ "$manager_scope" == system ]]; then systemctl "$1" "$manager_ref"; else systemd_user "$1" "$manager_ref"; fi
      ;;
    *) return 1 ;;
  esac
}

service_env() {
  local key="$1" line=""
  if [[ "$manager" == launchd ]]; then plutil -extract "EnvironmentVariables.$key" raw -o - "$plist" 2>/dev/null || true; return; fi
  [[ -r "$env_file" ]] || return 0
  line="$(grep -E "^${key}=" "$env_file" | tail -n 1 || true)"
  line="${line#*=}"; line="${line#\"}"; line="${line%\"}"; line="${line#\'}"; line="${line%\'}"
  printf '%s' "$line"
}
setup_env() {
  if [[ "$manager" == launchd ]]; then env_file=""
  elif [[ "$manager_scope" == system ]]; then env_file="/etc/cdp-cli/transcription.env"
  else env_file="${XDG_CONFIG_HOME:-$HOME/.config}/cdp-cli/transcription.env"; fi
  if [[ -n "${CDP_CHAOS_ENV_FILE:-}" ]]; then env_file="$CDP_CHAOS_ENV_FILE"; fi
}

health_fetch() {
  case "$1" in
    https://*) curl -kfsS --max-time 5 -H 'Accept: application/json' "$1" ;;
    http://*) curl -fsS --max-time 5 -H 'Accept: application/json' "$1" ;;
    *) return 2 ;;
  esac
}
health_ok() {
  if command -v jq >/dev/null 2>&1; then printf '%s' "$1" | jq -e '.status == "ok"' >/dev/null 2>&1
  elif command -v python3 >/dev/null 2>&1; then printf '%s' "$1" | python3 -c 'import json,sys; raise SystemExit(json.load(sys.stdin).get("status") != "ok")'
  else printf '%s' "$1" | grep -Eq '"status"[[:space:]]*:[[:space:]]*"ok"'; fi
}
health_summary() {
  if command -v jq >/dev/null 2>&1; then
    printf '%s' "$1" | jq -c '{status,providers:[(.providers // [])[]|{provider,ready,probe_ready,file:(.file_probe.ready // null),realtime:(.realtime_probe.ready // null),reason:(.probe_reason // "")}]} ' 2>/dev/null || printf 'unparseable'
  else printf '%s' "$1" | sed -n 's/.*"status"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/status=\1/p'; fi
}
choose_health() {
  local candidate payload=""
  if [[ -n "$health_override" ]]; then
    health_url="${health_override%/}"; [[ "$health_url" == */healthz ]] || health_url="$health_url/healthz"
    payload="$(health_fetch "$health_url" 2>/dev/null || true)"
    [[ -n "$payload" ]] || die "health endpoint is unreachable: $health_url"
    if ! health_ok "$payload"; then
      wait_health || die "health endpoint stayed degraded: $health_url"
      payload="$(health_fetch "$health_url" 2>/dev/null || true)"
    fi
  else
    for candidate in "https://$loopback:28765/healthz" "http://$loopback:28765/healthz" "http://$loopback:28766/healthz"; do
      payload="$(health_fetch "$candidate" 2>/dev/null || true)"
      if [[ -n "$payload" ]]; then health_url="$candidate"; break; fi
    done
    [[ -n "$health_url" ]] || die 'no local /healthz endpoint responded; pass --health-url for a tunnel'
    if ! health_ok "$payload"; then
      wait_health || die "health endpoint stayed degraded: $health_url"
      payload="$(health_fetch "$health_url" 2>/dev/null || true)"
    fi
  fi
  printf 'health_url=%s health=%s\n' "$health_url" "$(health_summary "$payload")"
}
choose_fixture() {
  local dir=""
  if [[ -n "$fixture_override" ]]; then fixture="$fixture_override"
  else
    dir="$(service_env CDP_TRANSCRIPTION_FIXTURE_DIR)"
    if [[ -n "$dir" && -f "$dir/001.webm" ]]; then fixture="$dir/001.webm"
    elif [[ -n "$dir" ]]; then fixture="$(find "$dir" -maxdepth 1 -type f -name '*.webm' -print 2>/dev/null | sort | head -n 1 || true)"; fi
  fi
  [[ -n "$fixture" && -f "$fixture" && -r "$fixture" ]] || fixture="$here/../testdata/transcription-fixtures/001.webm"
  [[ -f "$fixture" && -r "$fixture" ]] || die 'real transcription fixture is missing; pass --fixture PATH'
  printf 'fixture=%s\n' "$fixture"
}
choose_provider() {
  smoke_provider="$provider_override"
  [[ -n "$smoke_provider" ]] || smoke_provider="$(service_env CDP_TRANSCRIPTION_PROVIDER)"
  [[ -n "$smoke_provider" ]] || smoke_provider=chatgpt-web
  printf 'smoke_provider=%s\n' "$smoke_provider"
}
assert_pid() {
  local pid="$1" command_line=""
  positive "$pid" || die "manager did not report a valid PID: $pid"
  kill -0 "$pid" 2>/dev/null || die "manager PID is not alive: $pid"
  command_line="$(ps -p "$pid" -o command= 2>/dev/null || true)"
  printf '%s\n' "$command_line" | grep -Eq 'transcription[[:space:]]+serve' || die "manager PID $pid is not cdp transcription serve"
}

real_transcription() {
  local response="$temp/transcription.json" status=""; local -a args
  api_base="${health_url%/healthz}"
  args=(-sS --max-time "$request_wait" -o "$response" -w '%{http_code}' -X POST "$api_base/v1/audio/transcriptions" -F "model=$model" -F "duration_ms=$duration_ms" -F "provider=$smoke_provider" -F "file=@$fixture")
  [[ "$health_url" == https://* ]] && args=(-k "${args[@]}")
  status="$(curl "${args[@]}")" || die "real transcription request could not reach $api_base"
  case "$status" in 2??) ;; *) printf 'transcription_http=%s\n' "$status" >&2; die 'real transcription failed' ;; esac
  if command -v jq >/dev/null 2>&1; then
    jq -e '(.text // "") | length > 0' "$response" >/dev/null || die 'real transcription returned no text'
    printf 'transcription=PASS provider=%s text_length=%s http=%s\n' "$smoke_provider" "$(jq -r '(.text // "") | length' "$response")" "$status"
  elif command -v python3 >/dev/null 2>&1; then
    python3 - "$response" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as f: text = json.load(f).get("text", "")
if not isinstance(text, str) or not text.strip(): raise SystemExit("real transcription returned no text")
print("text_length=" + str(len(text)))
PY
    printf 'transcription=PASS provider=%s http=%s\n' "$smoke_provider" "$status"
  else
    grep -Eq '"text"[[:space:]]*:[[:space:]]*"[^"].*"' "$response" || die 'real transcription returned no text'
    printf 'transcription=PASS provider=%s http=%s\n' "$smoke_provider" "$status"
  fi
}
baseline() {
  baseline_pid="$(manager_pid || true)"; assert_pid "$baseline_pid"
  printf 'manager=%s scope=%s state=%s pid=%s\n' "$manager" "$manager_scope" "$(manager_state)" "$baseline_pid"
  choose_health; choose_fixture; choose_provider
  if [[ "$baseline_transcription" == true ]]; then real_transcription; fi
}

wait_pid() {
  local old="${1:-}" new_only="${2:-0}" deadline="$(( $(date +%s) + manager_wait ))" current=""
  while [[ "$(date +%s)" -lt "$deadline" ]]; do
    current="$(manager_pid || true)"
    if positive "$current" && manager_active && kill -0 "$current" 2>/dev/null; then
      if [[ "$new_only" == 0 || "$current" != "$old" ]]; then recovered_pid="$current"; return 0; fi
    fi
    sleep 1
  done
  return 1
}
wait_stopped() {
  local old="$1" deadline="$(( $(date +%s) + manager_wait ))"
  while [[ "$(date +%s)" -lt "$deadline" ]]; do
    if ! manager_active && ! kill -0 "$old" 2>/dev/null; then return 0; fi
    sleep 1
  done
  return 1
}
wait_health() {
  local deadline="$(( $(date +%s) + health_wait ))" payload=""
  while [[ "$(date +%s)" -lt "$deadline" ]]; do
    payload="$(health_fetch "$health_url" 2>/dev/null || true)"
    if [[ -n "$payload" ]] && health_ok "$payload"; then printf 'health_recovery=PASS %s\n' "$(health_summary "$payload")"; return 0; fi
    sleep 1
  done
  printf 'health_last=%s\n' "$(health_summary "$payload")" >&2
  return 1
}

state_owner() { case "$platform" in Darwin) stat -f '%u:%g' "$1" ;; Linux) stat -c '%u:%g' "$1" ;; esac; }
state_mode() { case "$platform" in Darwin) stat -f '%Lp' "$1" ;; Linux) stat -c '%a' "$1" ;; esac; }
prepare_state() {
  local path provider_name backup index=0 configured=""; local -a required_providers
  need jq; need stat; need cp; need chmod; need chown; need mv
  configured="$(service_env CDP_TRANSCRIPTION_PROVIDERS)"
  [[ -n "$configured" ]] || die 'state-expired requires an explicit provider allowlist on the actual provider host'
  state_dir="$(service_env CDP_STATE_DIR)"; [[ -n "$state_dir" ]] || state_dir="${CDP_STATE_DIR:-$HOME/.cdp-cli}"
  [[ -d "$state_dir" ]] || die "state directory is missing: $state_dir"
  state_files=()
  case "$state_provider" in
    microsoft-365-web) state_files=("$state_dir/webagent/m365/auth-template.json" "$state_dir/webagent/m365/capabilities.json"); required_providers=(microsoft-365-web) ;;
    chatgpt-web) state_files=("$state_dir/webagent/chatgpt/request-template.json"); required_providers=(chatgpt-web) ;;
    claude-web) state_files=("$state_dir/webagent/claude/request-template.json"); required_providers=(claude-web) ;;
    gemini-web) state_files=("$state_dir/webagent/gemini/dictation-template.json"); required_providers=(gemini-web) ;;
    both) state_files=("$state_dir/webagent/m365/auth-template.json" "$state_dir/webagent/m365/capabilities.json" "$state_dir/webagent/chatgpt/request-template.json"); required_providers=(microsoft-365-web chatgpt-web) ;;
  esac
  for provider_name in "${required_providers[@]}"; do
    printf '%s' "$configured" | grep -Eq "(^|,)[[:space:]]*$provider_name([,]|$)" || die "state provider is not configured: $provider_name"
  done
  mkdir -p "$temp/state-backup"; state_backups=()
  for path in "${state_files[@]}"; do
    [[ -f "$path" && ! -L "$path" && -r "$path" ]] || die "state file must be a readable regular file: $path"
    local owner="$(state_owner "$path")"
    if [[ "$(id -u)" != 0 && "${owner%%:*}" != "$(id -u)" ]]; then die "state file is not owned by current user: $path"; fi
    provider_name="$(basename "$(dirname "$path")")"
    backup="$temp/state-backup/${provider_name}-${index}.json"; cp -p "$path" "$backup" || die "could not back up state: $path"
    state_backups+=("$backup"); index=$((index + 1)); printf 'state_target=%s\n' "$path"
  done
}
expire_state() {
  local path temporary_file mode_bits owner index=0
  for path in "${state_files[@]}"; do
    [[ -f "$path" && ! -L "$path" ]] || die "state path changed before expiration: $path"
    temporary_file="$temp/state-expired-$index.json"
    jq --arg captured_at "$old_time" '.captured_at = $captured_at' "$path" > "$temporary_file" || die "could not expire state: $path"
    mode_bits="$(state_mode "$path")"; owner="$(state_owner "$path")"
    chmod "$mode_bits" "$temporary_file" || die "could not preserve state mode: $path"
    if [[ "$(id -u)" == 0 ]]; then chown "$owner" "$temporary_file"; elif [[ "${owner%%:*}" != "$(id -u)" ]]; then die "state owner changed: $path"; fi
    mv -f "$temporary_file" "$path" || die "could not install expired state: $path"
    state_mutated=1; index=$((index + 1))
  done
  printf 'state_expired=PASS provider=%s files=%s\n' "$state_provider" "${#state_files[@]}"
}
state_repaired() {
  local path
  for path in "${state_files[@]}"; do jq -e --arg old "$old_time" '(.captured_at // "") != $old and (.captured_at // "") != ""' "$path" >/dev/null 2>&1 || return 1; done
}
wait_state() {
  local deadline="$(( $(date +%s) + health_wait ))"
  while [[ "$(date +%s)" -lt "$deadline" ]]; do
    if state_repaired; then printf 'state_recovery=PASS provider=%s\n' "$state_provider"; return 0; fi
    sleep 1
  done
  return 1
}
restore_state() {
  local index=0 path
  [[ "${#state_files[@]}" == "${#state_backups[@]}" ]] || return 1
  for path in "${state_files[@]}"; do
    [[ ! -L "$path" ]] || return 1
    cp -p "${state_backups[$index]}" "$path" || return 1
    index=$((index + 1))
  done
}

process_failure() {
  local kind="$1" label="$2" started recovered
  assert_pid "$baseline_pid"; printf 'chaos_target=pid:%s signal=SIG%s scenario=%s\n' "$baseline_pid" "$kind" "$label"
  started="$(date +%s)"; chaos_active=1; kill -"$kind" "$baseline_pid" || die 'could not signal exact transcription PID'
  wait_pid "$baseline_pid" 1 || die "manager did not recreate transcription PID within ${manager_wait}s"
  recovered="$(date +%s)"; printf 'manager_recovery=PASS scenario=%s old_pid=%s new_pid=%s seconds=%s\n' "$label" "$baseline_pid" "$recovered_pid" "$((recovered - started))"
  wait_health || die "health did not recover within ${health_wait}s"; real_transcription; chaos_active=0
  printf 'scenario=PASS name=%s\n' "$label"
}
restart_failure() {
  local started recovered
  assert_pid "$baseline_pid"; printf 'chaos_target=manager:%s scenario=service-restart\n' "$manager_ref"
  started="$(date +%s)"; chaos_active=1; manager_ctl restart || die 'manager restart failed'
  wait_pid "$baseline_pid" 1 || die "manager restart did not produce a new PID within ${manager_wait}s"
  recovered="$(date +%s)"; printf 'manager_recovery=PASS scenario=service-restart old_pid=%s new_pid=%s seconds=%s\n' "$baseline_pid" "$recovered_pid" "$((recovered - started))"
  wait_health || die 'health did not recover after manager restart'; real_transcription; chaos_active=0
  printf 'scenario=PASS name=service-restart\n'
}
down_failure() {
  local started recovered
  assert_pid "$baseline_pid"; printf 'chaos_target=manager:%s scenario=service-down\n' "$manager_ref"
  started="$(date +%s)"; chaos_active=1; service_stopped=1; manager_ctl stop || die 'could not stop service'
  wait_stopped "$baseline_pid" || die "service did not stop within ${manager_wait}s"
  manager_ctl start || die 'could not start service'; wait_pid '' 0 || die "service did not start within ${manager_wait}s"
  service_stopped=0; recovered="$(date +%s)"; printf 'manager_recovery=PASS scenario=service-down old_pid=%s new_pid=%s seconds=%s\n' "$baseline_pid" "$recovered_pid" "$((recovered - started))"
  wait_health || die 'health did not recover after service down/up'; real_transcription; chaos_active=0
  printf 'scenario=PASS name=service-down\n'
}
state_failure() {
  local started recovered
  assert_pid "$baseline_pid"; prepare_state; printf 'chaos_target=state:%s scenario=state-expired\n' "$state_provider"
  started="$(date +%s)"; chaos_active=1; service_stopped=1; manager_ctl stop || die 'could not stop service before state expiration'
  wait_stopped "$baseline_pid" || die 'service did not stop before state expiration'; expire_state
  manager_ctl start || die 'could not start service after state expiration'; wait_pid '' 0 || die 'service did not start after state expiration'
  service_stopped=0; recovered="$(date +%s)"; printf 'manager_recovery=PASS scenario=state-expired old_pid=%s new_pid=%s seconds=%s\n' "$baseline_pid" "$recovered_pid" "$((recovered - started))"
  wait_state || die "provider lifecycle did not repair state within ${health_wait}s"; wait_health || die 'health did not recover after state expiration'; real_transcription
  state_mutated=0; chaos_active=0; printf 'scenario=PASS name=state-expired\n'
}
one() {
  printf 'scenario_start=%s\n' "$1"; baseline
  case "$1" in
    process-kill) process_failure "$signal" process-kill ;;
    process-term) process_failure TERM process-term ;;
    service-restart) restart_failure ;;
    service-down) down_failure ;;
    state-expired) state_failure ;;
    *) die "unsupported scenario: $1" ;;
  esac
}
chaos() {
  case "$scenario" in
    all) one process-term; one process-kill; one service-restart; one service-down; one state-expired ;;
    *) one "$scenario" ;;
  esac
  printf 'chaos=PASS platform=%s manager=%s scope=%s scenarios=%s\n' "$platform" "$manager" "$manager_scope" "$scenario"
}

cleanup() {
  local status="$?"
  set +e
  if [[ $state_mutated == 1 ]]; then
    printf '%s\n' 'chaos interrupted after state mutation; restoring exact backups' >&2
    manager_active && manager_ctl stop >/dev/null 2>&1 || true
    restore_state || printf '%s\n' 'state restore failed; inspect the exact state files' >&2
    manager_ctl start >/dev/null 2>&1 || true; wait_pid '' 0 >/dev/null 2>&1 || true
  fi
  if [[ $service_stopped == 1 ]]; then
    printf '%s\n' 'chaos interrupted with service stopped; starting exact service' >&2
    manager_ctl start >/dev/null 2>&1 || true; wait_pid '' 0 >/dev/null 2>&1 || true
  fi
  if [[ $chaos_active == 1 ]]; then manager_ctl restart >/dev/null 2>&1 || true; fi
  [[ -z "$temp" || ! -d "$temp" ]] || rm -rf "$temp"
  exit "$status"
}
main() {
  parse "$@"; need curl; need awk; need grep; need sed; need find
  temp="$(mktemp -d "${TMPDIR:-/tmp}/cdp-transcription-chaos.XXXXXX")"
  trap cleanup EXIT INT TERM HUP
  setup_manager; setup_env
  if [[ "$mode" == chaos ]]; then chaos; else baseline; printf 'check=PASS platform=%s manager=%s scope=%s\n' "$platform" "$manager" "$manager_scope"; fi
}
main "$@"
