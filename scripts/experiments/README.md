# CloudClaw Experiment Scripts

## Prerequisites

1. Runner and pool are already up on target server.
2. `AGENT_RUNTIME` is set (`opencode` or `claudecode`).
3. `go` and `docker` are installed.

Optional environment overrides:

- `CC_DATA_DIR`, `CC_DB_DRIVER`, `CC_DB_DSN`
- `OUT_BASE_DIR` (default: `./experiment_artifacts`)
- `RETRY_PRIORITY` (for runner restart experiments)

## 1) Throughput and latency

```bash
AGENT_RUNTIME=opencode scripts/experiments/01_throughput_latency.sh
```

Main knobs:

- `LEVELS` (default `10,20,50,100,200,500,1000`)
- `TASKS_PER_USER` (default `1`)
- `SUBMIT_WORKERS_MAX` (default `256`)

Output:

- `summary.csv`: per-level throughput and latency percentiles
- `*.json`: per-run detailed summary

## 2) Fault injection (kill runner/container)

```bash
AGENT_RUNTIME=opencode scripts/experiments/02_fault_injection.sh
```

Main knobs:

- `CONCURRENCY_USERS` (default `100`)
- `TASKS_PER_USER` (default `5`)
- `INJECT_INTERVAL_SEC` (default `5`)
- `RUNNER_KILL_RATIO` (default `30`, percentage)

Output:

- `summary.json` / `summary.csv`
- `injections.csv` (timestamped kill actions)

## 3) Isolation validation

```bash
scripts/experiments/03_isolation_validation.sh
```

Covers:

- cross-user same filename isolation
- malicious symlink/path checks
- oversized file/total size limits
- docker mount boundary checks

Output:

- `test_report.log`

## 4) Retry priority A/B

```bash
AGENT_RUNTIME=opencode scripts/experiments/04_retry_priority_gain.sh
```

This script compares `RETRY_PRIORITY=0` vs `RETRY_PRIORITY=1` by repeatedly running experiment 2 and collecting tail latency.

Main knobs:

- `ROUNDS` (default `3`)
- Any knobs from experiment 2 (load and injection intensity)

Output:

- `compare.csv` (raw per-round metrics)
- `compare.txt` (aggregated comparison)
