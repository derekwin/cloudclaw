#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

CLOUDCLAW_CTL="${CLOUDCLAW_CTL:-$REPO_ROOT/deploy/server/cloudclawctl.sh}"
DATA_DIR="${DATA_DIR:-$REPO_ROOT/cloudclaw_data/data}"
ARTIFACTS_BASE="${ARTIFACTS_BASE:-$REPO_ROOT/experiment_artifacts}"
CC_HOME_DIR="${CC_HOME_DIR:-$(dirname "$DATA_DIR")}"
RESET_DB_BIN="${RESET_DB_BIN:-$ARTIFACTS_BASE/bin/exp-reset-db}"
EXP_AUTO_INIT_RUNTIME="${CC_EXP_AUTO_INIT_RUNTIME:-1}"
EXP_AUTO_RESET_DB="${CC_EXP_AUTO_RESET_DB:-1}"
EXP_AUTO_CLEAN_STATE="${CC_EXP_AUTO_CLEAN_STATE:-1}"
EXP_SMOKE_BEFORE_RUN="${CC_EXP_SMOKE_BEFORE_RUN:-1}"
EXP_FORCE_KILL_STRAY_RUNNERS="${CC_EXP_FORCE_KILL_STRAY_RUNNERS:-1}"

log() {
  printf '[experiments] %s\n' "$*" >&2
}

die() {
  printf '[experiments] error: %s\n' "$*" >&2
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

require_env() {
  local name=""
  for name in "$@"; do
    if [ -z "${!name:-}" ]; then
      die "environment variable is required: $name"
    fi
  done
}

timestamp_utc() {
  date -u '+%Y%m%dT%H%M%SZ'
}

json_quote() {
  require_cmd python3
  python3 - "$1" <<'PY'
import json
import sys

print(json.dumps(sys.argv[1]))
PY
}

csv_len() {
  require_cmd python3
  python3 - "$1" <<'PY'
import sys

items = [part.strip() for part in sys.argv[1].split(",") if part.strip()]
print(len(items))
PY
}

default_pool_label() {
  if [ -n "${POOL_LABEL:-}" ]; then
    printf '%s\n' "$POOL_LABEL"
    return
  fi
  if [ -z "${AGENT_RUNTIME:-}" ]; then
    die "AGENT_RUNTIME is required to derive POOL_LABEL"
  fi
  printf 'app=%s-agent\n' "$AGENT_RUNTIME"
}

ensure_artifact_dir() {
  mkdir -p "$1"
}

build_go_binary() {
  local output="$1"
  local pkg="$2"

  ensure_artifact_dir "$(dirname "$output")"
  log "building $pkg -> $output"
  (
    cd "$REPO_ROOT"
    GOCACHE="${GOCACHE:-$REPO_ROOT/.gocache}" go build -o "$output" "$pkg"
  )
}

init_runtime_config() {
  local runtime_name="${1:-${AGENT_RUNTIME:-}}"
  if [ "$EXP_AUTO_INIT_RUNTIME" != "1" ]; then
    return
  fi
  if [ -z "$runtime_name" ]; then
    die "runtime name is required for automatic runtime init"
  fi
  log "ensuring runtime config is initialized: runtime=$runtime_name"
  AGENT_RUNTIME="$runtime_name" bash "$CLOUDCLAW_CTL" init
}

reset_experiment_db() {
  if [ "$EXP_AUTO_RESET_DB" != "1" ]; then
    return
  fi
  require_env DB_DSN
  log "resetting CloudClaw tables in current DB_DSN"

  if command -v psql >/dev/null 2>&1; then
    psql "$DB_DSN" -v ON_ERROR_STOP=1 <<'SQL'
DROP TABLE IF EXISTS task_results CASCADE;
DROP TABLE IF EXISTS task_events CASCADE;
DROP TABLE IF EXISTS user_data_files CASCADE;
DROP TABLE IF EXISTS snapshots CASCADE;
DROP TABLE IF EXISTS containers CASCADE;
DROP TABLE IF EXISTS tasks CASCADE;
SQL
    return
  fi

  log "psql not found, falling back to Go reset helper"
  build_go_binary "$RESET_DB_BIN" "./cmd/exp-reset-db"
  "$RESET_DB_BIN" --db-dsn "$DB_DSN"
}

clean_experiment_state() {
  local runs_dir="$DATA_DIR/runs"
  local user_runtime_dir="$CC_HOME_DIR/user-runtime"

  if [ "$EXP_AUTO_CLEAN_STATE" != "1" ]; then
    return
  fi

  log "cleaning local experiment state: runs=$runs_dir user_runtime=$user_runtime_dir"
  mkdir -p "$runs_dir"
  find "$runs_dir" -mindepth 1 -maxdepth 1 -exec rm -rf {} +

  mkdir -p "$user_runtime_dir"
  find "$user_runtime_dir" -mindepth 1 -maxdepth 1 -exec rm -rf {} +
}

prepare_experiment_run() {
  local runtime_name="${1:-${AGENT_RUNTIME:-}}"
  if [ -n "$runtime_name" ]; then
    stop_runner_if_running "$runtime_name"
  fi
  terminate_stray_runners
  init_runtime_config "$runtime_name"
  reset_experiment_db
  clean_experiment_state
}

stop_runner_if_running() {
  local runtime_name="${1:-${AGENT_RUNTIME:-}}"
  if [ -z "$runtime_name" ]; then
    return
  fi
  log "stopping runner before environment reset: runtime=$runtime_name"
  AGENT_RUNTIME="$runtime_name" bash "$CLOUDCLAW_CTL" runner stop >/dev/null 2>&1 || true
}

find_stray_runner_pids() {
  require_env DB_DSN
  require_cmd python3
  python3 - "$DB_DSN" "$REPO_ROOT" <<'PY'
import subprocess
import sys

dsn = sys.argv[1]
repo_root = sys.argv[2]
try:
    out = subprocess.check_output(["ps", "-eo", "pid=,args="], text=True)
except Exception:
    sys.exit(0)

for line in out.splitlines():
    line = line.strip()
    if not line:
        continue
    parts = line.split(None, 1)
    if len(parts) != 2:
        continue
    pid, args = parts
    if "cloudclaw run" not in args:
        continue
    if dsn not in args:
        continue
    if repo_root not in args and "cloudclaw" not in args:
        continue
    print(pid)
PY
}

terminate_stray_runners() {
  local pids=""
  local pid=""
  if [ "$EXP_FORCE_KILL_STRAY_RUNNERS" != "1" ]; then
    return
  fi
  require_env DB_DSN

  pids="$(find_stray_runner_pids || true)"
  if [ -z "$pids" ]; then
    return
  fi

  log "terminating stray cloudclaw runners using current DB_DSN: $(printf '%s' "$pids" | tr '\n' ' ' | xargs)"
  for pid in $pids; do
    kill "$pid" >/dev/null 2>&1 || true
  done
  sleep 1
  for pid in $pids; do
    if kill -0 "$pid" >/dev/null 2>&1; then
      kill -9 "$pid" >/dev/null 2>&1 || true
    fi
  done
}

smoke_check() {
  local runtime_name="${1:-${AGENT_RUNTIME:-}}"
  local output_file="${2:-}"
  if [ "$EXP_SMOKE_BEFORE_RUN" != "1" ]; then
    return
  fi
  require_env DB_DSN
  if [ -z "$runtime_name" ]; then
    die "runtime name is required for smoke check"
  fi
  log "running smoke check for runtime=$runtime_name"
  if [ -n "$output_file" ]; then
    if ! AGENT_RUNTIME="$runtime_name" DB_DSN="$DB_DSN" bash "$CLOUDCLAW_CTL" smoke >"$output_file" 2>&1; then
      die "smoke check failed; see $output_file"
    fi
    return
  fi
  AGENT_RUNTIME="$runtime_name" DB_DSN="$DB_DSN" bash "$CLOUDCLAW_CTL" smoke
}

capture_runner_log() {
  local output_file="$1"
  local log_file="$CC_HOME_DIR/logs/cloudclaw.log"
  ensure_artifact_dir "$(dirname "$output_file")"
  if [ -f "$log_file" ]; then
    cp "$log_file" "$output_file"
    return
  fi
  printf 'runner log not found: %s\n' "$log_file" >"$output_file"
}

restart_stack() {
  local pool_size="$1"
  local workspace_mode="$2"
  local workspace_state_mode="$3"
  local retry_priority="$4"

  require_env AGENT_RUNTIME DB_DSN
  log "restarting pool/runtime: pool_size=$pool_size workspace_mode=$workspace_mode workspace_state_mode=$workspace_state_mode retry_priority=$retry_priority"

  AGENT_RUNTIME="$AGENT_RUNTIME" \
  DB_DSN="$DB_DSN" \
  POOL_SIZE="$pool_size" \
  WORKSPACE_MODE="$workspace_mode" \
  WORKSPACE_STATE_MODE="$workspace_state_mode" \
  RETRY_PRIORITY="$retry_priority" \
  bash "$CLOUDCLAW_CTL" runner stop

  AGENT_RUNTIME="$AGENT_RUNTIME" \
  DB_DSN="$DB_DSN" \
  POOL_SIZE="$pool_size" \
  WORKSPACE_MODE="$workspace_mode" \
  WORKSPACE_STATE_MODE="$workspace_state_mode" \
  RETRY_PRIORITY="$retry_priority" \
  bash "$CLOUDCLAW_CTL" pool restart

  AGENT_RUNTIME="$AGENT_RUNTIME" \
  DB_DSN="$DB_DSN" \
  POOL_SIZE="$pool_size" \
  WORKSPACE_MODE="$workspace_mode" \
  WORKSPACE_STATE_MODE="$workspace_state_mode" \
  RETRY_PRIORITY="$retry_priority" \
  bash "$CLOUDCLAW_CTL" runner start

  sleep "${CLOUDCLAW_RESTART_SLEEP:-2}"
}

ensure_stack_running() {
  local pool_size="$1"
  local workspace_mode="$2"
  local workspace_state_mode="$3"
  local retry_priority="$4"

  require_env AGENT_RUNTIME DB_DSN

  AGENT_RUNTIME="$AGENT_RUNTIME" \
  DB_DSN="$DB_DSN" \
  POOL_SIZE="$pool_size" \
  WORKSPACE_MODE="$workspace_mode" \
  WORKSPACE_STATE_MODE="$workspace_state_mode" \
  RETRY_PRIORITY="$retry_priority" \
  bash "$CLOUDCLAW_CTL" pool start

  AGENT_RUNTIME="$AGENT_RUNTIME" \
  DB_DSN="$DB_DSN" \
  POOL_SIZE="$pool_size" \
  WORKSPACE_MODE="$workspace_mode" \
  WORKSPACE_STATE_MODE="$workspace_state_mode" \
  RETRY_PRIORITY="$retry_priority" \
  bash "$CLOUDCLAW_CTL" runner start
}

first_pool_container() {
  local label=""
  require_cmd docker
  label="$(default_pool_label)"
  docker ps --filter "label=$label" --format '{{.ID}}' | head -n 1
}
