# CloudClaw 四个实验一键顺序命令

本文件给出从环境准备到 4 个实验依次执行的完整命令。

## 0) 环境准备（只需一次）

```bash
cd ~/liujinyao/cloudclaw

# 统一运行时
export AGENT_RUNTIME=opencode

# 推荐基础运行参数（按你的机器）
export POOL_SIZE=12
export WORKSPACE_STATE_MODE=ephemeral
export WORKSPACE_MODE=mount
export CONTAINER_HARDEN=1
export CONTAINER_PIDS_LIMIT=256
export CONTAINER_NETWORK=host

# 初始化 + 启动 cloudclaw
bash deploy/server/cloudclawctl.sh init
bash deploy/server/cloudclawctl.sh up
```

## 1) 启动 PostgreSQL（推荐用于高并发实验）

```bash
cd ~/liujinyao/cloudclaw

# 启动本地 Postgres 容器（默认端口 15432）
scripts/experiments/00_postgres_up.sh

# 固定 CloudClaw 使用 Postgres
export DB_DRIVER=postgres
export DB_DSN='postgres://cloudclaw:cloudclaw@127.0.0.1:15432/cloudclaw?sslmode=disable'
export CC_DB_DRIVER=postgres
export CC_DB_DSN="$DB_DSN"

# 让 runner 生效
export AGENT_RUNTIME=opencode
bash deploy/server/cloudclawctl.sh runner restart
```

## 2) 实验一：吞吐与时延（并发 10~1000）

```bash
cd ~/liujinyao/cloudclaw
export AGENT_RUNTIME=opencode

AGENT_RUNTIME=opencode \
CC_DB_DRIVER=postgres \
CC_DB_DSN="$CC_DB_DSN" \
LEVELS=10,20,50,100,200,400,800,1000 \
TASKS_PER_USER=1 \
SUBMIT_WORKERS_MAX=128 \
POLL_INTERVAL=200ms \
DEQUEUE_LIMIT=400 \
MAX_RETRIES=0 \
TIMEOUT=45m \
VERBOSE_TASKSIM=false \
scripts/experiments/01_throughput_latency.sh
```

## 3) 实验二：故障注入（随机 kill runner/容器）

```bash
cd ~/liujinyao/cloudclaw
export AGENT_RUNTIME=opencode

AGENT_RUNTIME=opencode \
CC_DB_DRIVER=postgres \
CC_DB_DSN="$CC_DB_DSN" \
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
```

## 4) 实验三：隔离验证

```bash
cd ~/liujinyao/cloudclaw
scripts/experiments/03_isolation_validation.sh
```

## 5) 实验四：重试策略收益（priority=0 vs 1）

```bash
cd ~/liujinyao/cloudclaw
export AGENT_RUNTIME=opencode

AGENT_RUNTIME=opencode \
CC_DB_DRIVER=postgres \
CC_DB_DSN="$CC_DB_DSN" \
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
```

## 6) 常用查看命令（实验过程中）

```bash
export AGENT_RUNTIME=opencode
bash deploy/server/cloudclawctl.sh status
bash deploy/server/cloudclawctl.sh status watch 2
bash deploy/server/cloudclawctl.sh runner logs 200
```

## 7) 结束后清理（可选）

```bash
cd ~/liujinyao/cloudclaw

# 停止并删除 Postgres 容器
scripts/experiments/00_postgres_down.sh

# 若要连数据卷一起删除
# REMOVE_DATA=1 scripts/experiments/00_postgres_down.sh
```

