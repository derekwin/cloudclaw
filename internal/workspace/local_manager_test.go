package workspace

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"cloudclaw/internal/model"
)

type captureStore struct {
	replacedFiles []string
	runDir        string
	restoreCalls  int
	replaceCalls  int
}

func (s *captureStore) RunDir(taskID string, attempt int) string { return s.runDir }

func (s *captureStore) SaveSnapshot(userID, taskID, path string) (model.Snapshot, error) {
	return model.Snapshot{UserID: userID, TaskID: taskID, Path: path}, nil
}

func (s *captureStore) RestoreUserDataToDir(userID, dstDir string) error {
	s.restoreCalls++
	return nil
}

func (s *captureStore) ReplaceUserDataFromDir(userID, srcDir string) error {
	s.replaceCalls++
	files := make([]string, 0, 16)
	err := filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(files)
	s.replacedFiles = files
	return nil
}

func TestPersistPrunesTransientArtifactsInCopyMode(t *testing.T) {
	store := &captureStore{}
	manager, err := NewLocalManager(LocalManagerConfig{
		Store:            store,
		RuntimeName:      "opencode",
		SharedSkillsMode: "copy",
	})
	if err != nil {
		t.Fatalf("new local manager: %v", err)
	}

	runDir := t.TempDir()
	mustWrite := func(rel string) {
		abs := filepath.Join(runDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	mustWrite(".cloudclaw_shared_skills/s1.md")
	mustWrite(".opencode-home/.local/share/opencode/auth.json")
	mustWrite(".opencode-home/.local/share/opencode/opencode.db")
	mustWrite(".opencode-home/.local/share/opencode/opencode.db-shm")
	mustWrite(".opencode-home/.local/share/opencode/opencode.db-wal")
	mustWrite(".opencode-home/.local/share/opencode/bin/cache.txt")
	mustWrite(".opencode-home/.local/share/opencode/log/1.log")
	mustWrite(".opencode-home/.local/share/opencode/snapshot/a")
	mustWrite(".opencode-home/.local/share/opencode/storage/a")
	mustWrite(".opencode-home/.local/share/opencode/tool-output/a")
	mustWrite("workspace.txt")

	if err := manager.Persist(model.Task{ID: "t1", UserID: "u1"}, runDir); err != nil {
		t.Fatalf("persist: %v", err)
	}

	got := make(map[string]struct{}, len(store.replacedFiles))
	for _, f := range store.replacedFiles {
		got[f] = struct{}{}
	}

	for _, removed := range []string{
		".cloudclaw_shared_skills/s1.md",
		".opencode-home/.local/share/opencode/opencode.db-shm",
		".opencode-home/.local/share/opencode/opencode.db-wal",
		".opencode-home/.local/share/opencode/bin/cache.txt",
		".opencode-home/.local/share/opencode/log/1.log",
		".opencode-home/.local/share/opencode/snapshot/a",
		".opencode-home/.local/share/opencode/storage/a",
		".opencode-home/.local/share/opencode/tool-output/a",
	} {
		if _, ok := got[removed]; ok {
			t.Fatalf("expected %s to be pruned, got files=%v", removed, store.replacedFiles)
		}
	}

	for _, kept := range []string{
		".opencode-home/.local/share/opencode/auth.json",
		".opencode-home/.local/share/opencode/opencode.db",
		"workspace.txt",
	} {
		if _, ok := got[kept]; !ok {
			t.Fatalf("expected %s to be kept, got files=%v", kept, store.replacedFiles)
		}
	}
}

func TestPersistPrunesOpenclawRuntimeArtifactsInCopyMode(t *testing.T) {
	store := &captureStore{}
	manager, err := NewLocalManager(LocalManagerConfig{
		Store:            store,
		RuntimeName:      "openclaw",
		SharedSkillsMode: "copy",
	})
	if err != nil {
		t.Fatalf("new local manager: %v", err)
	}

	runDir := t.TempDir()
	mustWrite := func(rel string) {
		abs := filepath.Join(runDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	mustWrite(".openclaw-home/.local/share/openclaw/auth.json")
	mustWrite(".openclaw-home/.local/share/openclaw/openclaw.db")
	mustWrite(".openclaw-home/.local/share/openclaw/openclaw.db-shm")
	mustWrite(".openclaw-home/.local/share/openclaw/openclaw.db-wal")
	mustWrite(".openclaw-home/.local/share/openclaw/bin/cache.txt")
	mustWrite(".openclaw-home/.local/share/openclaw/log/1.log")
	mustWrite("workspace.txt")

	if err := manager.Persist(model.Task{ID: "to1", UserID: "u1"}, runDir); err != nil {
		t.Fatalf("persist: %v", err)
	}

	got := make(map[string]struct{}, len(store.replacedFiles))
	for _, f := range store.replacedFiles {
		got[f] = struct{}{}
	}

	for _, removed := range []string{
		".openclaw-home/.local/share/openclaw/openclaw.db-shm",
		".openclaw-home/.local/share/openclaw/openclaw.db-wal",
		".openclaw-home/.local/share/openclaw/bin/cache.txt",
		".openclaw-home/.local/share/openclaw/log/1.log",
	} {
		if _, ok := got[removed]; ok {
			t.Fatalf("expected %s to be pruned, got files=%v", removed, store.replacedFiles)
		}
	}

	for _, kept := range []string{
		".openclaw-home/.local/share/openclaw/auth.json",
		".openclaw-home/.local/share/openclaw/openclaw.db",
		"workspace.txt",
	} {
		if _, ok := got[kept]; !ok {
			t.Fatalf("expected %s to be kept, got files=%v", kept, store.replacedFiles)
		}
	}
}

func TestPrepareAndPersistEphemeralWorkspaceState(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "run")
	store := &captureStore{runDir: runDir}
	manager, err := NewLocalManager(LocalManagerConfig{
		Store:          store,
		RuntimeName:    "opencode",
		WorkspaceState: "ephemeral",
	})
	if err != nil {
		t.Fatalf("new local manager: %v", err)
	}

	stale := filepath.Join(runDir, "stale.txt")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(stale, []byte("stale"), 0o644); err != nil {
		t.Fatalf("write stale: %v", err)
	}

	prepared, err := manager.Prepare(model.Task{ID: "t3", UserID: "u1", Attempts: 1})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if prepared != runDir {
		t.Fatalf("expected run dir %s, got %s", runDir, prepared)
	}
	if store.restoreCalls != 0 {
		t.Fatalf("expected no DB restore call in ephemeral mode, got %d", store.restoreCalls)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("expected stale file cleaned, err=%v", err)
	}

	if err := manager.Persist(model.Task{ID: "t3", UserID: "u1"}, runDir); err != nil {
		t.Fatalf("persist: %v", err)
	}
	if store.replaceCalls != 0 {
		t.Fatalf("expected no DB replace call in ephemeral mode, got %d", store.replaceCalls)
	}
}

func TestPersistKeepsSharedSkillsInMountMode(t *testing.T) {
	store := &captureStore{}
	manager, err := NewLocalManager(LocalManagerConfig{
		Store:            store,
		RuntimeName:      "opencode",
		SharedSkillsMode: "mount",
	})
	if err != nil {
		t.Fatalf("new local manager: %v", err)
	}

	runDir := t.TempDir()
	path := filepath.Join(runDir, ".cloudclaw_shared_skills", "s1.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("skill"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := manager.Persist(model.Task{ID: "t2", UserID: "u1"}, runDir); err != nil {
		t.Fatalf("persist: %v", err)
	}

	found := false
	for _, f := range store.replacedFiles {
		if f == ".cloudclaw_shared_skills/s1.md" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected shared skills file to remain in mount mode, got %v", store.replacedFiles)
	}
}

func TestEphemeralModeCopiesUserRuntimeInAndOut(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "t1-attempt-1")
	userRuntimeDir := filepath.Join(root, "user-runtime")

	store := &captureStore{runDir: runDir}
	manager, err := NewLocalManager(LocalManagerConfig{
		Store:          store,
		RuntimeName:    "opencode",
		WorkspaceState: "ephemeral",
		UserRuntimeDir: userRuntimeDir,
	})
	if err != nil {
		t.Fatalf("new local manager: %v", err)
	}

	userDir := filepath.Join(userRuntimeDir, safeUserRuntimeName("u:test"), "opencode")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatalf("mkdir user runtime: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userDir, "opencode.db"), []byte("db1"), 0o644); err != nil {
		t.Fatalf("write user runtime seed: %v", err)
	}

	prepared, err := manager.Prepare(model.Task{ID: "t1", UserID: "u:test", Attempts: 1})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if prepared != runDir {
		t.Fatalf("expected run dir %s, got %s", runDir, prepared)
	}
	runState := filepath.Join(runDir, ".opencode-home", ".local", "share", "opencode")
	if _, err := os.Stat(filepath.Join(runState, "opencode.db")); err != nil {
		t.Fatalf("expected runtime copied into run dir, err=%v", err)
	}

	if err := os.WriteFile(filepath.Join(runState, "snapshot.txt"), []byte("snap"), 0o644); err != nil {
		t.Fatalf("write run state mutation: %v", err)
	}

	if err := manager.Persist(model.Task{ID: "t1", UserID: "u:test"}, runDir); err != nil {
		t.Fatalf("persist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(userDir, "snapshot.txt")); err != nil {
		t.Fatalf("expected runtime copied back to user runtime dir, err=%v", err)
	}
	if store.replaceCalls != 0 {
		t.Fatalf("expected no DB replace call in ephemeral user-runtime mode, got %d", store.replaceCalls)
	}
}

func TestSafeUserRuntimeName(t *testing.T) {
	got := safeUserRuntimeName("  u/a:b  ")
	if !strings.HasPrefix(got, "u_a_b-") {
		t.Fatalf("unexpected normalized user runtime name: %s", got)
	}
}

func TestEphemeralPersistKeepsExistingRuntimeWhenRunStateMissing(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "t2-attempt-1")
	userRuntimeDir := filepath.Join(root, "user-runtime")
	userID := "u-missing"

	store := &captureStore{runDir: runDir}
	manager, err := NewLocalManager(LocalManagerConfig{
		Store:          store,
		RuntimeName:    "opencode",
		WorkspaceState: "ephemeral",
		UserRuntimeDir: userRuntimeDir,
	})
	if err != nil {
		t.Fatalf("new local manager: %v", err)
	}

	userDir := filepath.Join(userRuntimeDir, safeUserRuntimeName(userID), "opencode")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatalf("mkdir user runtime: %v", err)
	}
	sentinel := filepath.Join(userDir, "auth.json")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o644); err != nil {
		t.Fatalf("seed user runtime: %v", err)
	}

	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir run dir: %v", err)
	}
	if err := manager.Persist(model.Task{ID: "t2", UserID: userID}, runDir); err != nil {
		t.Fatalf("persist: %v", err)
	}

	b, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf("read sentinel: %v", err)
	}
	if string(b) != "keep" {
		t.Fatalf("unexpected sentinel content after persist: %q", string(b))
	}
}

func TestEphemeralModeCopiesClaudeRuntimeInAndOut(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "tc-attempt-1")
	userRuntimeDir := filepath.Join(root, "user-runtime")

	store := &captureStore{runDir: runDir}
	manager, err := NewLocalManager(LocalManagerConfig{
		Store:          store,
		RuntimeName:    "claudecode",
		WorkspaceState: "ephemeral",
		UserRuntimeDir: userRuntimeDir,
	})
	if err != nil {
		t.Fatalf("new local manager: %v", err)
	}

	userDir := filepath.Join(userRuntimeDir, safeUserRuntimeName("claude:u"), "claudecode")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatalf("mkdir user runtime: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userDir, ".claude.json"), []byte("state1"), 0o644); err != nil {
		t.Fatalf("write user runtime seed: %v", err)
	}

	prepared, err := manager.Prepare(model.Task{ID: "tc", UserID: "claude:u", Attempts: 1})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if prepared != runDir {
		t.Fatalf("expected run dir %s, got %s", runDir, prepared)
	}
	runState := filepath.Join(runDir, ".claudecode-home")
	if _, err := os.Stat(filepath.Join(runState, ".claude.json")); err != nil {
		t.Fatalf("expected claude runtime copied into run dir, err=%v", err)
	}

	if err := os.WriteFile(filepath.Join(runState, "session.json"), []byte("s1"), 0o644); err != nil {
		t.Fatalf("write run state mutation: %v", err)
	}

	if err := manager.Persist(model.Task{ID: "tc", UserID: "claude:u"}, runDir); err != nil {
		t.Fatalf("persist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(userDir, "session.json")); err != nil {
		t.Fatalf("expected claude runtime copied back to user runtime dir, err=%v", err)
	}
}
