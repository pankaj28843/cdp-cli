#!/usr/bin/env bash
set -euo pipefail

action="${1:-install}"
repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cdp_bin="${CDP_BIN:-$HOME/.local/bin/cdp}"
log_dir="${CDP_LOG_DIR:-$HOME/.cdp-cli}"
display_value="${DISPLAY:-:0}"
xdg_runtime_dir="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
reconnect="${CDP_RECONNECT:-30s}"
block_start="# cdp-cli managed browser runtime tasks"
block_end="# End cdp-cli managed browser runtime tasks"

current_crontab() {
  crontab -l 2>/dev/null || true
}

without_block() {
  awk -v start="$block_start" -v end="$block_end" '
    $0 == start { inblock=1; next }
    $0 == end { inblock=0; next }
    !inblock { print }
  '
}

install_block() {
  local tmp
  tmp="$(mktemp)"
  current_crontab | without_block >"$tmp"
  cat >>"$tmp" <<EOF
$block_start
* * * * * /usr/bin/flock -n \$HOME/.cdp-cli/locks/cron-headed-heal.lock env CDP_BIN=$cdp_bin CDP_LOG_DIR=$log_dir DISPLAY=$display_value XDG_RUNTIME_DIR=$xdg_runtime_dir CDP_RECONNECT=$reconnect bash $repo_dir/scripts/cdp-headed-heal.sh >> \$HOME/.cdp-cli/keepalive-headed.log 2>&1
* * * * * /usr/bin/flock -n \$HOME/.cdp-cli/locks/keepalive-headless.lock $cdp_bin --browser-mode headless daemon keepalive --repair --reconnect $reconnect --json >> \$HOME/.cdp-cli/keepalive-headless.log 2>&1
* * * * * /usr/bin/flock -n \$HOME/.cdp-cli/locks/headless-health.lock CDP_BIN=$cdp_bin CDP_LOG_DIR=$log_dir bash $repo_dir/scripts/cdp-headless-healthcheck.sh >> \$HOME/.cdp-cli/headless-health.log 2>&1
0 */6 * * * /usr/bin/flock -n \$HOME/.cdp-cli/locks/headless-profile-seed.lock $cdp_bin --browser-mode headless browser profile seed --strategy copy-default --if-older-than 6h --json >> \$HOME/.cdp-cli/profile-seed-headless.log 2>&1
* * * * * /usr/bin/flock -n \$HOME/.cdp-cli/locks/page-cleanup-headless.lock $cdp_bin --browser-mode headless page cleanup --created-by cdp --idle-for 30m --close --max 10 --json >> \$HOME/.cdp-cli/page-cleanup-headless.log 2>&1
$block_end
EOF
  crontab "$tmp"
  rm -f "$tmp"
}

case "$action" in
  install)
    install_block
    ;;
  show)
    current_crontab | sed -n "/^$block_start$/,/^$block_end$/p"
    ;;
  remove)
    tmp="$(mktemp)"
    current_crontab | without_block >"$tmp"
    crontab "$tmp"
    rm -f "$tmp"
    ;;
  *)
    printf 'usage: %s [install|show|remove]\n' "$0" >&2
    exit 2
    ;;
esac
