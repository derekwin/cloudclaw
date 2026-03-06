package core

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"cloudclaw/internal/model"
)

type stubStore struct {
	mu              sync.Mutex
	markRetryReason string
}

func (s *stubStore) DequeueForRun(containerID string, leaseDuration time.Duration) (*model.Task, error) {
	return nil, nil
}

func (s *stubStore) Heartbeat(taskID, containerID string, leaseDuration time.Duration) error {
	return nil
}

func (s *stubStore) IsCancelRequested(taskID string) (bool, error) {
	return false, nil
}

func (s *stubStore) MarkTaskSucceeded(taskID, containerID string, usage model.TokenUsage, output string) error {
	return nil
}

func (s *stubStore) MarkTaskCanceled(taskID, containerID, reason string) error {
	return nil
}

func (s *stubStore) MarkTaskRetryOrFail(taskID, containerID, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.markRetryReason = reason
	return nil
}

func (s *stubStore) RecoverExpiredLeases() (int, error) {
	return 0, nil
}

func (s *stubStore) SetContainerStatus(info model.ContainerInfo) error {
	return nil
}

func (s *stubStore) retryReason() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.markRetryReason
}

type failingWorkspace struct{}

func (failingWorkspace) Prepare(task model.Task) (string, error) {
	return "", errors.New("prepare failed")
}

func (failingWorkspace) Persist(task model.Task, runDir string) error {
	return nil
}

func TestRunTaskPrepareFailureDoesNotBlock(t *testing.T) {
	s := &stubStore{}
	r := &Runner{
		cfg: RunnerConfig{
			Store:             s,
			Workspace:         failingWorkspace{},
			LeaseDuration:     30 * time.Second,
			HeartbeatInterval: time.Hour,
		},
	}

	task := model.Task{
		ID:       "tsk_1",
		UserID:   "u1",
		TaskType: "search",
	}

	done := make(chan struct{})
	go func() {
		r.runTask(context.Background(), "container-1", task)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("runTask blocked when workspace prepare failed")
	}

	if reason := s.retryReason(); !strings.Contains(reason, "workspace prepare failed") {
		t.Fatalf("unexpected retry reason: %q", reason)
	}
}

type queueStore struct {
	mu             sync.Mutex
	queue          []model.Task
	completed      int
	cancelOnFinish int
	cancel         context.CancelFunc
}

func (s *queueStore) DequeueForRun(containerID string, leaseDuration time.Duration) (*model.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) == 0 {
		return nil, nil
	}
	task := s.queue[0]
	s.queue = s.queue[1:]
	task.Attempts = 1
	task.ContainerID = containerID
	return &task, nil
}

func (s *queueStore) Heartbeat(taskID, containerID string, leaseDuration time.Duration) error {
	return nil
}

func (s *queueStore) IsCancelRequested(taskID string) (bool, error) {
	return false, nil
}

func (s *queueStore) MarkTaskSucceeded(taskID, containerID string, usage model.TokenUsage, output string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.completed++
	if s.completed >= s.cancelOnFinish && s.cancel != nil {
		s.cancel()
	}
	return nil
}

func (s *queueStore) MarkTaskCanceled(taskID, containerID, reason string) error {
	return nil
}

func (s *queueStore) MarkTaskRetryOrFail(taskID, containerID, reason string) error {
	return nil
}

func (s *queueStore) RecoverExpiredLeases() (int, error) {
	return 0, nil
}

func (s *queueStore) SetContainerStatus(info model.ContainerInfo) error {
	return nil
}

type fastRuntime struct {
	mu       sync.Mutex
	execAt   []time.Time
	taskSeen []string
}

func (r *fastRuntime) Name() string {
	return "fast-runtime"
}

func (r *fastRuntime) Execute(ctx context.Context, containerID string, task model.Task, workspaceDir string) (model.TokenUsage, error) {
	r.mu.Lock()
	r.execAt = append(r.execAt, time.Now())
	r.taskSeen = append(r.taskSeen, task.ID)
	r.mu.Unlock()
	return model.TokenUsage{}, nil
}

func (r *fastRuntime) snapshot() ([]time.Time, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	times := append([]time.Time(nil), r.execAt...)
	tasks := append([]string(nil), r.taskSeen...)
	return times, tasks
}

type tempWorkspace struct{}

func (tempWorkspace) Prepare(task model.Task) (string, error) {
	return os.MkdirTemp("", "runner-workspace-*")
}

func (tempWorkspace) Persist(task model.Task, runDir string) error {
	_ = os.RemoveAll(runDir)
	return nil
}

func TestWorkerLoopDrainsQueueWithoutPollingDelay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := &queueStore{
		queue: []model.Task{
			{ID: "t1", UserID: "u1", TaskType: "search"},
			{ID: "t2", UserID: "u1", TaskType: "search"},
		},
		cancelOnFinish: 2,
		cancel:         cancel,
	}
	rt := &fastRuntime{}
	poll := 300 * time.Millisecond

	r := &Runner{
		cfg: RunnerConfig{
			Store:             store,
			Runtime:           rt,
			Workspace:         tempWorkspace{},
			PollInterval:      poll,
			LeaseDuration:     30 * time.Second,
			HeartbeatInterval: time.Hour,
			Logger:            log.New(io.Discard, "", 0),
		},
	}

	done := make(chan struct{})
	go func() {
		r.workerLoop(ctx, "container-1")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("workerLoop did not stop in time")
	}

	times, tasks := rt.snapshot()
	if len(times) != 2 {
		t.Fatalf("expected 2 executions, got %d", len(times))
	}
	if tasks[0] != "t1" || tasks[1] != "t2" {
		t.Fatalf("unexpected task order: %+v", tasks)
	}
	diff := times[1].Sub(times[0])
	if diff >= poll/2 {
		t.Fatalf("expected second task to start quickly without poll delay, diff=%v poll=%v", diff, poll)
	}
}

type runTaskStore struct {
	mu               sync.Mutex
	heartbeatErr     error
	isCancelErr      error
	retryReason      string
	succeededCalls   int
	canceledCalls    int
	retryOrFailCalls int
}

func (s *runTaskStore) DequeueForRun(containerID string, leaseDuration time.Duration) (*model.Task, error) {
	return nil, nil
}

func (s *runTaskStore) Heartbeat(taskID, containerID string, leaseDuration time.Duration) error {
	return s.heartbeatErr
}

func (s *runTaskStore) IsCancelRequested(taskID string) (bool, error) {
	if s.isCancelErr != nil {
		return false, s.isCancelErr
	}
	return false, nil
}

func (s *runTaskStore) MarkTaskSucceeded(taskID, containerID string, usage model.TokenUsage, output string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.succeededCalls++
	return nil
}

func (s *runTaskStore) MarkTaskCanceled(taskID, containerID, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.canceledCalls++
	return nil
}

func (s *runTaskStore) MarkTaskRetryOrFail(taskID, containerID, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.retryOrFailCalls++
	s.retryReason = reason
	return nil
}

func (s *runTaskStore) RecoverExpiredLeases() (int, error) {
	return 0, nil
}

func (s *runTaskStore) SetContainerStatus(info model.ContainerInfo) error {
	return nil
}

func (s *runTaskStore) snapshot() (retryReason string, succeeded, canceled, retried int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.retryReason, s.succeededCalls, s.canceledCalls, s.retryOrFailCalls
}

type successRuntime struct{}

func (successRuntime) Name() string { return "success-runtime" }

func (successRuntime) Execute(ctx context.Context, containerID string, task model.Task, workspaceDir string) (model.TokenUsage, error) {
	return model.TokenUsage{}, nil
}

type blockingRuntime struct{}

func (blockingRuntime) Name() string { return "blocking-runtime" }

func (blockingRuntime) Execute(ctx context.Context, containerID string, task model.Task, workspaceDir string) (model.TokenUsage, error) {
	<-ctx.Done()
	return model.TokenUsage{}, ctx.Err()
}

func TestRunTaskCancelCheckFailureMarksRetry(t *testing.T) {
	s := &runTaskStore{isCancelErr: errors.New("store unavailable")}
	r := &Runner{
		cfg: RunnerConfig{
			Store:             s,
			Runtime:           successRuntime{},
			Workspace:         tempWorkspace{},
			LeaseDuration:     30 * time.Second,
			HeartbeatInterval: time.Hour,
			Logger:            log.New(io.Discard, "", 0),
		},
	}

	task := model.Task{ID: "t1", UserID: "u1", TaskType: "search"}
	r.runTask(context.Background(), "container-1", task)

	retryReason, succeeded, canceled, retried := s.snapshot()
	if succeeded != 0 {
		t.Fatalf("expected no success mark, got %d", succeeded)
	}
	if canceled != 0 {
		t.Fatalf("expected no canceled mark, got %d", canceled)
	}
	if retried != 1 {
		t.Fatalf("expected one retry/fail mark, got %d", retried)
	}
	if !strings.Contains(retryReason, "check cancel state after execution failed") {
		t.Fatalf("unexpected retry reason: %q", retryReason)
	}
}

func TestRunTaskHeartbeatFailureMarksRetryWithHeartbeatReason(t *testing.T) {
	s := &runTaskStore{heartbeatErr: errors.New("lease update failed")}
	r := &Runner{
		cfg: RunnerConfig{
			Store:             s,
			Runtime:           blockingRuntime{},
			Workspace:         tempWorkspace{},
			LeaseDuration:     30 * time.Second,
			HeartbeatInterval: 5 * time.Millisecond,
			Logger:            log.New(io.Discard, "", 0),
		},
	}

	task := model.Task{ID: "t1", UserID: "u1", TaskType: "search"}
	r.runTask(context.Background(), "container-1", task)

	retryReason, succeeded, canceled, retried := s.snapshot()
	if succeeded != 0 {
		t.Fatalf("expected no success mark, got %d", succeeded)
	}
	if canceled != 0 {
		t.Fatalf("expected no canceled mark, got %d", canceled)
	}
	if retried != 1 {
		t.Fatalf("expected one retry/fail mark, got %d", retried)
	}
	if !strings.Contains(retryReason, "heartbeat failed") {
		t.Fatalf("unexpected retry reason: %q", retryReason)
	}
}
