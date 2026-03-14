#!/bin/sh
set -eu

: "${CLOUDCLAW_WORKSPACE:?missing CLOUDCLAW_WORKSPACE}"
: "${CLOUDCLAW_INPUT:?missing CLOUDCLAW_INPUT}"
: "${CLOUDCLAW_USAGE_FILE:?missing CLOUDCLAW_USAGE_FILE}"

MOCK_TASK_SLEEP_MS="${MOCK_TASK_SLEEP_MS:-5000}"
MOCK_TASK_OUTPUT_PREFIX="${MOCK_TASK_OUTPUT_PREFIX:-mock runtime completed}"
MOCK_TASK_STATE_DIR="${MOCK_TASK_STATE_DIR:-$CLOUDCLAW_WORKSPACE/.mock-home}"

case "$MOCK_TASK_SLEEP_MS" in
  ''|*[!0-9]*)
    echo "invalid MOCK_TASK_SLEEP_MS: $MOCK_TASK_SLEEP_MS" >&2
    exit 1
    ;;
esac

mkdir -p "$CLOUDCLAW_WORKSPACE" "$MOCK_TASK_STATE_DIR"

python3 - "$MOCK_TASK_SLEEP_MS" <<'PY'
import sys
import time

ms = int(sys.argv[1])
time.sleep(ms / 1000.0)
PY

task_count_file="$MOCK_TASK_STATE_DIR/task-count.txt"
count=0
if [ -f "$task_count_file" ]; then
  count="$(cat "$task_count_file" 2>/dev/null || echo 0)"
fi
case "$count" in
  ''|*[!0-9]*) count=0 ;;
esac
count=$((count + 1))
printf '%s\n' "$count" >"$task_count_file"

printf '%s\n' "$CLOUDCLAW_INPUT" >"$MOCK_TASK_STATE_DIR/last-input.txt"
printf '%s | sleep_ms=%s | task_count=%s\n' "$MOCK_TASK_OUTPUT_PREFIX" "$MOCK_TASK_SLEEP_MS" "$count" >"$CLOUDCLAW_WORKSPACE/result.txt"

prompt_chars=$(printf "%s" "$CLOUDCLAW_INPUT" | wc -c | tr -d ' ')
resp_chars=$(wc -c < "$CLOUDCLAW_WORKSPACE/result.txt" | tr -d ' ')
pt=$(( (prompt_chars + 3) / 4 ))
ct=$(( (resp_chars + 3) / 4 ))
tt=$(( pt + ct ))
printf '{"prompt_tokens":%s,"completion_tokens":%s,"total_tokens":%s}\n' "$pt" "$ct" "$tt" >"$CLOUDCLAW_USAGE_FILE"
