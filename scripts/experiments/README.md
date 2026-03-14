# CloudClaw Paper Experiments

This directory contains the scripts used for the three short-paper experiments:

1. Throughput / latency
2. Fault recovery
3. Isolation / correctness

Artifacts are written to `./experiment_artifacts` by default.

## What The Scripts Do Automatically

Before each run, the experiment scripts will:

- run `cloudclawctl init`
- reset the CloudClaw tables in the database pointed to by `DB_DSN`
- clear local run state under the current `CC_HOME`
- stop stray `cloudclaw run` processes using the same `DB_DSN`
- restart the pool and runner

By default, the scripts skip the preflight smoke task and go straight to the real workload.
You can override the automatic steps if needed:

```bash
export CC_EXP_AUTO_INIT_RUNTIME=0
export CC_EXP_AUTO_RESET_DB=0
export CC_EXP_AUTO_CLEAN_STATE=0
export CC_EXP_SMOKE_BEFORE_RUN=1
export CC_EXP_FORCE_KILL_STRAY_RUNNERS=0
```

## Shared Config And Shared Skills

CloudClaw now treats runtime config and shared skills as two different things:

- runtime shared config:
  `CC_HOME/shared/opencode`
- shared skills:
  `CC_HOME/shared/skills`

For Docker experiments, shared skills are mounted into every container once, instead of being copied in and out for each task.

If you use shared prompts or workspace-level agent files, place them under:

```text
CC_HOME/shared/skills/workspace/
```

For example:

```text
CC_HOME/shared/skills/workspace/AGENT.md
CC_HOME/shared/skills/workspace/IDENTITY.md
CC_HOME/shared/skills/workspace/SOUL.md
```

## Recommended Paper Setup

Use a dedicated experiment home and a dedicated experiment database.

```bash
export AGENT_RUNTIME=opencode
export DB_DSN='postgres://cloudclaw:cloudclaw@127.0.0.1:15432/cloudclaw?sslmode=disable'
export CC_HOME=/home/yourname/cloudclaw_exp
```

Recommended stable prompt for paper experiments:

```bash
export CC_EXP_INPUT_PREFIX="Without using any tools, shell commands, file reads, file writes, or network access, reply with exactly one line: CLOUDCLAW_OK"
```

This prompt matters. It keeps `opencode run` in a simple one-shot QA path and avoids long-running agent loops.

The throughput and fault scripts submit this prompt as raw task input, so `tasksim` will not append `worker=... user=... idx=...`.

## Step 1: Throughput / Latency

Recommended paper setting:

```bash
export AGENT_RUNTIME=mock // Use a mock runtime for exp1 to prevent LLM API rate limiting from affecting high concurrency.
export CC_EXP_POOL_SIZES="1 2 4"
export CC_EXP_USERS="sim_u1,sim_u2,sim_u3,sim_u4"
export CC_EXP_TASKS_PER_USER_LIST="5 10 20 40"
export CC_EXP_WORKSPACE_MODES="mount copy"
export CC_EXP_WORKSPACE_STATE_MODES="ephemeral"
export CC_EXP_REPEAT=1

bash scripts/experiments/01_throughput_latency.sh
```

This produces the main scalability data for:

- throughput
- p95 / p99 latency
- queue latency
- run latency

## Step 2: Fault Recovery

Use a plain-text prompt here as well. The default is already a controlled non-tool workload.

Recommended paper setting:

```bash
export CC_EXP_FAULT_MODES="runner container"
export CC_EXP_RETRY_PRIORITIES="0 1"
export CC_EXP_TASKS_PER_USER=12
export CC_EXP_REPEAT=3
export CC_EXP_FAULT_DELAY_SECONDS=10
export CC_EXP_FAULT_DOWN_SECONDS=8
export CC_EXP_FAULT_COUNT=1

bash scripts/experiments/02_fault_recovery.sh
```

This produces the recovery data for:

- recovered task rate
- recovered task success rate
- recovery latency

## Step 3: Isolation / Correctness

This script checks correctness directly and writes a paper-friendly table.

```bash
bash scripts/experiments/03_isolation_validation.sh
```

## Step 4: Plot Figures

After the three experiments finish:

```bash
python3 scripts/experiments/plot_results.py \
  --artifacts-root ./experiment_artifacts \
  --output-dir ./experiment_artifacts/plots
```

The plotting script generates:

- SVG figures
- CSV summaries
- `REPORT.md`

It does not require `matplotlib`.

## One Copy-Paste Workflow

If you want a single paper-style workflow on the server, use:

```bash
export AGENT_RUNTIME=opencode
export DB_DSN='postgres://cloudclaw:cloudclaw@127.0.0.1:15432/cloudclaw?sslmode=disable'
export CC_HOME=/home/yourname/cloudclaw_exp

export CC_EXP_INPUT_PREFIX="Without using any tools, shell commands, file reads, file writes, or network access, reply with exactly one line: CLOUDCLAW_OK"

export CC_EXP_POOL_SIZES="1 2 4"
export CC_EXP_USERS="sim_u1,sim_u2,sim_u3,sim_u4"
export CC_EXP_TASKS_PER_USER_LIST="5 10 20 40"
export CC_EXP_WORKSPACE_MODES="mount copy"
export CC_EXP_WORKSPACE_STATE_MODES="ephemeral"
export CC_EXP_REPEAT=3
bash scripts/experiments/01_throughput_latency.sh

export CC_EXP_FAULT_MODES="runner container"
export CC_EXP_RETRY_PRIORITIES="0 1"
export CC_EXP_TASKS_PER_USER=12
export CC_EXP_FAULT_DELAY_SECONDS=10
export CC_EXP_FAULT_DOWN_SECONDS=8
export CC_EXP_FAULT_COUNT=1
bash scripts/experiments/02_fault_recovery.sh

bash scripts/experiments/03_isolation_validation.sh

python3 scripts/experiments/plot_results.py \
  --artifacts-root ./experiment_artifacts \
  --output-dir ./experiment_artifacts/plots
```

## Notes

- If a smoke check fails, fix that first. Do not trust later benchmark data.
- If you want clean paper results, use a dedicated `DB_DSN` and a dedicated `CC_HOME`.
- If you only want to verify the setup first, reduce to:
  `CC_EXP_POOL_SIZES="1"`, `CC_EXP_TASKS_PER_USER_LIST="1"`, `CC_EXP_REPEAT=1`
- Main scripts:
  `scripts/experiments/01_throughput_latency.sh`
  `scripts/experiments/02_fault_recovery.sh`
  `scripts/experiments/03_isolation_validation.sh`
