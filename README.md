# cloudclaw

## 快速开始（opencode）

```bash
cd cloudclaw
export AGENT_RUNTIME=opencode

# 1) 初始化共享配置目录（优先从宿主 ~/.config/opencode 复制；
#    如果宿主没有，则在宿主安装 opencode）
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
  - 容器内默认挂载到 `/workspace/.config/opencode`（可用 `OPENCODE_CONFIG_MOUNT_PATH` 覆盖）
  - `init` 在目录为空时优先从宿主 `~/.config/opencode` 复制；如果宿主没有，则在宿主安装 opencode
- 公共共享配置（所有 claudecode 容器共用）：
  - `./cloudclaw_data/shared/claudecode/config.json`
  - 容器内默认挂载到 `/workspace/.claudecode/config.json`
- 用户私有运行时数据：
  - opencode: `./cloudclaw_data/user-runtime/<normalized_user>-<crc32>/opencode/*`
  - claudecode: `./cloudclaw_data/user-runtime/<normalized_user>-<crc32>/claudecode/*`
  - 不直接挂载到容器；任务执行时采用 `runDir` copy-in/copy-out：
  - opencode `Prepare`：用户私有目录 -> `runDir/.opencode-home/.local/share/opencode`
  - opencode `Persist`：`runDir/.opencode-home/.local/share/opencode` -> 用户私有目录
  - claudecode `Prepare`：用户私有目录 -> `runDir/.claudecode-home`
  - claudecode `Persist`：`runDir/.claudecode-home` -> 用户私有目录

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
  - `./cloudclaw_data/shared/opencode` 挂载到容器 `OPENCODE_CONFIG_MOUNT_PATH`（默认 `/workspace/.config/opencode`，只读）
  - `./cloudclaw_data/data/runs` 挂载到容器 `WORKSPACE_MOUNT_PATH`（默认 `/workspace/cloudclaw/runs`）
  - 用户私有目录 `./cloudclaw_data/user-runtime/*` 仅由 runner 在宿主机侧做 copy-in/copy-out

## 关键默认值

- `AGENT_RUNTIME` 必填（`opencode` 或 `claudecode`）
- `CC_HOME` 默认 `./cloudclaw_data`
- `up` 实际执行：`install` +（配置缺失时）`init` + `pool start` + `runner start`
- 运行模式默认（opencode + claudecode）：
  - `WORKSPACE_STATE_MODE=ephemeral`
  - `WORKSPACE_MODE=mount`
