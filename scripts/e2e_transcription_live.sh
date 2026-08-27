#!/usr/bin/env bash
set -euo pipefail

binary="${1:-$(command -v cdp)}"

if [[ ! -x "$binary" ]]; then
  printf 'missing executable: %s\n' "$binary" >&2
  exit 2
fi
if [[ -z "${DISPLAY:-}" ]]; then
  printf 'live transcription e2e requires an active headed DISPLAY\n' >&2
  exit 2
fi
for command_name in curl espeak-ng ffmpeg jq paplay pactl; do
  command -v "$command_name" >/dev/null 2>&1 || {
    printf 'required command is unavailable: %s\n' "$command_name" >&2
    exit 2
  }
done

state_dir="$(mktemp -d)"
service_pid=""
page_id=""
service_url=""
audio_file="$state_dir/live-test.wav"
expected_phrase="This is a headed transcription test. The verification code is one two three four five."

cdp() {
  "$binary" --browser-mode headed --allow-over-budget "$@"
}

close_owned_page() {
  if [[ -n "$page_id" ]]; then
    cdp page close --target "$page_id" --wait-gone --json >/dev/null 2>&1 || true
  fi
}

cleanup() {
  set +e
  close_owned_page
  if [[ -n "$service_pid" ]] && kill -0 "$service_pid" 2>/dev/null; then
    kill "$service_pid" 2>/dev/null || true
    wait "$service_pid" 2>/dev/null || true
  fi
  rm -rf -- "$state_dir"
}
trap cleanup EXIT INT TERM

default_source="$(pactl get-default-source 2>/dev/null || true)"
if [[ -z "$default_source" ]]; then
  printf 'no default PulseAudio/PipeWire source\n' >&2
  exit 1
fi
if ! pactl list short sources | awk '{print $2}' | grep -Fxq "$default_source"; then
  printf 'default source is not enumerated: %s\n' "$default_source" >&2
  exit 1
fi

espeak-ng -v en-us -s 125 -w "$state_dir/raw.wav" "$expected_phrase"
ffmpeg -hide_banner -loglevel error -y -i "$state_dir/raw.wav" -ar 16000 -ac 1 "$audio_file"

port="$(python3 - <<'PY'
import socket

with socket.socket() as sock:
    sock.bind(("127.0.0.1", 0))
    print(sock.getsockname()[1])
PY
)"
service_url="http://127.0.0.1:$port"

# This is intentionally a transient provider-neutral server. The cdp service
# exercises ChatGPT and Bing through completed-file transcription and
# Microsoft 365 through its live/realtime path. The API is unauthenticated.
env -u CDP_STATE_DIR \
  -u CDP_TRANSCRIPTION_ADDRESS \
  -u CDP_TRANSCRIPTION_HTTP_ADDRESS \
  -u CDP_TRANSCRIPTION_PROVIDER \
  -u CDP_TRANSCRIPTION_PROVIDERS \
  "$binary" --browser-mode headed --allow-over-budget \
  transcription serve \
  --address "127.0.0.1:$port" \
  --http-address "" \
  --default-provider chatgpt-web \
  --providers chatgpt-web,microsoft-365-web,bing-web \
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
  health="$(curl -fsS --max-time 2 "$service_url/healthz" 2>/dev/null || true)"
  if jq -e '.status == "ok" and ([.providers[]] | length == 3)' <<<"$health" >/dev/null 2>&1; then
    break
  fi
  sleep 0.25
done
jq -e '.status == "ok" and ([.providers[].provider] | sort == ["bing-web", "chatgpt-web", "microsoft-365-web"])' <<<"$health" >/dev/null || {
  printf 'transient transcription service did not expose all requested providers\n' >&2
  exit 1
}

for provider in chatgpt-web microsoft-365-web bing-web; do
  if ! jq -e --arg provider "$provider" 'any(.providers[]; .provider == $provider and .ready == true)' <<<"$health" >/dev/null; then
    printf 'provider is not ready for live e2e: %s\n' "$provider" >&2
    exit 1
  fi
done

open_output="$(cdp open "$service_url/demo.html" \
  --new-tab \
  --created-by live-transcription-e2e \
  --run-id live-transcription-e2e \
  --task-id live-transcription-demo \
  --json)"
page_id="$(jq -er '.page.id' <<<"$open_output")"
cdp wait selector '#talkButton' --target "$page_id" --timeout 15s --json >/dev/null
cdp permissions grant microphone --origin "$service_url" --json >/dev/null

eval_value() {
  local expression="$1"
  cdp eval "$expression" --target "$page_id" --json | jq -c '.result.value'
}

wait_for_state() {
  local expression="$1"
  local condition="$2"
  local value=""
  for _ in $(seq 1 240); do
    value="$(eval_value "$expression" 2>/dev/null || true)"
    if [[ -n "$value" ]] && jq -e "$condition" <<<"$value" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.25
  done
  return 1
}

select_provider() {
  local provider="$1"
  cdp select '#provider' "$provider" --target "$page_id" --json >/dev/null
  cdp eval 'document.querySelector("#clearTranscript")?.click(); true' --target "$page_id" --json >/dev/null
  wait_for_state \
    '({provider: document.querySelector("#provider")?.value || "", finalHidden: document.querySelector("#finalTranscript")?.hidden !== false})' \
    ".provider == \"$provider\" and .finalHidden == true"
}

transcript_state='({label: document.querySelector("#talkLabel")?.textContent || "", status: document.querySelector("#primaryStatus")?.textContent || "", finalHidden: document.querySelector("#finalTranscript")?.hidden !== false, finalText: document.querySelector("#finalTranscript")?.textContent || ""})'

assert_transcript() {
  local provider="$1"
  local mode="$2"
  local value=""
  for _ in $(seq 1 360); do
    value="$(eval_value "$transcript_state" 2>/dev/null || true)"
    if [[ -n "$value" ]] && jq -e '.finalHidden == false and (.finalText | length) > 0' <<<"$value" >/dev/null 2>&1; then
      local markers
      local marker_expression
      if [[ "$provider" == "microsoft-365-web" ]]; then
        marker_expression='(() => { const text = (document.querySelector("#finalTranscript")?.textContent || "").toLowerCase().replace(/[^a-z0-9]+/g, " ").trim(); const required = ["transcription", "code"]; const code = text.includes("12345") || ["one", "two", "three", "four", "five"].every((item) => text.includes(item)); return { nonempty: text.length > 0, markers: Object.fromEntries(required.map((item) => [item, text.includes(item)])), code }; })()'
      else
        marker_expression='(() => { const text = (document.querySelector("#finalTranscript")?.textContent || "").toLowerCase().replace(/[^a-z0-9]+/g, " ").trim(); const required = ["transcription", "verification"]; const code = text.includes("12345") || ["one", "two", "three", "four", "five"].every((item) => text.includes(item)); return { nonempty: text.length > 0, markers: Object.fromEntries(required.map((item) => [item, text.includes(item)])), code }; })()'
      fi
      markers="$(eval_value "$marker_expression" 2>/dev/null || true)"
      if jq -e '.nonempty == true and ([.markers[]] | all) and .code == true' <<<"$markers" >/dev/null 2>&1; then
        printf 'provider=%s mode=%s transcript_markers=PASS\n' "$provider" "$mode"
        return 0
      fi
      printf 'provider=%s mode=%s returned a transcript without all semantic markers\n' "$provider" "$mode" >&2
      return 1
    fi
    if [[ -n "$value" ]] && jq -e '.status | test("failed|No words"; "i")' <<<"$value" >/dev/null 2>&1; then
      printf 'provider=%s mode=%s reported a transcription failure\n' "$provider" "$mode" >&2
      return 1
    fi
    sleep 0.5
  done
  printf 'provider=%s mode=%s timed out waiting for a final transcript\n' "$provider" "$mode" >&2
  return 1
}

select_provider chatgpt-web
cdp click '#talkButton' --target "$page_id" --activate --json >/dev/null
wait_for_state "$transcript_state" '.label == "Stop and transcribe"'
sleep 0.5
paplay "$audio_file"
sleep 0.5
cdp click '#talkButton' --target "$page_id" --json >/dev/null
assert_transcript chatgpt-web file

select_provider microsoft-365-web
cdp click '#talkButton' --target "$page_id" --activate --json >/dev/null
wait_for_state "$transcript_state" '.label == "Stop and finish"'
sleep 0.5
paplay "$audio_file"
sleep 0.5
cdp click '#talkButton' --target "$page_id" --json >/dev/null
assert_transcript microsoft-365-web realtime

select_provider bing-web
cdp click '#talkButton' --target "$page_id" --activate --json >/dev/null
wait_for_state "$transcript_state" '.label == "Stop and transcribe"'
sleep 0.5
paplay "$audio_file"
sleep 0.5
cdp click '#talkButton' --target "$page_id" --json >/dev/null
assert_transcript bing-web file

wait_for_state \
  '({provider: document.querySelector("#provider")?.value || "", stored: localStorage.getItem("cdp-transcription-demo-last-provider-v1") || ""})' \
  '.provider == "bing-web" and .stored == "bing-web"'
cdp page reload --target "$page_id" --json >/dev/null
wait_for_state \
  '({provider: document.querySelector("#provider")?.value || "", stored: localStorage.getItem("cdp-transcription-demo-last-provider-v1") || ""})' \
  '.provider == "bing-web" and .stored == "bing-web"'
printf 'demo provider persistence=PASS provider=bing-web\n'

printf 'live transcription e2e passed: ChatGPT file + Microsoft 365 realtime + Bing file; audio source=%s\n' "$default_source"
