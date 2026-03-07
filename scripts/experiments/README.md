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

# 测试

统一基线（先执行）

cd cloudclaw

export AGENT_RUNTIME=opencode
export POOL_SIZE=12
export WORKSPACE_STATE_MODE=ephemeral
export WORKSPACE_MODE=mount
export CONTAINER_HARDEN=1
export CONTAINER_PIDS_LIMIT=256
export CONTAINER_NETWORK=host

bash deploy/server/cloudclawctl.sh init
bash deploy/server/cloudclawctl.sh up
bash deploy/server/cloudclawctl.sh restart

1) 吞吐与时延（6.2-1）

AGENT_RUNTIME=opencode \
LEVELS=10,20,50,100,200,400,800,1000 \
TASKS_PER_USER=1 \
SUBMIT_WORKERS_MAX=128 \
POLL_INTERVAL=200ms \
DEQUEUE_LIMIT=400 \
MAX_RETRIES=0 \
TIMEOUT=45m \
VERBOSE_TASKSIM=false \
scripts/experiments/01_throughput_latency.sh

2) 故障注入（6.2-2）

AGENT_RUNTIME=opencode \
CONCURRENCY_USERS=200 \
TASKS_PER_USER=5 \
SUBMIT_WORKERS=96 \
POLL_INTERVAL=200ms \
DEQUEUE_LIMIT=400 \
MAX_RETRIES=4 \
TIMEOUT=60m \
INJECT_INTERVAL_SEC=8 \
RUNNER_KILL_RATIO=20 \
RESTART_RUNNER_AFTER_KILL=1 \
VERBOSE_TASKSIM=false \
scripts/experiments/02_fault_injection.sh
3) 隔离验证（6.2-3）

scripts/experiments/03_isolation_validation.sh
建议连跑 3 次取一致性。

4) 重试优先级收益（6.2-4）

AGENT_RUNTIME=opencode \
ROUNDS=5 \
CONCURRENCY_USERS=200 \
TASKS_PER_USER=5 \
SUBMIT_WORKERS=96 \
POLL_INTERVAL=200ms \
DEQUEUE_LIMIT=400 \
MAX_RETRIES=4 \
TIMEOUT=60m \
INJECT_INTERVAL_SEC=8 \
RUNNER_KILL_RATIO=20 \
RESTART_RUNNER_AFTER_KILL=1 \
POOL_LABEL=app=opencode-agent \
scripts/experiments/04_retry_priority_gain.sh