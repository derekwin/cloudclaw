#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./common.sh
source "$SCRIPT_DIR/common.sh"

need_cmd "$GO_BIN"
export CLOUDCLAW_TEST_POSTGRES_DSN="${CLOUDCLAW_TEST_POSTGRES_DSN:-$CC_DB_DSN}"
if [[ -z "${CLOUDCLAW_TEST_POSTGRES_DSN// }" ]]; then
  die "CLOUDCLAW_TEST_POSTGRES_DSN (or CC_DB_DSN/DB_DSN) is required"
fi

OUT_DIR="$(prepare_output_dir isolation_validation)"
REPORT="$OUT_DIR/test_report.log"

log "isolation validation started"
log "output dir: $OUT_DIR"

(
  cd "$REPO_ROOT"

  echo "=== store isolation and safety tests ==="
  "$GO_BIN" test ./internal/store -count=1 -v -run 'TestUserDataIsolationAcrossUsersWithSameFilename|TestReplaceUserDataRejectsSymlink|TestReplaceUserDataRejectsOversizedFile|TestReplaceUserDataRejectsOversizedTotal|TestIsSafeRelativePathRejectsTraversal'

  echo "=== workspace isolation tests ==="
  "$GO_BIN" test ./internal/workspace -count=1 -v -run 'TestSafeUserRuntimeName|TestEphemeralModeCopiesUserRuntimeInAndOut|TestEphemeralModeCopiesClaudeRuntimeInAndOut|TestEphemeralPersistKeepsExistingRuntimeWhenRunStateMissing'

  echo "=== mount boundary test ==="
  "$GO_BIN" test ./internal/engine -count=1 -v -run 'TestLayoutForMountedWorkspaceRejectsOutsideHostBase'
) | tee "$REPORT"

log "isolation validation finished"
log "report: $REPORT"
