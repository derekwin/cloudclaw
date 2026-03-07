package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	pooladapter "cloudclaw/internal/adapters/pool"
	"cloudclaw/internal/core"
	"cloudclaw/internal/dockerutil"
	"cloudclaw/internal/engine"
	"cloudclaw/internal/k8sutil"
	"cloudclaw/internal/model"
	"cloudclaw/internal/store"
	"cloudclaw/internal/workspace"
	"cloudclaw/pkg/cloudclaw"
)

type commonStoreFlags struct {
	dataDir  *string
	dbDriver *string
	dbDSN    *string
}

const (
	defaultK8sLabelSelector    = "app=opencode-agent"
	defaultDockerLabelSelector = "app=opencode-agent"
	defaultDockerImage         = "ghcr.io/anomalyco/opencode:latest"
	defaultDockerNamePrefix    = "opencode-agent"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	var err error
	switch cmd {
	case "run":
		err = runCmd(os.Args[2:])
	case "task":
		err = taskCmd(os.Args[2:])
	case "result":
		err = resultCmd(os.Args[2:])
	case "user-data":
		err = userDataCmd(os.Args[2:])
	case "queue-length":
		err = queueLengthCmd(os.Args[2:])
	case "container-status":
		err = containerStatusCmd(os.Args[2:])
	case "audit":
		err = auditCmd(os.Args[2:])
	case "help", "-h", "--help":
		usage()
		return
	default:
		err = fmt.Errorf("unknown command: %s", cmd)
	}

	if err != nil {
		log.Printf("error: %v", err)
		os.Exit(1)
	}
}

func runCmd(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	sf := bindCommonStoreFlags(fs)
	poll := fs.Duration("poll", 1*time.Second, "queue polling interval")
	lease := fs.Duration("lease", 30*time.Second, "task lease duration")
	heartbeat := fs.Duration("heartbeat", 5*time.Second, "task heartbeat interval")
	recovery := fs.Duration("recovery", 3*time.Second, "lease recovery interval")
	sharedSkillsDir := fs.String("shared-skills-dir", "", "global shared skills directory injected into each task workspace")
	sharedSkillsMode := fs.String("shared-skills-mode", "copy", "shared skills mode: copy|mount")
	sharedSkillsMountPath := fs.String("shared-skills-mount-path", "/workspace/.cloudclaw_shared_skills", "shared skills path inside pod/container when --shared-skills-mode=mount")
	workspaceStateMode := fs.String("workspace-state-mode", "db", "workspace state mode: db|ephemeral (none as alias)")
	userRuntimeDir := fs.String("user-runtime-dir", "", "host directory for per-user runtime state in ephemeral workspace mode")
	workspaceMode := fs.String("workspace-mode", "copy", "workspace transfer mode: copy|mount (docker executors only)")
	workspaceMountPath := fs.String("workspace-mount-path", "/workspace/cloudclaw/runs", "workspace path inside docker container when --workspace-mode=mount")
	executorMode := fs.String("executor", "", "executor mode (required): k8s-opencode|k8s-claudecode|docker-opencode|docker-claudecode")
	k8sNamespace := fs.String("k8s-namespace", "default", "kubernetes namespace for runtime pods")
	k8sContext := fs.String("k8s-context", "", "optional kubernetes context")
	k8sLabelSelector := fs.String("k8s-label-selector", defaultK8sLabelSelector, "label selector for runtime pods")
	k8sKubectl := fs.String("k8s-kubectl", "kubectl", "kubectl binary path")
	k8sRemoteDir := fs.String("k8s-remote-dir", "/workspace/cloudclaw", "task workspace base directory inside pod")
	k8sTaskCmd := fs.String("k8s-task-cmd", "", "task command executed inside selected pod; supports {{TASK_DIR}} {{TASK_FILE}} {{USAGE_FILE}} {{USERDATA_DIR}}")
	dockerBin := fs.String("docker-bin", "docker", "docker binary path")
	dockerLabelSelector := fs.String("docker-label-selector", defaultDockerLabelSelector, "docker container label selector, supports comma separated key=value")
	dockerRemoteDir := fs.String("docker-remote-dir", "/tmp/cloudclaw", "task workspace base directory inside container")
	dockerTaskCmd := fs.String("docker-task-cmd", "", "task command executed inside selected container; supports {{TASK_DIR}} {{TASK_FILE}} {{USAGE_FILE}} {{USERDATA_DIR}}")
	dockerManagePool := fs.Bool("docker-manage-pool", false, "whether cloudclaw should ensure docker prewarm pool")
	dockerPoolSize := fs.Int("docker-pool-size", 3, "target container count when --docker-manage-pool=true")
	dockerImage := fs.String("docker-image", defaultDockerImage, "docker image for prewarm pool")
	dockerNamePrefix := fs.String("docker-name-prefix", defaultDockerNamePrefix, "container name prefix for prewarm pool")
	dockerInitCmd := fs.String("docker-init-cmd", "sleep infinity", "container init command when creating prewarm pool")
	eventRetentionPerTask := fs.Int("event-retention-per-task", 2000, "max number of task events retained per task")
	maxUserDataBytes := fs.Int64("max-user-data-bytes", 256<<20, "max total bytes per user's persisted workspace data")
	maxUserDataFileBytes := fs.Int64("max-user-data-file-bytes", 32<<20, "max bytes per persisted user data file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*executorMode) == "" {
		return fmt.Errorf("executor is required: k8s-opencode|k8s-claudecode|docker-opencode|docker-claudecode")
	}
	applyExecutorRuntimeDefaults(*executorMode, k8sLabelSelector, dockerLabelSelector, dockerImage, dockerNamePrefix)

	s, err := store.NewWithConfig(store.Config{
		BaseDir:               *sf.dataDir,
		Driver:                *sf.dbDriver,
		DSN:                   *sf.dbDSN,
		EventRetentionPerTask: *eventRetentionPerTask,
		MaxUserDataBytes:      *maxUserDataBytes,
		MaxUserDataFileBytes:  *maxUserDataFileBytes,
	})
	if err != nil {
		return err
	}
	defer s.Close()

	workspaceManager, err := workspace.NewLocalManager(workspace.LocalManagerConfig{
		Store:            s,
		SharedSkillsDir:  strings.TrimSpace(*sharedSkillsDir),
		SharedSkillsMode: strings.TrimSpace(*sharedSkillsMode),
		WorkspaceState:   strings.TrimSpace(*workspaceStateMode),
		RuntimeName:      runtimeNameForExecutor(*executorMode),
		UserRuntimeDir:   strings.TrimSpace(*userRuntimeDir),
	})
	if err != nil {
		return err
	}

	var ex engine.Executor
	var pool portsPool
	switch *executorMode {
	case "k8s-opencode", "k8s-claudecode":
		if strings.TrimSpace(*k8sTaskCmd) == "" {
			return fmt.Errorf("k8s-task-cmd is required for %s executor", *executorMode)
		}
		pool, err = pooladapter.NewK8s(pooladapter.K8sOptions{
			Namespace:     *k8sNamespace,
			Context:       *k8sContext,
			KubectlBinary: *k8sKubectl,
			LabelSelector: *k8sLabelSelector,
		})
		if err != nil {
			return err
		}
		runtimeExecutor := engine.K8sRuntimeExecutor{
			Kubectl: k8sutil.Kubectl{
				Namespace: *k8sNamespace,
				Context:   *k8sContext,
				Binary:    *k8sKubectl,
			},
			RemoteBaseDir:   *k8sRemoteDir,
			TaskCommand:     *k8sTaskCmd,
			SharedSkillsDir: sharedSkillsDirForExecutor(*sharedSkillsMode, *sharedSkillsMountPath),
		}
		if *executorMode == "k8s-opencode" {
			ex = &engine.K8sOpencodeExecutor{K8sRuntimeExecutor: runtimeExecutor}
		} else {
			ex = &engine.K8sClaudecodeExecutor{K8sRuntimeExecutor: runtimeExecutor}
		}
	case "docker-opencode", "docker-claudecode":
		if strings.TrimSpace(*dockerTaskCmd) == "" {
			return fmt.Errorf("docker-task-cmd is required for %s executor", *executorMode)
		}
		pool, err = pooladapter.NewDocker(pooladapter.DockerOptions{
			Binary:                   *dockerBin,
			LabelSelector:            *dockerLabelSelector,
			ManagePool:               *dockerManagePool,
			PoolSize:                 *dockerPoolSize,
			Image:                    *dockerImage,
			NamePrefix:               *dockerNamePrefix,
			InitCmd:                  *dockerInitCmd,
			SharedSkillsHostDir:      strings.TrimSpace(*sharedSkillsDir),
			SharedSkillsContainerDir: sharedSkillsDirForExecutor(*sharedSkillsMode, *sharedSkillsMountPath),
			WorkspaceHostDir:         workspaceHostDirForDocker(*workspaceMode, *sf.dataDir),
			WorkspaceContainerDir:    workspaceContainerDirForDocker(*workspaceMode, *workspaceMountPath),
		})
		if err != nil {
			return err
		}
		runtimeExecutor := engine.DockerRuntimeExecutor{
			Docker:              dockerutil.Docker{Binary: *dockerBin},
			RemoteBaseDir:       *dockerRemoteDir,
			TaskCommand:         *dockerTaskCmd,
			SharedSkillsDir:     sharedSkillsDirForExecutor(*sharedSkillsMode, *sharedSkillsMountPath),
			WorkspaceMode:       strings.TrimSpace(*workspaceMode),
			RunDirHostBase:      workspaceHostDirForDocker(*workspaceMode, *sf.dataDir),
			RunDirContainerBase: workspaceContainerDirForDocker(*workspaceMode, *workspaceMountPath),
		}
		if *executorMode == "docker-opencode" {
			ex = &engine.DockerOpencodeExecutor{DockerRuntimeExecutor: runtimeExecutor}
		} else {
			ex = &engine.DockerClaudecodeExecutor{DockerRuntimeExecutor: runtimeExecutor}
		}
	default:
		return fmt.Errorf("unknown executor mode: %s", *executorMode)
	}

	r, err := core.NewRunner(core.RunnerConfig{
		Store:             s,
		Runtime:           ex,
		Workspace:         workspaceManager,
		Pool:              pool,
		PollInterval:      *poll,
		LeaseDuration:     *lease,
		HeartbeatInterval: *heartbeat,
		RecoveryInterval:  *recovery,
	})
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	log.Printf("cloudclaw runner started data_dir=%s db_driver=%s executor=%s pool=%s", *sf.dataDir, *sf.dbDriver, ex.Name(), pool.Name())
	return r.Run(ctx)
}

func taskCmd(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: cloudclaw task <submit|status|cancel|summary>")
	}
	subcmd := args[0]
	switch subcmd {
	case "submit":
		return taskSubmitCmd(args[1:])
	case "status":
		return taskStatusCmd(args[1:])
	case "cancel":
		return taskCancelCmd(args[1:])
	case "summary":
		return taskSummaryCmd(args[1:])
	default:
		return fmt.Errorf("unknown task command: %s", subcmd)
	}
}

func taskSubmitCmd(args []string) error {
	fs := flag.NewFlagSet("task submit", flag.ContinueOnError)
	sf := bindCommonStoreFlags(fs)
	userID := fs.String("user-id", "", "user id")
	taskType := fs.String("task-type", "", "task type")
	input := fs.String("input", "", "task input")
	maxRetries := fs.Int("max-retries", 2, "max retry count after first attempt")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return withClient(sf, func(cli *cloudclaw.Client) error {
		task, err := cli.SubmitTask(cloudclaw.SubmitTaskRequest{
			UserID:     *userID,
			TaskType:   *taskType,
			Input:      *input,
			MaxRetries: *maxRetries,
		})
		if err != nil {
			return err
		}
		return printJSON(task)
	})
}

func taskStatusCmd(args []string) error {
	fs := flag.NewFlagSet("task status", flag.ContinueOnError)
	sf := bindCommonStoreFlags(fs)
	taskID := fs.String("task-id", "", "task id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *taskID == "" {
		return fmt.Errorf("task-id is required")
	}
	return withClient(sf, func(cli *cloudclaw.Client) error {
		task, err := cli.GetTask(*taskID)
		if err != nil {
			return err
		}
		return printJSON(task)
	})
}

func taskCancelCmd(args []string) error {
	fs := flag.NewFlagSet("task cancel", flag.ContinueOnError)
	sf := bindCommonStoreFlags(fs)
	taskID := fs.String("task-id", "", "task id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *taskID == "" {
		return fmt.Errorf("task-id is required")
	}
	return withClient(sf, func(cli *cloudclaw.Client) error {
		task, err := cli.CancelTask(*taskID)
		if err != nil {
			return err
		}
		return printJSON(task)
	})
}

type taskSummaryTask struct {
	ID          string     `json:"id"`
	UserID      string     `json:"user_id"`
	TaskType    string     `json:"task_type"`
	Status      string     `json:"status"`
	Priority    int        `json:"priority"`
	Attempts    int        `json:"attempts"`
	ContainerID string     `json:"container_id,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
}

type taskSummaryContainer struct {
	ID        string           `json:"id"`
	State     string           `json:"state"`
	TaskID    string           `json:"task_id,omitempty"`
	UpdatedAt time.Time        `json:"updated_at"`
	Task      *taskSummaryTask `json:"task,omitempty"`
}

type taskSummaryResponse struct {
	GeneratedAt time.Time              `json:"generated_at"`
	QueueLength int                    `json:"queue_length"`
	Counts      map[string]int         `json:"counts"`
	Running     []taskSummaryTask      `json:"running"`
	Queued      []taskSummaryTask      `json:"queued"`
	RecentDone  []taskSummaryTask      `json:"recent_done"`
	Containers  []taskSummaryContainer `json:"containers"`
}

func taskSummaryCmd(args []string) error {
	fs := flag.NewFlagSet("task summary", flag.ContinueOnError)
	sf := bindCommonStoreFlags(fs)
	limit := fs.Int("limit", 10, "limit per task list section")
	format := fs.String("format", "json", "output format: json|text")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *limit <= 0 {
		return fmt.Errorf("limit must be > 0")
	}
	outFmt := strings.ToLower(strings.TrimSpace(*format))
	if outFmt != "json" && outFmt != "text" {
		return fmt.Errorf("unsupported format: %s (use json|text)", *format)
	}

	return withStore(sf, func(s *store.Store) error {
		queueLen, err := s.QueueLength()
		if err != nil {
			return err
		}
		counts, err := s.TaskStatusCounts()
		if err != nil {
			return err
		}
		running, err := s.ListTasksByStatus(model.StatusRunning, *limit)
		if err != nil {
			return err
		}
		queued, err := s.ListTasksByStatus(model.StatusQueued, *limit)
		if err != nil {
			return err
		}
		done, err := s.ListLatestFinishedTasks(*limit)
		if err != nil {
			return err
		}
		containers, err := s.ListContainers()
		if err != nil {
			return err
		}

		resp := taskSummaryResponse{
			GeneratedAt: time.Now().UTC(),
			QueueLength: queueLen,
			Counts: map[string]int{
				string(model.StatusQueued):   counts[model.StatusQueued],
				string(model.StatusRunning):  counts[model.StatusRunning],
				string(model.StatusSuccess):  counts[model.StatusSuccess],
				string(model.StatusFailed):   counts[model.StatusFailed],
				string(model.StatusCanceled): counts[model.StatusCanceled],
			},
			Running:    toSummaryTasks(running),
			Queued:     toSummaryTasks(queued),
			RecentDone: toSummaryTasks(done),
			Containers: make([]taskSummaryContainer, 0, len(containers)),
		}
		resp.Counts["TOTAL"] = resp.Counts[string(model.StatusQueued)] +
			resp.Counts[string(model.StatusRunning)] +
			resp.Counts[string(model.StatusSuccess)] +
			resp.Counts[string(model.StatusFailed)] +
			resp.Counts[string(model.StatusCanceled)]

		taskIndex := map[string]taskSummaryTask{}
		for _, group := range [][]taskSummaryTask{resp.Running, resp.Queued, resp.RecentDone} {
			for _, t := range group {
				taskIndex[t.ID] = t
			}
		}

		sort.Slice(containers, func(i, j int) bool { return containers[i].ID < containers[j].ID })
		for _, c := range containers {
			item := taskSummaryContainer{
				ID:        c.ID,
				State:     c.State,
				TaskID:    c.TaskID,
				UpdatedAt: c.UpdatedAt,
			}
			if strings.TrimSpace(c.TaskID) != "" {
				if t, ok := taskIndex[c.TaskID]; ok {
					tc := t
					item.Task = &tc
				} else if t, err := s.GetTask(c.TaskID); err == nil {
					tc := toSummaryTask(t)
					item.Task = &tc
				}
			}
			resp.Containers = append(resp.Containers, item)
		}

		if outFmt == "text" {
			printTaskSummaryText(resp)
			return nil
		}
		return printJSON(resp)
	})
}

func queueLengthCmd(args []string) error {
	fs := flag.NewFlagSet("queue-length", flag.ContinueOnError)
	sf := bindCommonStoreFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	return withClient(sf, func(cli *cloudclaw.Client) error {
		n, err := cli.QueueLength()
		if err != nil {
			return err
		}
		return printJSON(map[string]any{"queue_length": n})
	})
}

func containerStatusCmd(args []string) error {
	fs := flag.NewFlagSet("container-status", flag.ContinueOnError)
	sf := bindCommonStoreFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	return withClient(sf, func(cli *cloudclaw.Client) error {
		list, err := cli.ContainerStatus()
		if err != nil {
			return err
		}
		return printJSON(list)
	})
}

func auditCmd(args []string) error {
	fs := flag.NewFlagSet("audit", flag.ContinueOnError)
	sf := bindCommonStoreFlags(fs)
	taskID := fs.String("task-id", "", "task id (optional)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return withClient(sf, func(cli *cloudclaw.Client) error {
		events, err := cli.TaskEvents(*taskID)
		if err != nil {
			return err
		}
		return printJSON(events)
	})
}

func userDataCmd(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: cloudclaw user-data <prune-opencode-runtime>")
	}
	switch args[0] {
	case "prune-opencode-runtime":
		return userDataPruneOpencodeRuntimeCmd(args[1:])
	default:
		return fmt.Errorf("unknown user-data command: %s", args[0])
	}
}

func userDataPruneOpencodeRuntimeCmd(args []string) error {
	fs := flag.NewFlagSet("user-data prune-opencode-runtime", flag.ContinueOnError)
	sf := bindCommonStoreFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	return withStore(sf, func(s *store.Store) error {
		deleted, err := s.PruneOpencodeRuntimeArtifacts()
		if err != nil {
			return err
		}
		return printJSON(map[string]any{
			"deleted_rows": deleted,
		})
	})
}

func resultCmd(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: cloudclaw result <dequeue|get>")
	}
	switch args[0] {
	case "dequeue":
		return resultDequeueCmd(args[1:])
	case "get":
		return resultGetCmd(args[1:])
	default:
		return fmt.Errorf("unknown result command: %s", args[0])
	}
}

func resultDequeueCmd(args []string) error {
	fs := flag.NewFlagSet("result dequeue", flag.ContinueOnError)
	sf := bindCommonStoreFlags(fs)
	limit := fs.Int("limit", 20, "max number of result items to dequeue")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return withClient(sf, func(cli *cloudclaw.Client) error {
		items, err := cli.DequeueTaskResults(*limit)
		if err != nil {
			return err
		}
		return printJSON(items)
	})
}

func resultGetCmd(args []string) error {
	fs := flag.NewFlagSet("result get", flag.ContinueOnError)
	sf := bindCommonStoreFlags(fs)
	taskID := fs.String("task-id", "", "task id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*taskID) == "" {
		return fmt.Errorf("task-id is required")
	}
	return withClient(sf, func(cli *cloudclaw.Client) error {
		item, err := cli.GetTaskResult(*taskID)
		if err != nil {
			return err
		}
		return printJSON(item)
	})
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func usage() {
	fmt.Println(`cloudclaw commands:
	  cloudclaw run [--data-dir ./cloudclaw_data/data --db-driver sqlite --executor k8s-opencode|k8s-claudecode|docker-opencode|docker-claudecode]
	  cloudclaw task submit --user-id u1 --task-type search --input "..."
	  cloudclaw task status --task-id tsk_xxx
	  cloudclaw task cancel --task-id tsk_xxx
	  cloudclaw task summary [--limit 10 --format json|text]
	  cloudclaw result dequeue [--limit 20]
	  cloudclaw result get --task-id tsk_xxx
	  cloudclaw user-data prune-opencode-runtime
	  cloudclaw queue-length
	  cloudclaw container-status
	  cloudclaw audit [--task-id tsk_xxx]`)
}

func bindCommonStoreFlags(fs *flag.FlagSet) commonStoreFlags {
	dataDir := fs.String("data-dir", "./cloudclaw_data/data", "cloudclaw data directory")
	dbDriver := fs.String("db-driver", "sqlite", "database driver: sqlite|postgres")
	dbDSN := fs.String("db-dsn", "", "database dsn; sqlite default is <data-dir>/cloudclaw.db")
	return commonStoreFlags{
		dataDir:  dataDir,
		dbDriver: dbDriver,
		dbDSN:    dbDSN,
	}
}

type portsPool interface {
	Name() string
	ContainerIDs(ctx context.Context) ([]string, error)
	Reconcile(ctx context.Context) error
}

func withClient(sf commonStoreFlags, fn func(*cloudclaw.Client) error) error {
	cli, err := cloudclaw.NewClient(cloudclaw.Config{
		DataDir:  *sf.dataDir,
		DBDriver: *sf.dbDriver,
		DBDSN:    *sf.dbDSN,
	})
	if err != nil {
		return err
	}
	defer cli.Close()
	return fn(cli)
}

func withStore(sf commonStoreFlags, fn func(*store.Store) error) error {
	s, err := store.NewWithConfig(store.Config{
		BaseDir: *sf.dataDir,
		Driver:  *sf.dbDriver,
		DSN:     *sf.dbDSN,
	})
	if err != nil {
		return err
	}
	defer s.Close()
	return fn(s)
}

func sharedSkillsDirForExecutor(mode, mountPath string) string {
	if strings.EqualFold(strings.TrimSpace(mode), "mount") {
		return strings.TrimSpace(mountPath)
	}
	return ""
}

func workspaceHostDirForDocker(mode, dataDir string) string {
	if !strings.EqualFold(strings.TrimSpace(mode), "mount") {
		return ""
	}
	base := strings.TrimSpace(dataDir)
	if base == "" {
		base = "./cloudclaw_data/data"
	}
	abs, err := filepath.Abs(base)
	if err != nil {
		return filepath.Join(base, "runs")
	}
	return filepath.Join(abs, "runs")
}

func workspaceContainerDirForDocker(mode, mountPath string) string {
	if !strings.EqualFold(strings.TrimSpace(mode), "mount") {
		return ""
	}
	return strings.TrimSpace(mountPath)
}

func runtimeNameForExecutor(executorMode string) string {
	mode := strings.ToLower(strings.TrimSpace(executorMode))
	if strings.Contains(mode, "claudecode") {
		return "claudecode"
	}
	return "opencode"
}

func toSummaryTasks(tasks []model.Task) []taskSummaryTask {
	out := make([]taskSummaryTask, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, toSummaryTask(t))
	}
	return out
}

func toSummaryTask(t model.Task) taskSummaryTask {
	return taskSummaryTask{
		ID:          t.ID,
		UserID:      t.UserID,
		TaskType:    t.TaskType,
		Status:      string(t.Status),
		Priority:    t.Priority,
		Attempts:    t.Attempts,
		ContainerID: t.ContainerID,
		CreatedAt:   t.CreatedAt,
		UpdatedAt:   t.UpdatedAt,
		StartedAt:   t.StartedAt,
		FinishedAt:  t.FinishedAt,
	}
}

func printTaskSummaryText(summary taskSummaryResponse) {
	fmt.Printf("generated_at: %s\n", summary.GeneratedAt.Format(time.RFC3339))
	fmt.Printf("queue_length: %d\n", summary.QueueLength)
	fmt.Printf("counts: queued=%d running=%d succeeded=%d failed=%d canceled=%d total=%d\n",
		summary.Counts[string(model.StatusQueued)],
		summary.Counts[string(model.StatusRunning)],
		summary.Counts[string(model.StatusSuccess)],
		summary.Counts[string(model.StatusFailed)],
		summary.Counts[string(model.StatusCanceled)],
		summary.Counts["TOTAL"],
	)

	fmt.Println("running:")
	printSummaryTaskList(summary.Running)
	fmt.Println("queued:")
	printSummaryTaskList(summary.Queued)
	fmt.Println("recent_done:")
	printSummaryTaskList(summary.RecentDone)
	fmt.Println("containers:")
	if len(summary.Containers) == 0 {
		fmt.Println("  - (none)")
		return
	}
	for _, c := range summary.Containers {
		line := fmt.Sprintf("  - id=%s state=%s updated_at=%s", c.ID, c.State, c.UpdatedAt.Format(time.RFC3339))
		if strings.TrimSpace(c.TaskID) != "" {
			line += " task_id=" + c.TaskID
		}
		if c.Task != nil {
			line += fmt.Sprintf(" user=%s task_type=%s task_status=%s", c.Task.UserID, c.Task.TaskType, c.Task.Status)
		}
		fmt.Println(line)
	}
}

func printSummaryTaskList(tasks []taskSummaryTask) {
	if len(tasks) == 0 {
		fmt.Println("  - (none)")
		return
	}
	for _, t := range tasks {
		line := fmt.Sprintf(
			"  - id=%s user=%s type=%s status=%s attempts=%d priority=%d updated_at=%s",
			t.ID, t.UserID, t.TaskType, t.Status, t.Attempts, t.Priority, t.UpdatedAt.Format(time.RFC3339),
		)
		if strings.TrimSpace(t.ContainerID) != "" {
			line += " container=" + t.ContainerID
		}
		fmt.Println(line)
	}
}

func applyExecutorRuntimeDefaults(executorMode string, k8sLabelSelector, dockerLabelSelector, dockerImage, dockerNamePrefix *string) {
	mode := strings.ToLower(strings.TrimSpace(executorMode))
	if !strings.Contains(mode, "claudecode") {
		return
	}

	if strings.TrimSpace(*k8sLabelSelector) == defaultK8sLabelSelector {
		*k8sLabelSelector = "app=claudecode-agent"
	}
	if strings.TrimSpace(*dockerLabelSelector) == defaultDockerLabelSelector {
		*dockerLabelSelector = "app=claudecode-agent"
	}
	if strings.TrimSpace(*dockerImage) == defaultDockerImage {
		*dockerImage = "claudecode:latest"
	}
	if strings.TrimSpace(*dockerNamePrefix) == defaultDockerNamePrefix {
		*dockerNamePrefix = "claudecode-agent"
	}
}
