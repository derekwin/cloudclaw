# cloudclaw

## 快速开始（opencode）

```bash
cd cloudclaw
export AGENT_RUNTIME=opencode

# 1) 生成默认配置（来自容器内 opencode，不依赖宿主安装 opencode）
bash deploy/server/cloudclawctl.sh init

# 2) 修改默认模型/Provider 等配置
#    共享目录：./cloudclaw_data/shared/opencode
vim ./cloudclaw_data/shared/opencode/opencode.json

# 3) 启动
bash deploy/server/cloudclawctl.sh up
```

## 配置位置

- 公共共享配置（所有 opencode 容器共用）：
  - `./cloudclaw_data/shared/opencode`
  - 容器内映射为 `~/.config/opencode`
  - `init` 会优先尝试从镜像提取默认配置；若镜像未输出配置文件，则写入最小配置骨架（你手动补充 model/provider）
- 用户私有运行时数据：
  - `./cloudclaw_data/user-runtime/<user_id>/opencode/*`
  - 容器内映射为 `~/.local/share/opencode`

## 常用命令

```bash
bash deploy/server/cloudclawctl.sh status
bash deploy/server/cloudclawctl.sh runner logs 200
bash deploy/server/cloudclawctl.sh smoke

# 任务追踪（容器分配 + 事件 + 结果）
bash deploy/server/cloudclawctl.sh task trace <task_id>
bash deploy/server/cloudclawctl.sh result get <task_id>

# 下游消费结果队列
bash deploy/server/cloudclawctl.sh result dequeue 20

# 停止
bash deploy/server/cloudclawctl.sh down
```

## 安全部署建议

- 容器默认启用基础硬化：
  - `CONTAINER_HARDEN=1`（默认）启用 `no-new-privileges`、`cap-drop=ALL`、`pids-limit`
- 可选进一步收敛：
  - `CONTAINER_READONLY_ROOTFS=1` 开启只读根文件系统（自动挂载 `/tmp`、`/var/tmp` tmpfs）
- 建议固定镜像版本，不要长期使用 `latest`：
  - 例如 `BASE_IMAGE=ghcr.io/anomalyco/opencode:<固定版本>`
- 密钥建议放 `env-file`，不要写进仓库：
  - `AGENT_ENV_FILE=/abs/path/opencode.env`
- 可选放到私有网络：
  - `CONTAINER_NETWORK=<docker-network-name>`
- 目录挂载策略：
  - `./cloudclaw_data/shared/opencode` 挂载到容器 `~/.config/opencode`（只读）
  - `./cloudclaw_data/user-runtime/<user>/opencode` 映射为容器 `~/.local/share/opencode`（用户私有，可写）
