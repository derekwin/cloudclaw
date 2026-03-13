#!/usr/bin/env python3

from __future__ import annotations

import argparse
import csv
import json
import math
import re
from collections import defaultdict
from pathlib import Path
from statistics import mean
from typing import Any


def ensure_output_dir(path: Path) -> None:
    path.mkdir(parents=True, exist_ok=True)


def parse_json(path: Path) -> dict[str, Any]:
    with path.open("r", encoding="utf-8") as f:
        return json.load(f)


def infer_meta(summary_path: Path) -> dict[str, Any]:
    run_dir = summary_path.parent.name
    parts = summary_path.parts
    experiment = ""
    if "throughput_latency" in parts:
        experiment = "throughput_latency"
    elif "fault_recovery" in parts:
        experiment = "fault_recovery"
    elif "isolation_validation" in parts:
        experiment = "isolation_validation"

    meta: dict[str, Any] = {
        "experiment": experiment,
        "run_id": run_dir,
        "summary_file": str(summary_path.resolve()),
        "meta_inferred": True,
    }
    session_dir = summary_path.parent.parent
    session_env = session_dir / "session.env"
    if session_env.exists():
        for line in session_env.read_text(encoding="utf-8").splitlines():
            if "=" not in line:
                continue
            key, value = line.split("=", 1)
            key = key.strip()
            value = value.strip()
            if key == "session_id":
                meta["session_id"] = value
            elif key == "agent_runtime":
                meta["runtime"] = value

    if experiment == "throughput_latency":
        match = re.match(
            r"pool(?P<pool>\d+)_wm(?P<wm>.+?)_ws(?P<ws>.+?)_tasks(?P<tasks>\d+)_rep(?P<rep>\d+)$",
            run_dir,
        )
        if match:
            meta.update(
                {
                    "pool_size": int(match.group("pool")),
                    "workspace_mode": match.group("wm"),
                    "workspace_state_mode": match.group("ws"),
                    "tasks_per_user": int(match.group("tasks")),
                    "repeat_index": int(match.group("rep")),
                }
            )
    elif experiment == "fault_recovery":
        match = re.match(
            r"fault_(?P<mode>.+?)_retry(?P<retry>\d+)_rep(?P<rep>\d+)$",
            run_dir,
        )
        if match:
            meta.update(
                {
                    "fault_mode": match.group("mode"),
                    "retry_priority": int(match.group("retry")),
                    "repeat_index": int(match.group("rep")),
                }
            )
    return meta


def scan_runs(root: Path) -> list[dict[str, Any]]:
    runs: list[dict[str, Any]] = []
    for summary_path in root.rglob("summary.json"):
        summary = parse_json(summary_path)
        meta_path = summary_path.with_name("meta.json")
        if meta_path.exists():
            meta = parse_json(meta_path)
            meta["meta_inferred"] = False
        else:
            meta = infer_meta(summary_path)
        runs.append(
            {
                "meta": meta,
                "summary": summary,
                "run_dir": summary_path.parent,
            }
        )
    return runs


def aggregate_records(records, key_fields, x_field, metric_fields):
    grouped = defaultdict(list)
    for record in records:
        key = tuple(record.get(field) for field in key_fields) + (record.get(x_field),)
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

    rows.sort(key=lambda item: tuple(item.get(field) for field in key_fields) + (item.get(x_field),))
    return rows


def write_csv(path: Path, rows: list[dict[str, Any]]) -> None:
    ensure_output_dir(path.parent)
    if not rows:
        return
    with path.open("w", encoding="utf-8", newline="") as f:
        writer = csv.DictWriter(f, fieldnames=list(rows[0].keys()))
        writer.writeheader()
        writer.writerows(rows)


def svg_header(width: int, height: int) -> list[str]:
    return [
        f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" viewBox="0 0 {width} {height}">',
        '<style>',
        "text { font-family: Arial, sans-serif; fill: #1f2937; }",
        ".title { font-size: 18px; font-weight: bold; }",
        ".axis { font-size: 11px; }",
        ".legend { font-size: 11px; }",
        ".grid { stroke: #e5e7eb; stroke-width: 1; }",
        ".axis-line { stroke: #4b5563; stroke-width: 1.2; }",
        '</style>',
    ]


def write_svg(path: Path, lines: list[str]) -> None:
    ensure_output_dir(path.parent)
    lines.append("</svg>")
    path.write_text("\n".join(lines) + "\n", encoding="utf-8")


def scale(value: float, domain_min: float, domain_max: float, out_min: float, out_max: float) -> float:
    if math.isclose(domain_min, domain_max):
        return (out_min + out_max) / 2
    ratio = (value - domain_min) / (domain_max - domain_min)
    return out_min + ratio * (out_max - out_min)


def render_line_chart_svg(
    path: Path,
    title: str,
    x_label: str,
    y_label: str,
    series: dict[str, list[tuple[float, float]]],
) -> None:
    width = 860
    height = 460
    left = 70
    right = 30
    top = 50
    bottom = 70
    chart_w = width - left - right
    chart_h = height - top - bottom
    colors = ["#2563eb", "#dc2626", "#059669", "#d97706", "#7c3aed", "#0891b2"]

    all_x = [point[0] for points in series.values() for point in points]
    all_y = [point[1] for points in series.values() for point in points]
    if not all_x or not all_y:
        return
    x_min, x_max = min(all_x), max(all_x)
    y_min, y_max = 0.0, max(all_y) * 1.1 if max(all_y) > 0 else 1.0

    lines = svg_header(width, height)
    lines.append(f'<text class="title" x="{left}" y="28">{title}</text>')

    for step in range(6):
        y_val = y_min + (y_max - y_min) * step / 5
        y = top + chart_h - (chart_h * step / 5)
        lines.append(f'<line class="grid" x1="{left}" y1="{y:.1f}" x2="{left + chart_w}" y2="{y:.1f}" />')
        lines.append(f'<text class="axis" x="{left - 8}" y="{y + 4:.1f}" text-anchor="end">{y_val:.1f}</text>')

    x_ticks = sorted(set(all_x))
    for x_val in x_ticks:
        x = scale(x_val, x_min, x_max, left, left + chart_w)
        lines.append(f'<line class="grid" x1="{x:.1f}" y1="{top}" x2="{x:.1f}" y2="{top + chart_h}" />')
        lines.append(f'<text class="axis" x="{x:.1f}" y="{top + chart_h + 18}" text-anchor="middle">{int(x_val)}</text>')

    lines.append(f'<line class="axis-line" x1="{left}" y1="{top}" x2="{left}" y2="{top + chart_h}" />')
    lines.append(f'<line class="axis-line" x1="{left}" y1="{top + chart_h}" x2="{left + chart_w}" y2="{top + chart_h}" />')
    lines.append(f'<text class="axis" x="{left + chart_w / 2:.1f}" y="{height - 18}" text-anchor="middle">{x_label}</text>')
    lines.append(
        f'<text class="axis" x="20" y="{top + chart_h / 2:.1f}" transform="rotate(-90 20 {top + chart_h / 2:.1f})" text-anchor="middle">{y_label}</text>'
    )

    legend_y = top
    for index, (label, points) in enumerate(sorted(series.items())):
        color = colors[index % len(colors)]
        points = sorted(points, key=lambda item: item[0])
        coords = []
        for x_val, y_val in points:
            x = scale(x_val, x_min, x_max, left, left + chart_w)
            y = scale(y_val, y_min, y_max, top + chart_h, top)
            coords.append((x, y))
        polyline = " ".join(f"{x:.1f},{y:.1f}" for x, y in coords)
        lines.append(f'<polyline fill="none" stroke="{color}" stroke-width="2.4" points="{polyline}" />')
        for x, y in coords:
            lines.append(f'<circle cx="{x:.1f}" cy="{y:.1f}" r="3.5" fill="{color}" />')

        lx = left + chart_w - 200
        ly = legend_y + 20 * index
        lines.append(f'<line x1="{lx}" y1="{ly}" x2="{lx + 20}" y2="{ly}" stroke="{color}" stroke-width="2.4" />')
        lines.append(f'<text class="legend" x="{lx + 28}" y="{ly + 4}">{label}</text>')

    write_svg(path, lines)


def render_bar_chart_svg(
    path: Path,
    title: str,
    y_label: str,
    labels: list[str],
    values: list[float],
    color: str,
) -> None:
    width = 860
    height = 460
    left = 70
    right = 30
    top = 50
    bottom = 110
    chart_w = width - left - right
    chart_h = height - top - bottom
    y_max = max(values) * 1.15 if values and max(values) > 0 else 1.0

    lines = svg_header(width, height)
    lines.append(f'<text class="title" x="{left}" y="28">{title}</text>')

    for step in range(6):
        y_val = y_max * step / 5
        y = top + chart_h - (chart_h * step / 5)
        lines.append(f'<line class="grid" x1="{left}" y1="{y:.1f}" x2="{left + chart_w}" y2="{y:.1f}" />')
        lines.append(f'<text class="axis" x="{left - 8}" y="{y + 4:.1f}" text-anchor="end">{y_val:.1f}</text>')

    lines.append(f'<line class="axis-line" x1="{left}" y1="{top}" x2="{left}" y2="{top + chart_h}" />')
    lines.append(f'<line class="axis-line" x1="{left}" y1="{top + chart_h}" x2="{left + chart_w}" y2="{top + chart_h}" />')
    lines.append(
        f'<text class="axis" x="20" y="{top + chart_h / 2:.1f}" transform="rotate(-90 20 {top + chart_h / 2:.1f})" text-anchor="middle">{y_label}</text>'
    )

    count = len(values)
    if count > 0:
        bar_w = chart_w / max(count * 1.6, 1)
        gap = bar_w * 0.6
        start = left + (chart_w - (count * bar_w + max(count - 1, 0) * gap)) / 2
        for index, (label, value) in enumerate(zip(labels, values)):
            x = start + index * (bar_w + gap)
            y = scale(value, 0, y_max, top + chart_h, top)
            h = top + chart_h - y
            lines.append(f'<rect x="{x:.1f}" y="{y:.1f}" width="{bar_w:.1f}" height="{h:.1f}" fill="{color}" />')
            lines.append(f'<text class="axis" x="{x + bar_w / 2:.1f}" y="{top + chart_h + 16}" text-anchor="middle">{value:.1f}</text>')
            lines.append(
                f'<text class="axis" x="{x + bar_w / 2:.1f}" y="{top + chart_h + 34}" text-anchor="middle">{label}</text>'
            )

    write_svg(path, lines)


def throughput_records(runs: list[dict[str, Any]]) -> list[dict[str, Any]]:
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
                "pool_size": int(meta.get("pool_size", 0) or 0),
                "workspace_mode": meta.get("workspace_mode", ""),
                "workspace_state_mode": meta.get("workspace_state_mode", ""),
                "tasks_per_user": int(meta.get("tasks_per_user", 0) or 0),
                "submitted_total": int(meta.get("submitted_total", summary.get("submitted", 0)) or 0),
                "throughput_tps": float(summary.get("throughput_tps", 0.0)),
                "e2e_p95_ms": float(summary.get("end_to_end_latency_ms", {}).get("p95", 0.0)),
                "e2e_p99_ms": float(summary.get("end_to_end_latency_ms", {}).get("p99", 0.0)),
                "queue_p95_ms": float(summary.get("queue_latency_ms", {}).get("p95", 0.0)),
                "run_p95_ms": float(summary.get("run_latency_ms", {}).get("p95", 0.0)),
                "submitted": int(summary.get("submitted", 0)),
                "completed": int(summary.get("completed", 0)),
                "succeeded": int(summary.get("succeeded", 0)),
                "failed": int(summary.get("failed", 0)),
                "timeout_like": int(summary.get("completed", 0) < summary.get("submitted", 0)),
                "success_rate": (
                    float(summary.get("succeeded", 0)) / float(summary.get("submitted", 1))
                    if int(summary.get("submitted", 0)) > 0
                    else 0.0
                ),
                "meta_inferred": bool(meta.get("meta_inferred", False)),
            }
        )
    return rows


def fault_records(runs: list[dict[str, Any]]) -> list[dict[str, Any]]:
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
                "retry_priority": int(meta.get("retry_priority", 0) or 0),
                "pool_size": int(meta.get("pool_size", 0) or 0),
                "submitted": int(summary.get("submitted", 0)),
                "completed": int(summary.get("completed", 0)),
                "succeeded": int(summary.get("succeeded", 0)),
                "failed": int(summary.get("failed", 0)),
                "success_rate": (
                    float(summary.get("succeeded", 0)) / float(summary.get("submitted", 1))
                    if int(summary.get("submitted", 0)) > 0
                    else 0.0
                ),
                "recovered_task_success_rate": float(recovery.get("recovered_task_success_rate", 0.0)),
                "recovered_task_total": int(recovery.get("recovered_task_total", 0)),
                "recovery_p95_ms": float(recovery.get("recovery_latency_ms", {}).get("p95", 0.0)),
                "e2e_p95_ms": float(summary.get("end_to_end_latency_ms", {}).get("p95", 0.0)),
                "meta_inferred": bool(meta.get("meta_inferred", False)),
            }
        )
    return rows


def isolation_summary(runs: list[dict[str, Any]]) -> dict[str, Any] | None:
    candidates = [item for item in runs if item["meta"].get("experiment") == "isolation_validation"]
    if not candidates:
        return None
    candidates.sort(key=lambda item: item["summary"].get("generated_at", ""))
    return candidates[-1]["summary"]


def render_throughput(output_dir: Path, runs: list[dict[str, Any]]) -> list[str]:
    records = throughput_records(runs)
    if not records:
        return ["No throughput records found."]

    raw_path = output_dir / "throughput_latency_raw.csv"
    write_csv(raw_path, records)
    aggregated = aggregate_records(
        records,
        key_fields=["pool_size", "workspace_mode", "workspace_state_mode"],
        x_field="submitted_total",
        metric_fields=["throughput_tps", "e2e_p95_ms", "success_rate", "failed", "timeout_like"],
    )
    write_csv(output_dir / "throughput_latency_aggregated.csv", aggregated)

    series_tput = defaultdict(list)
    series_lat = defaultdict(list)
    for row in aggregated:
        pool_text = f"pool={row['pool_size']} " if int(row["pool_size"]) > 0 else ""
        label = f"{pool_text}{row['workspace_mode']}/{row['workspace_state_mode']}".strip()
        series_tput[label].append((float(row["submitted_total"]), float(row["throughput_tps"])))
        series_lat[label].append((float(row["submitted_total"]), float(row["e2e_p95_ms"])))

    render_line_chart_svg(
        output_dir / "throughput_latency_throughput.svg",
        "Throughput vs Submitted Tasks",
        "Submitted Tasks",
        "Throughput (tasks/s)",
        series_tput,
    )
    render_line_chart_svg(
        output_dir / "throughput_latency_p95.svg",
        "P95 End-to-End Latency vs Submitted Tasks",
        "Submitted Tasks",
        "P95 Latency (ms)",
        series_lat,
    )

    findings = []
    bad_runs = [row for row in records if row["success_rate"] < 0.95]
    if bad_runs:
        findings.append(f"Found {len(bad_runs)} throughput runs with success_rate < 95%; these are not paper-ready.")
    inferred = sum(1 for row in records if row["meta_inferred"])
    if inferred:
        findings.append(f"{inferred} throughput runs were recovered by inferring metadata from directory names.")
    if not findings:
        findings.append("All throughput runs look internally consistent.")
    return findings


def render_fault(output_dir: Path, runs: list[dict[str, Any]]) -> list[str]:
    records = fault_records(runs)
    if not records:
        return ["No fault-recovery records found."]

    write_csv(output_dir / "fault_recovery_raw.csv", records)
    aggregated = aggregate_records(
        records,
        key_fields=["fault_mode", "retry_priority"],
        x_field="pool_size",
        metric_fields=["recovered_task_success_rate", "recovery_p95_ms", "success_rate"],
    )
    write_csv(output_dir / "fault_recovery_aggregated.csv", aggregated)

    labels = []
    for row in aggregated:
        label = f"{row['fault_mode']} retry={row['retry_priority']}"
        if int(row["pool_size"]) > 0:
            label += f" pool={row['pool_size']}"
        labels.append(label)
    render_bar_chart_svg(
        output_dir / "fault_recovery_success.svg",
        "Recovered Task Success Rate",
        "Recovered Task Success Rate (%)",
        labels,
        [float(row["recovered_task_success_rate"]) * 100.0 for row in aggregated],
        "#2563eb",
    )
    render_bar_chart_svg(
        output_dir / "fault_recovery_latency.svg",
        "Recovery P95 Latency",
        "Recovery P95 Latency (ms)",
        labels,
        [float(row["recovery_p95_ms"]) for row in aggregated],
        "#dc2626",
    )

    findings = []
    bad_runs = [row for row in records if row["completed"] < row["submitted"]]
    if bad_runs:
        findings.append(f"Found {len(bad_runs)} fault runs with incomplete result collection before timeout.")
    inferred = sum(1 for row in records if row["meta_inferred"])
    if inferred:
        findings.append(f"{inferred} fault runs were recovered by inferring metadata from directory names.")
    if not findings:
        findings.append("All fault-recovery runs look internally consistent.")
    return findings


def render_isolation(output_dir: Path, runs: list[dict[str, Any]]) -> list[str]:
    summary = isolation_summary(runs)
    if summary is None:
        return ["No isolation-validation summary found."]

    checks = summary.get("checks", [])
    csv_rows = [{"name": item.get("name", ""), "status": item.get("status", ""), "details": item.get("details", "")} for item in checks]
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
        lines.append(f"| {item.get('name', '')} | {item.get('status', '')} | {item.get('details', '')} |")
    (output_dir / "isolation_validation.md").write_text("\n".join(lines) + "\n", encoding="utf-8")

    if int(summary.get("failed", 0)) == 0:
        return ["Isolation validation passed all checks."]
    return [f"Isolation validation reported {summary.get('failed', 0)} failed checks."]


def write_report(output_dir: Path, throughput_notes: list[str], fault_notes: list[str], isolation_notes: list[str]) -> None:
    report = [
        "# Experiment Plot Report",
        "",
        "## Throughput / Latency",
    ]
    report.extend(f"- {note}" for note in throughput_notes)
    report.extend(
        [
            "",
            "## Fault Recovery",
        ]
    )
    report.extend(f"- {note}" for note in fault_notes)
    report.extend(
        [
            "",
            "## Isolation Validation",
        ]
    )
    report.extend(f"- {note}" for note in isolation_notes)
    report.extend(
        [
            "",
            "Generated files:",
            "- `throughput_latency_raw.csv`",
            "- `throughput_latency_aggregated.csv`",
            "- `throughput_latency_throughput.svg`",
            "- `throughput_latency_p95.svg`",
            "- `fault_recovery_raw.csv`",
            "- `fault_recovery_aggregated.csv`",
            "- `fault_recovery_success.svg`",
            "- `fault_recovery_latency.svg`",
            "- `isolation_validation.csv`",
            "- `isolation_validation.md`",
        ]
    )
    (output_dir / "REPORT.md").write_text("\n".join(report) + "\n", encoding="utf-8")


def main() -> None:
    parser = argparse.ArgumentParser(description="Aggregate CloudClaw experiment outputs and render paper-ready SVG figures.")
    parser.add_argument("--artifacts-root", default="experiment_artifacts", help="root directory containing experiment outputs")
    parser.add_argument("--output-dir", default="experiment_artifacts/plots", help="directory for aggregated CSV files and figures")
    args = parser.parse_args()

    artifacts_root = Path(args.artifacts_root).resolve()
    output_dir = Path(args.output_dir).resolve()
    ensure_output_dir(output_dir)

    runs = scan_runs(artifacts_root)
    throughput_notes = render_throughput(output_dir, runs)
    fault_notes = render_fault(output_dir, runs)
    isolation_notes = render_isolation(output_dir, runs)
    write_report(output_dir, throughput_notes, fault_notes, isolation_notes)


if __name__ == "__main__":
    main()
