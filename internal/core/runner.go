package core

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"cloudclaw/internal/model"
	"cloudclaw/internal/ports"
)

type RunnerConfig struct {
	Store             ports.TaskStore
	Runtime           ports.RuntimeAdapter
	Workspace         ports.WorkspaceManager
	Pool              ports.PoolAdapter
	PollInterval      time.Duration
	LeaseDuration     time.Duration
	HeartbeatInterval time.Duration
	RecoveryInterval  time.Duration
	Logger            *log.Logger
}

type Runner struct {
	cfg RunnerConfig
}

func NewRunner(cfg RunnerConfig) (*Runner, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("store is required")
	}
	if cfg.Runtime == nil {
		return nil, fmt.Errorf("runtime adapter is required")
	}
	if cfg.Workspace == nil {
		return nil, fmt.Errorf("workspace manager is required")
	}
	if cfg.Pool == nil {
		return nil, fmt.Errorf("pool adapter is required")
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 1 * time.Second
	}
	if cfg.LeaseDuration <= 0 {
		cfg.LeaseDuration = 30 * time.Second
	}
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = 5 * time.Second
	}
	if cfg.RecoveryInterval <= 0 {
		cfg.RecoveryInterval = 3 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = log.New(os.Stdout, "[cloudclaw] ", log.LstdFlags)
	}
	return &Runner{cfg: cfg}, nil
}

func (r *Runner) Run(ctx context.Context) error {
	containerIDs, err := r.cfg.Pool.ContainerIDs(ctx)
	if err != nil {
		return err
	}
	if len(containerIDs) == 0 {
		return fmt.Errorf("pool %s returned no container ids", r.cfg.Pool.Name())
	}

	var wg sync.WaitGroup
	for _, containerID := range containerIDs {
		if err := r.setContainer(containerID, "idle", ""); err != nil {
			return err
		}
		wg.Add(1)
		go func(cid string) {
			defer wg.Done()
			r.workerLoop(ctx, cid)
		}(containerID)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		r.recoveryLoop(ctx)
	}()

	<-ctx.Done()
	wg.Wait()
	return nil
}

func (r *Runner) workerLoop(ctx context.Context, containerID string) {
	defer func() {
		_ = r.setContainer(containerID, "offline", "")
	}()

	for {
		if err := ctx.Err(); err != nil {
			return
		}
		task, err := r.cfg.Store.DequeueForRun(containerID, r.cfg.LeaseDuration)
		if err != nil {
			r.cfg.Logger.Printf("dequeue error (%s): %v", containerID, err)
			if !sleepWithContext(ctx, r.cfg.PollInterval) {
				return
			}
			continue
		}
		if task == nil {
			if !sleepWithContext(ctx, r.cfg.PollInterval) {
				return
			}
			continue
		}
		_ = r.setContainer(containerID, "running", task.ID)
		r.cfg.Logger.Printf("task %s assigned to %s (attempt=%d, runtime=%s)", task.ID, containerID, task.Attempts, r.cfg.Runtime.Name())
		r.runTask(ctx, containerID, *task)
		_ = r.setContainer(containerID, "idle", "")
	}
}

func sleepWithContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (r *Runner) runTask(ctx context.Context, containerID string, task model.Task) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var heartbeatErr struct {
		sync.Mutex
		err error
	}
	setHeartbeatErr := func(err error) {
		if err == nil {
			return
		}
		heartbeatErr.Lock()
		if heartbeatErr.err == nil {
			heartbeatErr.err = err
		}
		heartbeatErr.Unlock()
	}
	getHeartbeatErr := func() error {
		heartbeatErr.Lock()
		defer heartbeatErr.Unlock()
		return heartbeatErr.err
	}

	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(r.cfg.HeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				if err := r.cfg.Store.Heartbeat(task.ID, containerID, r.cfg.LeaseDuration); err != nil {
					setHeartbeatErr(fmt.Errorf("heartbeat failed: %w", err))
					cancel()
					return
				}
				cancelRequested, err := r.cfg.Store.IsCancelRequested(task.ID)
				if err != nil {
					setHeartbeatErr(fmt.Errorf("check cancel request failed: %w", err))
					cancel()
					return
				}
				if cancelRequested {
					cancel()
					return
				}
			}
		}
	}()

	runDir, err := r.cfg.Workspace.Prepare(task)
	if err != nil {
		_ = r.cfg.Store.MarkTaskRetryOrFail(task.ID, containerID, fmt.Sprintf("workspace prepare failed: %v", err))
		cancel()
		<-heartbeatDone
		return
	}
	defer func() {
		if err := os.RemoveAll(runDir); err != nil {
			r.cfg.Logger.Printf("cleanup run dir failed (%s): %v", runDir, err)
		}
	}()

	usage, execErr := r.cfg.Runtime.Execute(runCtx, containerID, task, runDir)
	cancel()
	<-heartbeatDone

	hbErr := getHeartbeatErr()
	cancelRequested, cancelCheckErr := r.cfg.Store.IsCancelRequested(task.ID)
	if cancelCheckErr != nil {
		r.cfg.Logger.Printf("check cancel request failed for task %s: %v", task.ID, cancelCheckErr)
		if execErr == nil && hbErr == nil {
			execErr = fmt.Errorf("check cancel state after execution failed: %w", cancelCheckErr)
		}
	}
	if hbErr != nil && (execErr == nil || errors.Is(execErr, context.Canceled)) {
		execErr = hbErr
	}

	if execErr == nil && !cancelRequested {
		if err := r.cfg.Workspace.Persist(task, runDir); err != nil {
			execErr = fmt.Errorf("persist user data failed: %w", err)
		}
	}

	if cancelRequested {
		reason := "canceled by user"
		if err := r.cfg.Store.MarkTaskCanceled(task.ID, containerID, reason); err != nil {
			r.cfg.Logger.Printf("mark canceled failed for task %s: %v", task.ID, err)
		}
		r.cfg.Logger.Printf("task %s canceled", task.ID)
		return
	}

	if execErr != nil {
		reason := execErr.Error()
		if errors.Is(execErr, context.Canceled) && hbErr == nil {
			reason = "runner interrupted"
		}
		if err := r.cfg.Store.MarkTaskRetryOrFail(task.ID, containerID, reason); err != nil {
			r.cfg.Logger.Printf("mark retry/fail failed for task %s: %v", task.ID, err)
		}
		r.cfg.Logger.Printf("task %s failed: %v", task.ID, execErr)
		return
	}

	output := readTaskOutput(runDir)
	if err := r.cfg.Store.MarkTaskSucceeded(task.ID, containerID, usage, output); err != nil {
		r.cfg.Logger.Printf("mark success failed for task %s: %v", task.ID, err)
		return
	}
	r.cfg.Logger.Printf("task %s succeeded on %s (output_len=%d, usage_total=%d)", task.ID, containerID, len(output), usage.TotalTokens)
}

func readTaskOutput(runDir string) string {
	b, err := os.ReadFile(filepath.Join(runDir, "result.txt"))
	if err != nil {
		return ""
	}
	return string(b)
}

func (r *Runner) recoveryLoop(ctx context.Context) {
	ticker := time.NewTicker(r.cfg.RecoveryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.cfg.Pool.Reconcile(ctx); err != nil {
				r.cfg.Logger.Printf("pool reconcile failed (%s): %v", r.cfg.Pool.Name(), err)
			}
			recovered, err := r.cfg.Store.RecoverExpiredLeases()
			if err != nil {
				r.cfg.Logger.Printf("recover leases failed: %v", err)
				continue
			}
			if recovered > 0 {
				r.cfg.Logger.Printf("recovered %d task(s) by lease expiration", recovered)
			}
		}
	}
}

func (r *Runner) setContainer(containerID, state, taskID string) error {
	return r.cfg.Store.SetContainerStatus(model.ContainerInfo{
		ID:        containerID,
		State:     state,
		TaskID:    taskID,
		UpdatedAt: time.Now().UTC(),
	})
}
