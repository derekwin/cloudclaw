# cloudclaw

CloudClaw 用于把任务调度到 picoclaw 容器执行，并处理重试恢复、用户数据持久化、审计和 token 用量。

## 推荐启动顺序（最简）

1. 进入仓库目录
2. 准备 picoclaw 配置（可先用默认，再按需导入完整配置）
3. 执行 `up`
4. 执行 `smoke` 验证

```bash
cd cloudclaw

bash deploy/server/cloudclawctl.sh up
bash deploy/server/cloudclawctl.sh smoke
bash deploy/server/cloudclawctl.sh status
```

停止：

```bash
bash deploy/server/cloudclawctl.sh down
```

## `cloudclawctl` 交互（分组命令）

```bash
bash deploy/server/cloudclawctl.sh help
```

常用分组：

- `home set <path>` / `home show`
- `config path|show|edit|import <file>|reset|help`
- `pool start|stop|restart|status`
- `runner start|stop|restart|status|logs [lines]`

快捷命令（兼容旧习惯）：

- `install`, `up`, `down`, `restart`, `status`, `smoke`

兼容旧别名（已保留）：

- `set-home`, `config-path`, `show-config`, `config-help`
- `start-pool`, `stop-pool`, `start`, `stop`

## 配置规则（当前实现）

- CloudClaw 使用共享配置文件作为真源：`$CC_HOME/shared/picoclaw/config.json`
- 容器内挂载路径默认：`/workspace/.picoclaw/config.json`
- 任务执行脚本读取该配置（支持完整 picoclaw 配置：`model_list/providers/tools/mcp/skills/channels/...`）
- `start-pool` 会重建容器以避免旧 env/旧挂载漂移

配置工作流建议：

```bash
# 查看/编辑当前共享配置
bash deploy/server/cloudclawctl.sh config path
bash deploy/server/cloudclawctl.sh config edit

# 或导入你已有的完整配置
bash deploy/server/cloudclawctl.sh config import /abs/path/picoclaw-config.json

# 配置更新后重建池
bash deploy/server/cloudclawctl.sh pool restart
```

说明：

- 如果共享配置已存在，`start-pool` 默认复用，不会覆盖
- `PICOCLAW_CONFIG_SOURCE` 可在启动时一次性导入配置
- `PICOCLAW_CONFIG_RESET=1` 可强制重置为托底模板

## 仅模型快速引导（可选）

如果你暂时不维护完整配置文件，也可以用 `PICO_*` 生成托底模型配置：

```bash
export PICO_MODEL='my-provider/my-model'
export PICO_API_BASE='https://your-provider.example.com/v1'
export PICO_API_KEY='your_api_key'
PICOCLAW_CONFIG_RESET=1 bash deploy/server/cloudclawctl.sh pool start
```

> 上述方式只保证模型最小可用。若需要 `mcp/tools/skills` 等完整配置，请使用 `config edit/import`。

## 数据目录

默认数据都放在仓库下的 `./cloudclaw_data`（可用 `CC_HOME` 覆盖）。

## 核心脚本

- `deploy/server/cloudclawctl.sh`：分组管理入口（home/config/pool/runner + shortcuts）
- `deploy/server/templates/run_picoclaw_task.sh`：容器内任务执行脚本
- `deploy/server/templates/Dockerfile.runner`：runner 镜像模板
