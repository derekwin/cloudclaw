# cloudclaw

## 快速开始（opencode）

```bash
cd cloudclaw
export AGENT_RUNTIME=opencode
export CONTAINER_NETWORK=host # 如果使用宿主本地ollama

# 1) 初始化共享配置目录（优先从宿主 ~/.config/opencode 复制；
#    如果宿主没有，则在宿主安装 opencode）
bash deploy/server/cloudclawctl.sh init

# 2) 修改默认模型/Provider 等配置
#    共享目录：./cloudclaw_data/shared/opencode
vim ./cloudclaw_data/shared/opencode/opencode.json

# 3) 启动
bash deploy/server/cloudclawctl.sh up
```

## 数据库配置（PostgreSQL）

- CloudClaw 仅支持 PostgreSQL。
- 通过环境变量 `DB_DRIVER=postgres`、`DB_DSN=<dsn>` 配置数据库连接。

### PostgreSQL（推荐用于高并发实验）

1) 启动本地 PostgreSQL 容器（已提供脚本）

```bash
scripts/experiments/00_postgres_up.sh
```

2) 按脚本输出设置环境变量（示例）

```bash
export DB_DRIVER=postgres
export DB_DSN='postgres://cloudclaw:cloudclaw@127.0.0.1:15432/cloudclaw?sslmode=disable'
export CC_DB_DRIVER=postgres
export CC_DB_DSN="$DB_DSN"
```

3) 重启 runner 生效

```bash
export AGENT_RUNTIME=opencode
bash deploy/server/cloudclawctl.sh runner restart
```

## 配置位置

- 公共共享配置（所有 opencode 容器共用）：
  - `./cloudclaw_data/shared/opencode`
  - 容器内默认挂载到 `/workspace/.config/opencode`（可用 `OPENCODE_CONFIG_MOUNT_PATH` 覆盖）
  - `init` 在目录为空时优先从宿主 `~/.config/opencode` 复制；如果宿主没有，则在宿主安装 opencode
- 公共共享配置（所有 openclaw 容器共用）：
  - `./cloudclaw_data/shared/openclaw`
  - 容器内默认挂载到 `/workspace/.config/openclaw`（可用 `OPENCLAW_CONFIG_MOUNT_PATH` 覆盖）
  - `init` 优先从宿主 `~/.config/openclaw` 复制；
  - 若宿主未安装 openclaw，会执行：`curl -fsSL https://openclaw.ai/install.sh | bash -s -- --install-method git`
  - 若 `~/.config/openclaw` 仍为空，会提示先在宿主完成 openclaw 配置，再重试 `init`
- 公共共享配置（所有 claudecode 容器共用）：
  - `./cloudclaw_data/shared/claudecode/config.json`
  - 容器内默认挂载到 `/workspace/.claudecode/config.json`
  - `init` 优先从宿主 `~/.claudecode/config.json` 复制；
  - 若宿主没有，则尝试读取宿主 Claude Code 官方配置 `~/.claude/settings.json`；
  - 若仍没有，则在宿主安装 Claude Code（`curl -fsSL https://claude.ai/install.sh | bash`）后再初始化
- 用户私有运行时数据：
  - opencode: `./cloudclaw_data/user-runtime/<normalized_user>-<crc32>/opencode/*`
  - openclaw: `./cloudclaw_data/user-runtime/<normalized_user>-<crc32>/openclaw/*`
  - claudecode: `./cloudclaw_data/user-runtime/<normalized_user>-<crc32>/claudecode/*`
  - 不直接挂载到容器；任务执行时采用 `runDir` copy-in/copy-out：
  - opencode `Prepare`：用户私有目录 -> `runDir/.opencode-home/.local/share/opencode`
  - opencode `Persist`：`runDir/.opencode-home/.local/share/opencode` -> 用户私有目录
  - openclaw `Prepare`：用户私有目录 -> `runDir/.openclaw-home/.local/share/openclaw`
  - openclaw `Persist`：`runDir/.openclaw-home/.local/share/openclaw` -> 用户私有目录
  - claudecode `Prepare`：用户私有目录 -> `runDir/.claudecode-home`
  - claudecode `Persist`：`runDir/.claudecode-home` -> 用户私有目录

## 常用命令

```bash
# 下面命令默认都要求先设置 runtime
export AGENT_RUNTIME=openclaw

bash deploy/server/cloudclawctl.sh status
bash deploy/server/cloudclawctl.sh status watch 2
bash deploy/server/cloudclawctl.sh runner logs 200
bash deploy/server/cloudclawctl.sh smoke

# 任务追踪（容器分配 + 事件 + 结果）
bash deploy/server/cloudclawctl.sh task trace <task_id>
bash deploy/server/cloudclawctl.sh task summary 10
bash deploy/server/cloudclawctl.sh result get <task_id>

# 下游消费结果队列
bash deploy/server/cloudclawctl.sh result dequeue 20

# 停止
bash deploy/server/cloudclawctl.sh down
```

## 运行自检（确认容器内 runtime 正常执行）

```bash
export AGENT_RUNTIME=openclaw

# 1) 提交任务并等待完成
bash deploy/server/cloudclawctl.sh smoke

# 2) 查看任务状态/容器分配/结果
bash deploy/server/cloudclawctl.sh task trace <task_id>
bash deploy/server/cloudclawctl.sh result get <task_id>
```

判定方式：
- `task status` 里 `status=SUCCEEDED` 且有 `container_id`：说明任务已在某个容器执行完成
- `result.output` 非空：可看到 runtime 的执行输出
- 若输出包含 “Unable to connect” 之类报错：说明容器内 runtime 已运行，但模型 provider 网络/鉴权不可达

## 安全部署建议

- 容器默认启用基础硬化：
  - `CONTAINER_HARDEN=1`（默认）启用 `no-new-privileges`、`cap-drop=ALL`、`pids-limit`
- 可选进一步收敛：
  - `CONTAINER_READONLY_ROOTFS=1` 开启只读根文件系统（自动挂载 `/tmp`、`/var/tmp` tmpfs）
- 密钥建议放 `env-file`，不要写进仓库：
  - `AGENT_ENV_FILE=/abs/path/opencode.env`
- 可选放到私有网络：
  - `CONTAINER_NETWORK=<docker-network-name>`
- 目录挂载策略：
  - `./cloudclaw_data/shared/opencode` 挂载到容器 `OPENCODE_CONFIG_MOUNT_PATH`（默认 `/workspace/.config/opencode`，只读）
  - `./cloudclaw_data/shared/openclaw` 挂载到容器 `OPENCLAW_CONFIG_MOUNT_PATH`（默认 `/workspace/.config/openclaw`，只读）
  - `./cloudclaw_data/data/runs` 挂载到容器 `WORKSPACE_MOUNT_PATH`（默认 `/workspace/cloudclaw/runs`）
  - 用户私有目录 `./cloudclaw_data/user-runtime/*` 仅由 runner 在宿主机侧做 copy-in/copy-out

## 关键默认值

- `AGENT_RUNTIME` 必填（`opencode` / `openclaw` / `claudecode`）
- `CC_HOME` 默认 `./cloudclaw_data`
- `DB_DRIVER` 默认 `postgres`
- `DB_DSN` 必填（示例：`postgres://cloudclaw:cloudclaw@127.0.0.1:15432/cloudclaw?sslmode=disable`）
- `up` 实际执行：`install` +（配置缺失时）`init` + `pool start` + `runner start`
- 运行模式默认（opencode + openclaw + claudecode）：
  - `WORKSPACE_STATE_MODE=ephemeral`
  - `WORKSPACE_MODE=mount`

## 批量任务测试

```
go run ./cmd/tasksim \
  --data-dir ./cloudclaw_data/data \
  --db-driver postgres \
  --db-dsn 'postgres://cloudclaw:cloudclaw@127.0.0.1:15432/cloudclaw?sslmode=disable' \
  --users sim_u1,sim_u2,sim_u3 \
  --tasks-per-user 5 \
  --submit-workers 4 \
  --poll-interval 1s \
  --dequeue-limit 20
```

### 结构化压测输出（新增）

`cmd/tasksim` 支持直接输出实验汇总（JSON/CSV）：

```bash
go run ./cmd/tasksim \
  --data-dir ./cloudclaw_data/data \
  --db-driver postgres \
  --db-dsn 'postgres://cloudclaw:cloudclaw@127.0.0.1:15432/cloudclaw?sslmode=disable' \
  --users sim_u1,sim_u2,sim_u3 \
  --tasks-per-user 5 \
  --submit-workers 4 \
  --poll-interval 200ms \
  --dequeue-limit 20 \
  --task-type exp_demo_001 \
  --summary-file ./experiment_artifacts/demo/summary.json \
  --append-csv ./experiment_artifacts/demo/summary.csv \
  --fetch-final-task=true \
  --collect-events=true \
  --verbose=false
```

### 论文实验脚本（6.2）

详见 `scripts/experiments/README.md`，包含：

1. `01_throughput_latency.sh`：固定池大小，负载从 10 到 1000。
2. `02_fault_injection.sh`：随机 kill runner/容器并统计恢复。
3. `03_isolation_validation.sh`：跨用户同名文件/恶意路径/超限文件验证。
4. `04_retry_priority_gain.sh`：对比 `RETRY_PRIORITY=0` 与 `RETRY_PRIORITY=1` 的尾延迟。
