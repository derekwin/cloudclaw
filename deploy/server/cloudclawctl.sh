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
POOL_LABEL="${POOL_LABEL:-app=picoclaw-agent}"
POOL_NAME_PREFIX="${POOL_NAME_PREFIX:-picoclaw-agent}"
DEFAULT_BASE_IMAGE="docker.io/sipeed/picoclaw:latest"
FALLBACK_BASE_IMAGE="${FALLBACK_BASE_IMAGE:-ghcr.io/sipeed/picoclaw:latest}"
BASE_IMAGE="${BASE_IMAGE:-$DEFAULT_BASE_IMAGE}"
RUNNER_IMAGE="${RUNNER_IMAGE:-cloudclaw/picoclaw-runner:latest}"
DOCKER_TASK_CMD="${DOCKER_TASK_CMD:-run_picoclaw_task.sh}"
DB_DRIVER="${DB_DRIVER:-sqlite}"
DB_DSN="${DB_DSN:-}"
PICO_MODEL_NAME="${PICO_MODEL_NAME:-default}"
PICO_MODEL="${PICO_MODEL:-${OPENROUTER_MODEL:-openai/gpt-5.2}}"
PICO_API_BASE="${PICO_API_BASE:-}"
PICO_API_KEY="${PICO_API_KEY:-${OPENROUTER_API_KEY:-}}"
PICO_REQUEST_TIMEOUT="${PICO_REQUEST_TIMEOUT:-300}"
OWNER_UID="${PICOCLAW_OWNER_UID:-${SUDO_UID:-$(id -u)}}"
OWNER_GID="${PICOCLAW_OWNER_GID:-${SUDO_GID:-$(id -g)}}"
PICOCLAW_CONTAINER_HOME="${PICOCLAW_CONTAINER_HOME:-/tmp}"

CLOUDCLAW_BIN="$CC_HOME/bin/cloudclaw"
RUNNER_DIR="$CC_HOME/runner"
SHARED_DIR="$CC_HOME/shared"
DATA_DIR="$CC_HOME/data"
PICOCLAW_HOME_DIR="${PICOCLAW_HOME_DIR:-$CC_HOME/picoclaw}"
PICOCLAW_CONFIG_FILE="$PICOCLAW_HOME_DIR/config.json"
LOG_DIR="$CC_HOME/logs"
RUN_DIR="$CC_HOME/run"
PID_FILE="$RUN_DIR/cloudclaw.pid"

log() {
  printf '[cloudclawctl] %s\n' "$*"
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || { echo "missing command: $1" >&2; exit 1; }
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
  if can_pull_image "$BASE_IMAGE"; then
    echo "$BASE_IMAGE"
    return 0
  fi

  if [ "$BASE_IMAGE" = "$DEFAULT_BASE_IMAGE" ] && can_pull_image "$FALLBACK_BASE_IMAGE"; then
    log "warning: cannot access $BASE_IMAGE, fallback to $FALLBACK_BASE_IMAGE"
    echo "$FALLBACK_BASE_IMAGE"
    return 0
  fi

  cat >&2 <<EOF
cannot access base image: $BASE_IMAGE
try one of:
  1) docker login ghcr.io
  2) BASE_IMAGE=<accessible-image> bash $0 up
  3) if Docker Hub has it: BASE_IMAGE=$FALLBACK_BASE_IMAGE bash $0 up
EOF
  return 1
}

ensure_dirs() {
  mkdir -p "$CC_HOME/bin" "$RUNNER_DIR" "$SHARED_DIR" "$DATA_DIR" "$PICOCLAW_HOME_DIR" "$LOG_DIR" "$RUN_DIR"
}

fix_picoclaw_permissions() {
  need_cmd docker
  ensure_dirs
  docker run --rm \
    --entrypoint /bin/sh \
    --user 0:0 \
    -v "$PICOCLAW_HOME_DIR:/root/.picoclaw" \
    "$RUNNER_IMAGE" \
    -lc "chown -R ${OWNER_UID}:${OWNER_GID} /root/.picoclaw 2>/dev/null || true; chmod -R u+rwX /root/.picoclaw 2>/dev/null || true" >/dev/null
}

picoclaw_config_needs_edit() {
  if [ ! -f "$PICOCLAW_CONFIG_FILE" ]; then
    return 0
  fi
  if [ ! -r "$PICOCLAW_CONFIG_FILE" ]; then
    return 0
  fi
  if grep -Eq 'YOUR_API_KEY(_HERE)?' "$PICOCLAW_CONFIG_FILE"; then
    return 0
  fi
  if grep -Eq '"api_key"[[:space:]]*:[[:space:]]*""' "$PICOCLAW_CONFIG_FILE"; then
    return 0
  fi
  return 1
}

init_picoclaw_config() {
  ensure_runner_image
  ensure_dirs
  if [ -f "$PICOCLAW_CONFIG_FILE" ]; then
    log "picoclaw config already exists: $PICOCLAW_CONFIG_FILE"
    fix_picoclaw_permissions
    return 0
  fi

  log "initializing picoclaw data at $PICOCLAW_HOME_DIR"
  docker run --rm \
    --entrypoint /bin/sh \
    --user "${OWNER_UID}:${OWNER_GID}" \
    -e "HOME=$PICOCLAW_CONTAINER_HOME" \
    -v "$PICOCLAW_HOME_DIR:$PICOCLAW_CONTAINER_HOME/.picoclaw" \
    "$RUNNER_IMAGE" \
    -lc 'picoclaw onboard >/dev/null 2>&1 || true; test -f "$HOME/.picoclaw/config.json"'

  fix_picoclaw_permissions

  if [ ! -f "$PICOCLAW_CONFIG_FILE" ]; then
    echo "failed to initialize picoclaw config file" >&2
    return 1
  fi

  cat <<EOF
[cloudclawctl] picoclaw config initialized:
  $PICOCLAW_CONFIG_FILE

Please edit this file (set provider/model/api_key), then run:
  bash $0 up
EOF
  return 2
}

require_picoclaw_config_ready() {
  if [ ! -f "$PICOCLAW_CONFIG_FILE" ]; then
    init_picoclaw_config
    return $?
  fi
  if [ ! -r "$PICOCLAW_CONFIG_FILE" ]; then
    log "picoclaw config is not readable, attempting to fix permissions"
    fix_picoclaw_permissions
  fi
  if [ ! -r "$PICOCLAW_CONFIG_FILE" ]; then
    cat <<EOF
[cloudclawctl] picoclaw config is not readable:
  $PICOCLAW_CONFIG_FILE

Please fix permissions (or remove and re-init), then run:
  bash $0 up
EOF
    return 2
  fi
  if picoclaw_config_needs_edit; then
    cat <<EOF
[cloudclawctl] picoclaw config needs provider/api_key setup:
  $PICOCLAW_CONFIG_FILE

Please finish the config, then run:
  bash $0 up
EOF
    return 2
  fi
  return 0
}

install_all() {
  need_cmd go
  need_cmd docker
  ensure_dirs

  log "building cloudclaw binary"
  (cd "$REPO_ROOT" && go build -o "$CLOUDCLAW_BIN" ./cmd/cloudclaw)

  log "preparing runner assets"
  cp "$SCRIPT_DIR/templates/run_picoclaw_task.sh" "$RUNNER_DIR/run_picoclaw_task.sh"
  cp "$SCRIPT_DIR/templates/Dockerfile.runner" "$RUNNER_DIR/Dockerfile.runner"
  chmod +x "$RUNNER_DIR/run_picoclaw_task.sh"

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
  ensure_runner_image
  ensure_dirs
  require_picoclaw_config_ready

  if [ -z "${PICO_API_KEY:-}" ]; then
    case "$PICO_MODEL" in
      ollama/*|vllm/*) ;;
      *) log "warning: PICO_API_KEY is empty for model=$PICO_MODEL; provider may reject requests" ;;
    esac
  fi

  for i in $(seq 1 "$POOL_SIZE"); do
    name="${POOL_NAME_PREFIX}-${i}"
    if docker ps --format '{{.Names}}' | grep -Fxq "$name"; then
      log "container already running: $name"
      continue
    fi

    if docker ps -a --format '{{.Names}}' | grep -Fxq "$name"; then
      log "starting existing container: $name"
      docker start "$name" >/dev/null
      continue
    fi

    log "creating container: $name"
    env_args=(
      "-e" "PICO_MODEL_NAME=${PICO_MODEL_NAME}"
      "-e" "PICO_MODEL=${PICO_MODEL}"
      "-e" "PICO_REQUEST_TIMEOUT=${PICO_REQUEST_TIMEOUT}"
    )
    if [ -n "${PICO_API_BASE:-}" ]; then
      env_args+=("-e" "PICO_API_BASE=${PICO_API_BASE}")
    fi
    if [ -n "${PICO_API_KEY:-}" ]; then
      env_args+=("-e" "PICO_API_KEY=${PICO_API_KEY}")
    fi

    docker run -d \
      --name "$name" \
      --label "$POOL_LABEL" \
      --restart unless-stopped \
      --add-host host.docker.internal:host-gateway \
      --user "${OWNER_UID}:${OWNER_GID}" \
      -e "HOME=$PICOCLAW_CONTAINER_HOME" \
      -v "$PICOCLAW_HOME_DIR:$PICOCLAW_CONTAINER_HOME/.picoclaw" \
      --entrypoint /bin/sh \
      "${env_args[@]}" \
      "$RUNNER_IMAGE" \
      -lc 'sleep infinity' >/dev/null
  done

  log "pool started"
}

stop_pool() {
  need_cmd docker
  ids="$(docker ps -aq --filter "label=$POOL_LABEL")"
  if [ -z "$ids" ]; then
    log "no containers found for label: $POOL_LABEL"
    return
  fi
  log "removing pool containers"
  docker rm -f $ids >/dev/null
}

start_cloudclaw() {
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
    --executor docker-picoclaw
    --docker-label-selector "$POOL_LABEL"
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

status_all() {
  log "cloudclaw status"
  if [ -f "$PID_FILE" ] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
    echo "  runner: running (pid=$(cat "$PID_FILE"))"
  else
    echo "  runner: stopped"
  fi

  echo "  containers:"
  docker ps --filter "label=$POOL_LABEL" --format '    - {{.Names}} | {{.Status}} | {{.Image}}' || true
}

smoke() {
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
Usage: $0 <command>

Commands:
  install      Build cloudclaw binary + build picoclaw runner image
  set-home     Persist cloudclaw home directory (absolute or repo-relative)
  init-config  Initialize picoclaw config data directory
  start-pool   Start docker picoclaw pool
  stop-pool    Stop/remove docker picoclaw pool
  start        Start cloudclaw runner daemon
  stop         Stop cloudclaw runner daemon
  status       Show cloudclaw + pool status
  smoke        Submit one smoke task and wait result
  up           install + start-pool + start
  down         stop + stop-pool

Environment overrides:
  CC_HOME (default: repo-relative ./cloudclaw_data unless overridden)
  CC_HOME_FILE (default: $REPO_ROOT/cloudclaw_data-home, stores persisted CC_HOME)
  POOL_SIZE (default: 3)
  POOL_LABEL (default: app=picoclaw-agent)
  POOL_NAME_PREFIX (default: picoclaw-agent)
  BASE_IMAGE (default: docker.io/sipeed/picoclaw:latest)
  FALLBACK_BASE_IMAGE (default: ghcr.io/sipeed/picoclaw:latest)
  RUNNER_IMAGE (default: cloudclaw/picoclaw-runner:latest)
  PICOCLAW_HOME_DIR (default: \$CC_HOME/picoclaw)
  PICOCLAW_CONTAINER_HOME (default: /tmp)
  PICOCLAW_OWNER_UID / PICOCLAW_OWNER_GID (optional owner for picoclaw data files)
  PICO_MODEL_NAME (default: default)
  PICO_MODEL (default: openai/gpt-5.2)
  PICO_API_BASE (optional; auto default for openai/openrouter/ollama/vllm)
  PICO_API_KEY (optional for ollama/vllm local, usually required for cloud providers)
  PICO_REQUEST_TIMEOUT (default: 300)
  OPENROUTER_API_KEY / OPENROUTER_MODEL (legacy compatibility)
  DOCKER_TASK_CMD (default: run_picoclaw_task.sh)
USAGE
}

cmd="${1:-}"
arg1="${2:-}"
case "$cmd" in
  install)
    install_all
    ;;
  set-home)
    if [ -z "$arg1" ]; then
      echo "usage: $0 set-home <path>" >&2
      exit 1
    fi
    mkdir -p "$(dirname "$CC_HOME_FILE")"
    printf '%s\n' "$arg1" > "$CC_HOME_FILE"
    resolved="$(resolve_home_path "$arg1")"
    mkdir -p "$resolved"
    log "saved CC_HOME path: $arg1 (resolved: $resolved)"
    ;;
  init-config)
    if init_picoclaw_config; then
      :
    else
      rc=$?
      if [ "$rc" -ne 2 ]; then
        exit "$rc"
      fi
    fi
    ;;
  start-pool)
    start_pool
    ;;
  stop-pool)
    stop_pool
    ;;
  start)
    start_cloudclaw
    ;;
  stop)
    stop_cloudclaw
    ;;
  status)
    status_all
    ;;
  smoke)
    smoke
    ;;
  up)
    install_all
    start_pool
    start_cloudclaw
    ;;
  down)
    stop_cloudclaw
    stop_pool
    ;;
  help|--help|-h|"")
    usage
    ;;
  *)
    echo "unknown command: $cmd" >&2
    usage
    exit 1
    ;;
esac
