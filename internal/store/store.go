package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"

	"cloudclaw/internal/fsutil"
	"cloudclaw/internal/model"
)

type SubmitTaskInput struct {
	UserID     string
	TaskType   string
	Input      string
	MaxRetries int
}

type diskState struct {
	Tasks      map[string]model.Task          `json:"tasks"`
	Containers map[string]model.ContainerInfo `json:"containers"`
	Snapshots  map[string]model.Snapshot      `json:"snapshots"`
}

type Store struct {
	baseDir    string
	statePath  string
	eventsPath string
	lockPath   string
	mu         sync.Mutex
}

func New(baseDir string) (*Store, error) {
	if baseDir == "" {
		return nil, errors.New("base dir is required")
	}
	if err := fsutil.EnsureDir(baseDir); err != nil {
		return nil, err
	}
	for _, p := range []string{
		filepath.Join(baseDir, "users"),
		filepath.Join(baseDir, "runs"),
		filepath.Join(baseDir, "snapshots"),
	} {
		if err := fsutil.EnsureDir(p); err != nil {
			return nil, err
		}
	}

	s := &Store{
		baseDir:    baseDir,
		statePath:  filepath.Join(baseDir, "state.json"),
		eventsPath: filepath.Join(baseDir, "task_events.jsonl"),
		lockPath:   filepath.Join(baseDir, "store.lock"),
	}
	if err := s.ensureInitialized(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) BaseDir() string {
	return s.baseDir
}

func (s *Store) UserDataDir(userID string) string {
	return filepath.Join(s.baseDir, "users", userID, "data")
}

func (s *Store) UserSnapshotBaseDir(userID string) string {
	return filepath.Join(s.baseDir, "snapshots", userID)
}

func (s *Store) RunDir(taskID string, attempt int) string {
	return filepath.Join(s.baseDir, "runs", fmt.Sprintf("%s-attempt-%d", taskID, attempt))
}

func (s *Store) ensureInitialized() error {
	return s.withLockedState(func(state *diskState) error {
		_ = state
		return nil
	})
}

func (s *Store) SubmitTask(in SubmitTaskInput) (model.Task, error) {
	now := time.Now().UTC()
	task := model.Task{
		ID:         genID("tsk"),
		UserID:     in.UserID,
		TaskType:   in.TaskType,
		Input:      in.Input,
		Priority:   1,
		Status:     model.StatusQueued,
		Attempts:   0,
		MaxRetries: max(0, in.MaxRetries),
		CreatedAt:  now,
		EnqueuedAt: now,
		UpdatedAt:  now,
	}

	err := s.withLockedState(func(state *diskState) error {
		state.Tasks[task.ID] = task
		return s.appendEventLocked(model.TaskEvent{
			ID:         genID("evt"),
			TaskID:     task.ID,
			FromStatus: "",
			ToStatus:   string(model.StatusQueued),
			Reason:     "task submitted",
			At:         now,
		})
	})
	return task, err
}

func (s *Store) GetTask(taskID string) (model.Task, error) {
	var out model.Task
	err := s.withLockedState(func(state *diskState) error {
		t, ok := state.Tasks[taskID]
		if !ok {
			return fmt.Errorf("task %s not found", taskID)
		}
		out = t
		return nil
	})
	return out, err
}

func (s *Store) QueueLength() (int, error) {
	total := 0
	err := s.withLockedState(func(state *diskState) error {
		for _, t := range state.Tasks {
			if t.Status == model.StatusQueued {
				total++
			}
		}
		return nil
	})
	return total, err
}

func (s *Store) CancelTask(taskID string) (model.Task, error) {
	now := time.Now().UTC()
	var out model.Task
	err := s.withLockedState(func(state *diskState) error {
		t, ok := state.Tasks[taskID]
		if !ok {
			return fmt.Errorf("task %s not found", taskID)
		}

		switch t.Status {
		case model.StatusQueued:
			from := t.Status
			t.Status = model.StatusCanceled
			t.UpdatedAt = now
			t.FinishedAt = &now
			t.ErrorMessage = "canceled by user"
			state.Tasks[taskID] = t
			if err := s.appendEventLocked(model.TaskEvent{
				ID:         genID("evt"),
				TaskID:     taskID,
				FromStatus: string(from),
				ToStatus:   string(model.StatusCanceled),
				Reason:     "task canceled before start",
				At:         now,
			}); err != nil {
				return err
			}
		case model.StatusRunning:
			t.CancelRequested = true
			t.UpdatedAt = now
			state.Tasks[taskID] = t
			if err := s.appendEventLocked(model.TaskEvent{
				ID:          genID("evt"),
				TaskID:      taskID,
				FromStatus:  string(model.StatusRunning),
				ToStatus:    string(model.StatusRunning),
				Reason:      "cancel requested",
				ContainerID: t.ContainerID,
				At:          now,
			}); err != nil {
				return err
			}
		case model.StatusSuccess, model.StatusFailed, model.StatusCanceled:
			return fmt.Errorf("task already terminal: %s", t.Status)
		}
		out = state.Tasks[taskID]
		return nil
	})
	return out, err
}

func (s *Store) DequeueForRun(containerID string, leaseDuration time.Duration) (*model.Task, error) {
	now := time.Now().UTC()
	var out *model.Task

	err := s.withLockedState(func(state *diskState) error {
		queued := make([]model.Task, 0, len(state.Tasks))
		for _, t := range state.Tasks {
			if t.Status == model.StatusQueued {
				queued = append(queued, t)
			}
		}
		if len(queued) == 0 {
			return nil
		}
		sort.Slice(queued, func(i, j int) bool {
			if queued[i].Priority != queued[j].Priority {
				return queued[i].Priority < queued[j].Priority
			}
			if !queued[i].EnqueuedAt.Equal(queued[j].EnqueuedAt) {
				return queued[i].EnqueuedAt.Before(queued[j].EnqueuedAt)
			}
			return queued[i].CreatedAt.Before(queued[j].CreatedAt)
		})

		picked := queued[0]
		picked.Status = model.StatusRunning
		picked.Attempts++
		picked.ContainerID = containerID
		picked.UpdatedAt = now
		picked.CancelRequested = false
		if picked.StartedAt == nil {
			picked.StartedAt = &now
		}
		leaseUntil := now.Add(leaseDuration)
		picked.LeaseUntil = &leaseUntil
		picked.LastHeartbeatAt = &now
		state.Tasks[picked.ID] = picked

		if err := s.appendEventLocked(model.TaskEvent{
			ID:          genID("evt"),
			TaskID:      picked.ID,
			FromStatus:  string(model.StatusQueued),
			ToStatus:    string(model.StatusRunning),
			Reason:      "assigned to container",
			ContainerID: containerID,
			At:          now,
		}); err != nil {
			return err
		}

		t := picked
		out = &t
		return nil
	})
	return out, err
}

func (s *Store) Heartbeat(taskID, containerID string, leaseDuration time.Duration) error {
	now := time.Now().UTC()
	return s.withLockedState(func(state *diskState) error {
		t, ok := state.Tasks[taskID]
		if !ok {
			return fmt.Errorf("task %s not found", taskID)
		}
		if t.Status != model.StatusRunning {
			return fmt.Errorf("task %s not running", taskID)
		}
		if t.ContainerID != containerID {
			return fmt.Errorf("task %s owned by container %s", taskID, t.ContainerID)
		}
		leaseUntil := now.Add(leaseDuration)
		t.LeaseUntil = &leaseUntil
		t.LastHeartbeatAt = &now
		t.UpdatedAt = now
		state.Tasks[taskID] = t
		return nil
	})
}

func (s *Store) IsCancelRequested(taskID string) (bool, error) {
	var requested bool
	err := s.withLockedState(func(state *diskState) error {
		t, ok := state.Tasks[taskID]
		if !ok {
			return fmt.Errorf("task %s not found", taskID)
		}
		requested = t.CancelRequested
		return nil
	})
	return requested, err
}

func (s *Store) MarkTaskSucceeded(taskID, containerID string, usage model.TokenUsage) error {
	now := time.Now().UTC()
	return s.withLockedState(func(state *diskState) error {
		t, ok := state.Tasks[taskID]
		if !ok {
			return fmt.Errorf("task %s not found", taskID)
		}
		if t.Status != model.StatusRunning {
			return fmt.Errorf("task %s not running", taskID)
		}
		if t.ContainerID != containerID {
			return fmt.Errorf("task %s owned by container %s", taskID, t.ContainerID)
		}
		from := t.Status
		t.Status = model.StatusSuccess
		t.Usage = &usage
		t.ErrorMessage = ""
		t.UpdatedAt = now
		t.FinishedAt = &now
		t.LeaseUntil = nil
		t.ContainerID = ""
		t.CancelRequested = false
		state.Tasks[taskID] = t
		return s.appendEventLocked(model.TaskEvent{
			ID:         genID("evt"),
			TaskID:     taskID,
			FromStatus: string(from),
			ToStatus:   string(model.StatusSuccess),
			Reason:     "task completed",
			At:         now,
		})
	})
}

func (s *Store) MarkTaskCanceled(taskID, containerID, reason string) error {
	now := time.Now().UTC()
	return s.withLockedState(func(state *diskState) error {
		t, ok := state.Tasks[taskID]
		if !ok {
			return fmt.Errorf("task %s not found", taskID)
		}
		if t.Status != model.StatusRunning {
			return fmt.Errorf("task %s not running", taskID)
		}
		if t.ContainerID != containerID {
			return fmt.Errorf("task %s owned by container %s", taskID, t.ContainerID)
		}
		from := t.Status
		t.Status = model.StatusCanceled
		t.ErrorMessage = reason
		t.UpdatedAt = now
		t.FinishedAt = &now
		t.LeaseUntil = nil
		t.ContainerID = ""
		t.CancelRequested = false
		state.Tasks[taskID] = t
		return s.appendEventLocked(model.TaskEvent{
			ID:         genID("evt"),
			TaskID:     taskID,
			FromStatus: string(from),
			ToStatus:   string(model.StatusCanceled),
			Reason:     reason,
			At:         now,
		})
	})
}

func (s *Store) MarkTaskRetryOrFail(taskID, containerID, reason string) error {
	now := time.Now().UTC()
	return s.withLockedState(func(state *diskState) error {
		t, ok := state.Tasks[taskID]
		if !ok {
			return fmt.Errorf("task %s not found", taskID)
		}
		if t.Status != model.StatusRunning {
			return fmt.Errorf("task %s not running", taskID)
		}
		if t.ContainerID != containerID {
			return fmt.Errorf("task %s owned by container %s", taskID, t.ContainerID)
		}
		from := t.Status
		maxAttempts := t.MaxRetries + 1
		if t.Attempts < maxAttempts {
			t.Status = model.StatusQueued
			t.Priority = 0
			t.EnqueuedAt = now
			t.ErrorMessage = reason
			t.UpdatedAt = now
			t.LeaseUntil = nil
			t.ContainerID = ""
			t.CancelRequested = false
			state.Tasks[taskID] = t
			return s.appendEventLocked(model.TaskEvent{
				ID:         genID("evt"),
				TaskID:     taskID,
				FromStatus: string(from),
				ToStatus:   string(model.StatusQueued),
				Reason:     fmt.Sprintf("retry scheduled: %s", reason),
				At:         now,
			})
		}

		t.Status = model.StatusFailed
		t.ErrorMessage = reason
		t.UpdatedAt = now
		t.FinishedAt = &now
		t.LeaseUntil = nil
		t.ContainerID = ""
		t.CancelRequested = false
		state.Tasks[taskID] = t
		return s.appendEventLocked(model.TaskEvent{
			ID:         genID("evt"),
			TaskID:     taskID,
			FromStatus: string(from),
			ToStatus:   string(model.StatusFailed),
			Reason:     reason,
			At:         now,
		})
	})
}

func (s *Store) RecoverExpiredLeases() (int, error) {
	now := time.Now().UTC()
	count := 0
	err := s.withLockedState(func(state *diskState) error {
		for id, t := range state.Tasks {
			if t.Status != model.StatusRunning || t.LeaseUntil == nil {
				continue
			}
			if t.LeaseUntil.After(now) {
				continue
			}
			t.Status = model.StatusQueued
			t.Priority = 0
			t.EnqueuedAt = now
			t.ErrorMessage = "lease expired, requeued"
			t.UpdatedAt = now
			t.ContainerID = ""
			t.LeaseUntil = nil
			t.CancelRequested = false
			state.Tasks[id] = t
			count++
			if err := s.appendEventLocked(model.TaskEvent{
				ID:         genID("evt"),
				TaskID:     id,
				FromStatus: string(model.StatusRunning),
				ToStatus:   string(model.StatusQueued),
				Reason:     "lease expired",
				At:         now,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	return count, err
}

func (s *Store) SaveSnapshot(userID, taskID, path string) (model.Snapshot, error) {
	now := time.Now().UTC()
	snap := model.Snapshot{
		ID:        genID("snap"),
		UserID:    userID,
		TaskID:    taskID,
		Path:      path,
		CreatedAt: now,
	}
	err := s.withLockedState(func(state *diskState) error {
		state.Snapshots[userID] = snap
		return nil
	})
	return snap, err
}

func (s *Store) LatestSnapshot(userID string) (model.Snapshot, bool, error) {
	var snap model.Snapshot
	var ok bool
	err := s.withLockedState(func(state *diskState) error {
		snap, ok = state.Snapshots[userID]
		return nil
	})
	return snap, ok, err
}

func (s *Store) SetContainerStatus(info model.ContainerInfo) error {
	return s.withLockedState(func(state *diskState) error {
		state.Containers[info.ID] = info
		return nil
	})
}

func (s *Store) ListContainers() ([]model.ContainerInfo, error) {
	list := []model.ContainerInfo{}
	err := s.withLockedState(func(state *diskState) error {
		list = make([]model.ContainerInfo, 0, len(state.Containers))
		for _, c := range state.Containers {
			list = append(list, c)
		}
		sort.Slice(list, func(i, j int) bool {
			return list[i].ID < list[j].ID
		})
		return nil
	})
	return list, err
}

func (s *Store) ListEvents(taskID string) ([]model.TaskEvent, error) {
	f, err := os.Open(s.eventsPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := []model.TaskEvent{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var evt model.TaskEvent
		if err := json.Unmarshal(line, &evt); err != nil {
			continue
		}
		if taskID == "" || evt.TaskID == taskID {
			out = append(out, evt)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) withLockedState(fn func(state *diskState) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	lockFile, err := os.OpenFile(s.lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer lockFile.Close()

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)

	state, err := s.loadStateLocked()
	if err != nil {
		return err
	}
	if err := fn(state); err != nil {
		return err
	}
	return s.saveStateLocked(state)
}

func (s *Store) loadStateLocked() (*diskState, error) {
	b, err := os.ReadFile(s.statePath)
	if errors.Is(err, os.ErrNotExist) {
		return &diskState{
			Tasks:      map[string]model.Task{},
			Containers: map[string]model.ContainerInfo{},
			Snapshots:  map[string]model.Snapshot{},
		}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return &diskState{
			Tasks:      map[string]model.Task{},
			Containers: map[string]model.ContainerInfo{},
			Snapshots:  map[string]model.Snapshot{},
		}, nil
	}
	var state diskState
	if err := json.Unmarshal(b, &state); err != nil {
		return nil, err
	}
	if state.Tasks == nil {
		state.Tasks = map[string]model.Task{}
	}
	if state.Containers == nil {
		state.Containers = map[string]model.ContainerInfo{}
	}
	if state.Snapshots == nil {
		state.Snapshots = map[string]model.Snapshot{}
	}
	return &state, nil
}

func (s *Store) saveStateLocked(state *diskState) error {
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := s.statePath + ".tmp"
	if err := os.WriteFile(tmpPath, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, s.statePath)
}

func (s *Store) appendEventLocked(evt model.TaskEvent) error {
	f, err := os.OpenFile(s.eventsPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	b, err := json.Marshal(evt)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

func genID(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UTC().UnixNano())
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
