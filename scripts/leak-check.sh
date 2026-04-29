#!/usr/bin/env bash
set -euo pipefail

patterns=(
  '/home/'
  'Personal/Code'
  '\.config/'
  'hosts\.yml'
  'Token:'
  'chrome-devtools:'
  '--autoConnect --logFile'
  '127\.0\.0\.1:[0-9]+'
)

args=(
  -n
  -S
  -g '!go.sum'
  -g '!bin/**'
  -g '!cdp'
  -g '!scripts/leak-check.sh'
  -g '!tmp/**'
)

for pattern in "${patterns[@]}"; do
  set +e
  rg "${args[@]}" -- "$pattern" . >/tmp/cdp-cli-leak-check.txt
  status=$?
  set -e

  if [[ "$status" -eq 0 ]]; then
    cat /tmp/cdp-cli-leak-check.txt >&2
    echo "public-repo hygiene scan failed for pattern: $pattern" >&2
    exit 1
  elif [[ "$status" -ne 1 ]]; then
    cat /tmp/cdp-cli-leak-check.txt >&2
    echo "public-repo hygiene scan could not complete for pattern: $pattern" >&2
    exit "$status"
  fi
done
