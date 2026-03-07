package workspace

import (
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"regexp"
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
	RuntimeName      string // opencode | openclaw | claudecode
	UserRuntimeDir   string // host dir to persist per-user runtime state for ephemeral mode
}

type LocalManager struct {
	store            PathStore
	sharedSkillsDir  string
	sharedSkillsMode string
	workspaceState   string
	runtimeName      string
	userRuntimeDir   string
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
		runtimeName:      normalizeRuntimeName(cfg.RuntimeName),
		userRuntimeDir:   strings.TrimSpace(cfg.UserRuntimeDir),
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
		if err := m.restoreUserRuntimeToRunDir(task.UserID, runDir); err != nil {
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

func normalizeRuntimeName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "claudecode":
		return "claudecode"
	case "openclaw":
		return "openclaw"
	default:
		return "opencode"
	}
}

func (m *LocalManager) Persist(task model.Task, runDir string) error {
	if m.workspaceState != "db" {
		if err := m.persistRunDirRuntimeToUser(task.UserID, runDir); err != nil {
			return err
		}
		return nil
	}

	// Shared skills are global resources and should never be persisted as user state.
	if m.sharedSkillsMode == "copy" {
		_ = os.RemoveAll(filepath.Join(runDir, ".cloudclaw_shared_skills"))
	}
	if err := m.pruneRuntimeStateArtifacts(runDir); err != nil {
		return err
	}
	if err := m.pruneRuntimeArtifacts(runDir); err != nil {
		return err
	}

	if err := m.store.ReplaceUserDataFromDir(task.UserID, runDir); err != nil {
		return err
	}
	_, err := m.store.SaveSnapshot(task.UserID, task.ID, fmt.Sprintf("db://user-data/%s/%s", task.UserID, task.ID))
	return err
}

func (m *LocalManager) restoreUserRuntimeToRunDir(userID, runDir string) error {
	if m.userRuntimeDir == "" {
		return nil
	}
	src := m.userRuntimeRuntimeDir(userID)
	info, err := os.Stat(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("user runtime path is not directory: %s", src)
	}
	dst := m.runtimeStateDir(runDir)
	if err := fsutil.RemoveAndRecreate(dst); err != nil {
		return err
	}
	return fsutil.CopyDir(src, dst)
}

func (m *LocalManager) persistRunDirRuntimeToUser(userID, runDir string) error {
	if m.userRuntimeDir == "" {
		return nil
	}
	src := m.runtimeStateDir(runDir)
	dest := m.userRuntimeRuntimeDir(userID)
	info, err := os.Stat(src)
	if err != nil {
		if os.IsNotExist(err) {
			// Keep previous runtime state if current task did not produce a state dir.
			return nil
		}
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("run dir runtime state path is not directory: %s", src)
	}
	if err := fsutil.RemoveAndRecreate(dest); err != nil {
		return err
	}
	return fsutil.CopyDir(src, dest)
}

func (m *LocalManager) userRuntimeRuntimeDir(userID string) string {
	normalized := safeUserRuntimeName(userID)
	return filepath.Join(m.userRuntimeDir, normalized, m.runtimeName)
}

func (m *LocalManager) runtimeStateDir(runDir string) string {
	switch m.runtimeName {
	case "claudecode":
		return filepath.Join(runDir, ".claudecode-home")
	case "openclaw":
		return filepath.Join(runDir, ".openclaw-home", ".local", "share", "openclaw")
	default:
		return filepath.Join(runDir, ".opencode-home", ".local", "share", "opencode")
	}
}

var unsafeUserCharPattern = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func safeUserRuntimeName(userID string) string {
	trimmed := strings.TrimSpace(userID)
	if trimmed == "" {
		trimmed = "anonymous"
	}
	base := unsafeUserCharPattern.ReplaceAllString(trimmed, "_")
	base = strings.Trim(base, "._-")
	if base == "" {
		base = "anonymous"
	}
	sum := crc32.ChecksumIEEE([]byte(trimmed))
	return fmt.Sprintf("%s-%d", base, sum)
}

func pruneRuntimeDataArtifacts(stateDir, dbBase string) error {
	for _, name := range []string{
		"bin",
		"log",
		"snapshot",
		"storage",
		"tool-output",
	} {
		if err := os.RemoveAll(filepath.Join(stateDir, name)); err != nil {
			return err
		}
	}
	for _, name := range []string{
		dbBase + ".db-shm",
		dbBase + ".db-wal",
		"opencode.db-shm",
		"opencode.db-wal",
		"openclaw.db-shm",
		"openclaw.db-wal",
	} {
		if err := os.Remove(filepath.Join(stateDir, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (m *LocalManager) pruneRuntimeStateArtifacts(runDir string) error {
	switch m.runtimeName {
	case "opencode":
		return pruneRuntimeDataArtifacts(filepath.Join(runDir, ".opencode-home", ".local", "share", "opencode"), "opencode")
	case "openclaw":
		return pruneRuntimeDataArtifacts(filepath.Join(runDir, ".openclaw-home", ".local", "share", "openclaw"), "openclaw")
	default:
		return nil
	}
}

func (m *LocalManager) pruneRuntimeArtifacts(runDir string) error {
	if m.runtimeName != "claudecode" {
		return nil
	}
	// Do not persist shared config copy into DB snapshots in claudecode db mode.
	return os.RemoveAll(filepath.Join(runDir, ".claudecode-home", "config.json"))
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
