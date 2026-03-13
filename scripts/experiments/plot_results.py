#!/usr/bin/env python3

from __future__ import annotations

import argparse
import csv
import json
import sys
from collections import defaultdict
from pathlib import Path
from statistics import mean


def scan_runs(root: Path):
    for meta_path in root.rglob("meta.json"):
        summary_path = meta_path.with_name("summary.json")
        if not summary_path.exists():
            continue
        with meta_path.open("r", encoding="utf-8") as f:
            meta = json.load(f)
        with summary_path.open("r", encoding="utf-8") as f:
            summary = json.load(f)
        yield {
            "meta": meta,
            "summary": summary,
            "run_dir": meta_path.parent,
        }


def ensure_output_dir(path: Path):
    path.mkdir(parents=True, exist_ok=True)


def aggregate_records(records, key_fields, x_field, metric_fields):
    grouped = defaultdict(list)
    for record in records:
        key = tuple(record[field] for field in key_fields) + (record[x_field],)
        grouped[key].append(record)

    rows = []
    for key, items in grouped.items():
        row = {}
        for index, field in enumerate(key_fields):
            row[field] = key[index]
        row[x_field] = key[len(key_fields)]
        row["runs"] = len(items)
        for metric in metric_fields:
            values = [float(item.get(metric, 0.0)) for item in items]
            row[metric] = mean(values) if values else 0.0
        rows.append(row)

    rows.sort(key=lambda item: tuple(item[field] for field in key_fields) + (item[x_field],))
    return rows


def write_csv(path: Path, rows):
    ensure_output_dir(path.parent)
    if not rows:
        return
    with path.open("w", encoding="utf-8", newline="") as f:
        writer = csv.DictWriter(f, fieldnames=list(rows[0].keys()))
        writer.writeheader()
        writer.writerows(rows)


def load_matplotlib():
    try:
        import matplotlib.pyplot as plt  # type: ignore
    except Exception as exc:  # pragma: no cover - helpful runtime message
        print(
            "plotting requires matplotlib; install it before generating figures "
            f"(import error: {exc})",
            file=sys.stderr,
        )
        return None
    return plt


def throughput_records(runs):
    rows = []
    for item in runs:
        meta = item["meta"]
        summary = item["summary"]
        if meta.get("experiment") != "throughput_latency":
            continue
        rows.append(
            {
                "session_id": meta.get("session_id", ""),
                "run_id": meta.get("run_id", ""),
                "runtime": meta.get("runtime", ""),
                "pool_size": int(meta.get("pool_size", 0)),
                "workspace_mode": meta.get("workspace_mode", ""),
                "workspace_state_mode": meta.get("workspace_state_mode", ""),
                "tasks_per_user": int(meta.get("tasks_per_user", 0)),
                "submitted_total": int(meta.get("submitted_total", summary.get("submitted", 0))),
                "throughput_tps": float(summary.get("throughput_tps", 0.0)),
                "e2e_p95_ms": float(summary.get("end_to_end_latency_ms", {}).get("p95", 0.0)),
                "e2e_p99_ms": float(summary.get("end_to_end_latency_ms", {}).get("p99", 0.0)),
                "queue_p95_ms": float(summary.get("queue_latency_ms", {}).get("p95", 0.0)),
                "run_p95_ms": float(summary.get("run_latency_ms", {}).get("p95", 0.0)),
            }
        )
    return rows


def fault_records(runs):
    rows = []
    for item in runs:
        meta = item["meta"]
        summary = item["summary"]
        if meta.get("experiment") != "fault_recovery":
            continue
        recovery = summary.get("event_recovery", {})
        rows.append(
            {
                "session_id": meta.get("session_id", ""),
                "run_id": meta.get("run_id", ""),
                "runtime": meta.get("runtime", ""),
                "fault_mode": meta.get("fault_mode", ""),
                "retry_priority": int(meta.get("retry_priority", 0)),
                "pool_size": int(meta.get("pool_size", 0)),
                "recovered_task_success_rate": float(recovery.get("recovered_task_success_rate", 0.0)),
                "recovered_task_total": int(recovery.get("recovered_task_total", 0)),
                "recovery_p95_ms": float(recovery.get("recovery_latency_ms", {}).get("p95", 0.0)),
                "e2e_p95_ms": float(summary.get("end_to_end_latency_ms", {}).get("p95", 0.0)),
            }
        )
    return rows


def load_latest_isolation_summary(runs):
    candidates = [item for item in runs if item["meta"].get("experiment") == "isolation_validation"]
    if not candidates:
        return None
    candidates.sort(key=lambda item: item["summary"].get("generated_at", ""))
    return candidates[-1]


def render_throughput(output_dir: Path, runs):
    records = throughput_records(runs)
    aggregated = aggregate_records(
        records,
        key_fields=["pool_size", "workspace_mode", "workspace_state_mode"],
        x_field="submitted_total",
        metric_fields=["throughput_tps", "e2e_p95_ms", "e2e_p99_ms", "queue_p95_ms", "run_p95_ms"],
    )
    write_csv(output_dir / "throughput_latency_aggregated.csv", aggregated)
    if not aggregated:
        return

    plt = load_matplotlib()
    if plt is None:
        return

    series = defaultdict(list)
    for row in aggregated:
        label = (
            f"pool={row['pool_size']} "
            f"{row['workspace_mode']}/"
            f"{row['workspace_state_mode']}"
        )
        series[label].append(row)

    fig, axes = plt.subplots(1, 2, figsize=(12, 4.5))
    for label, points in sorted(series.items()):
        points = sorted(points, key=lambda item: item["submitted_total"])
        xs = [item["submitted_total"] for item in points]
        axes[0].plot(xs, [item["throughput_tps"] for item in points], marker="o", label=label)
        axes[1].plot(xs, [item["e2e_p95_ms"] for item in points], marker="o", label=label)

    axes[0].set_title("Throughput vs Submitted Tasks")
    axes[0].set_xlabel("Submitted Tasks")
    axes[0].set_ylabel("Throughput (tasks/s)")
    axes[0].grid(alpha=0.3)

    axes[1].set_title("P95 E2E Latency vs Submitted Tasks")
    axes[1].set_xlabel("Submitted Tasks")
    axes[1].set_ylabel("P95 Latency (ms)")
    axes[1].grid(alpha=0.3)

    handles, labels = axes[0].get_legend_handles_labels()
    if handles:
        fig.legend(handles, labels, loc="lower center", ncol=2, frameon=False)
    fig.tight_layout(rect=(0, 0.08, 1, 1))
    fig.savefig(output_dir / "throughput_latency.png", dpi=200)
    plt.close(fig)


def render_fault(output_dir: Path, runs):
    records = fault_records(runs)
    aggregated = aggregate_records(
        records,
        key_fields=["fault_mode", "retry_priority"],
        x_field="pool_size",
        metric_fields=["recovered_task_success_rate", "recovered_task_total", "recovery_p95_ms", "e2e_p95_ms"],
    )
    write_csv(output_dir / "fault_recovery_aggregated.csv", aggregated)
    if not aggregated:
        return

    plt = load_matplotlib()
    if plt is None:
        return

    labels = [
        f"{row['fault_mode']}\nretry={row['retry_priority']}\npool={row['pool_size']}"
        for row in aggregated
    ]
    success_rate = [row["recovered_task_success_rate"] * 100.0 for row in aggregated]
    recovery_p95 = [row["recovery_p95_ms"] for row in aggregated]

    fig, axes = plt.subplots(1, 2, figsize=(12, 4.5))
    axes[0].bar(labels, success_rate, color="#4C78A8")
    axes[0].set_title("Recovered Task Success Rate")
    axes[0].set_ylabel("Success Rate (%)")
    axes[0].grid(axis="y", alpha=0.3)

    axes[1].bar(labels, recovery_p95, color="#F58518")
    axes[1].set_title("Recovery P95 Latency")
    axes[1].set_ylabel("Latency (ms)")
    axes[1].grid(axis="y", alpha=0.3)

    for ax in axes:
        ax.tick_params(axis="x", rotation=15)

    fig.tight_layout()
    fig.savefig(output_dir / "fault_recovery.png", dpi=200)
    plt.close(fig)


def render_isolation(output_dir: Path, runs):
    latest = load_latest_isolation_summary(runs)
    if latest is None:
        return

    summary = latest["summary"]
    checks = summary.get("checks", [])
    csv_rows = []
    for item in checks:
        csv_rows.append(
            {
                "name": item.get("name", ""),
                "status": item.get("status", ""),
                "details": item.get("details", ""),
            }
        )
    write_csv(output_dir / "isolation_validation.csv", csv_rows)

    lines = [
        "# Isolation Validation",
        "",
        f"- Runtime: `{summary.get('runtime', '')}`",
        f"- Passed: `{summary.get('passed', 0)}`",
        f"- Failed: `{summary.get('failed', 0)}`",
        f"- Skipped: `{summary.get('skipped', 0)}`",
        "",
        "| Check | Status | Details |",
        "| --- | --- | --- |",
    ]
    for item in checks:
        lines.append(
            f"| {item.get('name', '')} | {item.get('status', '')} | {item.get('details', '')} |"
        )

    ensure_output_dir(output_dir)
    (output_dir / "isolation_validation.md").write_text("\n".join(lines) + "\n", encoding="utf-8")


def main():
    parser = argparse.ArgumentParser(description="Aggregate CloudClaw experiment outputs and render paper-ready figures.")
    parser.add_argument(
        "--artifacts-root",
        default="experiment_artifacts",
        help="root directory containing experiment outputs",
    )
    parser.add_argument(
        "--output-dir",
        default="experiment_artifacts/plots",
        help="directory for aggregated CSV files and figures",
    )
    parser.add_argument(
        "--experiment",
        choices=["all", "throughput", "fault", "isolation"],
        default="all",
        help="which plots/tables to generate",
    )
    args = parser.parse_args()

    artifacts_root = Path(args.artifacts_root).resolve()
    output_dir = Path(args.output_dir).resolve()
    ensure_output_dir(output_dir)

    runs = list(scan_runs(artifacts_root))
    if args.experiment in ("all", "throughput"):
        render_throughput(output_dir, runs)
    if args.experiment in ("all", "fault"):
        render_fault(output_dir, runs)
    if args.experiment in ("all", "isolation"):
        render_isolation(output_dir, runs)


if __name__ == "__main__":
    main()
