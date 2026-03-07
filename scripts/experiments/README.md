# CloudClaw Experiment Scripts

按顺序完整执行命令见：

- `scripts/experiments/EXPERIMENT_RUNBOOK.md`

## Prerequisites

1. Runner and pool are already up on target server.
2. `AGENT_RUNTIME` is set (`opencode` / `openclaw` / `claudecode`).
3. `go` and `docker` are installed.

Optional environment overrides:

- `CC_DATA_DIR`, `CC_DB_DRIVER`, `CC_DB_DSN`
- `OUT_BASE_DIR` (default: `./experiment_artifacts`)
- `RETRY_PRIORITY` (for runner restart experiments)
- `INPUT_PREFIX` (default: `Reply with exactly OK and stop.`)

## PostgreSQL for Experiments (Required)

### Option A: start local postgres container

```bash
scripts/experiments/00_postgres_up.sh
```

Reset (clean) local postgres data and recreate:

```bash
scripts/experiments/00_postgres_up.sh clean
```

The script prints `DB_DSN` export commands. After exporting them, restart runner:

```bash
AGENT_RUNTIME=opencode bash deploy/server/cloudclawctl.sh runner restart
```

### Option B: use your own postgres instance

```bash
export DB_DRIVER=postgres
export DB_DSN='postgres://<user>:<pass>@<host>:<port>/<db>?sslmode=disable'
export CC_DB_DRIVER=postgres
export CC_DB_DSN="$DB_DSN"
AGENT_RUNTIME=opencode bash deploy/server/cloudclawctl.sh runner restart
```

Notes:

- `CC_DB_DRIVER` must be `postgres`.
- `CC_DB_DSN` (or `DB_DSN`) is required.

### Stop local postgres container

```bash
scripts/experiments/00_postgres_down.sh
# remove data volume as well:
# REMOVE_DATA=1 scripts/experiments/00_postgres_down.sh
```

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
