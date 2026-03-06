package store

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestDequeueForRunSerializesSameUser(t *testing.T) {
	s := newTestStore(t)
	t1, err := s.SubmitTask(SubmitTaskInput{UserID: "u1", TaskType: "search", Input: "a", MaxRetries: 1})
	if err != nil {
		t.Fatalf("submit t1: %v", err)
	}
	t2, err := s.SubmitTask(SubmitTaskInput{UserID: "u1", TaskType: "search", Input: "b", MaxRetries: 1})
	if err != nil {
		t.Fatalf("submit t2: %v", err)
	}

	first, err := s.DequeueForRun("container-01", 30*time.Second)
	if err != nil {
		t.Fatalf("dequeue first: %v", err)
	}
	if first == nil || first.ID != t1.ID {
		t.Fatalf("expected first dequeue %s, got %+v", t1.ID, first)
	}

	// Same user should not run concurrently on another container.
	second, err := s.DequeueForRun("container-02", 30*time.Second)
	if err != nil {
		t.Fatalf("dequeue second: %v", err)
	}
	if second != nil {
		t.Fatalf("expected no task due to same-user running lock, got %+v", second)
	}

	if err := s.MarkTaskSucceeded(t1.ID, "container-01", model.TokenUsage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2}, "ok"); err != nil {
		t.Fatalf("mark success: %v", err)
	}

	third, err := s.DequeueForRun("container-02", 30*time.Second)
	if err != nil {
		t.Fatalf("dequeue third: %v", err)
	}
	if third == nil || third.ID != t2.ID {
		t.Fatalf("expected dequeue %s after first finished, got %+v", t2.ID, third)
	}
}

func TestDequeueForRunAllowsDifferentUsersInParallel(t *testing.T) {
	s := newTestStore(t)
	t1, err := s.SubmitTask(SubmitTaskInput{UserID: "u1", TaskType: "search", Input: "a", MaxRetries: 1})
	if err != nil {
		t.Fatalf("submit t1: %v", err)
	}
	t2, err := s.SubmitTask(SubmitTaskInput{UserID: "u2", TaskType: "search", Input: "b", MaxRetries: 1})
	if err != nil {
		t.Fatalf("submit t2: %v", err)
	}

	first, err := s.DequeueForRun("container-01", 30*time.Second)
	if err != nil {
		t.Fatalf("dequeue first: %v", err)
	}
	if first == nil || first.ID != t1.ID {
		t.Fatalf("expected first dequeue %s, got %+v", t1.ID, first)
	}

	second, err := s.DequeueForRun("container-02", 30*time.Second)
	if err != nil {
		t.Fatalf("dequeue second: %v", err)
	}
	if second == nil || second.ID != t2.ID {
		t.Fatalf("expected second dequeue %s, got %+v", t2.ID, second)
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

func TestSubmitTaskValidation(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.SubmitTask(SubmitTaskInput{UserID: "", TaskType: "search"}); err == nil {
		t.Fatal("expected error for empty user id")
	}
	if _, err := s.SubmitTask(SubmitTaskInput{UserID: "u1", TaskType: ""}); err == nil {
		t.Fatal("expected error for empty task type")
	}

	task, err := s.SubmitTask(SubmitTaskInput{UserID: "  u1  ", TaskType: "  search  ", Input: "x"})
	if err != nil {
		t.Fatalf("submit task: %v", err)
	}
	if task.UserID != "u1" {
		t.Fatalf("expected trimmed user_id, got %q", task.UserID)
	}
	if task.TaskType != "search" {
		t.Fatalf("expected trimmed task_type, got %q", task.TaskType)
	}
}

func TestGenIDUniqueness(t *testing.T) {
	const n = 10000
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		id := genID("tsk")
		if !strings.HasPrefix(id, "tsk_") {
			t.Fatalf("unexpected id format: %s", id)
		}
		if _, ok := seen[id]; ok {
			t.Fatalf("duplicate id generated: %s", id)
		}
		seen[id] = struct{}{}
	}
}

func TestGenIDUniquenessConcurrent(t *testing.T) {
	const workers = 8
	const perWorker = 2000
	total := workers * perWorker

	ids := make(chan string, total)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < perWorker; j++ {
				ids <- genID("tsk")
			}
		}()
	}
	wg.Wait()
	close(ids)

	seen := make(map[string]struct{}, total)
	for id := range ids {
		if _, ok := seen[id]; ok {
			t.Fatalf("duplicate id generated concurrently: %s", id)
		}
		seen[id] = struct{}{}
	}
	if len(seen) != total {
		t.Fatalf("expected %d unique ids, got %d", total, len(seen))
	}
}

func TestTaskIDValidation(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.GetTask("   "); err == nil || !strings.Contains(err.Error(), "task id is required") {
		t.Fatalf("expected task id validation error, got: %v", err)
	}
	if _, err := s.CancelTask(" "); err == nil || !strings.Contains(err.Error(), "task id is required") {
		t.Fatalf("expected cancel validation error, got: %v", err)
	}
	if _, err := s.IsCancelRequested("\n"); err == nil || !strings.Contains(err.Error(), "task id is required") {
		t.Fatalf("expected cancel check validation error, got: %v", err)
	}
}

func TestSaveSnapshotValidation(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.SaveSnapshot(" ", "t1", "/tmp/snap"); err == nil || !strings.Contains(err.Error(), "user id is required") {
		t.Fatalf("expected user id validation error, got: %v", err)
	}
	if _, err := s.SaveSnapshot("u1", " ", "/tmp/snap"); err == nil || !strings.Contains(err.Error(), "task id is required") {
		t.Fatalf("expected task id validation error, got: %v", err)
	}
	if _, err := s.SaveSnapshot("u1", "t1", " "); err == nil || !strings.Contains(err.Error(), "snapshot path is required") {
		t.Fatalf("expected path validation error, got: %v", err)
	}
}

func TestUserDataRoundTripThroughDatabase(t *testing.T) {
	s := newTestStore(t)
	src := filepath.Join(t.TempDir(), "src")
	if err := os.MkdirAll(filepath.Join(src, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("A"), 0o644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "nested", "b.txt"), []byte("B"), 0o600); err != nil {
		t.Fatalf("write b.txt: %v", err)
	}

	if err := s.ReplaceUserDataFromDir("u1", src); err != nil {
		t.Fatalf("replace user data: %v", err)
	}

	dst := filepath.Join(t.TempDir(), "dst")
	if err := s.RestoreUserDataToDir("u1", dst); err != nil {
		t.Fatalf("restore user data: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dst, "a.txt")); err != nil || string(b) != "A" {
		t.Fatalf("unexpected a.txt, err=%v val=%q", err, string(b))
	}
	if b, err := os.ReadFile(filepath.Join(dst, "nested", "b.txt")); err != nil || string(b) != "B" {
		t.Fatalf("unexpected b.txt, err=%v val=%q", err, string(b))
	}
}

func TestReplaceUserDataRemovesDeletedFiles(t *testing.T) {
	s := newTestStore(t)
	src := filepath.Join(t.TempDir(), "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	oldFile := filepath.Join(src, "old.txt")
	if err := os.WriteFile(oldFile, []byte("old"), 0o644); err != nil {
		t.Fatalf("write old file: %v", err)
	}
	if err := s.ReplaceUserDataFromDir("u1", src); err != nil {
		t.Fatalf("initial replace user data: %v", err)
	}

	if err := os.Remove(oldFile); err != nil {
		t.Fatalf("remove old file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "new.txt"), []byte("new"), 0o644); err != nil {
		t.Fatalf("write new file: %v", err)
	}
	if err := s.ReplaceUserDataFromDir("u1", src); err != nil {
		t.Fatalf("second replace user data: %v", err)
	}

	dst := filepath.Join(t.TempDir(), "dst")
	if err := s.RestoreUserDataToDir("u1", dst); err != nil {
		t.Fatalf("restore user data: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "old.txt")); err == nil {
		t.Fatal("expected old.txt to be removed after replacement")
	}
	if b, err := os.ReadFile(filepath.Join(dst, "new.txt")); err != nil || string(b) != "new" {
		t.Fatalf("unexpected new.txt, err=%v val=%q", err, string(b))
	}
}

func TestTaskEventRetentionPerTask(t *testing.T) {
	d := t.TempDir()
	s, err := NewWithConfig(Config{
		BaseDir:               d,
		Driver:                "sqlite",
		EventRetentionPerTask: 3,
	})
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	task, err := s.SubmitTask(SubmitTaskInput{UserID: "u1", TaskType: "search", Input: "x", MaxRetries: 10})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	containerID := "container-1"
	for i := 0; i < 4; i++ {
		picked, err := s.DequeueForRun(containerID, 30*time.Second)
		if err != nil {
			t.Fatalf("dequeue #%d: %v", i+1, err)
		}
		if picked == nil {
			t.Fatalf("expected picked task on round %d", i+1)
		}
		if err := s.MarkTaskRetryOrFail(task.ID, containerID, "boom"); err != nil {
			t.Fatalf("mark retry #%d: %v", i+1, err)
		}
	}

	events, err := s.ListEvents(task.ID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events after retention pruning, got %d", len(events))
	}
}

func TestReplaceUserDataRejectsOversizedFile(t *testing.T) {
	d := t.TempDir()
	s, err := NewWithConfig(Config{
		BaseDir:              d,
		Driver:               "sqlite",
		MaxUserDataBytes:     1024,
		MaxUserDataFileBytes: 4,
	})
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	src := filepath.Join(t.TempDir(), "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "big.txt"), []byte("12345"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := s.ReplaceUserDataFromDir("u1", src); err == nil || !strings.Contains(err.Error(), "file too large") {
		t.Fatalf("expected file size limit error, got: %v", err)
	}
}

func TestReplaceUserDataRejectsOversizedTotal(t *testing.T) {
	d := t.TempDir()
	s, err := NewWithConfig(Config{
		BaseDir:              d,
		Driver:               "sqlite",
		MaxUserDataBytes:     6,
		MaxUserDataFileBytes: 10,
	})
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	src := filepath.Join(t.TempDir(), "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("1234"), 0o644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "b.txt"), []byte("1234"), 0o644); err != nil {
		t.Fatalf("write b.txt: %v", err)
	}
	if err := s.ReplaceUserDataFromDir("u1", src); err == nil || !strings.Contains(err.Error(), "total size exceeded") {
		t.Fatalf("expected total size limit error, got: %v", err)
	}
}

func TestPruneOpencodeRuntimeArtifacts(t *testing.T) {
	s := newTestStore(t)
	src := filepath.Join(t.TempDir(), "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}

	write := func(rel, content string) {
		abs := filepath.Join(src, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	write(".opencode-home/.local/share/opencode/auth.json", "auth")
	write(".opencode-home/.local/share/opencode/opencode.db", "db")
	write(".opencode-home/.local/share/opencode/opencode.db-shm", "shm")
	write(".opencode-home/.local/share/opencode/opencode.db-wal", "wal")
	write(".opencode-home/.local/share/opencode/bin/cache", "bin")
	write(".opencode-home/.local/share/opencode/log/1.log", "log")
	write(".opencode-home/.local/share/opencode/snapshot/a", "snapshot")
	write(".opencode-home/.local/share/opencode/storage/a", "storage")
	write(".opencode-home/.local/share/opencode/tool-output/a", "tool")
	write("workspace.txt", "ok")

	if err := s.ReplaceUserDataFromDir("u1", src); err != nil {
		t.Fatalf("replace user data: %v", err)
	}

	deleted, err := s.PruneOpencodeRuntimeArtifacts()
	if err != nil {
		t.Fatalf("prune opencode artifacts: %v", err)
	}
	if deleted == 0 {
		t.Fatal("expected deleted rows > 0")
	}

	dst := filepath.Join(t.TempDir(), "dst")
	if err := s.RestoreUserDataToDir("u1", dst); err != nil {
		t.Fatalf("restore user data: %v", err)
	}

	for _, removed := range []string{
		".opencode-home/.local/share/opencode/opencode.db-shm",
		".opencode-home/.local/share/opencode/opencode.db-wal",
		".opencode-home/.local/share/opencode/bin/cache",
		".opencode-home/.local/share/opencode/log/1.log",
		".opencode-home/.local/share/opencode/snapshot/a",
		".opencode-home/.local/share/opencode/storage/a",
		".opencode-home/.local/share/opencode/tool-output/a",
	} {
		if _, err := os.Stat(filepath.Join(dst, filepath.FromSlash(removed))); err == nil {
			t.Fatalf("expected %s removed", removed)
		}
	}

	for _, kept := range []string{
		".opencode-home/.local/share/opencode/auth.json",
		".opencode-home/.local/share/opencode/opencode.db",
		"workspace.txt",
	} {
		if _, err := os.Stat(filepath.Join(dst, filepath.FromSlash(kept))); err != nil {
			t.Fatalf("expected %s kept, err=%v", kept, err)
		}
	}
}

func TestDequeueTaskResultsSuccess(t *testing.T) {
	s := newTestStore(t)
	task, err := s.SubmitTask(SubmitTaskInput{
		UserID:     "u1",
		TaskType:   "smoke",
		Input:      "hello",
		MaxRetries: 0,
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	picked, err := s.DequeueForRun("container-1", 30*time.Second)
	if err != nil {
		t.Fatalf("dequeue: %v", err)
	}
	if picked == nil || picked.ID != task.ID {
		t.Fatalf("unexpected picked task: %+v", picked)
	}

	usage := model.TokenUsage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3}
	if err := s.MarkTaskSucceeded(task.ID, "container-1", usage, "done"); err != nil {
		t.Fatalf("mark success: %v", err)
	}

	items, err := s.DequeueTaskResults(10)
	if err != nil {
		t.Fatalf("dequeue results: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 result item, got %d", len(items))
	}
	got := items[0]
	if got.TaskID != task.ID || got.UserID != "u1" || got.TaskType != "smoke" {
		t.Fatalf("unexpected result identity: %+v", got)
	}
	if got.Status != model.StatusSuccess {
		t.Fatalf("expected success result, got %s", got.Status)
	}
	if got.Output != "done" {
		t.Fatalf("unexpected output: %q", got.Output)
	}
	if got.Usage == nil || got.Usage.TotalTokens != 3 {
		t.Fatalf("unexpected usage: %+v", got.Usage)
	}

	again, err := s.DequeueTaskResults(10)
	if err != nil {
		t.Fatalf("dequeue results second time: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("expected empty second dequeue, got %d", len(again))
	}
}

func TestDequeueTaskResultsRetryDoesNotEmitTerminalResult(t *testing.T) {
	s := newTestStore(t)
	task, err := s.SubmitTask(SubmitTaskInput{
		UserID:     "u1",
		TaskType:   "smoke",
		Input:      "hello",
		MaxRetries: 2,
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	picked, err := s.DequeueForRun("container-1", 30*time.Second)
	if err != nil {
		t.Fatalf("dequeue: %v", err)
	}
	if picked == nil || picked.ID != task.ID {
		t.Fatalf("unexpected picked task: %+v", picked)
	}

	if err := s.MarkTaskRetryOrFail(task.ID, "container-1", "boom"); err != nil {
		t.Fatalf("mark retry: %v", err)
	}
	items, err := s.DequeueTaskResults(10)
	if err != nil {
		t.Fatalf("dequeue results: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no terminal result for retry, got %d", len(items))
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
