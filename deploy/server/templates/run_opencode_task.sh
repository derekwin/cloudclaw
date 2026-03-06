#!/bin/sh
set -eu

: "${CLOUDCLAW_WORKSPACE:?missing CLOUDCLAW_WORKSPACE}"
: "${CLOUDCLAW_INPUT:?missing CLOUDCLAW_INPUT}"
: "${CLOUDCLAW_USAGE_FILE:?missing CLOUDCLAW_USAGE_FILE}"

OPENCODE_CONFIG="${OPENCODE_CONFIG:-/root/.config/opencode/opencode.json}"
OPENCODE_SHARED_CONFIG_DIR="${OPENCODE_SHARED_CONFIG_DIR:-/root/.config/opencode}"
OPENCODE_USER_CONFIG_FILE="${OPENCODE_USER_CONFIG_FILE:-$CLOUDCLAW_WORKSPACE/opencode.user.json}"
OPENCODE_MODEL_NAME="${OPENCODE_MODEL_NAME:-}"
OPENCODE_AGENT_NAME="${OPENCODE_AGENT_NAME:-}"
OPENCODE_ATTACH="${OPENCODE_ATTACH:-}"
OPENCODE_RUN_FORMAT="${OPENCODE_RUN_FORMAT:-default}"
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
USER_RUNTIME_HOME_BASE="${CLOUDCLAW_USER_RUNTIME_HOME_BASE:-}"
LEGACY_WORKSPACE_HOME="$CLOUDCLAW_WORKSPACE/.opencode-home"
DEFAULT_OPENCODE_HOME="$LEGACY_WORKSPACE_HOME"
USER_WORKSPACE_DIR="$CLOUDCLAW_WORKSPACE"
USE_EXTERNAL_OPENCODE_HOME=0
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

if [ -n "$USER_RUNTIME_HOME_BASE" ]; then
  normalized_user_id="$(printf '%s' "${CLOUDCLAW_USER_ID:-anonymous}" | LC_ALL=C sed 's/[^A-Za-z0-9._-]/_/g')"
  if [ -z "$normalized_user_id" ]; then
    normalized_user_id="anonymous"
  fi
  user_id_hash="$(printf '%s' "${CLOUDCLAW_USER_ID:-anonymous}" | cksum | awk '{print $1}')"
  normalized_user_id="${normalized_user_id}-${user_id_hash}"
  DEFAULT_OPENCODE_HOME="$USER_RUNTIME_HOME_BASE/$normalized_user_id/opencode-home"
  USER_WORKSPACE_DIR="$USER_RUNTIME_HOME_BASE/$normalized_user_id/workspace"
  USE_EXTERNAL_OPENCODE_HOME=1
fi

OPENCODE_HOME="${OPENCODE_HOME:-$DEFAULT_OPENCODE_HOME}"

if [ "$OPENCODE_USER_CONFIG_FILE" = "$CLOUDCLAW_WORKSPACE/opencode.user.json" ] && [ "$USER_WORKSPACE_DIR" != "$CLOUDCLAW_WORKSPACE" ]; then
  OPENCODE_USER_CONFIG_FILE="$USER_WORKSPACE_DIR/opencode.user.json"
fi

mkdir -p "$CLOUDCLAW_WORKSPACE" "$TASK_HOME" "$OPENCODE_HOME" "$USER_WORKSPACE_DIR"

# External user runtime home is persisted on host volume, not in task workspace DB snapshot.
if [ "$USE_EXTERNAL_OPENCODE_HOME" = "1" ] && [ "$OPENCODE_HOME" != "$LEGACY_WORKSPACE_HOME" ]; then
  rm -rf "$LEGACY_WORKSPACE_HOME"
fi

# Keep opencode session/auth/cache isolated per user workspace.
export HOME="$OPENCODE_HOME"
# Force XDG paths into task workspace to keep all opencode runtime state private.
export XDG_CONFIG_HOME="$OPENCODE_HOME/.config"
export XDG_DATA_HOME="$OPENCODE_HOME/.local/share"
mkdir -p "$XDG_CONFIG_HOME" "$XDG_DATA_HOME"

prune_persisted_opencode_state() {
  if [ "$OPENCODE_PERSIST_MODE" = "full" ]; then
    return
  fi
  if [ "$OPENCODE_PERSIST_MODE" = "auto" ] && [ "$USE_EXTERNAL_OPENCODE_HOME" = "1" ]; then
    return
  fi
  state_root="$XDG_DATA_HOME/opencode"
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

if [ ! -s "$OPENCODE_CONFIG" ]; then
  echo "missing or empty opencode config: $OPENCODE_CONFIG" >&2
  exit 1
fi

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

opencode_config_basename="$(basename "$OPENCODE_CONFIG")"
OPENCODE_RUN_CONFIG="$OPENCODE_CONFIG"

# Only create a merged config directory when workspace has per-user `.opencode`.
# This avoids per-task full-directory copies for the common shared-only path.
if [ -d "$USER_WORKSPACE_DIR/.opencode" ]; then
  MERGED_CONFIG_DIR="$(mktemp -d "$TMP_ROOT/opencode-config.XXXXXX")"
  if [ -d "$OPENCODE_SHARED_CONFIG_DIR" ]; then
    cp -R "$OPENCODE_SHARED_CONFIG_DIR/." "$MERGED_CONFIG_DIR/" || true
  fi
  cp -R "$USER_WORKSPACE_DIR/.opencode/." "$MERGED_CONFIG_DIR/" || true
  export OPENCODE_CONFIG_DIR="$MERGED_CONFIG_DIR"
  if [ -s "$MERGED_CONFIG_DIR/$opencode_config_basename" ]; then
    OPENCODE_RUN_CONFIG="$MERGED_CONFIG_DIR/$opencode_config_basename"
  fi
else
  export OPENCODE_CONFIG_DIR="$OPENCODE_SHARED_CONFIG_DIR"
fi

# Per-user overrides can be provided as JSON content.
if [ -s "$OPENCODE_USER_CONFIG_FILE" ]; then
  OPENCODE_CONFIG_CONTENT="$(cat "$OPENCODE_USER_CONFIG_FILE")"
  export OPENCODE_CONFIG_CONTENT
fi

cd "$USER_WORKSPACE_DIR"

set -- opencode run --format "$OPENCODE_RUN_FORMAT"
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

stderr_log="$(mktemp "$TMP_ROOT/opencode-run.XXXXXX.stderr.log")"
if ! OPENCODE_CONFIG="$OPENCODE_RUN_CONFIG" "$@" >"$CLOUDCLAW_WORKSPACE/result.txt" 2>"$stderr_log"; then
  cat "$stderr_log" >&2 || true
  echo "opencode run command failed" >&2
  exit 1
fi

# Prune large runtime artifacts before cloudclaw persists user data into DB.
prune_persisted_opencode_state

# Ensure legacy in-workspace home does not get written back to DB when external home is enabled.
if [ "$USE_EXTERNAL_OPENCODE_HOME" = "1" ] && [ "$OPENCODE_HOME" != "$LEGACY_WORKSPACE_HOME" ]; then
  rm -rf "$LEGACY_WORKSPACE_HOME"
fi

prompt_chars=$(printf "%s" "$CLOUDCLAW_INPUT" | wc -c | tr -d ' ')
resp_chars=$(wc -c < "$CLOUDCLAW_WORKSPACE/result.txt" | tr -d ' ')
pt=$(( (prompt_chars + 3) / 4 ))
ct=$(( (resp_chars + 3) / 4 ))
tt=$(( pt + ct ))
printf '{"prompt_tokens":%s,"completion_tokens":%s,"total_tokens":%s}\n' "$pt" "$ct" "$tt" > "$CLOUDCLAW_USAGE_FILE"
