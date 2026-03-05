package engine

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"cloudclaw/internal/fsutil"
	"cloudclaw/internal/model"
	"cloudclaw/internal/store"
)

type RunnerConfig struct {
	Store             *store.Store
	Executor          Executor
	PoolSize          int
	ContainerIDs      []string
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
	if cfg.Executor == nil {
		return nil, fmt.Errorf("executor is required")
	}
	if cfg.PoolSize <= 0 {
		cfg.PoolSize = 1
	}
	if len(cfg.ContainerIDs) == 0 {
		cfg.ContainerIDs = make([]string, cfg.PoolSize)
		for i := 0; i < cfg.PoolSize; i++ {
			cfg.ContainerIDs[i] = fmt.Sprintf("container-%02d", i+1)
		}
	}
	cfg.PoolSize = len(cfg.ContainerIDs)
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
	var wg sync.WaitGroup

	for _, containerID := range r.cfg.ContainerIDs {
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
	ticker := time.NewTicker(r.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			_ = r.setContainer(containerID, "offline", "")
			return
		case <-ticker.C:
			task, err := r.cfg.Store.DequeueForRun(containerID, r.cfg.LeaseDuration)
			if err != nil {
				r.cfg.Logger.Printf("dequeue error (%s): %v", containerID, err)
				continue
			}
			if task == nil {
				continue
			}
			_ = r.setContainer(containerID, "running", task.ID)
			r.cfg.Logger.Printf("task %s assigned to %s (attempt=%d)", task.ID, containerID, task.Attempts)
			r.runTask(ctx, containerID, *task)
			_ = r.setContainer(containerID, "idle", "")
		}
	}
}

func (r *Runner) runTask(ctx context.Context, containerID string, task model.Task) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

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
					cancel()
					return
				}
				cancelRequested, err := r.cfg.Store.IsCancelRequested(task.ID)
				if err == nil && cancelRequested {
					cancel()
					return
				}
			}
		}
	}()

	runDir, err := r.prepareRunWorkspace(task)
	if err != nil {
		_ = r.cfg.Store.MarkTaskRetryOrFail(task.ID, containerID, fmt.Sprintf("workspace prepare failed: %v", err))
		<-heartbeatDone
		return
	}

	usage, execErr := r.cfg.Executor.Execute(runCtx, containerID, task, runDir)
	cancel()
	<-heartbeatDone

	cancelRequested, _ := r.cfg.Store.IsCancelRequested(task.ID)
	if execErr == nil && !cancelRequested {
		if err := r.persistUserData(task, runDir); err != nil {
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
		if runCtx.Err() == context.Canceled {
			reason = "runner interrupted"
		}
		if err := r.cfg.Store.MarkTaskRetryOrFail(task.ID, containerID, reason); err != nil {
			r.cfg.Logger.Printf("mark retry/fail failed for task %s: %v", task.ID, err)
		}
		r.cfg.Logger.Printf("task %s failed: %v", task.ID, execErr)
		return
	}

	if err := r.cfg.Store.MarkTaskSucceeded(task.ID, containerID, usage); err != nil {
		r.cfg.Logger.Printf("mark success failed for task %s: %v", task.ID, err)
		return
	}
	r.cfg.Logger.Printf("task %s succeeded", task.ID)
}

func (r *Runner) prepareRunWorkspace(task model.Task) (string, error) {
	userDataDir := r.cfg.Store.UserDataDir(task.UserID)
	if err := fsutil.EnsureDir(userDataDir); err != nil {
		return "", err
	}
	runDir := r.cfg.Store.RunDir(task.ID, task.Attempts)
	if err := fsutil.RemoveAndRecreate(runDir); err != nil {
		return "", err
	}
	if err := fsutil.CopyDir(userDataDir, runDir); err != nil {
		return "", err
	}
	return runDir, nil
}

func (r *Runner) persistUserData(task model.Task, runDir string) error {
	userDataDir := r.cfg.Store.UserDataDir(task.UserID)
	if err := fsutil.RemoveAndRecreate(userDataDir); err != nil {
		return err
	}
	if err := fsutil.CopyDir(runDir, userDataDir); err != nil {
		return err
	}

	snapID := fmt.Sprintf("%s-%d", task.ID, time.Now().UTC().Unix())
	snapshotPath := filepath.Join(r.cfg.Store.UserSnapshotBaseDir(task.UserID), snapID)
	if err := fsutil.RemoveAndRecreate(snapshotPath); err != nil {
		return err
	}
	if err := fsutil.CopyDir(userDataDir, snapshotPath); err != nil {
		return err
	}
	_, err := r.cfg.Store.SaveSnapshot(task.UserID, task.ID, snapshotPath)
	return err
}

func (r *Runner) recoveryLoop(ctx context.Context) {
	ticker := time.NewTicker(r.cfg.RecoveryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
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
