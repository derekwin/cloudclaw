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

resolve_host_path() {
  local raw="$1"
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
CONTAINER_HARDEN="${CONTAINER_HARDEN:-1}"
CONTAINER_PIDS_LIMIT="${CONTAINER_PIDS_LIMIT:-512}"
CONTAINER_READONLY_ROOTFS="${CONTAINER_READONLY_ROOTFS:-0}"
CONTAINER_NETWORK="${CONTAINER_NETWORK:-}"
AGENT_ENV_FILE="${AGENT_ENV_FILE:-}"
OPENCODE_HOST_CONFIG_DIR="${OPENCODE_HOST_CONFIG_DIR:-$HOME/.config/opencode}"
CLAUDECODE_HOST_CONFIG_DIR="${CLAUDECODE_HOST_CONFIG_DIR:-$HOME/.claudecode}"
CLAUDECODE_OFFICIAL_CONFIG_DIR="${CLAUDECODE_OFFICIAL_CONFIG_DIR:-$HOME/.claude}"
OPENCODE_CONFIG_MOUNT_PATH="${OPENCODE_CONFIG_MOUNT_PATH:-/workspace/.config/opencode}"
CLAUDECODE_CONFIG_MOUNT_PATH="${CLAUDECODE_CONFIG_MOUNT_PATH:-/workspace/.claudecode}"
WORKSPACE_MOUNT_PATH="${WORKSPACE_MOUNT_PATH:-/workspace/cloudclaw/runs}"
OWNER_UID="${AGENT_OWNER_UID:-${OPENCODE_OWNER_UID:-${SUDO_UID:-$(id -u)}}}"
OWNER_GID="${AGENT_OWNER_GID:-${OPENCODE_OWNER_GID:-${SUDO_GID:-$(id -g)}}}"

CLOUDCLAW_BIN="$CC_HOME/bin/cloudclaw"
RUNNER_DIR="$CC_HOME/runner"
SHARED_DIR="$CC_HOME/shared"
SHARED_CLAUDECODE_DIR="$SHARED_DIR/claudecode"
SHARED_CLAUDECODE_CONFIG="$SHARED_CLAUDECODE_DIR/config.json"
SHARED_OPENCODE_DIR="$SHARED_DIR/opencode"
LEGACY_OPENCODE_CONFIG_DIR="$CC_HOME/opencode/config"
DATA_DIR="$CC_HOME/data"
USER_RUNTIME_DIR="$CC_HOME/user-runtime"
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
    die "AGENT_RUNTIME is required (opencode|claudecode)"
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
    opencode)
      RUNTIME_NAME="opencode"
      RUNTIME_EXECUTOR="docker-opencode"
      RUNTIME_CONFIG_DIR="$SHARED_OPENCODE_DIR"
      RUNTIME_CONFIG_FILE="$SHARED_OPENCODE_DIR/opencode.json"
      RUNTIME_CONFIG_MOUNT_PATH="$OPENCODE_CONFIG_MOUNT_PATH"
      RUNTIME_CONFIG_BASENAME="$(basename "$RUNTIME_CONFIG_FILE")"
      DEFAULT_BASE_IMAGE="ghcr.io/anomalyco/opencode:latest"
      POOL_LABEL="${POOL_LABEL:-app=opencode-agent}"
      POOL_NAME_PREFIX="${POOL_NAME_PREFIX:-opencode-agent}"
      BASE_IMAGE="${BASE_IMAGE:-$DEFAULT_BASE_IMAGE}"
      RUNNER_IMAGE="${RUNNER_IMAGE:-cloudclaw/opencode-runner:latest}"
      DOCKER_TASK_CMD="${DOCKER_TASK_CMD:-run_opencode_task.sh}"
      ;;
    claudecode)
      RUNTIME_NAME="claudecode"
      RUNTIME_EXECUTOR="docker-claudecode"
      RUNTIME_CONFIG_DIR="$SHARED_CLAUDECODE_DIR"
      RUNTIME_CONFIG_FILE="$SHARED_CLAUDECODE_CONFIG"
      RUNTIME_CONFIG_MOUNT_PATH="$CLAUDECODE_CONFIG_MOUNT_PATH"
      RUNTIME_CONFIG_BASENAME="config.json"
      DEFAULT_BASE_IMAGE="claudecode:latest"
      POOL_LABEL="${POOL_LABEL:-app=claudecode-agent}"
      POOL_NAME_PREFIX="${POOL_NAME_PREFIX:-claudecode-agent}"
      BASE_IMAGE="${BASE_IMAGE:-$DEFAULT_BASE_IMAGE}"
      RUNNER_IMAGE="${RUNNER_IMAGE:-cloudclaw/claudecode-runner:latest}"
      DOCKER_TASK_CMD="${DOCKER_TASK_CMD:-run_claudecode_task.sh}"
      ;;
    *)
      die "unsupported AGENT_RUNTIME: $AGENT_RUNTIME (supported: opencode|claudecode)"
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
  mkdir -p "$CC_HOME/bin" "$RUNNER_DIR" "$SHARED_DIR" "$SHARED_CLAUDECODE_DIR" "$SHARED_OPENCODE_DIR" "$DATA_DIR" "$DATA_DIR/runs" "$USER_RUNTIME_DIR" "$LOG_DIR" "$RUN_DIR"
  if [ -d "$LEGACY_OPENCODE_CONFIG_DIR" ] && [ -n "$(ls -A "$LEGACY_OPENCODE_CONFIG_DIR" 2>/dev/null)" ] && [ -z "$(ls -A "$SHARED_OPENCODE_DIR" 2>/dev/null)" ]; then
    cp -R "$LEGACY_OPENCODE_CONFIG_DIR/." "$SHARED_OPENCODE_DIR/" || true
    log "migrated legacy opencode shared config: $LEGACY_OPENCODE_CONFIG_DIR -> $SHARED_OPENCODE_DIR"
  fi
}

resolve_host_opencode_bin() {
  if command -v opencode >/dev/null 2>&1; then
    command -v opencode
    return 0
  fi
  for candidate in "$HOME/.local/bin/opencode" "$HOME/bin/opencode"; do
    if [ -x "$candidate" ]; then
      echo "$candidate"
      return 0
    fi
  done
  return 1
}

ensure_host_opencode_installed() {
  local opencode_bin
  if opencode_bin="$(resolve_host_opencode_bin)"; then
    log "host opencode already installed: $opencode_bin"
    return 0
  fi

  need_cmd curl
  need_cmd bash
  log "host opencode not found, installing: curl -fsSL https://opencode.ai/install | bash"
  if ! /bin/sh -c 'curl -fsSL https://opencode.ai/install | bash'; then
    die "failed to install opencode on host"
  fi
  if ! opencode_bin="$(resolve_host_opencode_bin)"; then
    die "opencode installed but executable not found (tried PATH and ~/.local/bin/opencode)"
  fi
  log "host opencode installed: $opencode_bin"
}

bootstrap_host_opencode_config_if_missing() {
  local opencode_bin host_config_home host_data_home
  if [ -d "$OPENCODE_HOST_CONFIG_DIR" ] && [ -n "$(ls -A "$OPENCODE_HOST_CONFIG_DIR" 2>/dev/null)" ]; then
    return 0
  fi

  ensure_host_opencode_installed
  opencode_bin="$(resolve_host_opencode_bin)" || die "opencode executable not found after installation"
  host_config_home="$(dirname "$OPENCODE_HOST_CONFIG_DIR")"
  host_data_home="${XDG_DATA_HOME:-$HOME/.local/share}"
  mkdir -p "$OPENCODE_HOST_CONFIG_DIR"

  # Trigger opencode to initialize default config under host XDG dirs.
  XDG_CONFIG_HOME="$host_config_home" XDG_DATA_HOME="$host_data_home" "$opencode_bin" config init >/tmp/cloudclaw-opencode-config-init.log 2>&1 || true
  XDG_CONFIG_HOME="$host_config_home" XDG_DATA_HOME="$host_data_home" "$opencode_bin" init >/tmp/cloudclaw-opencode-init.log 2>&1 || true
  XDG_CONFIG_HOME="$host_config_home" XDG_DATA_HOME="$host_data_home" "$opencode_bin" --help >/tmp/cloudclaw-opencode-help.log 2>&1 || true

  if [ ! -d "$OPENCODE_HOST_CONFIG_DIR" ] || [ -z "$(ls -A "$OPENCODE_HOST_CONFIG_DIR" 2>/dev/null)" ]; then
    die "host opencode config is still empty: $OPENCODE_HOST_CONFIG_DIR (run opencode once manually, then retry init)"
  fi
}

resolve_host_claude_bin() {
  if command -v claudecode >/dev/null 2>&1; then
    command -v claudecode
    return 0
  fi
  if command -v claude >/dev/null 2>&1; then
    command -v claude
    return 0
  fi
  for candidate in "$HOME/.local/bin/claudecode" "$HOME/bin/claudecode" "$HOME/.local/bin/claude" "$HOME/bin/claude"; do
    if [ -x "$candidate" ]; then
      echo "$candidate"
      return 0
    fi
  done
  return 1
}

ensure_host_claude_installed() {
  local claude_bin
  if claude_bin="$(resolve_host_claude_bin)"; then
    log "host claude code already installed: $claude_bin"
    return 0
  fi

  need_cmd curl
  need_cmd bash
  log "host claude code not found, installing: curl -fsSL https://claude.ai/install.sh | bash"
  if ! /bin/sh -c 'curl -fsSL https://claude.ai/install.sh | bash'; then
    die "failed to install claude code on host"
  fi
  if ! claude_bin="$(resolve_host_claude_bin)"; then
    die "claude code installed but executable not found (tried PATH and ~/.local/bin)"
  fi
  log "host claude code installed: $claude_bin"
}

bootstrap_host_claudecode_config_if_missing() {
  local claude_bin

  mkdir -p "$CLAUDECODE_HOST_CONFIG_DIR"
  if [ -s "$CLAUDECODE_HOST_CONFIG_DIR/config.json" ]; then
    return 0
  fi

  # Claude Code official user settings location.
  if [ -s "$CLAUDECODE_OFFICIAL_CONFIG_DIR/settings.json" ]; then
    cp -f "$CLAUDECODE_OFFICIAL_CONFIG_DIR/settings.json" "$CLAUDECODE_HOST_CONFIG_DIR/config.json"
    return 0
  fi

  ensure_host_claude_installed
  claude_bin="$(resolve_host_claude_bin)" || die "claude code executable not found after installation"

  "$claude_bin" config list >/tmp/cloudclaw-claude-config-list.log 2>&1 || true
  "$claude_bin" --help >/tmp/cloudclaw-claude-help.log 2>&1 || true

  if [ -s "$CLAUDECODE_OFFICIAL_CONFIG_DIR/settings.json" ]; then
    cp -f "$CLAUDECODE_OFFICIAL_CONFIG_DIR/settings.json" "$CLAUDECODE_HOST_CONFIG_DIR/config.json"
  fi
  if [ ! -s "$CLAUDECODE_HOST_CONFIG_DIR/config.json" ]; then
    die "host claudecode config is still missing: $CLAUDECODE_HOST_CONFIG_DIR/config.json"
  fi
}

ensure_runtime_config_ready() {
  load_runtime_profile
  ensure_dirs
  if [ "$RUNTIME_NAME" = "opencode" ]; then
    if [ -d "$RUNTIME_CONFIG_DIR" ] && [ -n "$(ls -A "$RUNTIME_CONFIG_DIR" 2>/dev/null)" ]; then
      return
    fi
    die "missing opencode shared config dir: $RUNTIME_CONFIG_DIR (run: AGENT_RUNTIME=opencode bash $0 config init-full)"
  fi
  if [ ! -s "$RUNTIME_CONFIG_FILE" ]; then
    die "missing ${RUNTIME_NAME} config: $RUNTIME_CONFIG_FILE (run: AGENT_RUNTIME=$RUNTIME_NAME bash $0 config init-full)"
  fi
}

ensure_runtime_config_for_up() {
  load_runtime_profile
  ensure_dirs
  if [ "$RUNTIME_NAME" = "opencode" ]; then
    if [ -d "$RUNTIME_CONFIG_DIR" ] && [ -n "$(ls -A "$RUNTIME_CONFIG_DIR" 2>/dev/null)" ]; then
      return
    fi
    log "opencode shared config not found, bootstrapping (same as: AGENT_RUNTIME=opencode $0 init)"
    init_full_config
    return
  fi
  if [ -s "$RUNTIME_CONFIG_FILE" ]; then
    return
  fi
  log "config not found, initializing (same as: AGENT_RUNTIME=$RUNTIME_NAME $0 init)"
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
  if [ "$RUNTIME_NAME" = "opencode" ]; then
    if [ -f "$RUNTIME_CONFIG_FILE" ]; then
      cat "$RUNTIME_CONFIG_FILE"
      return
    fi
    if [ -d "$RUNTIME_CONFIG_DIR" ]; then
      ls -la "$RUNTIME_CONFIG_DIR"
      return
    fi
    echo "config dir not found: $RUNTIME_CONFIG_DIR" >&2
    exit 1
  fi
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
  mkdir -p "$RUNTIME_CONFIG_DIR"
  cp "$src" "$RUNTIME_CONFIG_FILE"
  log "imported $RUNTIME_NAME config: $src -> $RUNTIME_CONFIG_FILE"
}

reset_config() {
  load_runtime_profile
  if [ "$RUNTIME_NAME" = "opencode" ]; then
    rm -rf "$RUNTIME_CONFIG_DIR"
  fi
  init_full_config
  log "reset $RUNTIME_NAME config: $RUNTIME_CONFIG_FILE"
}

edit_config() {
  load_runtime_profile
  ensure_dirs
  if [ "$RUNTIME_NAME" = "opencode" ]; then
    if [ ! -d "$RUNTIME_CONFIG_DIR" ] || [ -z "$(ls -A "$RUNTIME_CONFIG_DIR" 2>/dev/null)" ]; then
      log "opencode shared config dir is empty, initializing first"
      init_full_config
    fi
    editor="${EDITOR:-vi}"
    if ! command -v "$editor" >/dev/null 2>&1; then
      die "editor not found: $editor (set EDITOR to a valid command)"
    fi
    "$editor" "$RUNTIME_CONFIG_DIR"
    return
  fi
  if [ ! -s "$RUNTIME_CONFIG_FILE" ]; then
    log "config not found, initializing first"
    init_full_config
  fi
  editor="${EDITOR:-vi}"
  if ! command -v "$editor" >/dev/null 2>&1; then
    die "editor not found: $editor (set EDITOR to a valid command)"
  fi
  "$editor" "$RUNTIME_CONFIG_FILE"
}

init_opencode_config_full() {
  ensure_dirs
  mkdir -p "$RUNTIME_CONFIG_DIR"

  if [ -d "$RUNTIME_CONFIG_DIR" ] && [ -n "$(ls -A "$RUNTIME_CONFIG_DIR" 2>/dev/null)" ]; then
    log "opencode shared config already exists, skip bootstrap: $RUNTIME_CONFIG_DIR"
    return
  fi

  bootstrap_host_opencode_config_if_missing
  cp -R "$OPENCODE_HOST_CONFIG_DIR/." "$RUNTIME_CONFIG_DIR/" || true
  if [ -z "$(ls -A "$RUNTIME_CONFIG_DIR" 2>/dev/null)" ]; then
    die "failed to copy host opencode config: $OPENCODE_HOST_CONFIG_DIR -> $RUNTIME_CONFIG_DIR"
  fi

mkdir -p "$RUNTIME_CONFIG_DIR"/{agents,commands,modes,plugins,skills,tools,themes}
log "initialized opencode config: $RUNTIME_CONFIG_FILE"
}

init_claudecode_config_full() {
  ensure_dirs
  mkdir -p "$RUNTIME_CONFIG_DIR"
  if [ -d "$RUNTIME_CONFIG_DIR" ] && [ -n "$(ls -A "$RUNTIME_CONFIG_DIR" 2>/dev/null)" ]; then
    log "claudecode shared config already exists, skip bootstrap: $RUNTIME_CONFIG_DIR"
    return
  fi

  bootstrap_host_claudecode_config_if_missing
  cp -R "$CLAUDECODE_HOST_CONFIG_DIR/." "$RUNTIME_CONFIG_DIR/" || true
  if [ ! -s "$RUNTIME_CONFIG_FILE" ]; then
    die "failed to bootstrap claudecode config from host: $CLAUDECODE_HOST_CONFIG_DIR -> $RUNTIME_CONFIG_FILE"
  fi
  log "initialized claudecode config: $RUNTIME_CONFIG_FILE"
}

init_full_config() {
  load_runtime_profile
  if [ "$RUNTIME_NAME" = "opencode" ]; then
    init_opencode_config_full
    return
  fi
  init_claudecode_config_full
}

edit_config_hint() {
  load_runtime_profile
  ensure_dirs
  if [ "$RUNTIME_NAME" = "opencode" ]; then
    cat <<EOF
opencode config path:
  $RUNTIME_CONFIG_DIR

CloudClaw maintains this config under CC_HOME by default.

Edit files in this directory to configure sections like:
  - model / provider
  - mcp / agent / permission
  - hooks / formatter / linter / theme

Optional: initialize shared config from host ~/.config/opencode (auto-installs host opencode if missing):
  AGENT_RUNTIME=opencode bash $0 config init-full

Then run:
  AGENT_RUNTIME=opencode bash $0 pool start
EOF
    return
  fi

  cat <<EOF
claudecode config path:
  $RUNTIME_CONFIG_FILE

Optional: initialize claudecode shared config from host (auto-installs Claude Code if missing):
  AGENT_RUNTIME=claudecode bash $0 config init-full

Then run:
  AGENT_RUNTIME=claudecode bash $0 pool start
EOF
}

print_runtime_config_paths() {
  load_runtime_profile
  ensure_dirs
  if [ "$RUNTIME_NAME" = "opencode" ]; then
    cat <<EOF
runtime:
  $RUNTIME_NAME
shared config dir:
  $RUNTIME_CONFIG_DIR
default config file:
  $RUNTIME_CONFIG_FILE
container mount path:
  $RUNTIME_CONFIG_MOUNT_PATH
EOF
    return
  fi
  cat <<EOF
runtime:
  $RUNTIME_NAME
runtime config file:
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
  cp "$SCRIPT_DIR/templates/run_opencode_task.sh" "$RUNNER_DIR/run_opencode_task.sh"
  cp "$SCRIPT_DIR/templates/run_claudecode_task.sh" "$RUNNER_DIR/run_claudecode_task.sh"
  cp "$SCRIPT_DIR/templates/Dockerfile.runner" "$RUNNER_DIR/Dockerfile.runner"
  chmod +x "$RUNNER_DIR/run_opencode_task.sh" "$RUNNER_DIR/run_claudecode_task.sh"

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
    harden_args=()
    network_args=()
    env_file_args=()
    if [ "$RUNTIME_NAME" = "opencode" ]; then
      env_args+=(
        "-e" "OPENCODE_SHARED_CONFIG_DIR=${RUNTIME_CONFIG_MOUNT_PATH}"
        "-e" "OPENCODE_PERSIST_MODE=${OPENCODE_PERSIST_MODE:-full}"
      )
    else
      env_args+=(
        "-e" "CLAUDECODE_CONFIG_PATH=$(runtime_config_mount_file)"
        "-e" "CLAUDECODE_EXEC_MODE=${CLAUDECODE_EXEC_MODE:-gateway}"
      )
      if [ -n "${CLAUDECODE_GATEWAY_TOKEN:-}" ]; then
        env_args+=("-e" "CLAUDECODE_GATEWAY_TOKEN=${CLAUDECODE_GATEWAY_TOKEN}")
      fi
      if [ -n "${CLAUDECODE_GATEWAY_BIND:-}" ]; then
        env_args+=("-e" "CLAUDECODE_GATEWAY_BIND=${CLAUDECODE_GATEWAY_BIND}")
      fi
      if [ -n "${CLAUDECODE_GATEWAY_PORT:-}" ]; then
        env_args+=("-e" "CLAUDECODE_GATEWAY_PORT=${CLAUDECODE_GATEWAY_PORT}")
      fi
      if [ -n "${CLAUDECODE_GATEWAY_MANAGE:-}" ]; then
        env_args+=("-e" "CLAUDECODE_GATEWAY_MANAGE=${CLAUDECODE_GATEWAY_MANAGE}")
      fi
      if [ -n "${CLAUDECODE_AGENT_ID:-}" ]; then
        env_args+=("-e" "CLAUDECODE_AGENT_ID=${CLAUDECODE_AGENT_ID}")
      fi
      if [ -n "${CLAUDECODE_TIMEOUT_SECONDS:-}" ]; then
        env_args+=("-e" "CLAUDECODE_TIMEOUT_SECONDS=${CLAUDECODE_TIMEOUT_SECONDS}")
      fi
    fi
    if [ "$CONTAINER_HARDEN" = "1" ]; then
      harden_args+=(
        "--security-opt" "no-new-privileges:true"
        "--cap-drop" "ALL"
        "--pids-limit" "$CONTAINER_PIDS_LIMIT"
      )
    fi
    if [ "$CONTAINER_READONLY_ROOTFS" = "1" ]; then
      harden_args+=(
        "--read-only"
        "--tmpfs" "/tmp:rw,noexec,nosuid,nodev,size=256m"
        "--tmpfs" "/var/tmp:rw,noexec,nosuid,nodev,size=128m"
      )
    fi
    if [ -n "$CONTAINER_NETWORK" ]; then
      network_args+=("--network" "$CONTAINER_NETWORK")
    fi
    if [ -n "$AGENT_ENV_FILE" ]; then
      if [ ! -f "$AGENT_ENV_FILE" ]; then
        die "AGENT_ENV_FILE not found: $AGENT_ENV_FILE"
      fi
      env_file_args+=("--env-file" "$AGENT_ENV_FILE")
    fi

    docker run -d \
      --name "$name" \
      --label "$POOL_LABEL" \
      --restart unless-stopped \
      --add-host host.docker.internal:host-gateway \
      --user "${OWNER_UID}:${OWNER_GID}" \
      "${network_args[@]}" \
      "${env_file_args[@]}" \
      "${harden_args[@]}" \
      --entrypoint /bin/sh \
      -v "${RUNTIME_CONFIG_DIR}:${RUNTIME_CONFIG_MOUNT_PATH}:ro" \
      -v "${DATA_DIR}/runs:${WORKSPACE_MOUNT_PATH}" \
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
  workspace_state_mode="${WORKSPACE_STATE_MODE:-}"
  if [ -z "$workspace_state_mode" ]; then
    workspace_state_mode="ephemeral"
  fi
  workspace_mode="${WORKSPACE_MODE:-}"
  if [ -z "$workspace_mode" ]; then
    workspace_mode="mount"
  fi

  cmd=(
    "$CLOUDCLAW_BIN" run
    --data-dir "$DATA_DIR"
    --db-driver "$DB_DRIVER"
    --executor "$RUNTIME_EXECUTOR"
    --workspace-state-mode "$workspace_state_mode"
    --workspace-mode "$workspace_mode"
    --docker-label-selector "$POOL_LABEL"
    --docker-remote-dir "$DOCKER_REMOTE_DIR"
    --shared-skills-dir "$SHARED_DIR"
    --docker-task-cmd "$DOCKER_TASK_CMD"
    --user-runtime-dir "$USER_RUNTIME_DIR"
  )
  if [ "$workspace_mode" = "mount" ]; then
    cmd+=(--workspace-mount-path "$WORKSPACE_MOUNT_PATH")
  fi

  if [ -n "$DB_DSN" ]; then
    cmd+=(--db-dsn "$DB_DSN")
  fi

  nohup "${cmd[@]}" >"$LOG_DIR/cloudclaw.log" 2>&1 &
  echo "$!" > "$PID_FILE"
  log "cloudclaw started (pid=$!, log=$LOG_DIR/cloudclaw.log)"
}

prune_opencode_userdata() {
  ensure_dirs
  if [ ! -x "$CLOUDCLAW_BIN" ]; then
    die "cloudclaw binary not found: $CLOUDCLAW_BIN (run: $0 install)"
  fi

  cmd=(
    "$CLOUDCLAW_BIN" user-data prune-opencode-runtime
    --data-dir "$DATA_DIR"
    --db-driver "$DB_DRIVER"
  )
  if [ -n "$DB_DSN" ]; then
    cmd+=(--db-dsn "$DB_DSN")
  fi
  "${cmd[@]}"
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
  if [ "$RUNTIME_NAME" = "opencode" ]; then
    echo "  config_dir: $RUNTIME_CONFIG_DIR"
    echo "  config_file: $RUNTIME_CONFIG_FILE"
  else
    echo "  config: $RUNTIME_CONFIG_FILE"
  fi
}

smoke() {
  load_runtime_profile
  if [ ! -x "$CLOUDCLAW_BIN" ]; then
    echo "cloudclaw binary not found: $CLOUDCLAW_BIN" >&2
    exit 1
  fi

  submit_out="$($CLOUDCLAW_BIN task submit --data-dir "$DATA_DIR" --db-driver "$DB_DRIVER" --user-id smoke_user --task-type smoke --input "smoke test, tell me who you are.")"
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
  task   status <task_id> | events <task_id> | trace <task_id>
  result dequeue [limit]
         get <task_id>
  db     prune-opencode-runtime
  pool   start | stop | restart | status
  runner start | stop | restart | status | logs [lines]

Shortcuts:
  init        Initialize runtime shared config
  install     Build cloudclaw binary + runner image
  up          install + init(if missing) + pool start + runner start
  down        runner stop + pool stop
  restart     down + up
  status      Show runner + pool + config status
  smoke       Submit one smoke task and wait result
  help        Show this help

Legacy aliases (compatible):
  set-home <path>, config-path, show-config, config-help
  dequeue-results [limit]
  prune-opencode-runtime
  start-pool, stop-pool, start, stop

Examples:
  AGENT_RUNTIME=opencode $0 init
  AGENT_RUNTIME=opencode $0 up
  AGENT_RUNTIME=claudecode $0 init
  AGENT_RUNTIME=claudecode $0 up
  AGENT_RUNTIME=opencode $0 smoke
  AGENT_RUNTIME=opencode $0 task trace tsk_xxx

Environment overrides:
  AGENT_RUNTIME (required: opencode|claudecode)
  CC_HOME (default: repo-relative ./cloudclaw_data unless overridden)
  CC_HOME_FILE (default: $REPO_ROOT/cloudclaw_data-home, stores persisted CC_HOME)
  POOL_SIZE (default: 3)
  POOL_LABEL (runtime default: app=<runtime>-agent)
  POOL_NAME_PREFIX (runtime default: <runtime>-agent)
  BASE_IMAGE (runtime default: opencode=ghcr.io/anomalyco/opencode:latest, claudecode=claudecode:latest)
  FALLBACK_BASE_IMAGE (optional fallback image when BASE_IMAGE is unavailable)
  RUNNER_IMAGE (runtime default: cloudclaw/<runtime>-runner:latest)
  AGENT_OWNER_UID / AGENT_OWNER_GID (optional container user id)
  OPENCODE_HOST_CONFIG_DIR (default: ~/.config/opencode, source path for opencode init bootstrap)
  CONTAINER_HARDEN (default: 1; no-new-privileges + cap-drop + pids-limit)
  CONTAINER_PIDS_LIMIT (default: 512, used when CONTAINER_HARDEN=1)
  CONTAINER_READONLY_ROOTFS (default: 0; set 1 to enable read-only rootfs with tmpfs for /tmp)
  CONTAINER_NETWORK (optional docker network name, e.g. internal)
  AGENT_ENV_FILE (optional env file path for sensitive vars like API keys)
  OPENCODE_CONFIG_MOUNT_PATH (default: /workspace/.config/opencode)
  WORKSPACE_MOUNT_PATH (default: /workspace/cloudclaw/runs; container mount path for runDir mount mode)
  WORKSPACE_MODE (optional: mount|copy; default: mount)
  OPENCODE_PERSIST_MODE (optional: auto|minimal|full; default handled by runtime script)
  WORKSPACE_STATE_MODE (optional: db|ephemeral; default: ephemeral)
  CLAUDECODE_HOST_CONFIG_DIR (default: ~/.claudecode, source path for claudecode init bootstrap)
  CLAUDECODE_OFFICIAL_CONFIG_DIR (default: ~/.claude, official Claude Code settings dir, imported as config.json)
  CLAUDECODE_CONFIG_MOUNT_PATH (default: /workspace/.claudecode)
  DOCKER_TASK_CMD (runtime default: run_opencode_task.sh|run_claudecode_task.sh)
  DOCKER_REMOTE_DIR (default: /tmp/cloudclaw)
  CLAUDECODE_EXEC_MODE (default: gateway, runtime=claudecode only)
  CLAUDECODE_GATEWAY_TOKEN / CLAUDECODE_GATEWAY_BIND / CLAUDECODE_GATEWAY_PORT / CLAUDECODE_GATEWAY_MANAGE (optional)
  CLAUDECODE_AGENT_ID / CLAUDECODE_TIMEOUT_SECONDS (optional)

Notes:
  AGENT_RUNTIME must be specified; no default runtime is assumed.
  opencode shared config dir is fixed at: <CC_HOME>/shared/opencode
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

cmd_db() {
  local action="${1:-}"
  case "$action" in
    prune-opencode-runtime) prune_opencode_userdata ;;
    *) die "unknown db action: $action (use: prune-opencode-runtime)" ;;
  esac
}

cmd_task() {
  local action="${1:-}"
  local task_id="${2:-}"
  ensure_dirs
  if [ ! -x "$CLOUDCLAW_BIN" ]; then
    die "cloudclaw binary not found: $CLOUDCLAW_BIN (run: $0 install)"
  fi
  case "$action" in
    status)
      require_arg "<task_id>" "$task_id"
      cmd=(
        "$CLOUDCLAW_BIN" task status
        --data-dir "$DATA_DIR"
        --db-driver "$DB_DRIVER"
        --task-id "$task_id"
      )
      if [ -n "$DB_DSN" ]; then
        cmd+=(--db-dsn "$DB_DSN")
      fi
      "${cmd[@]}"
      ;;
    events)
      require_arg "<task_id>" "$task_id"
      cmd=(
        "$CLOUDCLAW_BIN" audit
        --data-dir "$DATA_DIR"
        --db-driver "$DB_DRIVER"
        --task-id "$task_id"
      )
      if [ -n "$DB_DSN" ]; then
        cmd+=(--db-dsn "$DB_DSN")
      fi
      "${cmd[@]}"
      ;;
    trace)
      require_arg "<task_id>" "$task_id"
      echo "# task status"
      cmd_task status "$task_id"
      echo
      echo "# task events"
      cmd_task events "$task_id"
      echo
      echo "# task result"
      cmd_result get "$task_id"
      ;;
    *)
      die "unknown task action: $action (use: status <task_id>|events <task_id>|trace <task_id>)"
      ;;
  esac
}

cmd_result() {
  local action="${1:-dequeue}"
  local arg="${2:-}"
  case "$action" in
    dequeue)
      if [ -z "$arg" ]; then
        arg="20"
      fi
      ensure_dirs
      if [ ! -x "$CLOUDCLAW_BIN" ]; then
        die "cloudclaw binary not found: $CLOUDCLAW_BIN (run: $0 install)"
      fi
      cmd=(
        "$CLOUDCLAW_BIN" result dequeue
        --data-dir "$DATA_DIR"
        --db-driver "$DB_DRIVER"
        --limit "$arg"
      )
      if [ -n "$DB_DSN" ]; then
        cmd+=(--db-dsn "$DB_DSN")
      fi
      "${cmd[@]}"
      ;;
    get)
      require_arg "<task_id>" "$arg"
      ensure_dirs
      if [ ! -x "$CLOUDCLAW_BIN" ]; then
        die "cloudclaw binary not found: $CLOUDCLAW_BIN (run: $0 install)"
      fi
      cmd=(
        "$CLOUDCLAW_BIN" result get
        --data-dir "$DATA_DIR"
        --db-driver "$DB_DRIVER"
        --task-id "$arg"
      )
      if [ -n "$DB_DSN" ]; then
        cmd+=(--db-dsn "$DB_DSN")
      fi
      "${cmd[@]}"
      ;;
    *)
      die "unknown result action: $action (use: dequeue [limit]|get <task_id>)"
      ;;
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
    task-status) cmd_task status "$arg1" ;;
    task-events) cmd_task events "$arg1" ;;
    task-trace) cmd_task trace "$arg1" ;;
    help|--help|-h|"") usage ;;
    # legacy aliases
    set-home) set_home "$arg1" ;;
    config-path) show_config_path ;;
    show-config) show_config ;;
    config-help)
      edit_config_hint
      print_runtime_config_paths
      ;;
    dequeue-results) cmd_result dequeue "${arg1:-20}" ;;
    prune-opencode-runtime) prune_opencode_userdata ;;
    start-pool) start_pool ;;
    stop-pool) stop_pool ;;
    start) start_cloudclaw ;;
    stop) stop_cloudclaw ;;
    *)
      die "unknown command: $action (run: $0 help)"
      ;;
  esac
}

main() {
  local group="${1:-help}"
  local action="${2:-}"
  local arg="${3:-}"
  case "$group" in
    home) cmd_home "$action" "$arg" ;;
    config) cmd_config "$action" "$arg" ;;
    task) cmd_task "$action" "$arg" ;;
    result) cmd_result "$action" "$arg" ;;
    db) cmd_db "$action" ;;
    pool) cmd_pool "$action" ;;
    runner) cmd_runner "$action" "$arg" ;;
    *) cmd_shortcut "$group" "$action" "$arg" ;;
  esac
}

main "${1:-}" "${2:-}" "${3:-}"
