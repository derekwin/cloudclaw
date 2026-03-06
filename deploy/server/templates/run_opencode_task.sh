#!/bin/sh
set -eu

: "${CLOUDCLAW_WORKSPACE:?missing CLOUDCLAW_WORKSPACE}"
: "${CLOUDCLAW_INPUT:?missing CLOUDCLAW_INPUT}"
: "${CLOUDCLAW_USAGE_FILE:?missing CLOUDCLAW_USAGE_FILE}"

OPENCODE_HOME="${OPENCODE_HOME:-$CLOUDCLAW_WORKSPACE/.opencode-home}"
OPENCODE_CONFIG="${OPENCODE_CONFIG:-/workspace/.opencode/opencode.json}"
OPENCODE_SHARED_CONFIG_DIR="${OPENCODE_SHARED_CONFIG_DIR:-/workspace/.opencode}"
OPENCODE_USER_CONFIG_FILE="${OPENCODE_USER_CONFIG_FILE:-$CLOUDCLAW_WORKSPACE/opencode.user.json}"
OPENCODE_MODEL_NAME="${OPENCODE_MODEL_NAME:-}"
OPENCODE_AGENT_NAME="${OPENCODE_AGENT_NAME:-}"
OPENCODE_ATTACH="${OPENCODE_ATTACH:-}"
OPENCODE_RUN_FORMAT="${OPENCODE_RUN_FORMAT:-default}"

TASK_HOME="$(dirname "$CLOUDCLAW_WORKSPACE")"
SHARED_DIR="${CLOUDCLAW_SHARED_SKILLS_DIR:-}"

mkdir -p "$CLOUDCLAW_WORKSPACE" "$TASK_HOME" "$OPENCODE_HOME"

# Keep opencode session/auth/cache isolated per user workspace.
export HOME="$OPENCODE_HOME"
export XDG_CONFIG_HOME="${XDG_CONFIG_HOME:-$OPENCODE_HOME/.config}"
export XDG_DATA_HOME="${XDG_DATA_HOME:-$OPENCODE_HOME/.local/share}"
mkdir -p "$XDG_CONFIG_HOME" "$XDG_DATA_HOME"

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
    if [ -f "$SHARED_WORKSPACE/$f" ] && [ ! -f "$CLOUDCLAW_WORKSPACE/$f" ]; then
      cp -f "$SHARED_WORKSPACE/$f" "$CLOUDCLAW_WORKSPACE/$f"
    fi
  done
fi

# Merge shared .opencode resources with per-user workspace .opencode resources.
MERGED_CONFIG_DIR="$TASK_HOME/opencode-config-dir"
rm -rf "$MERGED_CONFIG_DIR"
mkdir -p "$MERGED_CONFIG_DIR"
if [ -d "$OPENCODE_SHARED_CONFIG_DIR" ]; then
  cp -R "$OPENCODE_SHARED_CONFIG_DIR/." "$MERGED_CONFIG_DIR/" || true
fi
if [ -d "$CLOUDCLAW_WORKSPACE/.opencode" ]; then
  cp -R "$CLOUDCLAW_WORKSPACE/.opencode/." "$MERGED_CONFIG_DIR/" || true
fi
export OPENCODE_CONFIG_DIR="$MERGED_CONFIG_DIR"

# Prefer merged config so per-user `.opencode/<config-name>` can override shared config.
opencode_config_basename="$(basename "$OPENCODE_CONFIG")"
OPENCODE_RUN_CONFIG="$OPENCODE_CONFIG"
if [ -s "$MERGED_CONFIG_DIR/$opencode_config_basename" ]; then
  OPENCODE_RUN_CONFIG="$MERGED_CONFIG_DIR/$opencode_config_basename"
fi

# Per-user overrides can be provided as JSON content.
if [ -s "$OPENCODE_USER_CONFIG_FILE" ]; then
  OPENCODE_CONFIG_CONTENT="$(cat "$OPENCODE_USER_CONFIG_FILE")"
  export OPENCODE_CONFIG_CONTENT
fi

cd "$CLOUDCLAW_WORKSPACE"

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

stderr_log="$TASK_HOME/opencode-run.stderr.log"
if ! OPENCODE_CONFIG="$OPENCODE_RUN_CONFIG" "$@" >"$CLOUDCLAW_WORKSPACE/result.txt" 2>"$stderr_log"; then
  cat "$stderr_log" >&2 || true
  echo "opencode run command failed" >&2
  exit 1
fi

prompt_chars=$(printf "%s" "$CLOUDCLAW_INPUT" | wc -c | tr -d ' ')
resp_chars=$(wc -c < "$CLOUDCLAW_WORKSPACE/result.txt" | tr -d ' ')
pt=$(( (prompt_chars + 3) / 4 ))
ct=$(( (resp_chars + 3) / 4 ))
tt=$(( pt + ct ))
printf '{"prompt_tokens":%s,"completion_tokens":%s,"total_tokens":%s}\n' "$pt" "$ct" "$tt" > "$CLOUDCLAW_USAGE_FILE"
