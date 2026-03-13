#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/common.sh"

require_cmd go
require_cmd python3
require_cmd docker
require_env AGENT_RUNTIME DB_DSN

SESSION_ID="${SESSION_ID:-fault-$(timestamp_utc)}"
ARTIFACT_ROOT="${ARTIFACT_ROOT:-$ARTIFACTS_BASE/fault_recovery/$SESSION_ID}"
TASKSIM_BIN="${TASKSIM_BIN:-$ARTIFACTS_BASE/bin/tasksim}"

FAULT_MODES_STR="${CC_EXP_FAULT_MODES:-runner container}"
RETRY_PRIORITIES_STR="${CC_EXP_RETRY_PRIORITIES:-0}"

POOL_SIZE_VALUE="${CC_EXP_POOL_SIZE:-${POOL_SIZE:-3}}"
WORKSPACE_MODE_VALUE="${CC_EXP_WORKSPACE_MODE:-${WORKSPACE_MODE:-mount}}"
WORKSPACE_STATE_MODE_VALUE="${CC_EXP_WORKSPACE_STATE_MODE:-${WORKSPACE_STATE_MODE:-ephemeral}}"

USERS_CSV="${CC_EXP_USERS:-sim_u1,sim_u2,sim_u3,sim_u4}"
TASKS_PER_USER="${CC_EXP_TASKS_PER_USER:-12}"
SUBMIT_WORKERS="${CC_EXP_SUBMIT_WORKERS:-8}"
DEQUEUE_LIMIT="${CC_EXP_DEQUEUE_LIMIT:-50}"
POLL_INTERVAL="${CC_EXP_POLL_INTERVAL:-300ms}"
TIMEOUT="${CC_EXP_TIMEOUT:-20m}"
MAX_RETRIES="${CC_EXP_MAX_RETRIES:-3}"
REPEAT="${CC_EXP_REPEAT:-1}"

FAULT_DELAY_SECONDS="${CC_EXP_FAULT_DELAY_SECONDS:-10}"
FAULT_INTERVAL_SECONDS="${CC_EXP_FAULT_INTERVAL_SECONDS:-15}"
FAULT_DOWN_SECONDS="${CC_EXP_FAULT_DOWN_SECONDS:-8}"
FAULT_COUNT="${CC_EXP_FAULT_COUNT:-1}"
INPUT_PREFIX="${CC_EXP_INPUT_PREFIX:-fault recovery benchmark task with a detailed structured answer}"

read -r -a FAULT_MODES <<<"$FAULT_MODES_STR"
read -r -a RETRY_PRIORITIES <<<"$RETRY_PRIORITIES_STR"

ensure_artifact_dir "$ARTIFACT_ROOT"
build_go_binary "$TASKSIM_BIN" "./cmd/tasksim"

printf 'session_id=%s\nagent_runtime=%s\nusers=%s\nfault_modes=%s\nretry_priorities=%s\nfault_count=%s\n' \
  "$SESSION_ID" "$AGENT_RUNTIME" "$USERS_CSV" "$FAULT_MODES_STR" "$RETRY_PRIORITIES_STR" "$FAULT_COUNT" \
  >"$ARTIFACT_ROOT/session.env"

inject_fault() {
  local mode="$1"
  local retry_priority="$2"
  local down_seconds="$3"

  case "$mode" in
    runner)
      log "injecting runner fault for ${down_seconds}s"
      AGENT_RUNTIME="$AGENT_RUNTIME" DB_DSN="$DB_DSN" POOL_SIZE="$POOL_SIZE_VALUE" WORKSPACE_MODE="$WORKSPACE_MODE_VALUE" WORKSPACE_STATE_MODE="$WORKSPACE_STATE_MODE_VALUE" RETRY_PRIORITY="$retry_priority" bash "$CLOUDCLAW_CTL" runner stop
      sleep "$down_seconds"
      AGENT_RUNTIME="$AGENT_RUNTIME" DB_DSN="$DB_DSN" POOL_SIZE="$POOL_SIZE_VALUE" WORKSPACE_MODE="$WORKSPACE_MODE_VALUE" WORKSPACE_STATE_MODE="$WORKSPACE_STATE_MODE_VALUE" RETRY_PRIORITY="$retry_priority" bash "$CLOUDCLAW_CTL" runner start
      ;;
    container)
      local container_id=""
      container_id="$(first_pool_container)"
      if [ -z "$container_id" ]; then
        die "failed to find a running pool container for fault injection"
      fi
      log "injecting container fault on $container_id"
      docker kill "$container_id" >/dev/null
      sleep "$down_seconds"
      ;;
    *)
      die "unsupported fault mode: $mode"
      ;;
  esac
}

cleanup() {
  set +e
  ensure_stack_running "$POOL_SIZE_VALUE" "$WORKSPACE_MODE_VALUE" "$WORKSPACE_STATE_MODE_VALUE" "${RETRY_PRIORITIES[0]}"
}
trap cleanup EXIT

for retry_priority in "${RETRY_PRIORITIES[@]}"; do
  for fault_mode in "${FAULT_MODES[@]}"; do
    for rep in $(seq 1 "$REPEAT"); do
      run_slug="fault_${fault_mode}_retry${retry_priority}_rep${rep}"
      run_dir="$ARTIFACT_ROOT/$run_slug"
      summary_file="$run_dir/summary.json"
      task_type="exp_fault_${SESSION_ID}_${run_slug}"

      ensure_artifact_dir "$run_dir"
      prepare_experiment_run "$AGENT_RUNTIME"
      restart_stack "$POOL_SIZE_VALUE" "$WORKSPACE_MODE_VALUE" "$WORKSPACE_STATE_MODE_VALUE" "$retry_priority"

      log "running fault-recovery workload: $run_slug"
      (
        "$TASKSIM_BIN" \
          --data-dir "$DATA_DIR" \
          --db-driver postgres \
          --db-dsn "$DB_DSN" \
          --users "$USERS_CSV" \
          --tasks-per-user "$TASKS_PER_USER" \
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
          --collect-events=true \
          --verbose=false
      ) >"$run_dir/tasksim.log" 2>&1 &
      tasksim_pid="$!"

      sleep "$FAULT_DELAY_SECONDS"
      for fault_index in $(seq 1 "$FAULT_COUNT"); do
        inject_fault "$fault_mode" "$retry_priority" "$FAULT_DOWN_SECONDS"
        if [ "$fault_index" -lt "$FAULT_COUNT" ]; then
          sleep "$FAULT_INTERVAL_SECONDS"
        fi
      done

      wait "$tasksim_pid"

      cat >"$run_dir/meta.json" <<EOF
{
  "experiment": "fault_recovery",
  "session_id": $(json_quote "$SESSION_ID"),
  "run_id": $(json_quote "$run_slug"),
  "runtime": $(json_quote "$AGENT_RUNTIME"),
  "fault_mode": $(json_quote "$fault_mode"),
  "retry_priority": $retry_priority,
  "pool_size": $POOL_SIZE_VALUE,
  "workspace_mode": $(json_quote "$WORKSPACE_MODE_VALUE"),
  "workspace_state_mode": $(json_quote "$WORKSPACE_STATE_MODE_VALUE"),
  "auto_init_runtime": $(json_quote "$EXP_AUTO_INIT_RUNTIME"),
  "auto_reset_db": $(json_quote "$EXP_AUTO_RESET_DB"),
  "auto_clean_state": $(json_quote "$EXP_AUTO_CLEAN_STATE"),
  "users_csv": $(json_quote "$USERS_CSV"),
  "tasks_per_user": $TASKS_PER_USER,
  "submit_workers": $SUBMIT_WORKERS,
  "dequeue_limit": $DEQUEUE_LIMIT,
  "poll_interval": $(json_quote "$POLL_INTERVAL"),
  "timeout": $(json_quote "$TIMEOUT"),
  "max_retries": $MAX_RETRIES,
  "fault_delay_seconds": $FAULT_DELAY_SECONDS,
  "fault_interval_seconds": $FAULT_INTERVAL_SECONDS,
  "fault_down_seconds": $FAULT_DOWN_SECONDS,
  "fault_count": $FAULT_COUNT,
  "repeat_index": $rep,
  "summary_file": $(json_quote "$summary_file")
}
EOF

      ensure_stack_running "$POOL_SIZE_VALUE" "$WORKSPACE_MODE_VALUE" "$WORKSPACE_STATE_MODE_VALUE" "$retry_priority"
    done
  done
done

log "fault-recovery experiment completed: $ARTIFACT_ROOT"
