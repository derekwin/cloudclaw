#!/bin/sh
set -eu

: "${CLOUDCLAW_WORKSPACE:?missing CLOUDCLAW_WORKSPACE}"
: "${CLOUDCLAW_INPUT:?missing CLOUDCLAW_INPUT}"
: "${CLOUDCLAW_USAGE_FILE:?missing CLOUDCLAW_USAGE_FILE}"

OPENCLAW_HOME="${OPENCLAW_HOME:-/workspace/.openclaw}"
OPENCLAW_CONFIG_PATH="${OPENCLAW_CONFIG_PATH:-$OPENCLAW_HOME/openclaw.json}"
OPENCLAW_EXEC_MODE="$(printf '%s' "${OPENCLAW_EXEC_MODE:-gateway}" | tr '[:upper:]' '[:lower:]')"
OPENCLAW_SESSION_ID="${OPENCLAW_SESSION_ID:-cloudclaw-${CLOUDCLAW_TASK_ID:-task}}"
OPENCLAW_GATEWAY_BIND="${OPENCLAW_GATEWAY_BIND:-loopback}"
OPENCLAW_GATEWAY_PORT="${OPENCLAW_GATEWAY_PORT:-8688}"
OPENCLAW_GATEWAY_MANAGE="${OPENCLAW_GATEWAY_MANAGE:-1}"
OPENCLAW_GATEWAY_TOKEN="${OPENCLAW_GATEWAY_TOKEN:-}"
OPENCLAW_AGENT_ID="${OPENCLAW_AGENT_ID:-}"
OPENCLAW_TIMEOUT_SECONDS="${OPENCLAW_TIMEOUT_SECONDS:-}"

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

if [ ! -s "$OPENCLAW_CONFIG_PATH" ]; then
  echo "missing or empty openclaw config: $OPENCLAW_CONFIG_PATH" >&2
  exit 1
fi

mkdir -p "$OPENCLAW_HOME"
default_cfg="$OPENCLAW_HOME/openclaw.json"
if [ "$OPENCLAW_CONFIG_PATH" != "$default_cfg" ]; then
  cp -f "$OPENCLAW_CONFIG_PATH" "$default_cfg"
fi

openclaw_health() {
  if [ -n "$OPENCLAW_GATEWAY_TOKEN" ]; then
    OPENCLAW_HOME="$OPENCLAW_HOME" OPENCLAW_CONFIG_PATH="$OPENCLAW_CONFIG_PATH" openclaw health --token "$OPENCLAW_GATEWAY_TOKEN" >/dev/null 2>&1
    return $?
  fi
  OPENCLAW_HOME="$OPENCLAW_HOME" OPENCLAW_CONFIG_PATH="$OPENCLAW_CONFIG_PATH" openclaw health >/dev/null 2>&1
}

ensure_gateway() {
  if [ "$OPENCLAW_EXEC_MODE" != "gateway" ]; then
    return 0
  fi
  if openclaw_health; then
    return 0
  fi
  if [ "$OPENCLAW_GATEWAY_MANAGE" = "0" ]; then
    return 1
  fi

  OPENCLAW_HOME="$OPENCLAW_HOME" OPENCLAW_CONFIG_PATH="$OPENCLAW_CONFIG_PATH" \
    openclaw gateway --bind "$OPENCLAW_GATEWAY_BIND" --port "$OPENCLAW_GATEWAY_PORT" >"$TASK_HOME/openclaw-gateway.log" 2>&1 &

  for _ in $(seq 1 10); do
    if openclaw_health; then
      return 0
    fi
    sleep 1
  done
  return 1
}

if [ "$OPENCLAW_EXEC_MODE" = "gateway" ] && ! ensure_gateway; then
  echo "warning: openclaw gateway not ready, command may fallback to embedded mode" >&2
fi

set -- openclaw agent --message "$CLOUDCLAW_INPUT" --json
if [ "$OPENCLAW_EXEC_MODE" = "local" ]; then
  set -- "$@" --local
else
  set -- "$@" --session-id "$OPENCLAW_SESSION_ID"
fi
if [ -n "$OPENCLAW_AGENT_ID" ]; then
  set -- "$@" --agent "$OPENCLAW_AGENT_ID"
fi
if [ -n "$OPENCLAW_TIMEOUT_SECONDS" ]; then
  set -- "$@" --timeout "$OPENCLAW_TIMEOUT_SECONDS"
fi

output_json="$TASK_HOME/openclaw-output.json"
if ! OPENCLAW_HOME="$OPENCLAW_HOME" OPENCLAW_CONFIG_PATH="$OPENCLAW_CONFIG_PATH" "$@" >"$output_json"; then
  echo "openclaw agent command failed" >&2
  exit 1
fi

if command -v node >/dev/null 2>&1; then
  if ! node - "$output_json" >"$CLOUDCLAW_WORKSPACE/result.txt" <<'NODE'
const fs = require("fs");
const inputPath = process.argv[2];
const raw = fs.readFileSync(inputPath, "utf8");
let doc;
try {
  doc = JSON.parse(raw);
} catch (err) {
  process.stdout.write(raw);
  process.exit(0);
}

const out = [];
const collect = (v) => {
  if (!v) return;
  if (typeof v === "string") {
    if (v.trim().length > 0) out.push(v);
    return;
  }
  if (Array.isArray(v)) {
    for (const item of v) collect(item);
    return;
  }
  if (typeof v === "object") {
    if (typeof v.text === "string") collect(v.text);
    if (typeof v.content === "string") collect(v.content);
    if (typeof v.output === "string") collect(v.output);
    if (Array.isArray(v.content)) collect(v.content);
    if (Array.isArray(v.payloads)) collect(v.payloads);
  }
};

collect(doc?.result?.payloads);
collect(doc?.payloads);
collect(doc?.result?.text);
collect(doc?.result?.content);
collect(doc?.result?.output);
collect(doc?.text);
collect(doc?.content);

if (out.length === 0) {
  process.stdout.write(JSON.stringify(doc, null, 2));
} else {
  process.stdout.write(out.join("\n").trim() + "\n");
}
NODE
  then
    cp "$output_json" "$CLOUDCLAW_WORKSPACE/result.txt"
  fi
else
  cp "$output_json" "$CLOUDCLAW_WORKSPACE/result.txt"
fi

prompt_chars=$(printf "%s" "$CLOUDCLAW_INPUT" | wc -c | tr -d ' ')
resp_chars=$(wc -c < "$CLOUDCLAW_WORKSPACE/result.txt" | tr -d ' ')
pt=$(( (prompt_chars + 3) / 4 ))
ct=$(( (resp_chars + 3) / 4 ))
printf '{"prompt_tokens":%s,"completion_tokens":%s}\n' "$pt" "$ct" > "$CLOUDCLAW_USAGE_FILE"
