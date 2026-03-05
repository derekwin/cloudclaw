#!/bin/sh
set -eu

: "${CLOUDCLAW_WORKSPACE:?missing CLOUDCLAW_WORKSPACE}"
: "${CLOUDCLAW_INPUT:?missing CLOUDCLAW_INPUT}"
: "${CLOUDCLAW_USAGE_FILE:?missing CLOUDCLAW_USAGE_FILE}"

# Generic provider settings (preferred)
PICO_MODEL_NAME="${PICO_MODEL_NAME:-default}"
PICO_MODEL="${PICO_MODEL:-${OPENROUTER_MODEL:-openai/gpt-5.2}}"
PICO_API_KEY="${PICO_API_KEY:-${OPENROUTER_API_KEY:-}}"
PICO_API_BASE="${PICO_API_BASE:-}"
PICO_REQUEST_TIMEOUT="${PICO_REQUEST_TIMEOUT:-300}"

# Reasonable defaults for common local providers when api_base is not set.
if [ -z "$PICO_API_BASE" ]; then
  case "$PICO_MODEL" in
    openrouter/*) PICO_API_BASE="https://openrouter.ai/api/v1" ;;
    openai/*) PICO_API_BASE="https://api.openai.com/v1" ;;
    ollama/*) PICO_API_BASE="http://host.docker.internal:11434/v1" ;;
    vllm/*) PICO_API_BASE="http://host.docker.internal:8000/v1" ;;
  esac
fi

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

# Build model entry dynamically.
MODEL_ENTRY="\"model_name\": \"$PICO_MODEL_NAME\", \"model\": \"$PICO_MODEL\", \"request_timeout\": $PICO_REQUEST_TIMEOUT"
if [ -n "$PICO_API_BASE" ]; then
  MODEL_ENTRY="$MODEL_ENTRY, \"api_base\": \"$PICO_API_BASE\""
fi
if [ -n "$PICO_API_KEY" ]; then
  MODEL_ENTRY="$MODEL_ENTRY, \"api_key\": \"$PICO_API_KEY\""
fi

cat > "$TASK_HOME/config.json" <<JSON
{
  "agents": {
    "defaults": {
      "workspace": "$CLOUDCLAW_WORKSPACE",
      "model": "$PICO_MODEL_NAME",
      "model_name": "$PICO_MODEL_NAME",
      "max_tokens": 8192,
      "temperature": 0.7,
      "max_tool_iterations": 20
    }
  },
  "model_list": [
    {
      $MODEL_ENTRY
    }
  ]
}
JSON

PICOCLAW_HOME="$TASK_HOME" \
PICOCLAW_CONFIG="$TASK_HOME/config.json" \
picoclaw agent --model "$PICO_MODEL_NAME" -m "$CLOUDCLAW_INPUT" > "$CLOUDCLAW_WORKSPACE/result.txt"

prompt_chars=$(printf "%s" "$CLOUDCLAW_INPUT" | wc -c | tr -d ' ')
resp_chars=$(wc -c < "$CLOUDCLAW_WORKSPACE/result.txt" | tr -d ' ')
pt=$(( (prompt_chars + 3) / 4 ))
ct=$(( (resp_chars + 3) / 4 ))
printf '{"prompt_tokens":%s,"completion_tokens":%s}\n' "$pt" "$ct" > "$CLOUDCLAW_USAGE_FILE"
