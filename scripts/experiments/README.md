# Experiment Scripts

These scripts package the three short-paper experiments discussed for CloudClaw:

1. Throughput and latency sweep
2. Fault recovery under runner/container failures
3. Deterministic isolation and correctness validation

All scripts assume you already have:

- a reachable PostgreSQL instance
- `DB_DSN` exported
- the runtime pool image/config prepared for your target runtime
- `AGENT_RUNTIME` exported when the experiment needs the live runner (`opencode`, `openclaw`, or `claudecode`)

By default, every experiment run now performs a fresh setup step:

- initialize runtime config via `cloudclawctl init`
- reset the CloudClaw tables in the database pointed to by `DB_DSN`
- clear local `data/runs` and `user-runtime` state under the current `CC_HOME`
- run `cloudclawctl smoke` before the actual workload and store its output in the artifact directory

You can disable these with:

```bash
export CC_EXP_AUTO_INIT_RUNTIME=0
export CC_EXP_AUTO_RESET_DB=0
export CC_EXP_AUTO_CLEAN_STATE=0
export CC_EXP_SMOKE_BEFORE_RUN=0
```

## Layout

- `common.sh`: shared helpers for build/restart/artifact handling
- `01_throughput_latency.sh`: sweep workload size and runtime configuration
- `02_fault_recovery.sh`: inject runner/container faults while a workload is running
- `03_isolation_validation.sh`: run deterministic correctness checks and emit a paper table
- `plot_results.py`: aggregate artifacts into figures and summary tables

Artifacts are written under `./experiment_artifacts` by default.

## 1. Throughput / Latency

Minimal example:

```bash
export AGENT_RUNTIME=opencode
export DB_DSN='postgres://cloudclaw:cloudclaw@127.0.0.1:15432/cloudclaw?sslmode=disable'

# throughput / latency
export CC_EXP_POOL_SIZES="1 2 4"
export CC_EXP_USERS="sim_u1,sim_u2,sim_u3,sim_u4"
export CC_EXP_TASKS_PER_USER_LIST="5 10 20 40"
export CC_EXP_WORKSPACE_MODES="mount copy"
export CC_EXP_REPEAT=3

bash scripts/experiments/01_throughput_latency.sh
```

## 2. Fault Recovery

Minimal example:

```bash
export AGENT_RUNTIME=opencode
export DB_DSN='postgres://cloudclaw:cloudclaw@127.0.0.1:15432/cloudclaw?sslmode=disable'

export CC_EXP_FAULT_MODES="runner container"
export CC_EXP_RETRY_PRIORITIES="0 1"
export CC_EXP_TASKS_PER_USER=12
export CC_EXP_REPEAT=3
bash scripts/experiments/02_fault_recovery.sh

bash scripts/experiments/02_fault_recovery.sh
```

## 3. Isolation / Correctness

This script does not require the runner to be live; it directly checks store/workspace logic and writes a correctness table.

```bash
export DB_DSN='postgres://cloudclaw:cloudclaw@127.0.0.1:15432/cloudclaw?sslmode=disable'
export AGENT_RUNTIME=opencode

bash scripts/experiments/03_isolation_validation.sh
```

## Plotting / Aggregation

The plotting script scans `meta.json + summary.json` pairs and generates paper-ready figures.

```bash
python3 scripts/experiments/plot_results.py \
  --artifacts-root ./experiment_artifacts \
  --output-dir ./experiment_artifacts/plots
```

If you only want one figure family:

```bash
python3 scripts/experiments/plot_results.py --experiment throughput
python3 scripts/experiments/plot_results.py --experiment fault
python3 scripts/experiments/plot_results.py --experiment isolation
```

`plot_results.py` uses `matplotlib` for PNG figures. If your server environment does not have it, install it first or run the plotting step locally after copying the artifact directory.
