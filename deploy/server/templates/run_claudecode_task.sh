#!/bin/sh
set -eu

: "${CLOUDCLAW_WORKSPACE:?missing CLOUDCLAW_WORKSPACE}"
: "${CLOUDCLAW_INPUT:?missing CLOUDCLAW_INPUT}"
: "${CLOUDCLAW_USAGE_FILE:?missing CLOUDCLAW_USAGE_FILE}"

CLAUDECODE_HOME="${CLAUDECODE_HOME:-}"
CLAUDECODE_CONFIG_PATH="${CLAUDECODE_CONFIG_PATH:-/workspace/.claudecode/config.json}"
CLAUDECODE_EXEC_MODE="$(printf '%s' "${CLAUDECODE_EXEC_MODE:-gateway}" | tr '[:upper:]' '[:lower:]')"
CLAUDECODE_SESSION_ID="${CLAUDECODE_SESSION_ID:-cloudclaw-${CLOUDCLAW_TASK_ID:-task}}"
CLAUDECODE_GATEWAY_BIND="${CLAUDECODE_GATEWAY_BIND:-loopback}"
CLAUDECODE_GATEWAY_PORT="${CLAUDECODE_GATEWAY_PORT:-8688}"
CLAUDECODE_GATEWAY_MANAGE="${CLAUDECODE_GATEWAY_MANAGE:-1}"
CLAUDECODE_GATEWAY_TOKEN="${CLAUDECODE_GATEWAY_TOKEN:-}"
CLAUDECODE_AGENT_ID="${CLAUDECODE_AGENT_ID:-}"
CLAUDECODE_TIMEOUT_SECONDS="${CLAUDECODE_TIMEOUT_SECONDS:-}"

TASK_HOME="$(dirname "$CLOUDCLAW_WORKSPACE")"
SHARED_DIR="${CLOUDCLAW_SHARED_SKILLS_DIR:-}"
TMP_ROOT="${TMPDIR:-/tmp}"
output_json=""
gateway_log=""

mkdir -p "$TMP_ROOT"

cleanup() {
  if [ -n "$output_json" ] && [ -f "$output_json" ]; then
    rm -f "$output_json"
  fi
  if [ -n "$gateway_log" ] && [ -f "$gateway_log" ]; then
    rm -f "$gateway_log"
  fi
}
trap cleanup EXIT INT TERM

runtime_home_default="$CLOUDCLAW_WORKSPACE/.claudecode-home"
if [ -z "${CLAUDECODE_HOME:-}" ]; then
  CLAUDECODE_HOME="$runtime_home_default"
fi

mkdir -p "$CLOUDCLAW_WORKSPACE" "$TASK_HOME" "$CLAUDECODE_HOME"
gateway_log="$(mktemp -p "$TMP_ROOT" claudecode-gateway.XXXXXX)"

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

  if [ -d "$SHARED_WORKSPACE/skills" ]; then
    mkdir -p "$CLOUDCLAW_WORKSPACE/skills"
    (
      cd "$SHARED_WORKSPACE/skills"
      find . -type d -exec mkdir -p "$CLOUDCLAW_WORKSPACE/skills/{}" \;
      find . -type f | while IFS= read -r rel; do
        rel="${rel#./}"
        target="$CLOUDCLAW_WORKSPACE/skills/$rel"
        if [ ! -e "$target" ]; then
          mkdir -p "$(dirname "$target")"
          cp -f "$SHARED_WORKSPACE/skills/$rel" "$target"
        fi
      done
    )
  fi
fi

if [ ! -s "$CLAUDECODE_CONFIG_PATH" ]; then
  echo "missing or empty claudecode config: $CLAUDECODE_CONFIG_PATH" >&2
  exit 1
fi

default_cfg="$CLAUDECODE_HOME/config.json"
if [ "$CLAUDECODE_CONFIG_PATH" != "$default_cfg" ]; then
  cp -f "$CLAUDECODE_CONFIG_PATH" "$default_cfg"
fi
CLAUDECODE_CONFIG_PATH="$default_cfg"
export HOME="$CLAUDECODE_HOME"

claudecode_health() {
  if [ -n "$CLAUDECODE_GATEWAY_TOKEN" ]; then
    CLAUDECODE_HOME="$CLAUDECODE_HOME" CLAUDECODE_CONFIG_PATH="$CLAUDECODE_CONFIG_PATH" claudecode health --token "$CLAUDECODE_GATEWAY_TOKEN" >/dev/null 2>&1
    return $?
  fi
  CLAUDECODE_HOME="$CLAUDECODE_HOME" CLAUDECODE_CONFIG_PATH="$CLAUDECODE_CONFIG_PATH" claudecode health >/dev/null 2>&1
}

ensure_gateway() {
  if [ "$CLAUDECODE_EXEC_MODE" != "gateway" ]; then
    return 0
  fi
  if claudecode_health; then
    return 0
  fi
  if [ "$CLAUDECODE_GATEWAY_MANAGE" = "0" ]; then
    return 1
  fi

  CLAUDECODE_HOME="$CLAUDECODE_HOME" CLAUDECODE_CONFIG_PATH="$CLAUDECODE_CONFIG_PATH" \
    claudecode gateway --bind "$CLAUDECODE_GATEWAY_BIND" --port "$CLAUDECODE_GATEWAY_PORT" >"$gateway_log" 2>&1 &

  for _ in $(seq 1 10); do
    if claudecode_health; then
      return 0
    fi
    sleep 1
  done
  return 1
}

if [ "$CLAUDECODE_EXEC_MODE" = "gateway" ] && ! ensure_gateway; then
  echo "warning: claudecode gateway not ready, command may fallback to embedded mode" >&2
fi

set -- claudecode agent --message "$CLOUDCLAW_INPUT" --json
if [ "$CLAUDECODE_EXEC_MODE" = "local" ]; then
  set -- "$@" --local
else
  set -- "$@" --session-id "$CLAUDECODE_SESSION_ID"
fi
if [ -n "$CLAUDECODE_AGENT_ID" ]; then
  set -- "$@" --agent "$CLAUDECODE_AGENT_ID"
fi
if [ -n "$CLAUDECODE_TIMEOUT_SECONDS" ]; then
  set -- "$@" --timeout "$CLAUDECODE_TIMEOUT_SECONDS"
fi

output_json="$(mktemp -p "$TMP_ROOT" claudecode-output.XXXXXX)"
if ! CLAUDECODE_HOME="$CLAUDECODE_HOME" CLAUDECODE_CONFIG_PATH="$CLAUDECODE_CONFIG_PATH" "$@" >"$output_json"; then
  echo "claudecode agent command failed" >&2
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
