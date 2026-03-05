#!/bin/sh
set -eu

: "${CLOUDCLAW_WORKSPACE:?missing CLOUDCLAW_WORKSPACE}"
: "${CLOUDCLAW_INPUT:?missing CLOUDCLAW_INPUT}"
: "${CLOUDCLAW_USAGE_FILE:?missing CLOUDCLAW_USAGE_FILE}"

PICO_MODEL_NAME="${PICO_MODEL_NAME:-default}"
PICOCLAW_HOME="${PICOCLAW_HOME:-/workspace/.picoclaw}"
PICOCLAW_CONFIG="${PICOCLAW_CONFIG:-$PICOCLAW_HOME/config.json}"

TASK_HOME="$(dirname "$CLOUDCLAW_WORKSPACE")"
SHARED_DIR="${CLOUDCLAW_SHARED_SKILLS_DIR:-}"

mkdir -p "$CLOUDCLAW_WORKSPACE" "$TASK_HOME"

SHARED_WORKSPACE="$SHARED_DIR"
if [ -d "$SHARED_DIR/workspace" ]; then
  SHARED_WORKSPACE="$SHARED_DIR/workspace"
fi

if [ -n "$SHARED_DIR" ] && [ -d "$SHARED_WORKSPACE" ]; then
  for f in AGENT.md IDENTITY.md SOUL.md; do
    if [ -f "$SHARED_WORKSPACE/$f" ]; then
      cp -f "$SHARED_WORKSPACE/$f" "$CLOUDCLAW_WORKSPACE/$f"
    fi
  done

  if [ -d "$SHARED_WORKSPACE/skills" ]; then
    mkdir -p "$CLOUDCLAW_WORKSPACE/skills"
    cp -R "$SHARED_WORKSPACE/skills/." "$CLOUDCLAW_WORKSPACE/skills/" || true
  fi
fi

if [ ! -s "$PICOCLAW_CONFIG" ]; then
  echo "missing or empty picoclaw config: $PICOCLAW_CONFIG" >&2
  exit 1
fi

model_name_re="$(printf '%s' "$PICO_MODEL_NAME" | sed -e 's/[][(){}.^$*+?|\\/]/\\&/g')"
if ! grep -Eq "\"model_name\"[[:space:]]*:[[:space:]]*\"$model_name_re\"" "$PICOCLAW_CONFIG"; then
  echo "model \"$PICO_MODEL_NAME\" not found in $PICOCLAW_CONFIG" >&2
  exit 1
fi

PICOCLAW_HOME="$PICOCLAW_HOME" \
PICOCLAW_CONFIG="$PICOCLAW_CONFIG" \
picoclaw agent --model "$PICO_MODEL_NAME" -m "$CLOUDCLAW_INPUT" > "$CLOUDCLAW_WORKSPACE/result.txt"

prompt_chars=$(printf "%s" "$CLOUDCLAW_INPUT" | wc -c | tr -d ' ')
resp_chars=$(wc -c < "$CLOUDCLAW_WORKSPACE/result.txt" | tr -d ' ')
pt=$(( (prompt_chars + 3) / 4 ))
ct=$(( (resp_chars + 3) / 4 ))
printf '{"prompt_tokens":%s,"completion_tokens":%s}\n' "$pt" "$ct" > "$CLOUDCLAW_USAGE_FILE"
