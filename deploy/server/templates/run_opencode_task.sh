#!/bin/sh
set -eu

: "${CLOUDCLAW_WORKSPACE:?missing CLOUDCLAW_WORKSPACE}"
: "${CLOUDCLAW_INPUT:?missing CLOUDCLAW_INPUT}"
: "${CLOUDCLAW_USAGE_FILE:?missing CLOUDCLAW_USAGE_FILE}"

OPENCODE_SHARED_CONFIG_DIR="${OPENCODE_SHARED_CONFIG_DIR:-/workspace/.config/opencode}"
OPENCODE_USER_CONFIG_FILE="${OPENCODE_USER_CONFIG_FILE:-$CLOUDCLAW_WORKSPACE/opencode.user.json}"
OPENCODE_MODEL_NAME="${OPENCODE_MODEL_NAME:-}"
OPENCODE_AGENT_NAME="${OPENCODE_AGENT_NAME:-}"
OPENCODE_ATTACH="${OPENCODE_ATTACH:-}"
OPENCODE_RUN_FORMAT="${OPENCODE_RUN_FORMAT:-default}"
OPENCODE_BIN="${OPENCODE_BIN:-opencode}"
OPENCODE_STATE_SUBDIR="${OPENCODE_STATE_SUBDIR:-opencode}"
OPENCODE_WORKSPACE_CONFIG_DIRNAME="${OPENCODE_WORKSPACE_CONFIG_DIRNAME:-.opencode}"
OPENCODE_PERSIST_MODE="$(printf '%s' "${OPENCODE_PERSIST_MODE:-auto}" | tr '[:upper:]' '[:lower:]')"
case "$OPENCODE_PERSIST_MODE" in
  auto|minimal|full) ;;
  *)
    echo "invalid OPENCODE_PERSIST_MODE: $OPENCODE_PERSIST_MODE (supported: auto|minimal|full)" >&2
    exit 1
    ;;
esac

TASK_HOME="$(dirname "$CLOUDCLAW_WORKSPACE")"
SHARED_DIR="${CLOUDCLAW_SHARED_SKILLS_DIR:-}"
LEGACY_WORKSPACE_HOME="$CLOUDCLAW_WORKSPACE/.opencode-home"
DEFAULT_USER_HOME="$LEGACY_WORKSPACE_HOME"
DEFAULT_USER_DATA_HOME="$LEGACY_WORKSPACE_HOME/.local/share"
USER_WORKSPACE_DIR="$CLOUDCLAW_WORKSPACE"
USER_DATA_HOME="$DEFAULT_USER_DATA_HOME"
TMP_ROOT="${TMPDIR:-/tmp}"
MERGED_CONFIG_DIR=""
stderr_log=""

mkdir -p "$TMP_ROOT"

cleanup() {
  if [ -n "$MERGED_CONFIG_DIR" ] && [ -d "$MERGED_CONFIG_DIR" ]; then
    rm -rf "$MERGED_CONFIG_DIR"
  fi
  if [ -n "$stderr_log" ] && [ -f "$stderr_log" ]; then
    rm -f "$stderr_log"
  fi
}
trap cleanup EXIT INT TERM

USER_HOME="${OPENCODE_HOME:-$DEFAULT_USER_HOME}"

mkdir -p "$CLOUDCLAW_WORKSPACE" "$TASK_HOME" "$USER_HOME" "$USER_WORKSPACE_DIR" "$USER_DATA_HOME"

# Shared config is mounted from host and read by all containers.
export HOME="$USER_HOME"
export XDG_CONFIG_HOME="$(dirname "$OPENCODE_SHARED_CONFIG_DIR")"
# User data writes into per-task runDir (.opencode-home). Runner is responsible for
# copying user runtime state in before task execution and syncing it back after success.
export XDG_DATA_HOME="$USER_DATA_HOME"
mkdir -p "$XDG_CONFIG_HOME" "$XDG_DATA_HOME"

prune_persisted_opencode_state() {
  if [ "$OPENCODE_PERSIST_MODE" = "full" ]; then
    return
  fi
  state_root="$XDG_DATA_HOME/$OPENCODE_STATE_SUBDIR"
  if [ ! -d "$state_root" ]; then
    return
  fi
  # Keep only essential state for cross-task continuity.
  rm -rf \
    "$state_root/bin" \
    "$state_root/log" \
    "$state_root/snapshot" \
    "$state_root/storage" \
    "$state_root/tool-output"
  rm -f "$state_root/opencode.db-shm" "$state_root/opencode.db-wal"
}

# If old heavy state already exists in DB, prune it before this run.
prune_persisted_opencode_state

SHARED_WORKSPACE="$SHARED_DIR"
if [ -n "$SHARED_DIR" ] && [ -d "$SHARED_DIR/workspace" ]; then
  SHARED_WORKSPACE="$SHARED_DIR/workspace"
fi

if [ -n "$SHARED_DIR" ] && [ -d "$SHARED_WORKSPACE" ]; then
  for f in AGENT.md IDENTITY.md SOUL.md; do
    if [ -f "$SHARED_WORKSPACE/$f" ] && [ ! -f "$USER_WORKSPACE_DIR/$f" ]; then
      cp -f "$SHARED_WORKSPACE/$f" "$USER_WORKSPACE_DIR/$f"
    fi
  done
fi

# Only create a merged config directory when workspace has per-user `.opencode`.
# This avoids per-task full-directory copies for the common shared-only path.
if [ -d "$USER_WORKSPACE_DIR/$OPENCODE_WORKSPACE_CONFIG_DIRNAME" ]; then
  MERGED_CONFIG_DIR="$(mktemp -d -p "$TMP_ROOT" opencode-config.XXXXXX)"
  if [ -d "$OPENCODE_SHARED_CONFIG_DIR" ]; then
    cp -R "$OPENCODE_SHARED_CONFIG_DIR/." "$MERGED_CONFIG_DIR/" || true
  fi
  cp -R "$USER_WORKSPACE_DIR/$OPENCODE_WORKSPACE_CONFIG_DIRNAME/." "$MERGED_CONFIG_DIR/" || true
  export OPENCODE_CONFIG_DIR="$MERGED_CONFIG_DIR"
else
  export OPENCODE_CONFIG_DIR="$OPENCODE_SHARED_CONFIG_DIR"
fi

# Per-user overrides can be provided as JSON content.
if [ -s "$OPENCODE_USER_CONFIG_FILE" ]; then
  OPENCODE_CONFIG_CONTENT="$(cat "$OPENCODE_USER_CONFIG_FILE")"
  export OPENCODE_CONFIG_CONTENT
fi

cd "$USER_WORKSPACE_DIR"

if ! command -v "$OPENCODE_BIN" >/dev/null 2>&1; then
  echo "runtime command not found: $OPENCODE_BIN" >&2
  exit 1
fi

set -- "$OPENCODE_BIN" run --format "$OPENCODE_RUN_FORMAT"
if [ -n "$OPENCODE_MODEL_NAME" ]; then
  set -- "$@" --model "$OPENCODE_MODEL_NAME"
fi
if [ -n "$OPENCODE_AGENT_NAME" ]; then
  set -- "$@" --agent "$OPENCODE_AGENT_NAME"
fi
if [ -n "$OPENCODE_ATTACH" ]; then
  set -- "$@" --attach "$OPENCODE_ATTACH"
fi
set -- "$@" "$CLOUDCLAW_INPUT"

stderr_log="$(mktemp -p "$TMP_ROOT" opencode-run.XXXXXX)"
if ! "$@" >"$CLOUDCLAW_WORKSPACE/result.txt" 2>"$stderr_log"; then
  cat "$stderr_log" >&2 || true
  echo "opencode run command failed" >&2
  exit 1
fi
if [ ! -s "$CLOUDCLAW_WORKSPACE/result.txt" ] && [ -s "$stderr_log" ]; then
  cp "$stderr_log" "$CLOUDCLAW_WORKSPACE/result.txt"
fi

# Prune large runtime artifacts before cloudclaw persists user data into DB.
prune_persisted_opencode_state

prompt_chars=$(printf "%s" "$CLOUDCLAW_INPUT" | wc -c | tr -d ' ')
resp_chars=$(wc -c < "$CLOUDCLAW_WORKSPACE/result.txt" | tr -d ' ')
pt=$(( (prompt_chars + 3) / 4 ))
ct=$(( (resp_chars + 3) / 4 ))
tt=$(( pt + ct ))
printf '{"prompt_tokens":%s,"completion_tokens":%s,"total_tokens":%s}\n' "$pt" "$ct" "$tt" > "$CLOUDCLAW_USAGE_FILE"
