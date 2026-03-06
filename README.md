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
  - `init` 在目录为空时优先从宿主 `~/.config/opencode` 复制；复制不到再尝试镜像提取；最后兜底最小配置骨架
- 用户私有运行时数据：
  - `./cloudclaw_data/user-runtime/<normalized_user>/opencode/*`
  - 不直接挂载到容器；任务执行时采用 `runDir` copy-in/copy-out：
  - `Prepare`：用户私有目录 -> `runDir/.opencode-home/.local/share/opencode`
  - `Persist`：`runDir/.opencode-home/.local/share/opencode` -> 用户私有目录

## 常用命令

```bash
# 下面命令默认都要求先设置 runtime
export AGENT_RUNTIME=opencode

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

## 运行自检（确认容器内 opencode 正常执行）

```bash
export AGENT_RUNTIME=opencode

# 1) 提交任务并等待完成
bash deploy/server/cloudclawctl.sh smoke

# 2) 查看任务状态/容器分配/结果
bash deploy/server/cloudclawctl.sh task trace <task_id>
bash deploy/server/cloudclawctl.sh result get <task_id>
```

判定方式：
- `task status` 里 `status=SUCCEEDED` 且有 `container_id`：说明任务已在某个容器执行完成
- `result.output` 非空：可看到 opencode 的执行输出
- 若输出包含 “Unable to connect” 之类报错：说明容器内 opencode 已运行，但模型 provider 网络/鉴权不可达

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
  - `./cloudclaw_data/shared/opencode` 挂载到容器 `~/.config/opencode`（只读）
  - `./cloudclaw_data/data/runs` 挂载到容器 `WORKSPACE_MOUNT_PATH`（默认 `/workspace/cloudclaw/runs`）
  - 用户私有目录 `./cloudclaw_data/user-runtime/*` 仅由 runner 在宿主机侧做 copy-in/copy-out
