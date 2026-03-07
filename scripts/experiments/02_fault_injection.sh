#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./common.sh
source "$SCRIPT_DIR/common.sh"

ensure_runtime
need_cmd "$GO_BIN"
need_cmd docker

CONCURRENCY_USERS="${CONCURRENCY_USERS:-100}"
TASKS_PER_USER="${TASKS_PER_USER:-5}"
SUBMIT_WORKERS="${SUBMIT_WORKERS:-64}"
POLL_INTERVAL="${POLL_INTERVAL:-200ms}"
DEQUEUE_LIMIT="${DEQUEUE_LIMIT:-200}"
TIMEOUT="${TIMEOUT:-45m}"
MAX_RETRIES="${MAX_RETRIES:-3}"
VERBOSE_TASKSIM="${VERBOSE_TASKSIM:-false}"
INPUT_PREFIX="${INPUT_PREFIX:-Reply with exactly OK and stop.}"

INJECT_INTERVAL_SEC="${INJECT_INTERVAL_SEC:-5}"
RUNNER_KILL_RATIO="${RUNNER_KILL_RATIO:-30}"
RESTART_RUNNER_AFTER_KILL="${RESTART_RUNNER_AFTER_KILL:-1}"
POOL_LABEL="${POOL_LABEL:-$(default_pool_label)}"

OUT_DIR="$(prepare_output_dir fault_injection)"
SUMMARY_JSON="$OUT_DIR/summary.json"
SUMMARY_CSV="$OUT_DIR/summary.csv"
INJECT_LOG="$OUT_DIR/injections.csv"
RUN_META="$OUT_DIR/run_meta.txt"

echo "timestamp,action,target,result" > "$INJECT_LOG"
echo "AGENT_RUNTIME=$AGENT_RUNTIME" > "$RUN_META"
echo "RETRY_PRIORITY=${RETRY_PRIORITY:-0}" >> "$RUN_META"
echo "POOL_LABEL=$POOL_LABEL" >> "$RUN_META"
echo "CONCURRENCY_USERS=$CONCURRENCY_USERS" >> "$RUN_META"
echo "TASKS_PER_USER=$TASKS_PER_USER" >> "$RUN_META"
echo "INJECT_INTERVAL_SEC=$INJECT_INTERVAL_SEC" >> "$RUN_META"
echo "RUNNER_KILL_RATIO=$RUNNER_KILL_RATIO" >> "$RUN_META"
echo "INPUT_PREFIX=$INPUT_PREFIX" >> "$RUN_META"

log "fault injection experiment started"
log "output dir: $OUT_DIR"

users_csv="$(gen_users_csv "$CONCURRENCY_USERS" "fi_u")"
task_type="exp_fi_$(date +%s)"
pid_file="$(runner_pid_file)"

injector_pid=""
stop_injector() {
  if [[ -n "$injector_pid" ]] && kill -0 "$injector_pid" 2>/dev/null; then
    kill "$injector_pid" >/dev/null 2>&1 || true
    wait "$injector_pid" 2>/dev/null || true
  fi
}
trap stop_injector EXIT

inject_once() {
  local ts action target result
  ts="$(date '+%Y-%m-%dT%H:%M:%S%z')"

  if (( RANDOM % 100 < RUNNER_KILL_RATIO )); then
    action="kill_runner"
    target="runner"
    if [[ -f "$pid_file" ]]; then
      local pid
      pid="$(cat "$pid_file")"
      if kill -0 "$pid" 2>/dev/null; then
        if kill -9 "$pid" >/dev/null 2>&1; then
          result="ok"
          if [[ "$RESTART_RUNNER_AFTER_KILL" == "1" ]]; then
            sleep 1
            if run_cloudclawctl runner start >/dev/null 2>&1; then
              result="ok_restart"
            else
              result="ok_restart_failed"
            fi
          fi
        else
          result="kill_failed"
        fi
      else
        result="runner_not_alive"
      fi
    else
      result="pid_file_missing"
    fi
  else
    action="kill_container"
    local ids
    mapfile -t ids < <(docker ps --filter "label=$POOL_LABEL" --format '{{.ID}}')
    if (( ${#ids[@]} == 0 )); then
      target="none"
      result="no_container"
    else
      local idx
      idx=$(( RANDOM % ${#ids[@]} ))
      target="${ids[$idx]}"
      if docker kill -s KILL "$target" >/dev/null 2>&1; then
        result="ok"
      else
        result="kill_failed"
      fi
    fi
  fi

  echo "$ts,$action,$target,$result" >> "$INJECT_LOG"
}

(
  while true; do
    sleep "$INJECT_INTERVAL_SEC"
    inject_once
  done
) &
injector_pid="$!"

run_tasksim \
  --data-dir "$CC_DATA_DIR" \
  --db-driver "$CC_DB_DRIVER" \
  --db-dsn "$CC_DB_DSN" \
  --users "$users_csv" \
  --tasks-per-user "$TASKS_PER_USER" \
  --submit-workers "$SUBMIT_WORKERS" \
  --dequeue-limit "$DEQUEUE_LIMIT" \
  --poll-interval "$POLL_INTERVAL" \
  --timeout "$TIMEOUT" \
  --max-retries "$MAX_RETRIES" \
  --input-prefix "$INPUT_PREFIX" \
  --task-type "$task_type" \
  --summary-file "$SUMMARY_JSON" \
  --append-csv "$SUMMARY_CSV" \
  --fetch-final-task=true \
  --collect-events=true \
  --verbose="$VERBOSE_TASKSIM"

stop_injector

log "fault injection experiment finished"
log "summary json: $SUMMARY_JSON"
log "summary csv: $SUMMARY_CSV"
log "inject log: $INJECT_LOG"
