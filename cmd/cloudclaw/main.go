package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"cloudclaw/internal/dockerutil"
	"cloudclaw/internal/engine"
	"cloudclaw/internal/k8sutil"
	"cloudclaw/internal/store"
	"cloudclaw/pkg/cloudclaw"
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
	dataDir := fs.String("data-dir", "./data", "cloudclaw data directory")
	poolSize := fs.Int("pool-size", 2, "container pool size")
	poll := fs.Duration("poll", 1*time.Second, "queue polling interval")
	lease := fs.Duration("lease", 30*time.Second, "task lease duration")
	heartbeat := fs.Duration("heartbeat", 5*time.Second, "task heartbeat interval")
	recovery := fs.Duration("recovery", 3*time.Second, "lease recovery interval")
	executorMode := fs.String("executor", "mock", "executor mode: mock|cmd|k8s-picoclaw|docker-picoclaw")
	execCmd := fs.String("exec-cmd", "", "executor command when --executor=cmd")
	k8sNamespace := fs.String("k8s-namespace", "default", "kubernetes namespace for picoclaw pods")
	k8sContext := fs.String("k8s-context", "", "optional kubernetes context")
	k8sLabelSelector := fs.String("k8s-label-selector", "app=picoclaw-agent", "label selector for picoclaw pods")
	k8sKubectl := fs.String("k8s-kubectl", "kubectl", "kubectl binary path")
	k8sRemoteDir := fs.String("k8s-remote-dir", "/workspace/cloudclaw", "task workspace base directory inside pod")
	k8sTaskCmd := fs.String("k8s-task-cmd", "", "task command executed inside selected pod; supports {{TASK_DIR}} {{TASK_FILE}} {{USAGE_FILE}} {{USERDATA_DIR}}")
	dockerBin := fs.String("docker-bin", "docker", "docker binary path")
	dockerLabelSelector := fs.String("docker-label-selector", "app=picoclaw-agent", "docker container label selector, supports comma separated key=value")
	dockerRemoteDir := fs.String("docker-remote-dir", "/workspace/cloudclaw", "task workspace base directory inside container")
	dockerTaskCmd := fs.String("docker-task-cmd", "", "task command executed inside selected container; supports {{TASK_DIR}} {{TASK_FILE}} {{USAGE_FILE}} {{USERDATA_DIR}}")
	if err := fs.Parse(args); err != nil {
		return err
	}

	s, err := store.New(*dataDir)
	if err != nil {
		return err
	}

	var ex engine.Executor
	containerIDs := []string{}
	switch *executorMode {
	case "mock":
		ex = &engine.MockExecutor{}
		containerIDs = syntheticContainerIDs(*poolSize)
	case "cmd":
		ex = &engine.CommandExecutor{Command: *execCmd}
		containerIDs = syntheticContainerIDs(*poolSize)
	case "k8s-picoclaw":
		if strings.TrimSpace(*k8sTaskCmd) == "" {
			return fmt.Errorf("k8s-task-cmd is required for k8s-picoclaw executor")
		}
		kube := k8sutil.Kubectl{
			Namespace: *k8sNamespace,
			Context:   *k8sContext,
			Binary:    *k8sKubectl,
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		pods, err := kube.ListRunningPods(ctx, *k8sLabelSelector)
		if err != nil {
			return err
		}
		if len(pods) == 0 {
			return fmt.Errorf("no running pods found for selector %q in namespace %q", *k8sLabelSelector, *k8sNamespace)
		}
		sort.Strings(pods)
		containerIDs = pods
		ex = &engine.K8sPicoclawExecutor{
			Kubectl:       kube,
			RemoteBaseDir: *k8sRemoteDir,
			TaskCommand:   *k8sTaskCmd,
		}
	case "docker-picoclaw":
		if strings.TrimSpace(*dockerTaskCmd) == "" {
			return fmt.Errorf("docker-task-cmd is required for docker-picoclaw executor")
		}
		dk := dockerutil.Docker{Binary: *dockerBin}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		containers, err := dk.ListRunningContainers(ctx, *dockerLabelSelector)
		if err != nil {
			return err
		}
		if len(containers) == 0 {
			return fmt.Errorf("no running containers found for selector %q", *dockerLabelSelector)
		}
		sort.Strings(containers)
		containerIDs = containers
		ex = &engine.DockerPicoclawExecutor{
			Docker:        dk,
			RemoteBaseDir: *dockerRemoteDir,
			TaskCommand:   *dockerTaskCmd,
		}
	default:
		return fmt.Errorf("unknown executor mode: %s", *executorMode)
	}

	r, err := engine.NewRunner(engine.RunnerConfig{
		Store:             s,
		Executor:          ex,
		PoolSize:          len(containerIDs),
		ContainerIDs:      containerIDs,
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
	log.Printf("cloudclaw runner started data_dir=%s pool_size=%d executor=%s", *dataDir, len(containerIDs), *executorMode)
	return r.Run(ctx)
}

func syntheticContainerIDs(poolSize int) []string {
	if poolSize <= 0 {
		poolSize = 1
	}
	ids := make([]string, poolSize)
	for i := 0; i < poolSize; i++ {
		ids[i] = fmt.Sprintf("container-%02d", i+1)
	}
	return ids
}

func taskCmd(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: cloudclaw task <submit|status|cancel>")
	}
	subcmd := args[0]
	switch subcmd {
	case "submit":
		return taskSubmitCmd(args[1:])
	case "status":
		return taskStatusCmd(args[1:])
	case "cancel":
		return taskCancelCmd(args[1:])
	default:
		return fmt.Errorf("unknown task command: %s", subcmd)
	}
}

func taskSubmitCmd(args []string) error {
	fs := flag.NewFlagSet("task submit", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "./data", "cloudclaw data directory")
	userID := fs.String("user-id", "", "user id")
	taskType := fs.String("task-type", "", "task type")
	input := fs.String("input", "", "task input")
	maxRetries := fs.Int("max-retries", 2, "max retry count after first attempt")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cli, err := cloudclaw.NewClient(cloudclaw.Config{DataDir: *dataDir})
	if err != nil {
		return err
	}
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
}

func taskStatusCmd(args []string) error {
	fs := flag.NewFlagSet("task status", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "./data", "cloudclaw data directory")
	taskID := fs.String("task-id", "", "task id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *taskID == "" {
		return fmt.Errorf("task-id is required")
	}
	cli, err := cloudclaw.NewClient(cloudclaw.Config{DataDir: *dataDir})
	if err != nil {
		return err
	}
	task, err := cli.GetTask(*taskID)
	if err != nil {
		return err
	}
	return printJSON(task)
}

func taskCancelCmd(args []string) error {
	fs := flag.NewFlagSet("task cancel", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "./data", "cloudclaw data directory")
	taskID := fs.String("task-id", "", "task id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *taskID == "" {
		return fmt.Errorf("task-id is required")
	}
	cli, err := cloudclaw.NewClient(cloudclaw.Config{DataDir: *dataDir})
	if err != nil {
		return err
	}
	task, err := cli.CancelTask(*taskID)
	if err != nil {
		return err
	}
	return printJSON(task)
}

func queueLengthCmd(args []string) error {
	fs := flag.NewFlagSet("queue-length", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "./data", "cloudclaw data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cli, err := cloudclaw.NewClient(cloudclaw.Config{DataDir: *dataDir})
	if err != nil {
		return err
	}
	n, err := cli.QueueLength()
	if err != nil {
		return err
	}
	return printJSON(map[string]any{"queue_length": n})
}

func containerStatusCmd(args []string) error {
	fs := flag.NewFlagSet("container-status", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "./data", "cloudclaw data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cli, err := cloudclaw.NewClient(cloudclaw.Config{DataDir: *dataDir})
	if err != nil {
		return err
	}
	list, err := cli.ContainerStatus()
	if err != nil {
		return err
	}
	return printJSON(list)
}

func auditCmd(args []string) error {
	fs := flag.NewFlagSet("audit", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "./data", "cloudclaw data directory")
	taskID := fs.String("task-id", "", "task id (optional)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cli, err := cloudclaw.NewClient(cloudclaw.Config{DataDir: *dataDir})
	if err != nil {
		return err
	}
	events, err := cli.TaskEvents(*taskID)
	if err != nil {
		return err
	}
	return printJSON(events)
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func usage() {
	fmt.Println(`cloudclaw commands:
  cloudclaw run [--data-dir ./data --pool-size 2 --executor mock|cmd|k8s-picoclaw|docker-picoclaw]
  cloudclaw task submit --user-id u1 --task-type search --input "..."
  cloudclaw task status --task-id tsk_xxx
  cloudclaw task cancel --task-id tsk_xxx
  cloudclaw queue-length
  cloudclaw container-status
  cloudclaw audit [--task-id tsk_xxx]`)
}
