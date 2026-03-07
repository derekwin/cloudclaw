#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./common.sh
source "$SCRIPT_DIR/common.sh"

ensure_runtime
need_cmd "$GO_BIN"

LEVELS="${LEVELS:-10,20,50,100,200,500,1000}"
TASKS_PER_USER="${TASKS_PER_USER:-1}"
SUBMIT_WORKERS_MAX="${SUBMIT_WORKERS_MAX:-256}"
POLL_INTERVAL="${POLL_INTERVAL:-200ms}"
DEQUEUE_LIMIT="${DEQUEUE_LIMIT:-200}"
TIMEOUT="${TIMEOUT:-30m}"
MAX_RETRIES="${MAX_RETRIES:-2}"
VERBOSE_TASKSIM="${VERBOSE_TASKSIM:-false}"
INPUT_PREFIX="${INPUT_PREFIX:-Reply with exactly OK and stop.}"

OUT_DIR="$(prepare_output_dir throughput_latency)"
SUMMARY_CSV="$OUT_DIR/summary.csv"
RUN_META="$OUT_DIR/run_meta.txt"

echo "AGENT_RUNTIME=$AGENT_RUNTIME" > "$RUN_META"
echo "CC_DATA_DIR=$CC_DATA_DIR" >> "$RUN_META"
echo "CC_DB_DRIVER=$CC_DB_DRIVER" >> "$RUN_META"
echo "POOL_SIZE=${POOL_SIZE:-}" >> "$RUN_META"
echo "LEVELS=$LEVELS" >> "$RUN_META"
echo "INPUT_PREFIX=$INPUT_PREFIX" >> "$RUN_META"

log "throughput/latency experiment started"
log "output dir: $OUT_DIR"

levels=()
split_csv "$LEVELS" levels

for level in "${levels[@]}"; do
  [[ -z "$level" ]] && continue

  users_csv="$(gen_users_csv "$level" "tp_u")"
  submit_workers="$level"
  if (( submit_workers > SUBMIT_WORKERS_MAX )); then
    submit_workers="$SUBMIT_WORKERS_MAX"
  fi

  task_type="exp_tp_c${level}_$(date +%s)"
  summary_json="$OUT_DIR/${task_type}.json"

  log "run level=$level submit_workers=$submit_workers task_type=$task_type"
  run_tasksim \
    --data-dir "$CC_DATA_DIR" \
    --db-driver "$CC_DB_DRIVER" \
    --db-dsn "$CC_DB_DSN" \
    --users "$users_csv" \
    --tasks-per-user "$TASKS_PER_USER" \
    --submit-workers "$submit_workers" \
    --dequeue-limit "$DEQUEUE_LIMIT" \
    --poll-interval "$POLL_INTERVAL" \
    --timeout "$TIMEOUT" \
    --max-retries "$MAX_RETRIES" \
    --input-prefix "$INPUT_PREFIX" \
    --task-type "$task_type" \
    --summary-file "$summary_json" \
    --append-csv "$SUMMARY_CSV" \
    --fetch-final-task=true \
    --collect-events=false \
    --verbose="$VERBOSE_TASKSIM"
done

log "throughput/latency experiment finished"
log "summary csv: $SUMMARY_CSV"
