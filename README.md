# cloudclaw

CloudClaw 用于把任务调度到 picoclaw 容器执行，并处理重试恢复、用户数据回写、审计和 token 用量。

## 一键启动（服务器）

```bash
cd /Users/jacelau/code/opencode/cloudclaw
bash deploy/server/cloudclawctl.sh up
bash deploy/server/cloudclawctl.sh smoke
bash deploy/server/cloudclawctl.sh status
```

停止：

```bash
bash deploy/server/cloudclawctl.sh down
```

## Provider 配置（不依赖 OpenRouter）

脚本统一用 `PICO_*` 变量配置模型提供方。

### 1) OpenAI 兼容自定义 provider

```bash
export PICO_MODEL='my-provider/my-model'
export PICO_API_BASE='https://your-provider.example.com/v1'
export PICO_API_KEY='your_api_key'

bash deploy/server/cloudclawctl.sh up
```

### 2) Ollama（同机）

```bash
export PICO_MODEL='ollama/llama3.1:8b'
export PICO_API_BASE='http://host.docker.internal:11434/v1'
unset PICO_API_KEY

bash deploy/server/cloudclawctl.sh up
```

说明：`cloudclawctl` 启动容器时已加 `--add-host host.docker.internal:host-gateway`，容器可访问宿主机 Ollama。

## 核心脚本

- `deploy/server/cloudclawctl.sh`：install/up/down/status/smoke
- `deploy/server/templates/run_picoclaw_task.sh`：容器内任务执行脚本
- `deploy/server/templates/Dockerfile.runner`：runner 镜像模板
