package cloudclaw

import (
	"fmt"
	"time"

	"cloudclaw/internal/model"
	"cloudclaw/internal/store"
)

type Client struct {
	store *store.Store
}

type Config struct {
	DataDir  string
	DBDriver string
	DBDSN    string
}

func NewClient(cfg Config) (*Client, error) {
	if cfg.DataDir == "" {
		cfg.DataDir = "./data"
	}
	if cfg.DBDriver == "" {
		cfg.DBDriver = "sqlite"
	}
	s, err := store.NewWithConfig(store.Config{
		BaseDir: cfg.DataDir,
		Driver:  cfg.DBDriver,
		DSN:     cfg.DBDSN,
	})
	if err != nil {
		return nil, err
	}
	return &Client{store: s}, nil
}

func (c *Client) Close() error {
	if c == nil || c.store == nil {
		return nil
	}
	return c.store.Close()
}

type SubmitTaskRequest struct {
	UserID     string
	TaskType   string
	Input      string
	MaxRetries int
}

type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type Task struct {
	ID              string      `json:"id"`
	UserID          string      `json:"user_id"`
	TaskType        string      `json:"task_type"`
	Input           string      `json:"input"`
	Priority        int         `json:"priority"`
	Status          string      `json:"status"`
	Attempts        int         `json:"attempts"`
	MaxRetries      int         `json:"max_retries"`
	ContainerID     string      `json:"container_id,omitempty"`
	LeaseUntil      *time.Time  `json:"lease_until,omitempty"`
	ErrorMessage    string      `json:"error_message,omitempty"`
	CancelRequested bool        `json:"cancel_requested,omitempty"`
	CreatedAt       time.Time   `json:"created_at"`
	EnqueuedAt      time.Time   `json:"enqueued_at"`
	UpdatedAt       time.Time   `json:"updated_at"`
	StartedAt       *time.Time  `json:"started_at,omitempty"`
	FinishedAt      *time.Time  `json:"finished_at,omitempty"`
	LastHeartbeatAt *time.Time  `json:"last_heartbeat_at,omitempty"`
	Usage           *TokenUsage `json:"usage,omitempty"`
}

type ContainerInfo struct {
	ID        string    `json:"id"`
	State     string    `json:"state"`
	TaskID    string    `json:"task_id,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

type TaskEvent struct {
	ID          string    `json:"id"`
	TaskID      string    `json:"task_id"`
	FromStatus  string    `json:"from_status"`
	ToStatus    string    `json:"to_status"`
	Reason      string    `json:"reason"`
	ContainerID string    `json:"container_id,omitempty"`
	At          time.Time `json:"at"`
}

func (c *Client) SubmitTask(req SubmitTaskRequest) (Task, error) {
	if req.UserID == "" {
		return Task{}, fmt.Errorf("user_id is required")
	}
	if req.TaskType == "" {
		return Task{}, fmt.Errorf("task_type is required")
	}
	task, err := c.store.SubmitTask(store.SubmitTaskInput{
		UserID:     req.UserID,
		TaskType:   req.TaskType,
		Input:      req.Input,
		MaxRetries: req.MaxRetries,
	})
	if err != nil {
		return Task{}, err
	}
	return toSDKTask(task), nil
}

func (c *Client) GetTask(taskID string) (Task, error) {
	task, err := c.store.GetTask(taskID)
	if err != nil {
		return Task{}, err
	}
	return toSDKTask(task), nil
}

func (c *Client) CancelTask(taskID string) (Task, error) {
	task, err := c.store.CancelTask(taskID)
	if err != nil {
		return Task{}, err
	}
	return toSDKTask(task), nil
}

func (c *Client) QueueLength() (int, error) {
	return c.store.QueueLength()
}

func (c *Client) ContainerStatus() ([]ContainerInfo, error) {
	containers, err := c.store.ListContainers()
	if err != nil {
		return nil, err
	}
	out := make([]ContainerInfo, 0, len(containers))
	for _, c := range containers {
		out = append(out, ContainerInfo{
			ID:        c.ID,
			State:     c.State,
			TaskID:    c.TaskID,
			UpdatedAt: c.UpdatedAt,
		})
	}
	return out, nil
}

func (c *Client) TaskEvents(taskID string) ([]TaskEvent, error) {
	events, err := c.store.ListEvents(taskID)
	if err != nil {
		return nil, err
	}
	out := make([]TaskEvent, 0, len(events))
	for _, e := range events {
		out = append(out, TaskEvent{
			ID:          e.ID,
			TaskID:      e.TaskID,
			FromStatus:  e.FromStatus,
			ToStatus:    e.ToStatus,
			Reason:      e.Reason,
			ContainerID: e.ContainerID,
			At:          e.At,
		})
	}
	return out, nil
}

func toSDKTask(t model.Task) Task {
	task := Task{
		ID:              t.ID,
		UserID:          t.UserID,
		TaskType:        t.TaskType,
		Input:           t.Input,
		Priority:        t.Priority,
		Status:          string(t.Status),
		Attempts:        t.Attempts,
		MaxRetries:      t.MaxRetries,
		ContainerID:     t.ContainerID,
		LeaseUntil:      t.LeaseUntil,
		ErrorMessage:    t.ErrorMessage,
		CancelRequested: t.CancelRequested,
		CreatedAt:       t.CreatedAt,
		EnqueuedAt:      t.EnqueuedAt,
		UpdatedAt:       t.UpdatedAt,
		StartedAt:       t.StartedAt,
		FinishedAt:      t.FinishedAt,
		LastHeartbeatAt: t.LastHeartbeatAt,
	}
	if t.Usage != nil {
		task.Usage = &TokenUsage{
			PromptTokens:     t.Usage.PromptTokens,
			CompletionTokens: t.Usage.CompletionTokens,
			TotalTokens:      t.Usage.TotalTokens,
		}
	}
	return task
}
