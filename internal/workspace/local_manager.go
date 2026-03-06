package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cloudclaw/internal/fsutil"
	"cloudclaw/internal/model"
)

type PathStore interface {
	RunDir(taskID string, attempt int) string
	SaveSnapshot(userID, taskID, path string) (model.Snapshot, error)
	RestoreUserDataToDir(userID, dstDir string) error
	ReplaceUserDataFromDir(userID, srcDir string) error
}

type LocalManagerConfig struct {
	Store            PathStore
	SharedSkillsDir  string
	SharedSkillsMode string // copy | mount
	WorkspaceState   string // db | ephemeral (alias: none)
}

type LocalManager struct {
	store            PathStore
	sharedSkillsDir  string
	sharedSkillsMode string
	workspaceState   string
}

func NewLocalManager(cfg LocalManagerConfig) (*LocalManager, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("workspace store is required")
	}
	return &LocalManager{
		store:            cfg.Store,
		sharedSkillsDir:  cfg.SharedSkillsDir,
		sharedSkillsMode: normalizeSharedSkillsMode(cfg.SharedSkillsMode),
		workspaceState:   normalizeWorkspaceStateMode(cfg.WorkspaceState),
	}, nil
}

func (m *LocalManager) Prepare(task model.Task) (string, error) {
	if task.Attempts <= 0 {
		task.Attempts = 1
	}
	runDir := m.store.RunDir(task.ID, task.Attempts)
	if m.workspaceState == "db" {
		if err := m.store.RestoreUserDataToDir(task.UserID, runDir); err != nil {
			return "", err
		}
	} else {
		if err := fsutil.RemoveAndRecreate(runDir); err != nil {
			return "", err
		}
	}
	if m.sharedSkillsMode == "copy" {
		if err := m.syncSharedSkills(runDir); err != nil {
			return "", err
		}
	}
	return runDir, nil
}

func normalizeSharedSkillsMode(mode string) string {
	if strings.EqualFold(strings.TrimSpace(mode), "mount") {
		return "mount"
	}
	return "copy"
}

func normalizeWorkspaceStateMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "none", "ephemeral":
		return "ephemeral"
	default:
		return "db"
	}
}

func (m *LocalManager) Persist(task model.Task, runDir string) error {
	if m.workspaceState != "db" {
		return nil
	}

	// Shared skills are global resources and should never be persisted as user state.
	if m.sharedSkillsMode == "copy" {
		_ = os.RemoveAll(filepath.Join(runDir, ".cloudclaw_shared_skills"))
	}
	if err := pruneRuntimeArtifacts(runDir); err != nil {
		return err
	}

	if err := m.store.ReplaceUserDataFromDir(task.UserID, runDir); err != nil {
		return err
	}
	_, err := m.store.SaveSnapshot(task.UserID, task.ID, fmt.Sprintf("db://user-data/%s/%s", task.UserID, task.ID))
	return err
}

func pruneRuntimeArtifacts(runDir string) error {
	opencodeStateDir := filepath.Join(runDir, ".opencode-home", ".local", "share", "opencode")
	for _, name := range []string{
		"bin",
		"log",
		"snapshot",
		"storage",
		"tool-output",
	} {
		if err := os.RemoveAll(filepath.Join(opencodeStateDir, name)); err != nil {
			return err
		}
	}
	for _, name := range []string{"opencode.db-shm", "opencode.db-wal"} {
		if err := os.Remove(filepath.Join(opencodeStateDir, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (m *LocalManager) syncSharedSkills(runDir string) error {
	if m.sharedSkillsDir == "" {
		return nil
	}
	target := filepath.Join(runDir, ".cloudclaw_shared_skills")
	if err := fsutil.RemoveAndRecreate(target); err != nil {
		return err
	}
	return fsutil.CopyDir(m.sharedSkillsDir, target)
}
