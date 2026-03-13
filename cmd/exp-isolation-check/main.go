package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"cloudclaw/internal/model"
	"cloudclaw/internal/store"
	"cloudclaw/internal/workspace"
)

type checkResult struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Details string `json:"details"`
}

type summary struct {
	Experiment  string        `json:"experiment"`
	Runtime     string        `json:"runtime"`
	GeneratedAt time.Time     `json:"generated_at"`
	Passed      int           `json:"passed"`
	Failed      int           `json:"failed"`
	Skipped     int           `json:"skipped"`
	Checks      []checkResult `json:"checks"`
}

type config struct {
	dataDir        string
	dbDriver       string
	dbDSN          string
	runtimeName    string
	outputJSON     string
	outputCSV      string
	outputMarkdown string
}

func main() {
	cfg := parseFlags()
	s, err := runChecks(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "exp-isolation-check failed: %v\n", err)
		os.Exit(1)
	}
	if err := writeOutputs(cfg, s); err != nil {
		fmt.Fprintf(os.Stderr, "write outputs failed: %v\n", err)
		os.Exit(1)
	}
	if s.Failed > 0 {
		os.Exit(1)
	}
}

func parseFlags() config {
	cfg := config{}
	flag.StringVar(&cfg.dataDir, "data-dir", "./cloudclaw_data/data", "base data dir used for temporary experiment state")
	flag.StringVar(&cfg.dbDriver, "db-driver", "postgres", "database driver")
	flag.StringVar(&cfg.dbDSN, "db-dsn", "", "database dsn")
	flag.StringVar(&cfg.runtimeName, "runtime", "opencode", "runtime name: opencode|openclaw|claudecode")
	flag.StringVar(&cfg.outputJSON, "output-json", "", "path to write summary json")
	flag.StringVar(&cfg.outputCSV, "output-csv", "", "path to write per-check csv")
	flag.StringVar(&cfg.outputMarkdown, "output-markdown", "", "path to write markdown table")
	flag.Parse()

	cfg.dbDriver = strings.ToLower(strings.TrimSpace(cfg.dbDriver))
	if cfg.dbDriver == "" {
		cfg.dbDriver = "postgres"
	}
	if cfg.dbDriver == "postgresql" {
		cfg.dbDriver = "postgres"
	}
	if cfg.dbDriver != "postgres" {
		fmt.Fprintf(os.Stderr, "unsupported db driver: %s\n", cfg.dbDriver)
		os.Exit(2)
	}
	if strings.TrimSpace(cfg.dbDSN) == "" {
		fmt.Fprintln(os.Stderr, "db-dsn is required")
		os.Exit(2)
	}
	return cfg
}

func runChecks(cfg config) (summary, error) {
	s := summary{
		Experiment:  "isolation_validation",
		Runtime:     normalizeRuntimeName(cfg.runtimeName),
		GeneratedAt: time.Now().UTC(),
	}

	prefix := fmt.Sprintf("expiso_%d", time.Now().UTC().UnixNano())
	checks := []func(config, string) checkResult{
		checkSameFilenameIsolation,
		checkSymlinkRejected,
		checkOversizedFileRejected,
		checkEphemeralRuntimePersistence,
	}
	for _, fn := range checks {
		result := fn(cfg, prefix)
		s.Checks = append(s.Checks, result)
		switch result.Status {
		case "PASS":
			s.Passed++
		case "SKIP":
			s.Skipped++
		default:
			s.Failed++
		}
	}
	return s, nil
}

func checkSameFilenameIsolation(cfg config, prefix string) checkResult {
	s, err := openStore(cfg, filepath.Join(cfg.dataDir, prefix, "same-filename"), 0, 0)
	if err != nil {
		return fail("same_filename_isolated", err)
	}
	defer s.Close()

	writeTree := func(userID, content string) error {
		src := filepath.Join(cfg.dataDir, prefix, "tmp", userID)
		if err := os.RemoveAll(src); err != nil {
			return err
		}
		if err := os.MkdirAll(src, 0o755); err != nil {
			return err
		}
		path := filepath.Join(src, "workspace.txt")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return err
		}
		return s.ReplaceUserDataFromDir(userID, src)
	}

	user1 := prefix + "_u1"
	user2 := prefix + "_u2"
	if err := writeTree(user1, "alpha"); err != nil {
		return fail("same_filename_isolated", err)
	}
	if err := writeTree(user2, "beta"); err != nil {
		return fail("same_filename_isolated", err)
	}

	dst1 := filepath.Join(cfg.dataDir, prefix, "restore", "u1")
	dst2 := filepath.Join(cfg.dataDir, prefix, "restore", "u2")
	if err := s.RestoreUserDataToDir(user1, dst1); err != nil {
		return fail("same_filename_isolated", err)
	}
	if err := s.RestoreUserDataToDir(user2, dst2); err != nil {
		return fail("same_filename_isolated", err)
	}
	b1, err := os.ReadFile(filepath.Join(dst1, "workspace.txt"))
	if err != nil {
		return fail("same_filename_isolated", err)
	}
	b2, err := os.ReadFile(filepath.Join(dst2, "workspace.txt"))
	if err != nil {
		return fail("same_filename_isolated", err)
	}
	if string(b1) != "alpha" || string(b2) != "beta" {
		return checkResult{
			Name:    "same_filename_isolated",
			Status:  "FAIL",
			Details: fmt.Sprintf("unexpected contents: user1=%q user2=%q", string(b1), string(b2)),
		}
	}
	return checkResult{
		Name:    "same_filename_isolated",
		Status:  "PASS",
		Details: "same-named files remained user-scoped across store round-trip",
	}
}

func checkSymlinkRejected(cfg config, prefix string) checkResult {
	if runtime.GOOS == "windows" {
		return checkResult{
			Name:    "symlink_escape_rejected",
			Status:  "SKIP",
			Details: "symlink validation is skipped on windows",
		}
	}
	s, err := openStore(cfg, filepath.Join(cfg.dataDir, prefix, "symlink"), 0, 0)
	if err != nil {
		return fail("symlink_escape_rejected", err)
	}
	defer s.Close()

	src := filepath.Join(cfg.dataDir, prefix, "tmp", "symlink")
	if err := os.RemoveAll(src); err != nil {
		return fail("symlink_escape_rejected", err)
	}
	if err := os.MkdirAll(src, 0o755); err != nil {
		return fail("symlink_escape_rejected", err)
	}
	if err := os.WriteFile(filepath.Join(src, "real.txt"), []byte("x"), 0o644); err != nil {
		return fail("symlink_escape_rejected", err)
	}
	if err := os.Symlink("real.txt", filepath.Join(src, "link.txt")); err != nil {
		return fail("symlink_escape_rejected", err)
	}

	err = s.ReplaceUserDataFromDir(prefix+"_symlink", src)
	if err == nil {
		return checkResult{
			Name:    "symlink_escape_rejected",
			Status:  "FAIL",
			Details: "expected symlink-based escape attempt to be rejected",
		}
	}
	if !strings.Contains(strings.ToLower(err.Error()), "symlink") {
		return fail("symlink_escape_rejected", err)
	}
	return checkResult{
		Name:    "symlink_escape_rejected",
		Status:  "PASS",
		Details: "symlink-based path escape attempt was rejected",
	}
}

func checkOversizedFileRejected(cfg config, prefix string) checkResult {
	s, err := openStore(cfg, filepath.Join(cfg.dataDir, prefix, "oversized"), 1024, 4)
	if err != nil {
		return fail("oversized_file_rejected", err)
	}
	defer s.Close()

	src := filepath.Join(cfg.dataDir, prefix, "tmp", "oversized")
	if err := os.RemoveAll(src); err != nil {
		return fail("oversized_file_rejected", err)
	}
	if err := os.MkdirAll(src, 0o755); err != nil {
		return fail("oversized_file_rejected", err)
	}
	if err := os.WriteFile(filepath.Join(src, "big.txt"), []byte("12345"), 0o644); err != nil {
		return fail("oversized_file_rejected", err)
	}

	err = s.ReplaceUserDataFromDir(prefix+"_oversized", src)
	if err == nil {
		return checkResult{
			Name:    "oversized_file_rejected",
			Status:  "FAIL",
			Details: "expected file larger than MaxUserDataFileBytes to be rejected",
		}
	}
	if !strings.Contains(strings.ToLower(err.Error()), "file too large") {
		return fail("oversized_file_rejected", err)
	}
	return checkResult{
		Name:    "oversized_file_rejected",
		Status:  "PASS",
		Details: "per-file size limit rejected an oversized persisted file",
	}
}

func checkEphemeralRuntimePersistence(cfg config, prefix string) checkResult {
	s, err := openStore(cfg, filepath.Join(cfg.dataDir, prefix, "ephemeral"), 0, 0)
	if err != nil {
		return fail("ephemeral_runtime_persistence", err)
	}
	defer s.Close()

	userRuntimeDir := filepath.Join(cfg.dataDir, prefix, "user-runtime")
	manager, err := workspace.NewLocalManager(workspace.LocalManagerConfig{
		Store:          s,
		RuntimeName:    cfg.runtimeName,
		WorkspaceState: "ephemeral",
		UserRuntimeDir: userRuntimeDir,
	})
	if err != nil {
		return fail("ephemeral_runtime_persistence", err)
	}

	userID := prefix + "_persist"
	task1 := model.Task{ID: prefix + "_task1", UserID: userID, Attempts: 1}
	runDir1, err := manager.Prepare(task1)
	if err != nil {
		return fail("ephemeral_runtime_persistence", err)
	}
	stateFile1 := filepath.Join(runtimeStateDir(cfg.runtimeName, runDir1), "session.txt")
	if err := os.MkdirAll(filepath.Dir(stateFile1), 0o755); err != nil {
		return fail("ephemeral_runtime_persistence", err)
	}
	if err := os.WriteFile(stateFile1, []byte("token-alpha"), 0o644); err != nil {
		return fail("ephemeral_runtime_persistence", err)
	}
	if err := manager.Persist(task1, runDir1); err != nil {
		return fail("ephemeral_runtime_persistence", err)
	}

	task2 := model.Task{ID: prefix + "_task2", UserID: userID, Attempts: 1}
	runDir2, err := manager.Prepare(task2)
	if err != nil {
		return fail("ephemeral_runtime_persistence", err)
	}
	stateFile2 := filepath.Join(runtimeStateDir(cfg.runtimeName, runDir2), "session.txt")
	b, err := os.ReadFile(stateFile2)
	if err != nil {
		return fail("ephemeral_runtime_persistence", err)
	}
	if string(b) != "token-alpha" {
		return checkResult{
			Name:    "ephemeral_runtime_persistence",
			Status:  "FAIL",
			Details: fmt.Sprintf("unexpected restored runtime state: %q", string(b)),
		}
	}
	return checkResult{
		Name:    "ephemeral_runtime_persistence",
		Status:  "PASS",
		Details: "runtime state persisted across tasks in ephemeral workspace mode",
	}
}

func writeOutputs(cfg config, s summary) error {
	if cfg.outputJSON != "" {
		if err := writeJSON(cfg.outputJSON, s); err != nil {
			return err
		}
	}
	if cfg.outputCSV != "" {
		if err := writeCSV(cfg.outputCSV, s.Checks); err != nil {
			return err
		}
	}
	if cfg.outputMarkdown != "" {
		if err := writeMarkdown(cfg.outputMarkdown, s); err != nil {
			return err
		}
	}
	return nil
}

func writeJSON(path string, s summary) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(s)
}

func writeCSV(path string, checks []checkResult) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	if err := w.Write([]string{"name", "status", "details"}); err != nil {
		return err
	}
	for _, item := range checks {
		if err := w.Write([]string{item.Name, item.Status, item.Details}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

func writeMarkdown(path string, s summary) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Isolation Validation\n\n")
	fmt.Fprintf(&b, "- Runtime: `%s`\n", s.Runtime)
	fmt.Fprintf(&b, "- Passed: `%d`\n", s.Passed)
	fmt.Fprintf(&b, "- Failed: `%d`\n", s.Failed)
	fmt.Fprintf(&b, "- Skipped: `%d`\n\n", s.Skipped)
	fmt.Fprintf(&b, "| Check | Status | Details |\n")
	fmt.Fprintf(&b, "| --- | --- | --- |\n")
	for _, item := range s.Checks {
		fmt.Fprintf(&b, "| %s | %s | %s |\n", item.Name, item.Status, item.Details)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func openStore(cfg config, baseDir string, maxBytes, maxFileBytes int64) (*store.Store, error) {
	return store.NewWithConfig(store.Config{
		BaseDir:              baseDir,
		Driver:               cfg.dbDriver,
		DSN:                  cfg.dbDSN,
		MaxUserDataBytes:     maxBytes,
		MaxUserDataFileBytes: maxFileBytes,
	})
}

func runtimeStateDir(runtimeName, runDir string) string {
	switch normalizeRuntimeName(runtimeName) {
	case "claudecode":
		return filepath.Join(runDir, ".claudecode-home")
	case "openclaw":
		return filepath.Join(runDir, ".openclaw-home", ".local", "share", "openclaw")
	default:
		return filepath.Join(runDir, ".opencode-home", ".local", "share", "opencode")
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

func fail(name string, err error) checkResult {
	return checkResult{
		Name:    name,
		Status:  "FAIL",
		Details: err.Error(),
	}
}
