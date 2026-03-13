#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/common.sh"

require_cmd go
require_cmd python3
require_env AGENT_RUNTIME DB_DSN

SESSION_ID="${SESSION_ID:-throughput-$(timestamp_utc)}"
ARTIFACT_ROOT="${ARTIFACT_ROOT:-$ARTIFACTS_BASE/throughput_latency/$SESSION_ID}"
TASKSIM_BIN="${TASKSIM_BIN:-$ARTIFACTS_BASE/bin/tasksim}"

POOL_SIZES_STR="${CC_EXP_POOL_SIZES:-${POOL_SIZE:-3}}"
WORKSPACE_MODES_STR="${CC_EXP_WORKSPACE_MODES:-${WORKSPACE_MODE:-mount}}"
WORKSPACE_STATE_MODES_STR="${CC_EXP_WORKSPACE_STATE_MODES:-${WORKSPACE_STATE_MODE:-ephemeral}}"
TASKS_PER_USER_LIST_STR="${CC_EXP_TASKS_PER_USER_LIST:-5 10 20 40}"

USERS_CSV="${CC_EXP_USERS:-sim_u1,sim_u2,sim_u3,sim_u4}"
SUBMIT_WORKERS="${CC_EXP_SUBMIT_WORKERS:-8}"
DEQUEUE_LIMIT="${CC_EXP_DEQUEUE_LIMIT:-50}"
POLL_INTERVAL="${CC_EXP_POLL_INTERVAL:-300ms}"
TIMEOUT="${CC_EXP_TIMEOUT:-15m}"
MAX_RETRIES="${CC_EXP_MAX_RETRIES:-2}"
REPEAT="${CC_EXP_REPEAT:-1}"
INPUT_PREFIX="${CC_EXP_INPUT_PREFIX:-throughput latency benchmark task}"
RETRY_PRIORITY="${CC_EXP_RETRY_PRIORITY:-0}"

read -r -a POOL_SIZES <<<"$POOL_SIZES_STR"
read -r -a WORKSPACE_MODES <<<"$WORKSPACE_MODES_STR"
read -r -a WORKSPACE_STATE_MODES <<<"$WORKSPACE_STATE_MODES_STR"
read -r -a TASKS_PER_USER_LIST <<<"$TASKS_PER_USER_LIST_STR"

ensure_artifact_dir "$ARTIFACT_ROOT"
build_go_binary "$TASKSIM_BIN" "./cmd/tasksim"

printf 'session_id=%s\nagent_runtime=%s\nusers=%s\npool_sizes=%s\nworkspace_modes=%s\nworkspace_state_modes=%s\ntasks_per_user=%s\nrepeat=%s\n' \
  "$SESSION_ID" "$AGENT_RUNTIME" "$USERS_CSV" "$POOL_SIZES_STR" "$WORKSPACE_MODES_STR" "$WORKSPACE_STATE_MODES_STR" "$TASKS_PER_USER_LIST_STR" "$REPEAT" \
  >"$ARTIFACT_ROOT/session.env"

user_count="$(csv_len "$USERS_CSV")"

for workspace_mode in "${WORKSPACE_MODES[@]}"; do
  for workspace_state_mode in "${WORKSPACE_STATE_MODES[@]}"; do
    for pool_size in "${POOL_SIZES[@]}"; do
      for tasks_per_user in "${TASKS_PER_USER_LIST[@]}"; do
        for rep in $(seq 1 "$REPEAT"); do
          run_slug="pool${pool_size}_wm${workspace_mode}_ws${workspace_state_mode}_tasks${tasks_per_user}_rep${rep}"
          run_dir="$ARTIFACT_ROOT/$run_slug"
          summary_file="$run_dir/summary.json"
          meta_file="$run_dir/meta.json"
          smoke_file="$run_dir/preflight_smoke.log"
          runner_log_file="$run_dir/runner.log"
          task_type="exp_tput_${SESSION_ID}_${run_slug}"
          submitted_total=$((user_count * tasks_per_user))

          ensure_artifact_dir "$run_dir"
          prepare_experiment_run "$AGENT_RUNTIME"
          restart_stack "$pool_size" "$workspace_mode" "$workspace_state_mode" "$RETRY_PRIORITY"
          smoke_check "$AGENT_RUNTIME" "$smoke_file"
          log "running throughput sweep: $run_slug submitted_total=$submitted_total"

          run_exit=0
          if ! "$TASKSIM_BIN" \
            --data-dir "$DATA_DIR" \
            --db-driver postgres \
            --db-dsn "$DB_DSN" \
            --users "$USERS_CSV" \
            --tasks-per-user "$tasks_per_user" \
            --submit-workers "$SUBMIT_WORKERS" \
            --dequeue-limit "$DEQUEUE_LIMIT" \
            --poll-interval "$POLL_INTERVAL" \
            --timeout "$TIMEOUT" \
            --max-retries "$MAX_RETRIES" \
            --task-type "$task_type" \
            --input-prefix "$INPUT_PREFIX" \
            --summary-file "$summary_file" \
            --append-csv "$ARTIFACT_ROOT/tasksim_summary.csv" \
            --fetch-final-task=true \
            --collect-events=false \
            --verbose=false \
            >"$run_dir/tasksim.log" 2>&1; then
            run_exit=$?
          fi

          cat >"$meta_file" <<EOF
{
  "experiment": "throughput_latency",
  "session_id": $(json_quote "$SESSION_ID"),
  "run_id": $(json_quote "$run_slug"),
  "runtime": $(json_quote "$AGENT_RUNTIME"),
  "pool_size": $pool_size,
  "workspace_mode": $(json_quote "$workspace_mode"),
  "workspace_state_mode": $(json_quote "$workspace_state_mode"),
  "retry_priority": $RETRY_PRIORITY,
  "auto_init_runtime": $(json_quote "$EXP_AUTO_INIT_RUNTIME"),
  "auto_reset_db": $(json_quote "$EXP_AUTO_RESET_DB"),
  "auto_clean_state": $(json_quote "$EXP_AUTO_CLEAN_STATE"),
  "users_csv": $(json_quote "$USERS_CSV"),
  "user_count": $user_count,
  "tasks_per_user": $tasks_per_user,
  "submitted_total": $submitted_total,
  "submit_workers": $SUBMIT_WORKERS,
  "dequeue_limit": $DEQUEUE_LIMIT,
  "poll_interval": $(json_quote "$POLL_INTERVAL"),
  "timeout": $(json_quote "$TIMEOUT"),
  "max_retries": $MAX_RETRIES,
  "repeat_index": $rep,
  "summary_file": $(json_quote "$summary_file"),
  "tasksim_exit_code": $run_exit
}
EOF
          if [ "$run_exit" -ne 0 ]; then
            log "throughput run failed or timed out: $run_slug exit_code=$run_exit"
          fi
          capture_runner_log "$runner_log_file"
        done
      done
    done
  done
done

log "throughput/latency experiment completed: $ARTIFACT_ROOT"
