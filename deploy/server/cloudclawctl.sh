#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
CC_HOME_FILE="${CC_HOME_FILE:-$REPO_ROOT/cloudclaw_data-home}"

resolve_home_path() {
  local raw="$1"
  if [ -z "$raw" ]; then
    raw="cloudclaw_data"
  fi
  case "$raw" in
    "~") raw="$HOME" ;;
    "~/"*) raw="$HOME/${raw#~/}" ;;
  esac
  case "$raw" in
    /*) printf '%s\n' "$raw" ;;
    *) printf '%s/%s\n' "$REPO_ROOT" "$raw" ;;
  esac
}

if [ -n "${CC_HOME:-}" ]; then
  CC_HOME="$(resolve_home_path "$CC_HOME")"
elif [ -f "$CC_HOME_FILE" ]; then
  saved_home="$(head -n 1 "$CC_HOME_FILE" | tr -d '\r')"
  CC_HOME="$(resolve_home_path "$saved_home")"
else
  CC_HOME="$(resolve_home_path "cloudclaw_data")"
fi
POOL_SIZE="${POOL_SIZE:-3}"
AGENT_RUNTIME="${AGENT_RUNTIME:-}"
POOL_LABEL="${POOL_LABEL:-}"
POOL_NAME_PREFIX="${POOL_NAME_PREFIX:-}"
BASE_IMAGE="${BASE_IMAGE:-}"
FALLBACK_BASE_IMAGE="${FALLBACK_BASE_IMAGE:-}"
RUNNER_IMAGE="${RUNNER_IMAGE:-}"
DOCKER_TASK_CMD="${DOCKER_TASK_CMD:-}"
DOCKER_REMOTE_DIR="${DOCKER_REMOTE_DIR:-/tmp/cloudclaw}"
DB_DRIVER="${DB_DRIVER:-sqlite}"
DB_DSN="${DB_DSN:-}"
PICOCLAW_CONFIG_MOUNT_PATH="${PICOCLAW_CONFIG_MOUNT_PATH:-/workspace/.picoclaw}"
OPENCLAW_CONFIG_MOUNT_PATH="${OPENCLAW_CONFIG_MOUNT_PATH:-/workspace/.openclaw}"
OWNER_UID="${AGENT_OWNER_UID:-${PICOCLAW_OWNER_UID:-${SUDO_UID:-$(id -u)}}}"
OWNER_GID="${AGENT_OWNER_GID:-${PICOCLAW_OWNER_GID:-${SUDO_GID:-$(id -g)}}}"

CLOUDCLAW_BIN="$CC_HOME/bin/cloudclaw"
RUNNER_DIR="$CC_HOME/runner"
SHARED_DIR="$CC_HOME/shared"
SHARED_PICO_DIR="$SHARED_DIR/picoclaw"
SHARED_PICO_CONFIG="$SHARED_PICO_DIR/config.json"
SHARED_OPENCLAW_DIR="$SHARED_DIR/openclaw"
SHARED_OPENCLAW_CONFIG="$SHARED_OPENCLAW_DIR/openclaw.json"
DATA_DIR="$CC_HOME/data"
LOG_DIR="$CC_HOME/logs"
RUN_DIR="$CC_HOME/run"
PID_FILE="$RUN_DIR/cloudclaw.pid"

RUNTIME_PROFILE_LOADED=0
RUNTIME_NAME=""
RUNTIME_EXECUTOR=""
RUNTIME_CONFIG_DIR=""
RUNTIME_CONFIG_FILE=""
RUNTIME_CONFIG_MOUNT_PATH=""
RUNTIME_CONFIG_BASENAME=""
DEFAULT_BASE_IMAGE=""

log() {
  printf '[cloudclawctl] %s\n' "$*"
}

die() {
  echo "$*" >&2
  exit 1
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || { echo "missing command: $1" >&2; exit 1; }
}

require_arg() {
  local name="$1"
  local value="${2:-}"
  if [ -z "$value" ]; then
    die "missing argument: $name"
  fi
}

require_runtime() {
  if [ -z "$AGENT_RUNTIME" ]; then
    die "AGENT_RUNTIME is required (picoclaw|openclaw)"
  fi
}

load_runtime_profile() {
  if [ "$RUNTIME_PROFILE_LOADED" = "1" ]; then
    return
  fi
  require_runtime

  local runtime
  runtime="$(printf '%s' "$AGENT_RUNTIME" | tr '[:upper:]' '[:lower:]')"
  case "$runtime" in
    picoclaw)
      RUNTIME_NAME="picoclaw"
      RUNTIME_EXECUTOR="docker-picoclaw"
      RUNTIME_CONFIG_DIR="$SHARED_PICO_DIR"
      RUNTIME_CONFIG_FILE="$SHARED_PICO_CONFIG"
      RUNTIME_CONFIG_MOUNT_PATH="$PICOCLAW_CONFIG_MOUNT_PATH"
      RUNTIME_CONFIG_BASENAME="config.json"
      DEFAULT_BASE_IMAGE="docker.io/sipeed/picoclaw:latest"
      POOL_LABEL="${POOL_LABEL:-app=picoclaw-agent}"
      POOL_NAME_PREFIX="${POOL_NAME_PREFIX:-picoclaw-agent}"
      BASE_IMAGE="${BASE_IMAGE:-$DEFAULT_BASE_IMAGE}"
      FALLBACK_BASE_IMAGE="${FALLBACK_BASE_IMAGE:-ghcr.io/sipeed/picoclaw:latest}"
      RUNNER_IMAGE="${RUNNER_IMAGE:-cloudclaw/picoclaw-runner:latest}"
      DOCKER_TASK_CMD="${DOCKER_TASK_CMD:-run_picoclaw_task.sh}"
      ;;
    openclaw)
      RUNTIME_NAME="openclaw"
      RUNTIME_EXECUTOR="docker-openclaw"
      RUNTIME_CONFIG_DIR="$SHARED_OPENCLAW_DIR"
      RUNTIME_CONFIG_FILE="$SHARED_OPENCLAW_CONFIG"
      RUNTIME_CONFIG_MOUNT_PATH="$OPENCLAW_CONFIG_MOUNT_PATH"
      RUNTIME_CONFIG_BASENAME="openclaw.json"
      DEFAULT_BASE_IMAGE="openclaw:local"
      POOL_LABEL="${POOL_LABEL:-app=openclaw-agent}"
      POOL_NAME_PREFIX="${POOL_NAME_PREFIX:-openclaw-agent}"
      BASE_IMAGE="${BASE_IMAGE:-$DEFAULT_BASE_IMAGE}"
      RUNNER_IMAGE="${RUNNER_IMAGE:-cloudclaw/openclaw-runner:latest}"
      DOCKER_TASK_CMD="${DOCKER_TASK_CMD:-run_openclaw_task.sh}"
      ;;
    *)
      die "unsupported AGENT_RUNTIME: $AGENT_RUNTIME (supported: picoclaw|openclaw)"
      ;;
  esac
  AGENT_RUNTIME="$runtime"
  RUNTIME_PROFILE_LOADED=1
}

runtime_config_mount_file() {
  printf '%s/%s\n' "$RUNTIME_CONFIG_MOUNT_PATH" "$RUNTIME_CONFIG_BASENAME"
}

runner_image_exists() {
  docker image inspect "$RUNNER_IMAGE" >/dev/null 2>&1
}

ensure_runner_image() {
  need_cmd docker
  if runner_image_exists; then
    return 0
  fi
  log "runner image not found, running install first"
  install_all
}

can_pull_image() {
  local image="$1"
  docker image inspect "$image" >/dev/null 2>&1 || docker manifest inspect "$image" >/dev/null 2>&1
}

resolve_base_image() {
  load_runtime_profile
  if can_pull_image "$BASE_IMAGE"; then
    echo "$BASE_IMAGE"
    return 0
  fi

  if [ -n "$FALLBACK_BASE_IMAGE" ] && [ "$BASE_IMAGE" = "$DEFAULT_BASE_IMAGE" ] && can_pull_image "$FALLBACK_BASE_IMAGE"; then
    log "warning: cannot access $BASE_IMAGE, fallback to $FALLBACK_BASE_IMAGE"
    echo "$FALLBACK_BASE_IMAGE"
    return 0
  fi

  fallback_hint=""
  if [ -n "$FALLBACK_BASE_IMAGE" ]; then
    fallback_hint="  3) if Docker Hub has it: BASE_IMAGE=$FALLBACK_BASE_IMAGE bash $0 up"
  fi

  cat >&2 <<EOF
cannot access base image: $BASE_IMAGE
try one of:
  1) docker login ghcr.io
  2) BASE_IMAGE=<accessible-image> bash $0 up
$fallback_hint
EOF
  return 1
}

ensure_dirs() {
  mkdir -p "$CC_HOME/bin" "$RUNNER_DIR" "$SHARED_DIR" "$SHARED_PICO_DIR" "$SHARED_OPENCLAW_DIR" "$DATA_DIR" "$LOG_DIR" "$RUN_DIR"
}

ensure_runtime_config_ready() {
  load_runtime_profile
  ensure_dirs
  if [ ! -s "$RUNTIME_CONFIG_FILE" ]; then
    die "missing ${RUNTIME_NAME} config: $RUNTIME_CONFIG_FILE (run: AGENT_RUNTIME=$RUNTIME_NAME bash $0 config init-full)"
  fi
}

ensure_runtime_config_for_up() {
  load_runtime_profile
  ensure_dirs
  if [ -s "$RUNTIME_CONFIG_FILE" ]; then
    return
  fi
  log "config not found, generating full template (same as: AGENT_RUNTIME=$RUNTIME_NAME $0 init)"
  init_full_config
}

show_config_path() {
  load_runtime_profile
  ensure_dirs
  echo "$RUNTIME_CONFIG_FILE"
}

show_config() {
  load_runtime_profile
  ensure_dirs
  if [ ! -f "$RUNTIME_CONFIG_FILE" ]; then
    echo "config not found: $RUNTIME_CONFIG_FILE" >&2
    exit 1
  fi
  cat "$RUNTIME_CONFIG_FILE"
}

import_config() {
  local src="$1"
  require_arg "<path-to-config.json>" "$src"
  load_runtime_profile
  ensure_dirs
  if [ ! -f "$src" ]; then
    die "config source not found: $src"
  fi
  cp "$src" "$RUNTIME_CONFIG_FILE"
  log "imported $RUNTIME_NAME config: $src -> $RUNTIME_CONFIG_FILE"
}

reset_config() {
  load_runtime_profile
  init_full_config
  log "reset $RUNTIME_NAME config: $RUNTIME_CONFIG_FILE"
}

edit_config() {
  load_runtime_profile
  ensure_dirs
  if [ ! -s "$RUNTIME_CONFIG_FILE" ]; then
    log "config not found, initializing full template first"
    init_full_config
  fi
  editor="${EDITOR:-vi}"
  if ! command -v "$editor" >/dev/null 2>&1; then
    die "editor not found: $editor (set EDITOR to a valid command)"
  fi
  "$editor" "$RUNTIME_CONFIG_FILE"
}

init_picoclaw_config_full() {
  ensure_dirs
  ensure_runner_image

  if [ -f "$RUNTIME_CONFIG_FILE" ]; then
    cp "$RUNTIME_CONFIG_FILE" "$RUNTIME_CONFIG_FILE.bak"
    log "backup created: $RUNTIME_CONFIG_FILE.bak"
  fi

  docker run --rm --entrypoint /bin/sh "$RUNNER_IMAGE" -lc '
set -eu
HOME=/tmp/picoclaw-home
mkdir -p "$HOME"

if command -v picoclaw >/dev/null 2>&1; then
  picoclaw onboard </dev/null >/tmp/picoclaw-onboard.log 2>&1 || true
fi

for p in \
  "$HOME/.picoclaw/config.json" \
  "/root/.picoclaw/config.json" \
  "/app/config/config.example.json" \
  "/config/config.example.json" \
  "/workspace/config/config.example.json"; do
  if [ -s "$p" ]; then
    cat "$p"
    exit 0
  fi
done

example="$(find / -maxdepth 5 -type f -name config.example.json 2>/dev/null | head -n 1 || true)"
if [ -n "$example" ] && [ -s "$example" ]; then
  cat "$example"
  exit 0
fi

echo "unable to generate full config from picoclaw image" >&2
exit 1
' > "$RUNTIME_CONFIG_FILE.tmp"

if [ ! -s "$RUNTIME_CONFIG_FILE.tmp" ]; then
  rm -f "$RUNTIME_CONFIG_FILE.tmp"
  die "generated config is empty"
fi

mv -f "$RUNTIME_CONFIG_FILE.tmp" "$RUNTIME_CONFIG_FILE"
log "initialized full config template: $RUNTIME_CONFIG_FILE"
}

init_openclaw_config_full() {
  ensure_dirs
  ensure_runner_image

  if [ -f "$RUNTIME_CONFIG_FILE" ]; then
    cp "$RUNTIME_CONFIG_FILE" "$RUNTIME_CONFIG_FILE.bak"
    log "backup created: $RUNTIME_CONFIG_FILE.bak"
  fi

  docker run --rm --entrypoint /bin/sh "$RUNNER_IMAGE" -lc '
set -eu
HOME=/tmp/openclaw-home
mkdir -p "$HOME"

if command -v openclaw >/dev/null 2>&1; then
  openclaw config init >/tmp/openclaw-config-init.log 2>&1 || true
  openclaw init >/tmp/openclaw-init.log 2>&1 || true
fi

for p in \
  "$HOME/.openclaw/openclaw.json" \
  "$HOME/.config/openclaw/openclaw.json" \
  "/root/.openclaw/openclaw.json" \
  "/root/.config/openclaw/openclaw.json" \
  "/workspace/.openclaw/openclaw.json" \
  "/app/config/openclaw.json" \
  "/app/config/openclaw.example.json" \
  "/config/openclaw.json" \
  "/config/openclaw.example.json" \
  "/workspace/config/openclaw.json" \
  "/workspace/config/openclaw.example.json"; do
  if [ -s "$p" ]; then
    cat "$p"
    exit 0
  fi
done

example="$(find / -maxdepth 6 -type f \( -name "openclaw*.json" -o -name "*openclaw*config*.json" -o -name "config.example.json" \) 2>/dev/null | head -n 1 || true)"
if [ -n "$example" ] && [ -s "$example" ]; then
  cat "$example"
  exit 0
fi

echo "unable to generate full config from openclaw image" >&2
exit 1
' > "$RUNTIME_CONFIG_FILE.tmp"

if [ ! -s "$RUNTIME_CONFIG_FILE.tmp" ]; then
  rm -f "$RUNTIME_CONFIG_FILE.tmp"
  die "generated config is empty"
fi

mv -f "$RUNTIME_CONFIG_FILE.tmp" "$RUNTIME_CONFIG_FILE"
log "initialized full config template: $RUNTIME_CONFIG_FILE"
}

init_full_config() {
  load_runtime_profile
  if [ "$RUNTIME_NAME" = "picoclaw" ]; then
    init_picoclaw_config_full
    return
  fi
  init_openclaw_config_full
}

edit_config_hint() {
  load_runtime_profile
  ensure_dirs
  if [ "$RUNTIME_NAME" = "picoclaw" ]; then
    cat <<EOF
picoclaw config path:
  $RUNTIME_CONFIG_FILE

Edit this file to configure full sections like:
  - model_list / providers
  - tools (mcp / skills / web / exec ...)
  - channels / heartbeat / gateway

Optional: generate a full template from picoclaw first:
  AGENT_RUNTIME=picoclaw bash $0 config init-full

Then run:
  AGENT_RUNTIME=picoclaw bash $0 pool start
EOF
    return
  fi

  cat <<EOF
openclaw config path:
  $RUNTIME_CONFIG_FILE

Optional: generate a full template from openclaw first:
  AGENT_RUNTIME=openclaw bash $0 config init-full

Then run:
  AGENT_RUNTIME=openclaw bash $0 pool start
EOF
}

print_runtime_config_paths() {
  load_runtime_profile
  ensure_dirs
  cat <<EOF
runtime:
  $RUNTIME_NAME
shared config:
  $RUNTIME_CONFIG_FILE
container mount path:
  $(runtime_config_mount_file)
EOF
}

install_all() {
  load_runtime_profile
  need_cmd go
  need_cmd docker
  ensure_dirs

  log "building cloudclaw binary"
  (cd "$REPO_ROOT" && go build -o "$CLOUDCLAW_BIN" ./cmd/cloudclaw)

  log "preparing runner assets"
  cp "$SCRIPT_DIR/templates/run_picoclaw_task.sh" "$RUNNER_DIR/run_picoclaw_task.sh"
  cp "$SCRIPT_DIR/templates/run_openclaw_task.sh" "$RUNNER_DIR/run_openclaw_task.sh"
  cp "$SCRIPT_DIR/templates/Dockerfile.runner" "$RUNNER_DIR/Dockerfile.runner"
  chmod +x "$RUNNER_DIR/run_picoclaw_task.sh" "$RUNNER_DIR/run_openclaw_task.sh"

  resolved_base_image="$(resolve_base_image)"
  log "using base image: $resolved_base_image"

  log "building runner image: $RUNNER_IMAGE"
  docker build \
    --build-arg "BASE_IMAGE=$resolved_base_image" \
    -t "$RUNNER_IMAGE" \
    -f "$RUNNER_DIR/Dockerfile.runner" \
    "$RUNNER_DIR"

  log "install complete"
}

start_pool() {
  load_runtime_profile
  ensure_runner_image
  ensure_dirs
  ensure_runtime_config_ready

  for i in $(seq 1 "$POOL_SIZE"); do
    name="${POOL_NAME_PREFIX}-${i}"
    if docker ps -a --format '{{.Names}}' | grep -Fxq "$name"; then
      log "recreating container to refresh mounted config/env: $name"
      docker rm -f "$name" >/dev/null
    fi

    log "creating container: $name"
    env_args=()
    if [ "$RUNTIME_NAME" = "picoclaw" ]; then
      env_args+=(
        "-e" "PICOCLAW_HOME=${RUNTIME_CONFIG_MOUNT_PATH}"
        "-e" "PICOCLAW_CONFIG=$(runtime_config_mount_file)"
      )
    else
      env_args+=(
        "-e" "OPENCLAW_HOME=${RUNTIME_CONFIG_MOUNT_PATH}"
        "-e" "OPENCLAW_CONFIG_PATH=$(runtime_config_mount_file)"
        "-e" "OPENCLAW_EXEC_MODE=${OPENCLAW_EXEC_MODE:-gateway}"
      )
      if [ -n "${OPENCLAW_GATEWAY_TOKEN:-}" ]; then
        env_args+=("-e" "OPENCLAW_GATEWAY_TOKEN=${OPENCLAW_GATEWAY_TOKEN}")
      fi
      if [ -n "${OPENCLAW_GATEWAY_BIND:-}" ]; then
        env_args+=("-e" "OPENCLAW_GATEWAY_BIND=${OPENCLAW_GATEWAY_BIND}")
      fi
      if [ -n "${OPENCLAW_GATEWAY_PORT:-}" ]; then
        env_args+=("-e" "OPENCLAW_GATEWAY_PORT=${OPENCLAW_GATEWAY_PORT}")
      fi
      if [ -n "${OPENCLAW_GATEWAY_MANAGE:-}" ]; then
        env_args+=("-e" "OPENCLAW_GATEWAY_MANAGE=${OPENCLAW_GATEWAY_MANAGE}")
      fi
      if [ -n "${OPENCLAW_AGENT_ID:-}" ]; then
        env_args+=("-e" "OPENCLAW_AGENT_ID=${OPENCLAW_AGENT_ID}")
      fi
      if [ -n "${OPENCLAW_TIMEOUT_SECONDS:-}" ]; then
        env_args+=("-e" "OPENCLAW_TIMEOUT_SECONDS=${OPENCLAW_TIMEOUT_SECONDS}")
      fi
    fi

    docker run -d \
      --name "$name" \
      --label "$POOL_LABEL" \
      --restart unless-stopped \
      --add-host host.docker.internal:host-gateway \
      --user "${OWNER_UID}:${OWNER_GID}" \
      --entrypoint /bin/sh \
      -v "${RUNTIME_CONFIG_DIR}:${RUNTIME_CONFIG_MOUNT_PATH}:ro" \
      "${env_args[@]}" \
      "$RUNNER_IMAGE" \
      -lc 'sleep infinity' >/dev/null
  done

  log "pool started"
}

stop_pool() {
  load_runtime_profile
  need_cmd docker
  ids="$(docker ps -aq --filter "label=$POOL_LABEL")"
  if [ -z "$ids" ]; then
    log "no containers found for label: $POOL_LABEL"
    return
  fi
  log "removing pool containers"
  docker rm -f $ids >/dev/null
}

restart_pool() {
  stop_pool
  start_pool
}

status_pool() {
  load_runtime_profile
  need_cmd docker
  echo "  containers:"
  docker ps --filter "label=$POOL_LABEL" --format '    - {{.Names}} | {{.Status}} | {{.Image}}' || true
}

start_cloudclaw() {
  load_runtime_profile
  ensure_dirs
  if [ ! -x "$CLOUDCLAW_BIN" ]; then
    echo "cloudclaw binary not found: $CLOUDCLAW_BIN (run: $0 install)" >&2
    exit 1
  fi

  if [ -f "$PID_FILE" ] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
    log "cloudclaw already running (pid=$(cat "$PID_FILE"))"
    return
  fi

  log "starting cloudclaw runner"
  cmd=(
    "$CLOUDCLAW_BIN" run
    --data-dir "$DATA_DIR"
    --db-driver "$DB_DRIVER"
    --executor "$RUNTIME_EXECUTOR"
    --docker-label-selector "$POOL_LABEL"
    --docker-remote-dir "$DOCKER_REMOTE_DIR"
    --shared-skills-dir "$SHARED_DIR"
    --docker-task-cmd "$DOCKER_TASK_CMD"
  )

  if [ -n "$DB_DSN" ]; then
    cmd+=(--db-dsn "$DB_DSN")
  fi

  nohup "${cmd[@]}" >"$LOG_DIR/cloudclaw.log" 2>&1 &
  echo "$!" > "$PID_FILE"
  log "cloudclaw started (pid=$!, log=$LOG_DIR/cloudclaw.log)"
}

stop_cloudclaw() {
  if [ ! -f "$PID_FILE" ]; then
    log "cloudclaw is not running"
    return
  fi
  pid="$(cat "$PID_FILE")"
  if kill -0 "$pid" 2>/dev/null; then
    log "stopping cloudclaw (pid=$pid)"
    kill "$pid"
    sleep 1
    if kill -0 "$pid" 2>/dev/null; then
      kill -9 "$pid" || true
    fi
  fi
  rm -f "$PID_FILE"
}

restart_cloudclaw() {
  stop_cloudclaw
  start_cloudclaw
}

runner_logs() {
  load_runtime_profile
  ensure_dirs
  lines="${1:-100}"
  if [ ! -f "$LOG_DIR/cloudclaw.log" ]; then
    die "log file not found: $LOG_DIR/cloudclaw.log"
  fi
  tail -n "$lines" "$LOG_DIR/cloudclaw.log"
}

status_all() {
  load_runtime_profile
  log "cloudclaw status"
  if [ -f "$PID_FILE" ] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
    echo "  runner: running (pid=$(cat "$PID_FILE"))"
  else
    echo "  runner: stopped"
  fi

  status_pool
  echo "  runtime: $RUNTIME_NAME"
  echo "  config: $RUNTIME_CONFIG_FILE"
}

smoke() {
  load_runtime_profile
  if [ ! -x "$CLOUDCLAW_BIN" ]; then
    echo "cloudclaw binary not found: $CLOUDCLAW_BIN" >&2
    exit 1
  fi

  submit_out="$($CLOUDCLAW_BIN task submit --data-dir "$DATA_DIR" --db-driver "$DB_DRIVER" --user-id smoke_user --task-type smoke --input "smoke test")"
  task_id="$(printf '%s' "$submit_out" | sed -n 's/.*"id": "\([^"]*\)".*/\1/p' | head -n1)"
  if [ -z "$task_id" ]; then
    echo "failed to parse task id from submit output" >&2
    echo "$submit_out"
    exit 1
  fi

  log "submitted smoke task: $task_id"
  for _ in $(seq 1 30); do
    status_json="$($CLOUDCLAW_BIN task status --data-dir "$DATA_DIR" --db-driver "$DB_DRIVER" --task-id "$task_id")"
    status="$(printf '%s' "$status_json" | sed -n 's/.*"status": "\([^"]*\)".*/\1/p' | head -n1)"
    if [ "$status" = "SUCCEEDED" ]; then
      echo "$status_json"
      return
    fi
    if [ "$status" = "FAILED" ] || [ "$status" = "CANCELED" ]; then
      echo "$status_json"
      exit 1
    fi
    sleep 1
  done

  log "smoke task did not finish in time"
  $CLOUDCLAW_BIN task status --data-dir "$DATA_DIR" --db-driver "$DB_DRIVER" --task-id "$task_id"
  exit 1
}

usage() {
  cat <<USAGE
Usage:
  $0 <group> <action> [args]
  $0 <shortcut>

Groups:
  home   set <path> | show
  config path | show | edit | import <file> | reset | init-full | help
  pool   start | stop | restart | status
  runner start | stop | restart | status | logs [lines]

Shortcuts:
  init        Generate runtime config template
  install     Build cloudclaw binary + runner image
  up          install + init(if missing) + pool start + runner start
  down        runner stop + pool stop
  restart     down + up
  status      Show runner + pool + config status
  smoke       Submit one smoke task and wait result
  help        Show this help

Legacy aliases (compatible):
  set-home <path>, config-path, show-config, config-help
  start-pool, stop-pool, start, stop

Examples:
  AGENT_RUNTIME=picoclaw $0 init
  AGENT_RUNTIME=picoclaw $0 up
  AGENT_RUNTIME=openclaw $0 init
  AGENT_RUNTIME=openclaw $0 up
  AGENT_RUNTIME=picoclaw $0 smoke

Environment overrides:
  AGENT_RUNTIME (required: picoclaw|openclaw)
  CC_HOME (default: repo-relative ./cloudclaw_data unless overridden)
  CC_HOME_FILE (default: $REPO_ROOT/cloudclaw_data-home, stores persisted CC_HOME)
  POOL_SIZE (default: 3)
  POOL_LABEL (runtime default: app=<runtime>-agent)
  POOL_NAME_PREFIX (runtime default: <runtime>-agent)
  BASE_IMAGE (runtime default: picoclaw=docker.io/sipeed/picoclaw:latest, openclaw=openclaw:local)
  FALLBACK_BASE_IMAGE (default for picoclaw: ghcr.io/sipeed/picoclaw:latest)
  RUNNER_IMAGE (runtime default: cloudclaw/<runtime>-runner:latest)
  AGENT_OWNER_UID / AGENT_OWNER_GID (optional container user id)
  PICOCLAW_CONFIG_MOUNT_PATH (default: /workspace/.picoclaw)
  OPENCLAW_CONFIG_MOUNT_PATH (default: /workspace/.openclaw)
  DOCKER_TASK_CMD (runtime default: run_picoclaw_task.sh|run_openclaw_task.sh)
  DOCKER_REMOTE_DIR (default: /tmp/cloudclaw)
  OPENCLAW_EXEC_MODE (default: gateway, runtime=openclaw only)
  OPENCLAW_GATEWAY_TOKEN / OPENCLAW_GATEWAY_BIND / OPENCLAW_GATEWAY_PORT / OPENCLAW_GATEWAY_MANAGE (optional)
  OPENCLAW_AGENT_ID / OPENCLAW_TIMEOUT_SECONDS (optional)

Notes:
  AGENT_RUNTIME must be specified; no default runtime is assumed.
  "up" auto-runs init when runtime config does not exist.
  pool startup always refreshes containers to avoid stale config/env drift.
  cloudclaw task execution only reads mounted runtime config.
USAGE
}

set_home() {
  local raw_path="$1"
  require_arg "<path>" "$raw_path"
  mkdir -p "$(dirname "$CC_HOME_FILE")"
  printf '%s\n' "$raw_path" > "$CC_HOME_FILE"
  resolved="$(resolve_home_path "$raw_path")"
  mkdir -p "$resolved"
  log "saved CC_HOME path: $raw_path (resolved: $resolved)"
}

cmd_home() {
  local action="${1:-show}"
  local arg="${2:-}"
  case "$action" in
    set) set_home "$arg" ;;
    show) echo "$CC_HOME" ;;
    *) die "unknown home action: $action (use: set|show)" ;;
  esac
}

cmd_config() {
  local action="${1:-help}"
  local arg="${2:-}"
  case "$action" in
    path) show_config_path ;;
    show) show_config ;;
    edit) edit_config ;;
    import) import_config "$arg" ;;
    reset) reset_config ;;
    init-full) init_full_config ;;
    help)
      edit_config_hint
      print_runtime_config_paths
      ;;
    *)
      die "unknown config action: $action (use: path|show|edit|import|reset|init-full|help)"
      ;;
  esac
}

cmd_pool() {
  local action="${1:-status}"
  case "$action" in
    start) start_pool ;;
    stop) stop_pool ;;
    restart) restart_pool ;;
    status) status_pool ;;
    *) die "unknown pool action: $action (use: start|stop|restart|status)" ;;
  esac
}

cmd_runner() {
  local action="${1:-status}"
  local arg="${2:-}"
  case "$action" in
    start) start_cloudclaw ;;
    stop) stop_cloudclaw ;;
    restart) restart_cloudclaw ;;
    status) status_all ;;
    logs) runner_logs "$arg" ;;
    *) die "unknown runner action: $action (use: start|stop|restart|status|logs)" ;;
  esac
}

cmd_shortcut() {
  local action="${1:-help}"
  local arg1="${2:-}"
  local arg2="${3:-}"
  case "$action" in
    init) init_full_config ;;
    install) install_all ;;
    up)
      install_all
      ensure_runtime_config_for_up
      start_pool
      start_cloudclaw
      ;;
    down)
      stop_cloudclaw
      stop_pool
      ;;
    restart)
      stop_cloudclaw
      stop_pool
      install_all
      start_pool
      start_cloudclaw
      ;;
    status) status_all ;;
    smoke) smoke ;;
    help|--help|-h|"") usage ;;
    # legacy aliases
    set-home) set_home "$arg1" ;;
    config-path) show_config_path ;;
    show-config) show_config ;;
    config-help)
      edit_config_hint
      print_runtime_config_paths
      ;;
    start-pool) start_pool ;;
    stop-pool) stop_pool ;;
    start) start_cloudclaw ;;
    stop) stop_cloudclaw ;;
    *)
      die "unknown command: $action (run: $0 help)"
      ;;
  esac
  _unused="$arg2"
}

main() {
  local group="${1:-help}"
  local action="${2:-}"
  local arg="${3:-}"
  case "$group" in
    home) cmd_home "$action" "$arg" ;;
    config) cmd_config "$action" "$arg" ;;
    pool) cmd_pool "$action" ;;
    runner) cmd_runner "$action" "$arg" ;;
    *) cmd_shortcut "$group" "$action" "$arg" ;;
  esac
}

main "${1:-}" "${2:-}" "${3:-}"
