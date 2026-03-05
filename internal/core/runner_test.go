package core

import (
	"context"
	"errors"
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

func (s *stubStore) MarkTaskSucceeded(taskID, containerID string, usage model.TokenUsage) error {
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
