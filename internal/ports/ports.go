package ports

import (
	"context"
	"time"

	"cloudclaw/internal/model"
)

type TaskStore interface {
	DequeueForRun(containerID string, leaseDuration time.Duration) (*model.Task, error)
	Heartbeat(taskID, containerID string, leaseDuration time.Duration) error
	IsCancelRequested(taskID string) (bool, error)
	MarkTaskSucceeded(taskID, containerID string, usage model.TokenUsage, output string) error
	MarkTaskCanceled(taskID, containerID, reason string) error
	MarkTaskRetryOrFail(taskID, containerID, reason string) error
	RecoverExpiredLeases() (int, error)
	SetContainerStatus(info model.ContainerInfo) error
}

type RuntimeAdapter interface {
	Name() string
	Execute(ctx context.Context, containerID string, task model.Task, workspaceDir string) (model.TokenUsage, error)
}

type WorkspaceManager interface {
	Prepare(task model.Task) (string, error)
	Persist(task model.Task, runDir string) error
}

type PoolAdapter interface {
	Name() string
	ContainerIDs(ctx context.Context) ([]string, error)
	Reconcile(ctx context.Context) error
}
