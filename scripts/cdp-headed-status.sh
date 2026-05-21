#!/usr/bin/env bash
set -euo pipefail

cdp_bin="${CDP_BIN:-$HOME/.local/bin/cdp}"
display_value="${DISPLAY:-:0}"
xdg_runtime_dir="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"

state_dir_arg=()
if [[ -n "${CDP_STATE_DIR:-}" ]]; then
  state_dir_arg=(--state-dir "$CDP_STATE_DIR")
fi

exec env DISPLAY="$display_value" XDG_RUNTIME_DIR="$xdg_runtime_dir" "$cdp_bin" --browser-mode headed "${state_dir_arg[@]}" doctor --check daemon --json
