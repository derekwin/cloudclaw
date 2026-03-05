package store

import (
	"testing"
	"time"

	"cloudclaw/internal/model"
)

func TestRetryTaskPrioritized(t *testing.T) {
	s := newTestStore(t)

	t1, err := s.SubmitTask(SubmitTaskInput{UserID: "u1", TaskType: "search", Input: "a", MaxRetries: 2})
	if err != nil {
		t.Fatalf("submit t1: %v", err)
	}
	t2, err := s.SubmitTask(SubmitTaskInput{UserID: "u2", TaskType: "search", Input: "b", MaxRetries: 2})
	if err != nil {
		t.Fatalf("submit t2: %v", err)
	}

	picked, err := s.DequeueForRun("container-01", 30*time.Second)
	if err != nil {
		t.Fatalf("dequeue first: %v", err)
	}
	if picked == nil || picked.ID != t1.ID {
		t.Fatalf("expected first dequeue %s, got %+v", t1.ID, picked)
	}

	if err := s.MarkTaskRetryOrFail(t1.ID, "container-01", "boom"); err != nil {
		t.Fatalf("mark retry: %v", err)
	}

	next, err := s.DequeueForRun("container-01", 30*time.Second)
	if err != nil {
		t.Fatalf("dequeue second: %v", err)
	}
	if next == nil || next.ID != t1.ID {
		t.Fatalf("expected retry task %s ahead of %s, got %+v", t1.ID, t2.ID, next)
	}

	task, err := s.GetTask(t1.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.Attempts != 2 {
		t.Fatalf("expected attempts=2, got %d", task.Attempts)
	}
}

func TestRecoverExpiredLease(t *testing.T) {
	s := newTestStore(t)

	t1, err := s.SubmitTask(SubmitTaskInput{UserID: "u1", TaskType: "search", Input: "x", MaxRetries: 1})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	picked, err := s.DequeueForRun("container-01", 5*time.Millisecond)
	if err != nil {
		t.Fatalf("dequeue: %v", err)
	}
	if picked == nil {
		t.Fatal("expected a picked task")
	}

	time.Sleep(8 * time.Millisecond)
	recovered, err := s.RecoverExpiredLeases()
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("expected recovered=1, got %d", recovered)
	}

	task, err := s.GetTask(t1.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.Status != model.StatusQueued {
		t.Fatalf("expected status queued after recover, got %s", task.Status)
	}
	if task.Priority != 0 {
		t.Fatalf("expected retry priority=0, got %d", task.Priority)
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	d := t.TempDir()
	s, err := New(d)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return s
}
