package workspace

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
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

func TestPrepareAndPersistEphemeralWorkspaceState(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "run")
	store := &captureStore{runDir: runDir}
	manager, err := NewLocalManager(LocalManagerConfig{
		Store:          store,
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
