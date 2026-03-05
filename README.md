# cloudclaw

CloudClaw 用于把任务调度到 picoclaw 容器执行，并处理重试恢复、用户数据持久化、审计和 token 用量。

## 推荐启动顺序（最简）

1. 进入仓库目录
2. 先设置 `PICO_*` provider 环境变量
3. 执行 `up`
4. 执行 `smoke` 验证

```bash
cd cloudclaw

# 例子：OpenAI 兼容 provider
export PICO_MODEL='my-provider/my-model'
export PICO_API_BASE='https://your-provider.example.com/v1'
export PICO_API_KEY='your_api_key'

bash deploy/server/cloudclawctl.sh up
bash deploy/server/cloudclawctl.sh smoke
bash deploy/server/cloudclawctl.sh status
```

停止：

```bash
bash deploy/server/cloudclawctl.sh down
```

## Provider 配置规则（请按这个理解）

- cloudclaw 任务执行时只读容器里的 `PICO_*` 环境变量。
- 不再依赖宿主机的 `~/.picoclaw/config.json`；任务会在容器内按 `PICO_*` 动态生成执行配置。

## 常见场景

### OpenAI 兼容服务

```bash
export PICO_MODEL='my-provider/my-model'
export PICO_API_BASE='https://your-provider.example.com/v1'
export PICO_API_KEY='your_api_key'
bash deploy/server/cloudclawctl.sh up
```

### Ollama（同机）

```bash
export PICO_MODEL='ollama/llama3.1:8b'
export PICO_API_BASE='http://host.docker.internal:11434/v1'
unset PICO_API_KEY
bash deploy/server/cloudclawctl.sh up
```

说明：容器已带 `--add-host host.docker.internal:host-gateway`，可访问宿主机服务。
默认任务运行目录在容器内为 `/tmp/cloudclaw`（可用 `DOCKER_REMOTE_DIR` 覆盖），避免 `/workspace` 权限问题。

## 变更环境变量后要做什么

`PICO_*` 是在容器创建时注入的。你改了变量后，建议执行一次：

```bash
bash deploy/server/cloudclawctl.sh down
bash deploy/server/cloudclawctl.sh up
```

## 数据目录

默认数据都放在仓库下的 `./cloudclaw_data`（可用 `CC_HOME` 覆盖）。

## 核心脚本

- `deploy/server/cloudclawctl.sh`：install/up/down/status/smoke
- `deploy/server/templates/run_picoclaw_task.sh`：容器内任务执行脚本
- `deploy/server/templates/Dockerfile.runner`：runner 镜像模板
