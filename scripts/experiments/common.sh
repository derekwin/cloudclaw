#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
CLOUDCLAWCTL="$REPO_ROOT/deploy/server/cloudclawctl.sh"

GO_BIN="${GO_BIN:-go}"
CC_DB_DRIVER="${CC_DB_DRIVER:-sqlite}"
CC_DB_DSN="${CC_DB_DSN:-}"
CC_DATA_DIR="${CC_DATA_DIR:-$REPO_ROOT/cloudclaw_data/data}"

log() {
  printf '[exp] %s\n' "$*"
}

die() {
  printf '[exp][error] %s\n' "$*" >&2
  exit 1
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "missing command: $1"
}

expand_home_path() {
  local raw="$1"
  case "$raw" in
    "~") printf '%s\n' "$HOME" ;;
    "~/"*) printf '%s/%s\n' "$HOME" "${raw#~/}" ;;
    *) printf '%s\n' "$raw" ;;
  esac
}

resolve_repo_path() {
  local raw
  raw="$(expand_home_path "$1")"
  case "$raw" in
    /*) printf '%s\n' "$raw" ;;
    *) printf '%s/%s\n' "$REPO_ROOT" "$raw" ;;
  esac
}

resolve_cc_home() {
  if [[ -n "${CC_HOME:-}" ]]; then
    resolve_repo_path "$CC_HOME"
    return 0
  fi

  local home_file="$REPO_ROOT/cloudclaw_data-home"
  if [[ -f "$home_file" ]]; then
    local saved
    saved="$(head -n 1 "$home_file" | tr -d '\r')"
    if [[ -n "$saved" ]]; then
      resolve_repo_path "$saved"
      return 0
    fi
  fi

  printf '%s\n' "$REPO_ROOT/cloudclaw_data"
}

default_pool_label() {
  local runtime
  runtime="$(printf '%s' "${AGENT_RUNTIME:-}" | tr '[:upper:]' '[:lower:]')"
  case "$runtime" in
    opencode) printf '%s\n' 'app=opencode-agent' ;;
    claudecode) printf '%s\n' 'app=claudecode-agent' ;;
    *) die "AGENT_RUNTIME is required (opencode|claudecode)" ;;
  esac
}

runner_pid_file() {
  local cc_home
  cc_home="$(resolve_cc_home)"
  printf '%s/run/cloudclaw.pid\n' "$cc_home"
}

ensure_runtime() {
  if [[ -z "${AGENT_RUNTIME:-}" ]]; then
    die "AGENT_RUNTIME is required (opencode|claudecode)"
  fi
}

run_cloudclawctl() {
  ensure_runtime
  AGENT_RUNTIME="$AGENT_RUNTIME" RETRY_PRIORITY="${RETRY_PRIORITY:-0}" bash "$CLOUDCLAWCTL" "$@"
}

split_csv() {
  local raw="$1"
  local -n out_ref="$2"
  IFS=',' read -r -a out_ref <<<"$raw"
  for i in "${!out_ref[@]}"; do
    out_ref[$i]="$(echo "${out_ref[$i]}" | xargs)"
  done
}

gen_users_csv() {
  local n="$1"
  local prefix="${2:-u}"
  local users=()
  local i
  for ((i=1; i<=n; i++)); do
    users+=("${prefix}${i}")
  done
  local IFS=,
  printf '%s\n' "${users[*]}"
}

prepare_output_dir() {
  local exp_name="$1"
  local base="${OUT_BASE_DIR:-$REPO_ROOT/experiment_artifacts}"
  local ts
  ts="$(date '+%Y%m%d_%H%M%S')"
  local out_dir="${OUT_DIR:-$base/$exp_name/$ts}"
  mkdir -p "$out_dir"
  printf '%s\n' "$out_dir"
}

run_tasksim() {
  (cd "$REPO_ROOT" && "$GO_BIN" run ./cmd/tasksim "$@")
}
