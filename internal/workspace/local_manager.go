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
}

type LocalManager struct {
	store            PathStore
	sharedSkillsDir  string
	sharedSkillsMode string
}

func NewLocalManager(cfg LocalManagerConfig) (*LocalManager, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("workspace store is required")
	}
	return &LocalManager{
		store:            cfg.Store,
		sharedSkillsDir:  cfg.SharedSkillsDir,
		sharedSkillsMode: normalizeSharedSkillsMode(cfg.SharedSkillsMode),
	}, nil
}

func (m *LocalManager) Prepare(task model.Task) (string, error) {
	if task.Attempts <= 0 {
		task.Attempts = 1
	}
	runDir := m.store.RunDir(task.ID, task.Attempts)
	if err := m.store.RestoreUserDataToDir(task.UserID, runDir); err != nil {
		return "", err
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

func (m *LocalManager) Persist(task model.Task, runDir string) error {
	// Shared skills are global resources and should never be persisted as user state.
	if m.sharedSkillsMode == "copy" {
		_ = os.RemoveAll(filepath.Join(runDir, ".cloudclaw_shared_skills"))
	}

	if err := m.store.ReplaceUserDataFromDir(task.UserID, runDir); err != nil {
		return err
	}
	_, err := m.store.SaveSnapshot(task.UserID, task.ID, fmt.Sprintf("db://user-data/%s/%s", task.UserID, task.ID))
	return err
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
