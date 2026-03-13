#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/common.sh"

require_cmd go
require_cmd python3
require_env DB_DSN

SESSION_ID="${SESSION_ID:-isolation-$(timestamp_utc)}"
ARTIFACT_ROOT="${ARTIFACT_ROOT:-$ARTIFACTS_BASE/isolation_validation/$SESSION_ID}"
CHECKER_BIN="${CHECKER_BIN:-$ARTIFACTS_BASE/bin/exp-isolation-check}"
RUNTIME_NAME="${CC_EXP_RUNTIME:-${AGENT_RUNTIME:-opencode}}"

ensure_artifact_dir "$ARTIFACT_ROOT"
build_go_binary "$CHECKER_BIN" "./cmd/exp-isolation-check"
prepare_experiment_run "$RUNTIME_NAME"

summary_file="$ARTIFACT_ROOT/summary.json"
csv_file="$ARTIFACT_ROOT/checks.csv"
md_file="$ARTIFACT_ROOT/checks.md"

log "running deterministic isolation validation for runtime=$RUNTIME_NAME"
"$CHECKER_BIN" \
  --data-dir "$DATA_DIR" \
  --db-driver postgres \
  --db-dsn "$DB_DSN" \
  --runtime "$RUNTIME_NAME" \
  --output-json "$summary_file" \
  --output-csv "$csv_file" \
  --output-markdown "$md_file"

cat >"$ARTIFACT_ROOT/meta.json" <<EOF
{
  "experiment": "isolation_validation",
  "session_id": $(json_quote "$SESSION_ID"),
  "run_id": $(json_quote "$SESSION_ID"),
  "runtime": $(json_quote "$RUNTIME_NAME"),
  "auto_init_runtime": $(json_quote "$EXP_AUTO_INIT_RUNTIME"),
  "auto_reset_db": $(json_quote "$EXP_AUTO_RESET_DB"),
  "auto_clean_state": $(json_quote "$EXP_AUTO_CLEAN_STATE"),
  "summary_file": $(json_quote "$summary_file"),
  "csv_file": $(json_quote "$csv_file"),
  "markdown_file": $(json_quote "$md_file")
}
EOF

log "isolation validation completed: $ARTIFACT_ROOT"
