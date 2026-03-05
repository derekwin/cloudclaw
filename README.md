# cloudclaw

`cloudclaw` 是一个基于 picoclaw 的任务调度程序（Go，单进程），提供 CLI + Go SDK。

V1 当前重点：
- 任务队列（FCFS）
- 失败任务优先重试（插队）
- 容器/执行异常自动恢复（lease + heartbeat）
- 审计日志
- 任务结束 token usage 记录

## 执行模式

`cloudclaw run` 支持 4 种执行器：
- `mock`：本地模拟执行
- `cmd`：本地 shell 命令执行
- `k8s-picoclaw`：K8s 中选择已有 picoclaw Pod 执行任务
- `docker-picoclaw`：单机 Docker 中选择已有 picoclaw 容器执行任务

## K8s Picoclaw 模式

### 1) 部署 picoclaw 容器池

```bash
kubectl apply -f deploy/k8s/picoclaw-agent-pool.yaml
```

默认通过 `app=picoclaw-agent` 标签发现 Running Pod。

### 2) 运行 cloudclaw（k8s 执行器）

```bash
go run ./cmd/cloudclaw run \
  --data-dir ./data \
  --executor k8s-picoclaw \
  --k8s-namespace default \
  --k8s-label-selector app=picoclaw-agent \
  --k8s-task-cmd 'picoclaw agent -m "$CLOUDCLAW_INPUT" > {{USERDATA_DIR}}/result.txt; printf "{\"prompt_tokens\":10,\"completion_tokens\":20}\n" > {{USAGE_FILE}}'
```

说明：
- `--k8s-task-cmd` 在目标 Pod 内执行，必须由你按 picoclaw 镜像实际命令填写。
- 可用模板变量：
  - `{{TASK_DIR}}`
  - `{{TASK_FILE}}`
  - `{{USERDATA_DIR}}`
  - `{{USAGE_FILE}}`
- 自动注入环境变量：
  - `CLOUDCLAW_TASK_ID`
  - `CLOUDCLAW_USER_ID`
  - `CLOUDCLAW_TASK_TYPE`
  - `CLOUDCLAW_INPUT`
  - `CLOUDCLAW_TASK_FILE`
  - `CLOUDCLAW_WORKSPACE`
  - `CLOUDCLAW_USAGE_FILE`

### 3) 提交任务

```bash
go run ./cmd/cloudclaw task submit \
  --data-dir ./data \
  --user-id u1 \
  --task-type search \
  --input "搜索内容" \
  --max-retries 2
```

### 4) 查看状态

```bash
go run ./cmd/cloudclaw task status --data-dir ./data --task-id <TASK_ID>
go run ./cmd/cloudclaw queue-length --data-dir ./data
go run ./cmd/cloudclaw container-status --data-dir ./data
go run ./cmd/cloudclaw audit --data-dir ./data --task-id <TASK_ID>
```

## Docker Picoclaw 模式（单机）

### 1) 批量启动容器池

```bash
bash deploy/docker/start-picoclaw-pool.sh 5 ghcr.io/sipeed/picoclaw:latest
```

默认启动 5 个容器，标签 `app=picoclaw-agent`。

### 2) 运行 cloudclaw（docker 执行器）

```bash
go run ./cmd/cloudclaw run \
  --data-dir ./data \
  --executor docker-picoclaw \
  --docker-label-selector app=picoclaw-agent \
  --docker-task-cmd 'picoclaw agent -m "$CLOUDCLAW_INPUT" > {{USERDATA_DIR}}/result.txt; printf "{\"prompt_tokens\":10,\"completion_tokens\":20}\n" > {{USAGE_FILE}}'
```

说明：
- `--docker-task-cmd` 在目标容器内执行，必须按 picoclaw 镜像实际命令填写。
- 支持的模板变量与 K8s 模式一致：`{{TASK_DIR}} {{TASK_FILE}} {{USERDATA_DIR}} {{USAGE_FILE}}`。
- 注入环境变量与 K8s 模式一致：`CLOUDCLAW_TASK_ID`、`CLOUDCLAW_USER_ID`、`CLOUDCLAW_INPUT` 等。

### 3) 停止容器池

```bash
bash deploy/docker/stop-picoclaw-pool.sh app=picoclaw-agent
```

## 执行协议（k8s/docker）

每次任务执行时：
1. 从标签匹配的 Running Pod/Container 列表中选一个空闲执行容器。  
2. 将用户工作目录与任务 payload (`task.json`) 下发到该容器。  
3. 在容器中执行 `--k8s-task-cmd` 或 `--docker-task-cmd`。  
4. 拉回用户工作目录与 `usage.json`。  
5. 成功则写入 `SUCCEEDED` 和 token usage；失败则重试并插队。

## 调度与恢复规则

状态机：`QUEUED -> RUNNING -> SUCCEEDED | FAILED | CANCELED`

补充：
- 失败可重试：回到 `QUEUED`，优先级提升（置顶）
- lease 过期：自动回到 `QUEUED` 并优先重试
- 取消：支持排队中取消和运行中取消

## 数据目录

默认 `./data`：
- `state.json`：任务、容器、快照元数据
- `task_events.jsonl`：审计日志
- `users/<user_id>/data`：用户持久目录
- `runs/<task_id>-attempt-<n>`：单次执行目录
- `snapshots/<user_id>/<snapshot_id>`：执行后快照

## Go SDK

```go
package main

import (
    "fmt"

    "cloudclaw/pkg/cloudclaw"
)

func main() {
    client, _ := cloudclaw.NewClient(cloudclaw.Config{DataDir: "./data"})
    task, _ := client.SubmitTask(cloudclaw.SubmitTaskRequest{
        UserID: "u1", TaskType: "search", Input: "hello", MaxRetries: 2,
    })
    t, _ := client.GetTask(task.ID)
    fmt.Println(t.Status)
}
```

## 开发测试

```bash
go test ./...
```

## 参考

- [sipeed/picoclaw](https://github.com/sipeed/picoclaw)
