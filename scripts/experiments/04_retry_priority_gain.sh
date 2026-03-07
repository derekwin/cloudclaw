#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./common.sh
source "$SCRIPT_DIR/common.sh"

ensure_runtime
need_cmd "$GO_BIN"

ROUNDS="${ROUNDS:-3}"
OUT_DIR="$(prepare_output_dir retry_priority_gain)"
COMPARE_CSV="$OUT_DIR/compare.csv"
COMPARE_TXT="$OUT_DIR/compare.txt"

echo "mode,round,e2e_p99_ms,e2e_p95_ms,throughput_tps,retries_observed,recovered_task_success_rate" > "$COMPARE_CSV"

extract_metrics() {
  local csv_file="$1"
  awk -F',' '
    NR==1 {
      for (i=1; i<=NF; i++) idx[$i]=i;
      next;
    }
    NR>1 {
      last_p99=$(idx["e2e_p99_ms"]);
      last_p95=$(idx["e2e_p95_ms"]);
      last_tp=$(idx["throughput_tps"]);
      last_retries=$(idx["retries_observed"]);
      last_recover=$(idx["recovered_task_success_rate"]);
    }
    END {
      printf "%s,%s,%s,%s,%s\n", last_p99, last_p95, last_tp, last_retries, last_recover;
    }
  ' "$csv_file"
}

avg_for_mode() {
  local mode="$1"
  awk -F',' -v m="$mode" '
    NR==1 { next }
    $1==m {
      n++;
      p99+=$3;
      p95+=$4;
      tp+=$5;
      rec+=$7;
    }
    END {
      if (n==0) {
        printf "0,0,0,0\n";
      } else {
        printf "%.2f,%.2f,%.4f,%.4f\n", p99/n, p95/n, tp/n, rec/n;
      }
    }
  ' "$COMPARE_CSV"
}

log "retry-priority A/B experiment started"
log "output dir: $OUT_DIR"

for mode in 0 1; do
  log "switch runner retry priority to $mode"
  RETRY_PRIORITY="$mode" run_cloudclawctl runner restart >/dev/null

  for ((r=1; r<=ROUNDS; r++)); do
    round_out="$OUT_DIR/mode_${mode}/round_${r}"
    mkdir -p "$round_out"

    log "mode=$mode round=$r"
    RETRY_PRIORITY="$mode" OUT_DIR="$round_out" "$SCRIPT_DIR/02_fault_injection.sh"

    summary_csv="$round_out/summary.csv"
    if [[ ! -f "$summary_csv" ]]; then
      die "missing summary csv: $summary_csv"
    fi

    metrics="$(extract_metrics "$summary_csv")"
    echo "$mode,$r,$metrics" >> "$COMPARE_CSV"
  done

done

m0="$(avg_for_mode 0)"
m1="$(avg_for_mode 1)"
m0_p99="$(echo "$m0" | cut -d',' -f1)"
m1_p99="$(echo "$m1" | cut -d',' -f1)"

improve="0"
if awk "BEGIN {exit !($m0_p99 > 0)}"; then
  improve="$(awk "BEGIN {printf \"%.2f\", (($m1_p99 - $m0_p99) / $m0_p99) * 100}")"
fi

{
  echo "retry priority A/B summary"
  echo "- mode 0 avg (p99,p95,throughput,recovery_success_rate): $m0"
  echo "- mode 1 avg (p99,p95,throughput,recovery_success_rate): $m1"
  echo "- p99 change (mode1 vs mode0): ${improve}%"
} | tee "$COMPARE_TXT"

log "retry-priority A/B experiment finished"
log "compare csv: $COMPARE_CSV"
log "compare txt: $COMPARE_TXT"
