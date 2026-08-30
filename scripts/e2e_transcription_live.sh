#!/usr/bin/env bash
set -euo pipefail

binary="${1:-$(command -v cdp)}"

if [[ ! -x "$binary" ]]; then
  printf 'missing executable: %s\n' "$binary" >&2
  exit 2
fi
for command_name in curl ffmpeg jq python3; do
  command -v "$command_name" >/dev/null 2>&1 || {
    printf 'required command is unavailable: %s\n' "$command_name" >&2
    exit 2
  }
done

fixture="${CDP_TRANSCRIPTION_E2E_FIXTURE:-$(pwd -P)/testdata/transcription-fixtures/001.webm}"
if [[ ! -r "$fixture" ]]; then
  printf 'live transcription fixture is unavailable: %s\n' "$fixture" >&2
  exit 2
fi

state_dir="$(mktemp -d)"
service_pid=""
service_url=""
config_path="$state_dir/config.json"

# The provider gate must exercise every provider named by this invocation.
# Keep the user's policy out of this transient process so a local
# agents.disabled_providers entry cannot make the requested-provider assertion
# nondeterministic. Provider auth/templates still come from the normal owner
# state directory used by cdp.
printf '%s\n' '{}' >"$config_path"

cleanup() {
  set +e
  if [[ -n "$service_pid" ]] && kill -0 "$service_pid" 2>/dev/null; then
    kill "$service_pid" 2>/dev/null || true
    wait "$service_pid" 2>/dev/null || true
  fi
  rm -rf -- "$state_dir"
}
trap cleanup EXIT INT TERM

port="$(python3 - <<'PY'
import socket

with socket.socket() as sock:
    sock.bind(("127.0.0.1", 0))
    print(sock.getsockname()[1])
PY
)"
service_url="http://127.0.0.1:$port"

# This transient provider-neutral service is tested through the public file
# upload API. No demo page, browser microphone permission, or host audio device
# is part of this provider gate.
env -u CDP_STATE_DIR \
  -u CDP_TRANSCRIPTION_ADDRESS \
  -u CDP_TRANSCRIPTION_HTTP_ADDRESS \
  -u CDP_TRANSCRIPTION_PROVIDER \
  -u CDP_TRANSCRIPTION_PROVIDERS \
  "$binary" --config "$config_path" --browser-mode headed --allow-over-budget \
  transcription serve \
  --address "127.0.0.1:$port" \
  --http-address "" \
  --default-provider chatgpt-web \
  --providers chatgpt-web,claude-web,gemini-web,microsoft-365-web,bing-web \
  --auth-refresh-interval 0s \
  --max-audio-bytes 1073741824 \
  --print-ready >"$state_dir/service-ready.json" 2>"$state_dir/service.stderr" &
service_pid=$!

health=""
for _ in $(seq 1 120); do
  if ! kill -0 "$service_pid" 2>/dev/null; then
    printf 'transient transcription service exited before health became ready\n' >&2
    exit 1
  fi
  health="$(curl -sS --max-time 2 "$service_url/healthz" 2>/dev/null || true)"
  if jq -e '([.providers[]] | length == 5)' <<<"$health" >/dev/null 2>&1; then
    break
  fi
  sleep 0.25
done
jq -e '([.providers[].provider] | sort == ["bing-web", "chatgpt-web", "claude-web", "gemini-web", "microsoft-365-web"])' <<<"$health" >/dev/null || {
  printf 'transient transcription service did not expose all requested providers\n' >&2
  exit 1
}

for provider in chatgpt-web claude-web gemini-web microsoft-365-web bing-web; do
  if ! jq -e --arg provider "$provider" \
    'any(.providers[]; .provider == $provider and .file == true)' \
    <<<"$health" >/dev/null; then
    printf 'provider does not expose file transcription for live e2e: %s\n' "$provider" >&2
    exit 1
  fi
done

assert_provider() {
  local provider="$1"
  local payload response status
  payload="$(curl --noproxy '*' --connect-timeout 8 --max-time 720 \
    --show-error --silent --write-out $'\n%{http_code}' \
    -H "X-Request-ID: public-live-$provider-$$" \
    -F "file=@$fixture;type=audio/webm" \
    -F 'model=whisper-1' \
    -F 'response_format=text' \
    -F "provider=$provider" \
    "$service_url/v1/audio/transcriptions")"
  status="${payload##*$'\n'}"
  response="${payload%$'\n'*}"
  case "$status" in
    2??) ;;
    *)
      printf 'provider=%s api=file-upload http=%s error=' "$provider" "$status" >&2
      if ! jq -c '{type:(.error.type // "unknown"),code:(.error.code // ""),message:(.error.message // "provider request failed")}' <<<"$response" >&2; then
        printf '%s\n' '{"type":"unknown","code":"unparseable","message":"provider request failed"}' >&2
      fi
      return 1
      ;;
  esac
  if ! printf '%s' "$response" | jq -Rse '
    ascii_downcase as $text |
    ($text | length) > 0 and
    (["latest", "project", "notes", "meeting"] |
      all(. as $marker | $text | contains($marker)))
  ' >/dev/null; then
    printf 'provider=%s api=file-upload transcript_markers=FAIL\n' "$provider" >&2
    return 1
  fi
  printf 'provider=%s api=file-upload transcript_markers=PASS text_length=%d\n' \
    "$provider" "${#response}"
}

for provider in chatgpt-web claude-web gemini-web microsoft-365-web bing-web; do
  assert_provider "$provider"
done

health="$(curl -sS --max-time 5 "$service_url/healthz")"
jq -e '
  .status == "ok" and
  ([.providers[].provider] | sort == ["bing-web", "chatgpt-web", "claude-web", "gemini-web", "microsoft-365-web"]) and
  all(.providers[]; .ready == true)
' <<<"$health" >/dev/null || {
  printf 'provider health was not ready after the sequential live requests\n' >&2
  exit 1
}

printf 'live provider transcription e2e passed: five sequential file-upload API providers\n'
