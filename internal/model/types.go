package model

import "time"

type TaskStatus string

const (
	StatusQueued   TaskStatus = "QUEUED"
	StatusRunning  TaskStatus = "RUNNING"
	StatusSuccess  TaskStatus = "SUCCEEDED"
	StatusFailed   TaskStatus = "FAILED"
	StatusCanceled TaskStatus = "CANCELED"
)

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
	Priority        int         `json:"priority"` // 0=retry-first, 1=normal
	Status          TaskStatus  `json:"status"`
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

type TaskEvent struct {
	ID          string    `json:"id"`
	TaskID      string    `json:"task_id"`
	FromStatus  string    `json:"from_status"`
	ToStatus    string    `json:"to_status"`
	Reason      string    `json:"reason"`
	ContainerID string    `json:"container_id,omitempty"`
	At          time.Time `json:"at"`
}

type TaskResult struct {
	ID           string      `json:"id"`
	TaskID       string      `json:"task_id"`
	UserID       string      `json:"user_id"`
	TaskType     string      `json:"task_type"`
	ContainerID  string      `json:"container_id,omitempty"`
	Status       TaskStatus  `json:"status"`
	ErrorMessage string      `json:"error_message,omitempty"`
	Output       string      `json:"output"`
	Usage        *TokenUsage `json:"usage,omitempty"`
	CreatedAt    time.Time   `json:"created_at"`
}

type Snapshot struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	TaskID    string    `json:"task_id"`
	Path      string    `json:"path"`
	CreatedAt time.Time `json:"created_at"`
}

type ContainerInfo struct {
	ID        string    `json:"id"`
	State     string    `json:"state"` // idle/running/offline
	TaskID    string    `json:"task_id,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}
