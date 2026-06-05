#!/usr/bin/env bash
set -euo pipefail

binary="${1:-./bin/cdp}"

if [[ ! -x "$binary" ]]; then
  echo "missing executable: $binary" >&2
  exit 2
fi

state_dir="$(mktemp -d)"
config_dir="$state_dir/config"
export XDG_CONFIG_HOME="$config_dir"
trap 'rm -rf "$state_dir"' EXIT

"$binary" --help >/tmp/cdp-cli-help.txt
"$binary" version --json | jq -e '.version and .commit and .date' >/dev/null
"$binary" version --json --compact | jq -e '.version and .commit and .date' >/dev/null
"$binary" describe --json | jq -e '.ok == true and (.commands.children | length > 5)' >/dev/null
"$binary" describe --jq '.globals | index("--json")' >/dev/null
"$binary" describe --jq '.globals | index("--compact")' >/dev/null
"$binary" describe --jq '.globals | index("--connection")' >/dev/null
"$binary" describe --jq '.globals | index("--browser-mode")' >/dev/null
"$binary" describe --jq '.globals | index("--browserMode")' >/dev/null
"$binary" describe --command "version" --json | jq -e '.ok == true and .commands.name == "version" and (.commands.examples | any(contains("version --json")))' >/dev/null
"$binary" describe --command "pages" --json | jq -e '.ok == true and .commands.name == "pages" and (.commands.flags[] | select(.name == "title-contains" and .type == "string"))' >/dev/null
"$binary" describe --command "daemon start" --json | jq -e '.ok == true and .commands.name == "start" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "daemon status" --json | jq -e '.ok == true and .commands.name == "status" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "daemon stop" --json | jq -e '.ok == true and .commands.name == "stop" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "daemon restart" --json | jq -e '.ok == true and .commands.name == "restart" and (.commands.examples | any(contains("--autoConnect")))' >/dev/null
"$binary" describe --command "daemon keepalive" --json | jq -e '.ok == true and .commands.name == "keepalive" and (.commands.examples | any(contains("--browser-mode headed"))) and (.commands.examples | any(contains("--browser-mode headless"))) and (.commands.examples | any(contains("cdp cron install --profile agent")))' >/dev/null
"$binary" describe --command "daemon logs" --json | jq -e '.ok == true and .commands.name == "logs" and (.commands.examples | any(contains("--tail")))' >/dev/null
"$binary" describe --command "cron install" --json | jq -e '.ok == true and .commands.name == "install" and (.commands.examples | any(contains("--profile agent"))) and (.commands.examples | any(contains("--dry-run"))) and (.commands.flags[] | select(.name == "dry-run"))' >/dev/null
"$binary" describe --command "cron migrate pages-polling" --json | jq -e '.ok == true and .commands.name == "pages-polling" and (.commands.examples | any(contains("migrate pages-polling --json"))) and (.commands.examples | any(contains("--apply"))) and (.commands.flags[] | select(.name == "apply"))' >/dev/null
"$binary" describe --command "cron heal headed" --json | jq -e '.ok == true and .commands.name == "headed" and (.commands.examples | any(contains("--reconnect 30s")))' >/dev/null
"$binary" describe --command "doctor" --json | jq -e '.ok == true and .commands.name == "doctor" and (.commands.examples | any(contains("scheduled-tasks")))' >/dev/null
"$binary" describe --command "browser mode get" --json | jq -e '.ok == true and .commands.name == "get" and (.commands.examples | any(contains("--browser-mode headless")))' >/dev/null
"$binary" describe --command "browser profile status" --json | jq -e '.ok == true and .commands.name == "status" and .commands.short == "Show managed headless browser profile status" and (.commands.examples | any(contains("--browser-mode headless")))' >/dev/null
"$binary" describe --command "browser profile seed" --json | jq -e '.ok == true and .commands.name == "seed" and .commands.short == "Create managed headless browser profile metadata" and (.commands.examples | any(contains("--browser-mode headless"))) and (.commands.examples | any(contains("--strategy managed"))) and (.commands.examples | any(contains("--strategy copy-default"))) and (.commands.flags[] | select(.name == "strategy"))' >/dev/null
"$binary" describe --command "connection" --json | jq -e '.ok == true and .commands.name == "connection" and (.commands.examples | any(contains("connection list")))' >/dev/null
"$binary" describe --command "connection add" --json | jq -e '.ok == true and .commands.name == "add" and (.commands.examples | any(contains("--auto-connect")))' >/dev/null
"$binary" describe --command "connection select" --json | jq -e '.ok == true and .commands.name == "select" and (.commands.examples | any(contains("connection select")))' >/dev/null
"$binary" describe --command "connection current" --json | jq -e '.ok == true and .commands.name == "current" and (.commands.examples | any(contains("connection current")))' >/dev/null
"$binary" doctor --state-dir "$state_dir" --json | jq -e '.ok == true and (.checks | length >= 3)' >/dev/null
"$binary" doctor --check daemon --state-dir "$state_dir" --json | jq -e '.ok == true and (.checks | length == 1) and .checks[0].name == "daemon"' >/dev/null
"$binary" doctor --check scheduled-tasks --state-dir "$state_dir" --json | jq -e '.checks | length == 1 and .[0].name == "scheduled-tasks" and .[0].details.source == "crontab -l" and (.[0].details.has_headed_daemon_keepalive | type == "boolean") and (.[0].details.has_headless_daemon_keepalive | type == "boolean") and (.[0].details.has_pages_polling_keepalive | type == "boolean") and (.[0].details.pages_polling_count | type == "number") and (.[0].details.has_ambiguous_page_cleanup | type == "boolean") and (.[0].details.has_unflocked_cdp_task | type == "boolean") and (.[0].next_commands | index("cdp cron status --json")) and (.[0].next_commands | index("cdp cron diff --json")) and (.[0].next_commands | index("cdp cron install --profile agent --json"))' >/dev/null
"$binary" doctor --capabilities --json | jq -e '.ok == true and (.capabilities | map(.name) | index("raw_protocol"))' >/dev/null
"$binary" doctor --capabilities --json | jq -e '.ok == true and (.capabilities[] | select(.name == "advanced_storage" and .status == "implemented"))' >/dev/null
"$binary" doctor --capabilities --json | jq -e '.ok == true and (.capabilities[] | select(.name == "raw_protocol" and (.verify_commands | index("cdp protocol metadata --json"))))' >/dev/null
"$binary" doctor --capabilities --json | jq -e '.ok == true and (.capabilities[] | select(.name == "artifacts" and (.evidence_commands | index("cdp workflow debug-bundle --out-dir tmp/debug-bundle --json"))))' >/dev/null
"$binary" doctor --capabilities --json | jq -e '.ok == true and (.capabilities[] | select(.name == "accessibility" and .status == "implemented" and (.verify_commands | index("cdp a11y tree --json"))))' >/dev/null
"$binary" doctor --capabilities --json | jq -e '.ok == true and (.capabilities[] | select(.name == "performance" and .status == "implemented" and (.evidence_commands | index("cdp workflow perf '\''https://example.com'\'' --wait 1s --trace tmp/perf.local.json --json"))))' >/dev/null
"$binary" doctor --capabilities --json | jq -e '.ok == true and (.capabilities[] | select(.name == "memory" and .status == "implemented" and (.verify_commands | index("cdp memory counters --json"))))' >/dev/null
"$binary" doctor --capabilities --json | jq -e '.ok == true and (.capabilities[] | select(.name == "emulation" and .status == "implemented" and (.verify_commands | index("cdp emulate user-agent --help")) and (.verify_commands | index("cdp emulate cpu --help")) and (.verify_commands | index("cdp emulate network --help"))))' >/dev/null
"$binary" doctor --capabilities --json | jq -e '.ok == true and ([.capabilities[] | select(.name == "network_throttling")] | length == 0)' >/dev/null
"$binary" doctor --capabilities --json | jq -e '.ok == true and (.bootstrap_path.validate_commands | index("cdp daemon health --json")) and (.bootstrap_path.validate_commands | index("cdp cron status --json")) and (.bootstrap_path.recover_commands | index("cdp daemon logs --tail 50 --json")) and (.bootstrap_path.recover_commands | index("cdp cron diff --json")) and (.bootstrap_path.stop_signals | index("human_required"))' >/dev/null
"$binary" doctor --capabilities --json | jq -e '.ok == true and (.bootstrap_path.validate_commands | index("cdp doctor --check scheduled-tasks --json"))' >/dev/null
"$binary" doctor --capabilities --json | jq -e '.ok == true and (.bootstrap_path.validate_commands | index("cdp doctor --check headless-security --json"))' >/dev/null
"$binary" explain-error not_implemented --json | jq -e '.ok == true and .error.exit_code == 8' >/dev/null
"$binary" exit-codes --json | jq -e '.ok == true and (.exit_codes | map(.name) | index("not_implemented"))' >/dev/null
"$binary" schema error-envelope --json | jq -e '.ok == true and .schema.name == "error-envelope"' >/dev/null
"$binary" schema describe --json | jq -e '.ok == true and .schema.name == "describe" and (.schema.fields | map(.name) | index("commands"))' >/dev/null
"$binary" schema doctor --json | jq -e '.ok == true and .schema.name == "doctor" and (.schema.fields | map(.name) | index("checks"))' >/dev/null
"$binary" schema doctor-capabilities --json | jq -e '.ok == true and .schema.name == "doctor-capabilities" and (.schema.fields | map(.name) | index("capabilities")) and (.schema.fields | map(.name) | index("bootstrap_path"))' >/dev/null
"$binary" schema scheduled-tasks --json | jq -e '.ok == true and .schema.name == "scheduled-tasks" and (.schema.fields | map(.name) | index("details")) and (.schema.fields[] | select(.name == "details").description | contains("legacy pages polling")) and (.schema.fields | map(.name) | index("next_commands"))' >/dev/null
"$binary" schema cron --json | jq -e '.ok == true and .schema.name == "cron" and (.schema.fields | map(.name) | index("next_commands")) and (.schema.fields | map(.name) | index("browser_mode")) and (.schema.fields | map(.name) | index("dry_run"))' >/dev/null
"$binary" schema cron-migrate-pages-polling --json | jq -e '.ok == true and .schema.name == "cron-migrate-pages-polling" and (.schema.fields | map(.name) | index("candidate_count")) and (.schema.fields | map(.name) | index("managed_keepalive_installed")) and (.schema.fields | map(.name) | index("next_commands"))' >/dev/null
"$binary" schema headless-security --json | jq -e '.ok == true and .schema.name == "headless-security" and (.schema.fields | map(.name) | index("browser_mode")) and (.schema.fields | map(.name) | index("details")) and (.schema.fields | map(.name) | index("next_commands"))' >/dev/null
"$binary" schema version --json | jq -e '.ok == true and .schema.name == "version" and (.schema.fields | map(.name) | index("version"))' >/dev/null
"$binary" schema pages --json | jq -e '.ok == true and .schema.name == "pages" and (.schema.fields | map(.name) | index("pages")) and (.schema.fields | map(.name) | index("budget"))' >/dev/null
"$binary" schema targets --json | jq -e '.ok == true and .schema.name == "targets" and (.schema.fields | map(.name) | index("targets"))' >/dev/null
"$binary" schema open --json | jq -e '.ok == true and .schema.name == "open" and (.schema.fields | map(.name) | index("page"))' >/dev/null
"$binary" schema eval --json | jq -e '.ok == true and .schema.name == "eval" and (.schema.fields | map(.name) | index("result"))' >/dev/null
"$binary" schema page-action --json | jq -e '.ok == true and .schema.name == "page-action" and (.schema.fields | map(.name) | index("action"))' >/dev/null
"$binary" schema snapshot --json | jq -e '.ok == true and .schema.name == "snapshot" and (.schema.fields | map(.name) | index("warnings")) and (.schema.fields | map(.name) | index("diagnostics"))' >/dev/null
"$binary" schema connection-add --json | jq -e '.ok == true and .schema.name == "connection-add" and (.schema.fields | map(.name) | index("connection"))' >/dev/null
"$binary" schema connection-list --json | jq -e '.ok == true and .schema.name == "connection-list" and (.schema.fields | map(.name) | index("connections"))' >/dev/null
"$binary" schema connection-select --json | jq -e '.ok == true and .schema.name == "connection-select" and (.schema.fields | map(.name) | index("connection"))' >/dev/null
"$binary" schema connection-current --json | jq -e '.ok == true and .schema.name == "connection-current" and (.schema.fields | map(.name) | index("connection")) and (.schema.fields | map(.name) | index("effective_connection")) and (.schema.fields | map(.name) | index("connection_matches_effective"))' >/dev/null
"$binary" schema connection-remove --json | jq -e '.ok == true and .schema.name == "connection-remove" and (.schema.fields | map(.name) | index("removed"))' >/dev/null
"$binary" schema connection-prune --json | jq -e '.ok == true and .schema.name == "connection-prune" and (.schema.fields | map(.name) | index("removed"))' >/dev/null
"$binary" schema browser-mode --json | jq -e '.ok == true and .schema.name == "browser-mode" and (.schema.fields | map(.name) | index("browser_mode")) and (.schema.fields | map(.name) | index("browser_mode_source"))' >/dev/null
"$binary" schema browser-profile-status --json | jq -e '.ok == true and .schema.name == "browser-profile-status" and (.schema.fields | map(.name) | index("managed_browser")) and (.schema.fields | map(.name) | index("profile_perm")) and (.schema.fields | map(.name) | index("metadata_perm")) and (.schema.fields | map(.name) | index("next_commands"))' >/dev/null
"$binary" schema browser-profile-seed --json | jq -e '.ok == true and .schema.name == "browser-profile-seed" and (.schema.fields | map(.name) | index("browser_mode")) and (.schema.fields | map(.name) | index("seed_strategy")) and (.schema.fields | map(.name) | index("managed_browser")) and (.schema.fields | map(.name) | index("default_profile_copied"))' >/dev/null
"$binary" schema managed-browser --json | jq -e '.ok == true and .schema.name == "managed-browser" and (.schema.fields | map(.name) | index("user_data_dir")) and (.schema.fields | map(.name) | index("profile_seed_strategy"))' >/dev/null
"$binary" schema connection-resolve --json | jq -e '.ok == true and .schema.name == "connection-resolve" and (.schema.fields | map(.name) | index("source")) and (.schema.fields | map(.name) | index("browser_mode")) and (.schema.fields | map(.name) | index("browser_mode_source"))' >/dev/null
"$binary" schema protocol-exec --json | jq -e '.ok == true and .schema.name == "protocol-exec" and (.schema.fields | map(.name) | index("scope")) and (.schema.fields | map(.name) | index("artifact"))' >/dev/null
"$binary" schema protocol-examples --json | jq -e '.ok == true and .schema.name == "protocol-examples" and (.schema.fields[] | select(.name == "examples").description | contains("required/optional param names"))' >/dev/null
"$binary" schema protocol-metadata --json | jq -e '.ok == true and .schema.name == "protocol-metadata"' >/dev/null
"$binary" schema protocol-domains --json | jq -e '.ok == true and .schema.name == "protocol-domains"' >/dev/null
"$binary" schema protocol-search --json | jq -e '.ok == true and .schema.name == "protocol-search"' >/dev/null
"$binary" schema protocol-describe --json | jq -e '.ok == true and .schema.name == "protocol-describe"' >/dev/null
"$binary" schema daemon-restart --json | jq -e '.ok == true and .schema.name == "daemon-restart" and (.schema.fields | map(.name) | index("restart"))' >/dev/null
"$binary" schema daemon-keepalive --json | jq -e '.ok == true and .schema.name == "daemon-keepalive" and (.schema.fields | map(.name) | index("browser_mode")) and (.schema.fields | map(.name) | index("lock"))' >/dev/null
"$binary" schema daemon-status --json | jq -e '.ok == true and .schema.name == "daemon-status" and (.schema.fields | map(.name) | index("daemon"))' >/dev/null
"$binary" schema daemon-logs --json | jq -e '.ok == true and .schema.name == "daemon-logs" and (.schema.fields | map(.name) | index("browser_mode")) and (.schema.fields | map(.name) | index("entries"))' >/dev/null
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
CDP_FAKE_CRONTAB="$fake_crontab_store" CDP_CRONTAB_BIN="$fake_crontab_bin" "$binary" cron install --dry-run --state-dir "$state_dir" --json | jq -e '.ok == true and .dry_run == true and (.warnings | any(contains("unmanaged cdp pages polling")))' >/dev/null
CDP_FAKE_CRONTAB="$fake_crontab_store" CDP_CRONTAB_BIN="$fake_crontab_bin" "$binary" cron migrate pages-polling --state-dir "$state_dir" --json | jq -e '.ok == true and .action == "would_remove" and .dry_run == true and .applied == false and .candidate_count == 1 and .removed_count == 0 and .managed_keepalive_installed == false and (.warnings | any(contains("managed daemon keepalive is not installed")))' >/dev/null
CDP_FAKE_CRONTAB="$fake_crontab_store" CDP_CRONTAB_BIN="$fake_crontab_bin" "$binary" cron install --profile agent --state-dir "$state_dir" --json | jq -e '.ok == true and .changed == true and (.warnings | any(contains("unmanaged cdp pages polling"))) and (.managed_block.entries | length == 5)' >/dev/null
CDP_FAKE_CRONTAB="$fake_crontab_store" CDP_CRONTAB_BIN="$fake_crontab_bin" "$binary" cron migrate pages-polling --apply --state-dir "$state_dir" --json | jq -e '.ok == true and .action == "removed" and .dry_run == false and .applied == true and .candidate_count == 1 and .removed_count == 1 and .managed_keepalive_installed == true and (.removed_entries | length == 1)' >/dev/null
rg -q '^0 0 \* \* \* /usr/local/bin/backup$' "$fake_crontab_store"
rg -q 'cdp-cli managed browser runtime tasks' "$fake_crontab_store"
rg -q -- '--browser-mode headed daemon keepalive --auto-connect --repair --probe passive' "$fake_crontab_store"
! rg -q 'cdp pages --browser-mode headed' "$fake_crontab_store"
cat >"$fake_crontab_store" <<'EOF_CRONTAB'
SHELL=/bin/sh
0 0 * * * /usr/local/bin/backup
EOF_CRONTAB
CDP_FAKE_CRONTAB="$fake_crontab_store" CDP_CRONTAB_BIN="$fake_crontab_bin" "$binary" cron status --state-dir "$state_dir" --json | jq -e '.ok == true and .installed == false and (.intended_block.entries | length == 5)' >/dev/null
CDP_FAKE_CRONTAB="$fake_crontab_store" CDP_CRONTAB_BIN="$fake_crontab_bin" "$binary" cron diff --state-dir "$state_dir" --json | jq -e '.ok == true and .installed == false and .actions[0].action == "append_managed_block"' >/dev/null
CDP_FAKE_CRONTAB="$fake_crontab_store" CDP_CRONTAB_BIN="$fake_crontab_bin" "$binary" --browser-mode headed cron install --profile agent --dry-run --state-dir "$state_dir" --json | jq -e '.ok == true and .dry_run == true and .changed == true and .installed == false and (.intended_block.entries | length == 1) and (.intended_block.entries[0] | contains("daemon keepalive --auto-connect --repair --probe passive"))' >/dev/null
CDP_FAKE_CRONTAB="$fake_crontab_store" CDP_CRONTAB_BIN="$fake_crontab_bin" "$binary" cron install --profile agent --state-dir "$state_dir" --json | jq -e '.ok == true and .changed == true and (.managed_block.entries | length == 5)' >/dev/null
CDP_FAKE_CRONTAB="$fake_crontab_store" CDP_CRONTAB_BIN="$fake_crontab_bin" "$binary" cron install --profile agent --state-dir "$state_dir" --json | jq -e '.ok == true and .changed == false and .action == "unchanged"' >/dev/null
rg -q '^SHELL=/bin/sh$' "$fake_crontab_store"
rg -q -- '--browser-mode headed daemon keepalive --auto-connect --repair --probe passive' "$fake_crontab_store"
! rg -q 'cron heal headed' "$fake_crontab_store"
rg -q 'command -v flock' "$fake_crontab_store"
rg -q -- '--strategy managed' "$fake_crontab_store"
! rg -q -e '/usr/bin/flock -n' -e '--strategy copy-default' "$fake_crontab_store"
CDP_FAKE_CRONTAB="$fake_crontab_store" CDP_CRONTAB_BIN="$fake_crontab_bin" "$binary" cron remove --state-dir "$state_dir" --json | jq -e '.ok == true and .changed == true and .removed == true' >/dev/null
! rg -q 'cdp-cli managed browser runtime tasks' "$fake_crontab_store"
rg -q '^0 0 \* \* \* /usr/local/bin/backup$' "$fake_crontab_store"

"$binary" schema daemon-health --json | jq -e '.ok == true and .schema.name == "daemon-health" and (.schema.fields | map(.name) | index("health"))' >/dev/null
"$binary" describe --command "open" --json | jq -e '.ok == true and .commands.name == "open" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "click" --json | jq -e '.ok == true and .commands.name == "click" and (.commands.examples | any(contains("--by role"))) and (.commands.examples | any(contains("--trial"))) and (.commands.examples | any(contains("--force"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "trial")) and (.commands.flags[] | select(.name == "force"))' >/dev/null
"$binary" describe --command "fill" --json | jq -e '.ok == true and .commands.name == "fill" and (.commands.examples | any(contains("--by label"))) and (.commands.examples | any(contains("--trial"))) and (.commands.examples | any(contains("--force"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "trial")) and (.commands.flags[] | select(.name == "force"))' >/dev/null
"$binary" describe --command "type" --json | jq -e '.ok == true and .commands.name == "type" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "press" --json | jq -e '.ok == true and .commands.name == "press" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "hover" --json | jq -e '.ok == true and .commands.name == "hover" and (.commands.examples | any(contains("--by role"))) and (.commands.examples | any(contains("--trial"))) and (.commands.examples | any(contains("--force"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "trial")) and (.commands.flags[] | select(.name == "force"))' >/dev/null
"$binary" describe --command "drag" --json | jq -e '.ok == true and .commands.name == "drag" and (.commands.examples | any(contains("--by test-id"))) and (.commands.examples | any(contains("--trial"))) and (.commands.examples | any(contains("--force"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "trial")) and (.commands.flags[] | select(.name == "force"))' >/dev/null
"$binary" describe --command "select" --json | jq -e '.ok == true and .commands.name == "select" and (.commands.examples | any(contains("--by label"))) and (.commands.examples | any(contains("--trial"))) and (.commands.examples | any(contains("--force"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "trial")) and (.commands.flags[] | select(.name == "force"))' >/dev/null
"$binary" describe --command "check" --json | jq -e '.ok == true and .commands.name == "check" and (.commands.examples | any(contains("--by label"))) and (.commands.examples | any(contains("--by role"))) and (.commands.examples | any(contains("--trial"))) and (.commands.examples | any(contains("--force"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "trial")) and (.commands.flags[] | select(.name == "force"))' >/dev/null
"$binary" describe --command "uncheck" --json | jq -e '.ok == true and .commands.name == "uncheck" and (.commands.examples | any(contains("--by label"))) and (.commands.examples | any(contains("--by role"))) and (.commands.examples | any(contains("--trial"))) and (.commands.examples | any(contains("--force"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "trial")) and (.commands.flags[] | select(.name == "force"))' >/dev/null
"$binary" describe --command "frames" --json | jq -e '.ok == true and .commands.name == "frames" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "pages" --json | jq -e '.ok == true and .commands.name == "pages" and (.commands.examples | any(contains("--title-contains")))' >/dev/null
"$binary" describe --command "eval" --json | jq -e '.ok == true and .commands.name == "eval" and (.commands.examples | any(contains("--title-contains")))' >/dev/null
"$binary" describe --command "observe" --json | jq -e '.ok == true and .commands.name == "observe" and (.commands.examples | any(contains("observe")))' >/dev/null
"$binary" schema observe --json | jq -e '.ok == true and .schema.name == "observe"' >/dev/null
"$binary" describe --command "page select" --json | jq -e '.ok == true and .commands.name == "select" and (.commands.examples | any(contains("--url-contains")))' >/dev/null
"$binary" describe --command "page reload" --json | jq -e '.ok == true and .commands.name == "reload" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "page back" --json | jq -e '.ok == true and .commands.name == "back" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "page forward" --json | jq -e '.ok == true and .commands.name == "forward" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "page activate" --json | jq -e '.ok == true and .commands.name == "activate" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "page close" --json | jq -e '.ok == true and .commands.name == "close" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "page cleanup" --json | jq -e '.ok == true and .commands.name == "cleanup" and (.commands.examples | any(contains("--close")))' >/dev/null
"$binary" schema page-cleanup --json | jq -e '.ok == true and .schema.name == "page-cleanup" and (.schema.fields | map(.name) | index("candidates"))' >/dev/null
"$binary" describe --json | jq -e '.commands.children[] | select(.name == "page") | .children[] | select(.name == "cleanup")' >/dev/null
help_output="$("$binary" --help)"
rg -q "cleanup routine|page cleanup|clean" <<<"$help_output"
page_cleanup_describe="$("$binary" describe --command "page cleanup" --json)"
page_cleanup_examples="$(jq -r '.commands.examples[]' <<<"$page_cleanup_describe")"
rg -q -- '--browser-mode headed page cleanup' <<<"$page_cleanup_examples"
rg -q -- '--browser-mode headless page cleanup --created-by cdp --idle-for 30m --close --force' <<<"$page_cleanup_examples"
page_cleanup_short="$(jq -r '.commands.short' <<<"$page_cleanup_describe")"
rg -q 'cron cleanup' <<<"$page_cleanup_short"
"$binary" describe --command "page cleanup" --json | jq -e '.commands.flags[] | select(.name == "max")' >/dev/null
"$binary" describe --command "text" --json | jq -e '.ok == true and .commands.name == "text" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "locator find" --json | jq -e '.ok == true and .commands.name == "find" and (.commands.examples | any(contains("--by role"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role"))' >/dev/null
"$binary" describe --command "html" --json | jq -e '.ok == true and .commands.name == "html" and (.commands.examples | any(contains("--diagnose-empty"))) and (.commands.flags[] | select(.name == "diagnose-empty"))' >/dev/null
"$binary" describe --command "dom query" --json | jq -e '.ok == true and .commands.name == "query" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "css inspect" --json | jq -e '.ok == true and .commands.name == "inspect" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "layout overflow" --json | jq -e '.ok == true and .commands.name == "overflow" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "wait text" --json | jq -e '.ok == true and .commands.name == "text" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "wait selector" --json | jq -e '.ok == true and .commands.name == "selector" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "wait locator" --json | jq -e '.ok == true and .commands.name == "locator" and (.commands.examples | any(contains("--by role"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "strict"))' >/dev/null
"$binary" describe --command "wait eval" --json | jq -e '.ok == true and .commands.name == "eval" and (.commands.examples | any(contains("__rendered")))' >/dev/null
"$binary" describe --command "assert value" --json | jq -e '.ok == true and .commands.name == "value" and (.commands.examples | any(contains("--by label"))) and (.commands.examples | any(contains("--poll"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "assert text" --json | jq -e '.ok == true and .commands.name == "text" and (.commands.examples | any(contains("--by role"))) and (.commands.examples | any(contains("--poll"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "assert url" --json | jq -e '.ok == true and .commands.name == "url" and (.commands.examples | any(contains("--mode contains"))) and (.commands.examples | any(contains("--poll"))) and (.commands.flags[] | select(.name == "mode")) and (.commands.flags[] | select(.name == "poll")) and (.commands.flags[] | select(.name == "url-contains"))' >/dev/null
"$binary" describe --command "assert title" --json | jq -e '.ok == true and .commands.name == "title" and (.commands.examples | any(contains("--mode exact"))) and (.commands.examples | any(contains("--poll"))) and (.commands.flags[] | select(.name == "mode")) and (.commands.flags[] | select(.name == "poll")) and (.commands.flags[] | select(.name == "title-contains"))' >/dev/null
"$binary" describe --command "assert count" --json | jq -e '.ok == true and .commands.name == "count" and (.commands.examples | any(contains("--by role"))) and (.commands.examples | any(contains("--poll"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "assert attribute" --json | jq -e '.ok == true and .commands.name == "attribute" and (.commands.examples | any(contains("--mode exact"))) and (.commands.examples | any(contains("--by role"))) and (.commands.flags[] | select(.name == "mode")) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "assert focused" --json | jq -e '.ok == true and .commands.name == "focused" and (.commands.examples | any(contains("--by label"))) and (.commands.examples | any(contains("--poll"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "assert css" --json | jq -e '.ok == true and .commands.name == "css" and (.commands.examples | any(contains("background-color"))) and (.commands.examples | any(contains("--by role"))) and (.commands.flags[] | select(.name == "mode")) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "assert role" --json | jq -e '.ok == true and .commands.name == "role" and (.commands.examples | any(contains("--by role"))) and (.commands.examples | any(contains("--poll"))) and (.commands.flags[] | select(.name == "mode")) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "assert name" --json | jq -e '.ok == true and .commands.name == "name" and (.commands.examples | any(contains("--mode exact"))) and (.commands.examples | any(contains("--by role"))) and (.commands.flags[] | select(.name == "mode")) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "assert visible" --json | jq -e '.ok == true and .commands.name == "visible" and (.commands.examples | any(contains("--by role"))) and (.commands.examples | any(contains("--poll"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "assert hidden" --json | jq -e '.ok == true and .commands.name == "hidden" and (.commands.examples | any(contains("--by role"))) and (.commands.examples | any(contains("--poll"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "assert enabled" --json | jq -e '.ok == true and .commands.name == "enabled" and (.commands.examples | any(contains("--by role"))) and (.commands.examples | any(contains("--poll"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "assert disabled" --json | jq -e '.ok == true and .commands.name == "disabled" and (.commands.examples | any(contains("--by role"))) and (.commands.examples | any(contains("--poll"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "assert editable" --json | jq -e '.ok == true and .commands.name == "editable" and (.commands.examples | any(contains("--by label"))) and (.commands.examples | any(contains("--poll"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "assert readonly" --json | jq -e '.ok == true and .commands.name == "readonly" and (.commands.examples | any(contains("--by label"))) and (.commands.examples | any(contains("--poll"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "assert checked" --json | jq -e '.ok == true and .commands.name == "checked" and (.commands.examples | any(contains("--by label"))) and (.commands.examples | any(contains("--poll"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "assert unchecked" --json | jq -e '.ok == true and .commands.name == "unchecked" and (.commands.examples | any(contains("--by label"))) and (.commands.examples | any(contains("--poll"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "assert indeterminate" --json | jq -e '.ok == true and .commands.name == "indeterminate" and (.commands.examples | any(contains("--by role"))) and (.commands.examples | any(contains("--poll"))) and (.commands.flags[] | select(.name == "by")) and (.commands.flags[] | select(.name == "role")) and (.commands.flags[] | select(.name == "poll"))' >/dev/null
"$binary" describe --command "snapshot" --json | jq -e '.ok == true and .commands.name == "snapshot" and (.commands.examples | any(contains("--diagnose-empty"))) and (.commands.flags[] | select(.name == "debug-empty"))' >/dev/null
"$binary" describe --command "screenshot" --json | jq -e '.ok == true and .commands.name == "screenshot" and (.commands.examples | any(contains("--preset mobile"))) and (.commands.examples | any(contains("--tile-full-page"))) and (.commands.examples | any(contains("--element"))) and (.commands.flags[] | select(.name == "crop")) and (.commands.flags[] | select(.name == "navigate")) and (.commands.flags[] | select(.name == "preset")) and (.commands.flags[] | select(.name == "tile-full-page")) and (.commands.flags[] | select(.name == "out-dir"))' >/dev/null
"$binary" describe --command "screenshot render" --json | jq -e '.ok == true and .commands.name == "render" and (.commands.examples | any(contains("--serve"))) and (.commands.flags[] | select(.name == "wait-for"))' >/dev/null
"$binary" describe --command "console" --json | jq -e '.ok == true and .commands.name == "console" and (.commands.examples | any(contains("--errors")))' >/dev/null
"$binary" describe --command "network" --json | jq -e '.ok == true and .commands.name == "network" and (.commands.examples | any(contains("--failed")))' >/dev/null
"$binary" describe --command "network capture" --json | jq -e '.ok == true and .commands.name == "capture" and (.commands.examples | any(contains("--redact"))) and (.commands.examples | any(contains("--har-out"))) and (.commands.flags[] | select(.name == "har-out"))' >/dev/null
"$binary" describe --command "storage" --json | jq -e '.ok == true and .commands.name == "storage" and (.commands.children | map(.name) | index("snapshot"))' >/dev/null
"$binary" describe --command "storage cookies set" --json | jq -e '.ok == true and .commands.name == "set" and (.commands.examples | any(contains("--name")))' >/dev/null
"$binary" describe --command "storage indexeddb" --json | jq -e '.ok == true and .commands.name == "indexeddb" and (.commands.children | map(.name) | index("put"))' >/dev/null
"$binary" describe --command "storage indexeddb put" --json | jq -e '.ok == true and .commands.name == "put" and (.commands.examples | any(contains("@tmp/value.json")))' >/dev/null
"$binary" describe --command "storage cache" --json | jq -e '.ok == true and .commands.name == "cache" and (.commands.children | map(.name) | index("put"))' >/dev/null
"$binary" describe --command "storage cache put" --json | jq -e '.ok == true and .commands.name == "put" and (.commands.examples | any(contains("--content-type")))' >/dev/null
"$binary" describe --command "storage service-workers" --json | jq -e '.ok == true and .commands.name == "service-workers" and (.commands.children | map(.name) | index("unregister"))' >/dev/null
"$binary" describe --command "storage service-workers unregister" --json | jq -e '.ok == true and .commands.name == "unregister" and (.commands.examples | any(contains("--scope")))' >/dev/null
"$binary" describe --command "protocol exec" --json | jq -e '.ok == true and .commands.name == "exec" and (.commands.examples | any(contains("--target")))' >/dev/null
"$binary" describe --command "protocol examples" --json | jq -e '.ok == true and .commands.name == "examples" and (.commands.examples | any(contains("Page.captureScreenshot")))' >/dev/null
"$binary" describe --command "workflow visible-posts" --json | jq -e '.ok == true and .commands.name == "visible-posts" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "workflow hacker-news" --json | jq -e '.ok == true and .commands.name == "hacker-news" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "workflow a11y" --json | jq -e '.ok == true and .commands.name == "a11y" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "workflow console-errors" --json | jq -e '.ok == true and .commands.name == "console-errors" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "workflow network-failures" --json | jq -e '.ok == true and .commands.name == "network-failures" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "workflow page-load" --json | jq -e '.ok == true and .commands.name == "page-load" and (.commands.examples | any(contains("--reload")))' >/dev/null
"$binary" describe --command "workflow rendered-extract" --json | jq -e '.ok == true and .commands.name == "rendered-extract" and (.commands.examples | any(contains("--serp google"))) and (.commands.flags[] | select(.name == "out-dir"))' >/dev/null
"$binary" describe --command "workflow web-research" --json | jq -e '.ok == true and .commands.name == "web-research" and (.commands.children | map(.name) | index("extract"))' >/dev/null
"$binary" describe --command "workflow web-research serp" --json | jq -e '.ok == true and .commands.name == "serp" and (.commands.examples | any(contains("--serp google"))) and (.commands.examples | any(contains("--serp all"))) and (.commands.examples | any(contains("--parallel-engines"))) and (.commands.examples | any(contains("--serp duckduckgo"))) and (.commands.examples | any(contains("--fallback-serp google"))) and (.commands.examples | any(contains("--result-pages 3"))) and (.commands.examples | any(contains("--fast-fail-blocked"))) and (.commands.examples | any(contains("--progress stderr"))) and (.commands.flags[] | select(.name == "serp" and (.usage | contains("comma-separated")))) and (.commands.flags[] | select(.name == "parallel-engines")) and (.commands.flags[] | select(.name == "fallback-serp" and (.usage | contains("auto")))) and (.commands.flags[] | select(.name == "candidate-out")) and (.commands.flags[] | select(.name == "result-pages")) and (.commands.flags[] | select(.name == "fast-fail-blocked")) and (.commands.flags[] | select(.name == "blocked-failure-threshold")) and (.commands.flags[] | select(.name == "progress"))' >/dev/null
"$binary" describe --command "workflow web-research extract" --json | jq -e '.ok == true and .commands.name == "extract" and (.commands.examples | any(contains("--parallel 4"))) and (.commands.examples | any(contains("--parallel 10"))) and (.commands.flags[] | select(.name == "url-file"))' >/dev/null
"$binary" describe --command "workflow verify" --json | jq -e '.ok == true and .commands.name == "verify" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "workflow perf" --json | jq -e '.ok == true and .commands.name == "perf" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "workflow debug-bundle" --json | jq -e '.ok == true and .commands.name == "debug-bundle" and (.commands.examples | length > 0)' >/dev/null
"$binary" describe --command "workflow action-capture" --json | jq -e '.ok == true and .commands.name == "action-capture" and (.commands.examples | any(contains("--include network,console,dom,text,a11y"))) and (.commands.flags[] | select(.name == "evidence-out-dir" and (.usage | contains("accessibility")) and (.usage | contains("manifest")))) and (.commands.flags[] | select(.name == "include" and (.usage | contains("a11y")))) and (.commands.flags[] | select(.name == "a11y-depth")) and (.commands.flags[] | select(.name == "a11y-limit"))' >/dev/null
"$binary" schema screenshot --json | jq -e '.ok == true and .schema.name == "screenshot"' >/dev/null
"$binary" schema console --json | jq -e '.ok == true and .schema.name == "console"' >/dev/null
"$binary" schema network --json | jq -e '.ok == true and .schema.name == "network"' >/dev/null
"$binary" schema network-capture --json | jq -e '.ok == true and .schema.name == "network-capture" and (.schema.fields | map(.name) | index("capture")) and (.schema.fields | map(.name) | index("capture.artifact_safety")) and (.schema.fields | map(.name) | index("har"))' >/dev/null
"$binary" schema storage --json | jq -e '.ok == true and .schema.name == "storage"' >/dev/null
"$binary" schema storage-cache --json | jq -e '.ok == true and .schema.name == "storage-cache" and (.schema.fields | map(.name) | index("storage"))' >/dev/null
"$binary" schema storage-indexeddb --json | jq -e '.ok == true and .schema.name == "storage-indexeddb" and (.schema.fields | map(.name) | index("storage"))' >/dev/null
"$binary" schema storage-service-workers --json | jq -e '.ok == true and .schema.name == "storage-service-workers" and (.schema.fields | map(.name) | index("storage"))' >/dev/null
"$binary" schema storage-snapshot --json | jq -e '.ok == true and .schema.name == "storage-snapshot" and (.schema.fields | map(.name) | index("snapshot")) and (.schema.fields | map(.name) | index("storage.artifact_safety")) and (.schema.fields[] | select(.name == "snapshot").description | contains("--redact safe"))' >/dev/null
"$binary" schema storage-diff --json | jq -e '.ok == true and .schema.name == "storage-diff" and (.schema.fields | map(.name) | index("diff"))' >/dev/null
"$binary" schema page-select --json | jq -e '.ok == true and .schema.name == "page-select" and (.schema.fields | map(.name) | index("selected_page"))' >/dev/null
"$binary" schema text --json | jq -e '.ok == true and .schema.name == "text"' >/dev/null
"$binary" schema locator-find --json | jq -e '.ok == true and .schema.name == "locator-find" and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("matches")) and (.schema.fields | map(.name) | index("next_commands"))' >/dev/null
"$binary" schema html --json | jq -e '.ok == true and .schema.name == "html" and (.schema.fields | map(.name) | index("diagnostics"))' >/dev/null
"$binary" schema dom-query --json | jq -e '.ok == true and .schema.name == "dom-query"' >/dev/null
"$binary" schema css-inspect --json | jq -e '.ok == true and .schema.name == "css-inspect"' >/dev/null
"$binary" schema layout-overflow --json | jq -e '.ok == true and .schema.name == "layout-overflow"' >/dev/null
"$binary" schema wait --json | jq -e '.ok == true and .schema.name == "wait" and (.schema.fields[] | select(.name == "wait").description | contains("evidence")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("matches"))' >/dev/null
"$binary" schema assert-value --json | jq -e '.ok == true and .schema.name == "assert-value" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields[] | select(.name == "assertion").description | contains("diff")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms"))' >/dev/null
"$binary" schema assert-text --json | jq -e '.ok == true and .schema.name == "assert-text" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields[] | select(.name == "assertion").description | contains("diff")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms"))' >/dev/null
"$binary" schema assert-url --json | jq -e '.ok == true and .schema.name == "assert-url" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields[] | select(.name == "assertion").description | contains("diff")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms")) and (.schema.fields[] | select(.name == "target").description | contains("final observed URL"))' >/dev/null
"$binary" schema assert-title --json | jq -e '.ok == true and .schema.name == "assert-title" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields[] | select(.name == "assertion").description | contains("diff")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms")) and (.schema.fields[] | select(.name == "target").description | contains("final observed URL"))' >/dev/null
"$binary" schema assert-count --json | jq -e '.ok == true and .schema.name == "assert-count" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields[] | select(.name == "assertion").description | contains("diff")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms")) and (.schema.fields[] | select(.name == "locator").description | contains("multiple matches"))' >/dev/null
"$binary" schema assert-attribute --json | jq -e '.ok == true and .schema.name == "assert-attribute" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields[] | select(.name == "assertion").description | contains("diff")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms"))' >/dev/null
"$binary" schema assert-focused --json | jq -e '.ok == true and .schema.name == "assert-focused" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields[] | select(.name == "assertion").description | contains("active element")) and (.schema.fields[] | select(.name == "assertion").description | contains("diff")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms"))' >/dev/null
"$binary" schema assert-css --json | jq -e '.ok == true and .schema.name == "assert-css" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields[] | select(.name == "assertion").description | contains("computed value")) and (.schema.fields[] | select(.name == "assertion").description | contains("diff")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms"))' >/dev/null
"$binary" schema assert-role --json | jq -e '.ok == true and .schema.name == "assert-role" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields[] | select(.name == "assertion").description | contains("actual role")) and (.schema.fields[] | select(.name == "assertion").description | contains("diff")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms"))' >/dev/null
"$binary" schema assert-name --json | jq -e '.ok == true and .schema.name == "assert-name" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields[] | select(.name == "assertion").description | contains("actual accessible name")) and (.schema.fields[] | select(.name == "assertion").description | contains("diff")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms"))' >/dev/null
"$binary" schema assert-visible --json | jq -e '.ok == true and .schema.name == "assert-visible" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields[] | select(.name == "assertion").description | contains("diff")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms"))' >/dev/null
"$binary" schema assert-hidden --json | jq -e '.ok == true and .schema.name == "assert-hidden" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields[] | select(.name == "assertion").description | contains("diff")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms"))' >/dev/null
"$binary" schema assert-enabled --json | jq -e '.ok == true and .schema.name == "assert-enabled" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields[] | select(.name == "assertion").description | contains("diff")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms"))' >/dev/null
"$binary" schema assert-disabled --json | jq -e '.ok == true and .schema.name == "assert-disabled" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields[] | select(.name == "assertion").description | contains("diff")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms"))' >/dev/null
"$binary" schema assert-editable --json | jq -e '.ok == true and .schema.name == "assert-editable" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields[] | select(.name == "assertion").description | contains("diff")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms"))' >/dev/null
"$binary" schema assert-readonly --json | jq -e '.ok == true and .schema.name == "assert-readonly" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields[] | select(.name == "assertion").description | contains("diff")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms"))' >/dev/null
"$binary" schema assert-checked --json | jq -e '.ok == true and .schema.name == "assert-checked" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields[] | select(.name == "assertion").description | contains("diff")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms"))' >/dev/null
"$binary" schema assert-unchecked --json | jq -e '.ok == true and .schema.name == "assert-unchecked" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields[] | select(.name == "assertion").description | contains("diff")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms"))' >/dev/null
"$binary" schema assert-indeterminate --json | jq -e '.ok == true and .schema.name == "assert-indeterminate" and (.schema.fields | map(.name) | index("assertion")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector")) and (.schema.fields[] | select(.name == "assertion").description | contains("indeterminate")) and (.schema.fields[] | select(.name == "assertion").description | contains("diff")) and (.schema.fields[] | select(.name == "assertion").description | contains("elapsed_ms"))' >/dev/null
"$binary" schema workflow-hacker-news --json | jq -e '.ok == true and .schema.name == "workflow-hacker-news" and (.schema.fields | map(.name) | index("organization"))' >/dev/null
"$binary" schema workflow-console-errors --json | jq -e '.ok == true and .schema.name == "workflow-console-errors"' >/dev/null
"$binary" schema workflow-network-failures --json | jq -e '.ok == true and .schema.name == "workflow-network-failures"' >/dev/null
"$binary" schema workflow-a11y --json | jq -e '.ok == true and .schema.name == "workflow-a11y" and (.schema.fields | map(.name) | index("a11y"))' >/dev/null
"$binary" schema workflow-page-load --json | jq -e '.ok == true and .schema.name == "workflow-page-load" and (.schema.fields | map(.name) | index("content_state")) and (.schema.fields | map(.name) | index("storage"))' >/dev/null
"$binary" schema workflow-rendered-extract --json | jq -e '.ok == true and .schema.name == "workflow-rendered-extract" and (.schema.fields | map(.name) | index("quality")) and (.schema.fields | map(.name) | index("artifacts"))' >/dev/null
"$binary" schema workflow-web-research-serp --json | jq -e '.ok == true and .schema.name == "workflow-web-research-serp" and (.schema.fields | map(.name) | index("candidates")) and (.schema.fields | map(.name) | index("warnings")) and (.schema.fields | map(.name) | index("failures")) and (.schema.fields | map(.name) | index("artifacts")) and (.schema.fields[] | select(.name == "workflow").description | contains("reusable engine lanes"))' >/dev/null
"$binary" schema workflow-web-research-extract --json | jq -e '.ok == true and .schema.name == "workflow-web-research-extract" and (.schema.fields | map(.name) | index("quality")) and (.schema.fields | map(.name) | index("failures")) and (.schema.fields[] | select(.name == "workflow").description | contains("backpressure"))' >/dev/null
"$binary" schema workflow-verify --json | jq -e '.ok == true and .schema.name == "workflow-verify" and (.schema.fields | map(.name) | index("requests"))' >/dev/null
"$binary" schema workflow-perf --json | jq -e '.ok == true and .schema.name == "workflow-perf" and (.schema.fields | map(.name) | index("performance"))' >/dev/null
"$binary" schema workflow-debug-bundle --json | jq -e '.ok == true and .schema.name == "workflow-debug-bundle" and (.schema.fields | map(.name) | index("artifacts"))' >/dev/null
"$binary" schema workflow-action-capture --json | jq -e '.ok == true and .schema.name == "workflow-action-capture" and (.schema.fields | map(.name) | index("evidence")) and (.schema.fields[] | select(.name == "evidence").description | contains("--evidence-out-dir")) and (.schema.fields[] | select(.name == "evidence").description | contains("accessibility")) and (.schema.fields[] | select(.name == "evidence").description | contains("action-window event")) and (.schema.fields[] | select(.name == "evidence").description | contains("manifest")) and (.schema.fields[] | select(.name == "artifacts").description | contains("manifest"))' >/dev/null
"$binary" schema click --json | jq -e '.ok == true and .schema.name == "click" and (.schema.fields | map(.name) | index("click")) and (.schema.fields | map(.name) | index("actionability")) and (.schema.fields | map(.name) | index("before_target")) and (.schema.fields | map(.name) | index("after_target")) and (.schema.fields | map(.name) | index("final_target")) and (.schema.fields | map(.name) | index("page_state")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector"))' >/dev/null
"$binary" schema fill --json | jq -e '.ok == true and .schema.name == "fill" and (.schema.fields | map(.name) | index("fill")) and (.schema.fields | map(.name) | index("actionability")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector"))' >/dev/null
"$binary" schema select --json | jq -e '.ok == true and .schema.name == "select" and (.schema.fields | map(.name) | index("select")) and (.schema.fields | map(.name) | index("actionability")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector"))' >/dev/null
"$binary" schema check --json | jq -e '.ok == true and .schema.name == "check" and (.schema.fields | map(.name) | index("check")) and (.schema.fields | map(.name) | index("actionability")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector"))' >/dev/null
"$binary" schema uncheck --json | jq -e '.ok == true and .schema.name == "uncheck" and (.schema.fields | map(.name) | index("uncheck")) and (.schema.fields | map(.name) | index("actionability")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector"))' >/dev/null
"$binary" schema type --json | jq -e '.ok == true and .schema.name == "type" and (.schema.fields | map(.name) | index("type"))' >/dev/null
"$binary" schema press --json | jq -e '.ok == true and .schema.name == "press" and (.schema.fields | map(.name) | index("press"))' >/dev/null
"$binary" schema hover --json | jq -e '.ok == true and .schema.name == "hover" and (.schema.fields | map(.name) | index("hover")) and (.schema.fields | map(.name) | index("actionability")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector"))' >/dev/null
"$binary" schema drag --json | jq -e '.ok == true and .schema.name == "drag" and (.schema.fields | map(.name) | index("drag")) and (.schema.fields | map(.name) | index("actionability")) and (.schema.fields | map(.name) | index("locator")) and (.schema.fields | map(.name) | index("resolved_selector"))' >/dev/null
"$binary" schema frames --json | jq -e '.ok == true and .schema.name == "frames" and (.schema.fields | map(.name) | index("frames"))' >/dev/null

"$binary" describe --command "a11y tree" --json | jq -e '.ok == true and .commands.name == "tree" and (.commands.flags[] | select(.name == "depth"))' >/dev/null
"$binary" describe --command "a11y find" --json | jq -e '.ok == true and .commands.name == "find" and (.commands.flags[] | select(.name == "role"))' >/dev/null
"$binary" describe --command "emulate viewport" --json | jq -e '.ok == true and .commands.name == "viewport" and (.commands.examples | any(contains("--preset")))' >/dev/null
"$binary" describe --command "emulate user-agent" --json | jq -e '.ok == true and .commands.name == "user-agent" and (.commands.examples | any(contains("--user-agent")))' >/dev/null
"$binary" describe --command "emulate geolocation" --json | jq -e '.ok == true and .commands.name == "geolocation" and (.commands.examples | any(contains("--latitude")))' >/dev/null
"$binary" describe --command "emulate cpu" --json | jq -e '.ok == true and .commands.name == "cpu" and (.commands.examples | any(contains("--rate")))' >/dev/null
"$binary" describe --command "emulate network" --json | jq -e '.ok == true and .commands.name == "network" and (.commands.examples | any(contains("--preset slow-3g"))) and (.commands.flags[] | select(.name == "download-kbps"))' >/dev/null
"$binary" describe --command "dialog accept" --json | jq -e '.ok == true and .commands.name == "accept" and (.commands.flags[] | select(.name == "prompt-text"))' >/dev/null
"$binary" describe --command "events tap" --json | jq -e '.ok == true and .commands.name == "tap" and (.commands.flags[] | select(.name == "max-events"))' >/dev/null
"$binary" describe --command "protocol compat" --json | jq -e '.ok == true and .commands.name == "compat" and (.commands.examples | any(contains("--requires")))' >/dev/null
"$binary" describe --command "memory heap-snapshot" --json | jq -e '.ok == true and .commands.name == "heap-snapshot" and (.commands.flags[] | select(.name == "out"))' >/dev/null
"$binary" describe --command "perf summary" --json | jq -e '.ok == true and .commands.name == "summary" and (.commands.flags[] | select(.name == "duration"))' >/dev/null
"$binary" describe --command "workflow feeds" --json | jq -e '.ok == true and .commands.name == "feeds" and (.commands.examples | any(contains("--wait-load")))' >/dev/null
"$binary" describe --command "workflow responsive-audit" --json | jq -e '.ok == true and .commands.name == "responsive-audit"' >/dev/null
"$binary" schema protocol-compat --json | jq -e '.ok == true and .schema.name == "protocol-compat"' >/dev/null
"$binary" schema a11y --json | jq -e '.ok == true and .schema.name == "a11y"' >/dev/null
"$binary" schema workflow-feeds --json | jq -e '.ok == true and .schema.name == "workflow-feeds"' >/dev/null

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
HOME="$profile_copy_home" XDG_CONFIG_HOME="$profile_copy_config_dir" "$binary" --state-dir "$state_dir-copy-default" browser profile seed --strategy copy-default --json | jq -e '.ok == true and .seeded == true and .exists == true and .seed_action == "seeded" and .seed_strategy == "copy-default" and .managed_browser.browser_mode == "headless" and .managed_browser.profile_seed_strategy == "copy-default" and .managed_browser.default_profile_copied == true and .managed_browser.copied_file_count >= 6 and (.managed_browser | has("ownership_token") | not) and (.managed_browser | has("process_start_time") | not)' >/dev/null
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

mode_status_dir="$state_dir/mode-status"
"$binary" daemon status --state-dir "$mode_status_dir" --json | jq -e '.ok == true and .daemon.browser_mode == "headed" and .daemon.state' >/dev/null
"$binary" --browser-mode headless daemon status --state-dir "$mode_status_dir" --json | jq -e '.ok == true and .daemon.browser_mode == "headless" and (.daemon.next_commands | index("cdp --browser-mode headless browser profile status --json"))' >/dev/null
"$binary" daemon logs --state-dir "$mode_status_dir" --json | jq -e '.ok == true and .browser_mode == "headed" and .log.count == 0 and (.entries | length == 0)' >/dev/null
"$binary" --browser-mode headless daemon logs --state-dir "$mode_status_dir" --json | jq -e '.ok == true and .browser_mode == "headless" and (.log.path | contains("headless/daemon.log")) and .log.count == 0 and (.entries | length == 0)' >/dev/null

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
