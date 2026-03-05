# cloudclaw

`cloudclaw` 是一个基于 picoclaw 的任务管理内核（Go 实现，单程序），提供：

- 任务队列与调度（FCFS，失败任务优先重试）
- 容器执行（本地命令 / K8s Pod / Docker 容器池）
- 可恢复执行（lease + heartbeat + 自动重排）
- 用户数据持久化与审计
- CLI + Go SDK + Python SDK

## 存储

默认使用 **SQLite**（本地文件）。

- `--db-driver sqlite`（默认）
- `--db-driver postgres --db-dsn '<postgres dsn>'`（可切换）

SQLite 默认数据库路径：`<data-dir>/cloudclaw.db`。

## 任务状态

`QUEUED -> RUNNING -> SUCCEEDED | FAILED | CANCELED`

补充规则：
- 失败且可重试：回 `QUEUED`，并提升优先级到队首。
- lease 过期：回 `QUEUED`，并提升优先级到队首。

## 执行器模式

- `mock`: 本地模拟执行
- `cmd`: 本地 shell 命令执行
- `k8s-picoclaw`: 从 K8s 中选已有 Running Pod 执行
- `docker-picoclaw`: 从 Docker 中选已有容器执行（或由 cloudclaw 自动维护预备池）

## 快速开始（SQLite）

### 1) 启动 runner

```bash
go run ./cmd/cloudclaw run \
  --data-dir ./data \
  --db-driver sqlite \
  --executor mock
```

### 2) 提交任务

```bash
go run ./cmd/cloudclaw task submit \
  --data-dir ./data \
  --db-driver sqlite \
  --user-id u1 \
  --task-type search \
  --input "搜索内容" \
  --max-retries 2
```

### 3) 查看状态

```bash
go run ./cmd/cloudclaw task status --data-dir ./data --db-driver sqlite --task-id <TASK_ID>
go run ./cmd/cloudclaw queue-length --data-dir ./data --db-driver sqlite
go run ./cmd/cloudclaw container-status --data-dir ./data --db-driver sqlite
go run ./cmd/cloudclaw audit --data-dir ./data --db-driver sqlite --task-id <TASK_ID>
```

## 共享技能目录

可选参数：`--shared-skills-dir /path/to/skills`

runner 会把该目录注入到每个任务工作目录下：
- `.cloudclaw_shared_skills`

并暴露环境变量给执行容器：
- `CLOUDCLAW_SHARED_SKILLS_DIR`

该目录不会回写到用户私有数据。

## Docker 容器池（单机）

### 手动批量启动

```bash
bash deploy/docker/start-picoclaw-pool.sh 5 ghcr.io/sipeed/picoclaw:latest
```

### 由 cloudclaw 自动维护预备池

```bash
go run ./cmd/cloudclaw run \
  --data-dir ./data \
  --db-driver sqlite \
  --executor docker-picoclaw \
  --docker-manage-pool \
  --docker-pool-size 5 \
  --docker-image ghcr.io/sipeed/picoclaw:latest \
  --docker-name-prefix picoclaw-agent \
  --docker-label-selector app=picoclaw-agent \
  --docker-task-cmd 'picoclaw agent -m "$CLOUDCLAW_INPUT" > {{USERDATA_DIR}}/result.txt; printf "{""prompt_tokens"":10,""completion_tokens"":20}\n" > {{USAGE_FILE}}'
```

### 停止容器池

```bash
bash deploy/docker/stop-picoclaw-pool.sh app=picoclaw-agent
```

## K8s 执行

```bash
go run ./cmd/cloudclaw run \
  --data-dir ./data \
  --db-driver sqlite \
  --executor k8s-picoclaw \
  --k8s-namespace default \
  --k8s-label-selector app=picoclaw-agent \
  --k8s-task-cmd 'picoclaw agent -m "$CLOUDCLAW_INPUT" > {{USERDATA_DIR}}/result.txt; printf "{""prompt_tokens"":10,""completion_tokens"":20}\n" > {{USAGE_FILE}}'
```

K8s 清单：
- `deploy/k8s/picoclaw-agent-pool.yaml`
- `deploy/k8s/cloudclaw-rbac.yaml`

## Go SDK

路径：`pkg/cloudclaw`

```go
client, _ := cloudclaw.NewClient(cloudclaw.Config{
  DataDir:  "./data",
  DBDriver: "sqlite",
})
defer client.Close()

task, _ := client.SubmitTask(cloudclaw.SubmitTaskRequest{
  UserID: "u1", TaskType: "search", Input: "hello", MaxRetries: 2,
})
status, _ := client.GetTask(task.ID)
fmt.Println(status.Status)
```

## Python SDK

路径：`sdk/python`

```bash
pip install -e ./sdk/python
```

```python
from cloudclaw import Client

client = Client(binary="cloudclaw", data_dir="./data", db_driver="sqlite")
task = client.submit_task(user_id="u1", task_type="search", input_text="hello")
print(client.get_task_status(task["id"]))
```

## 开发测试

```bash
GOCACHE=$(pwd)/.gocache GOMODCACHE=$(pwd)/.gomodcache go test ./...
```

## 参考

- [sipeed/picoclaw](https://github.com/sipeed/picoclaw)
