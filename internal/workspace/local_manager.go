package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"cloudclaw/internal/fsutil"
	"cloudclaw/internal/model"
)

type PathStore interface {
	UserDataDir(userID string) string
	RunDir(taskID string, attempt int) string
	UserSnapshotBaseDir(userID string) string
	SaveSnapshot(userID, taskID, path string) (model.Snapshot, error)
}

type LocalManagerConfig struct {
	Store           PathStore
	SharedSkillsDir string
}

type LocalManager struct {
	store           PathStore
	sharedSkillsDir string
}

func NewLocalManager(cfg LocalManagerConfig) (*LocalManager, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("workspace store is required")
	}
	return &LocalManager{
		store:           cfg.Store,
		sharedSkillsDir: cfg.SharedSkillsDir,
	}, nil
}

func (m *LocalManager) Prepare(task model.Task) (string, error) {
	userDataDir := m.store.UserDataDir(task.UserID)
	if err := fsutil.EnsureDir(userDataDir); err != nil {
		return "", err
	}
	if task.Attempts <= 0 {
		task.Attempts = 1
	}
	runDir := m.store.RunDir(task.ID, task.Attempts)
	if err := fsutil.RemoveAndRecreate(runDir); err != nil {
		return "", err
	}
	if err := fsutil.CopyDir(userDataDir, runDir); err != nil {
		return "", err
	}
	if err := m.syncSharedSkills(runDir); err != nil {
		return "", err
	}
	return runDir, nil
}

func (m *LocalManager) Persist(task model.Task, runDir string) error {
	// Shared skills are global resources and should never be persisted as user state.
	_ = os.RemoveAll(filepath.Join(runDir, ".cloudclaw_shared_skills"))

	userDataDir := m.store.UserDataDir(task.UserID)
	if err := fsutil.RemoveAndRecreate(userDataDir); err != nil {
		return err
	}
	if err := fsutil.CopyDir(runDir, userDataDir); err != nil {
		return err
	}

	snapID := fmt.Sprintf("%s-%d", task.ID, time.Now().UTC().Unix())
	snapshotPath := filepath.Join(m.store.UserSnapshotBaseDir(task.UserID), snapID)
	if err := fsutil.RemoveAndRecreate(snapshotPath); err != nil {
		return err
	}
	if err := fsutil.CopyDir(userDataDir, snapshotPath); err != nil {
		return err
	}
	_, err := m.store.SaveSnapshot(task.UserID, task.ID, snapshotPath)
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
