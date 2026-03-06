# cloudclaw

## 快速开始（opencode）

```bash
cd cloudclaw
export AGENT_RUNTIME=opencode

# 1) 生成默认配置（来自容器内 opencode，不依赖宿主安装 opencode）
bash deploy/server/cloudclawctl.sh init

# 2) 修改默认模型/Provider 等配置
vim ./cloudclaw_data/opencode/config/opencode.json

# 3) 启动
bash deploy/server/cloudclawctl.sh up
```

## 配置位置

- 公共共享配置（所有 opencode 容器共用）：
  - `./cloudclaw_data/opencode/config/opencode.json`
  - `init` 会优先尝试从镜像提取默认配置；若镜像未输出配置文件，则写入最小配置骨架（你手动补充 model/provider）
- 用户私有运行时数据：
  - `./cloudclaw_data/user-runtime/<user_id>/...`

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
