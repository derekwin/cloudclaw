package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cloudclaw/internal/model"
)

func TestResolveUsagePrefersUsageFileAndKeepsExplicitTotal(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "usage.json"), []byte(`{"prompt_tokens":10,"completion_tokens":5,"total_tokens":99}`), 0o644); err != nil {
		t.Fatalf("write usage.json: %v", err)
	}

	usage := resolveUsage(dir, model.Task{Input: "hello"})
	if usage.PromptTokens != 10 || usage.CompletionTokens != 5 || usage.TotalTokens != 99 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
}

func TestResolveUsageFallsBackToEstimatedResult(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "result.txt"), []byte("abcd"), 0o644); err != nil {
		t.Fatalf("write result.txt: %v", err)
	}

	usage := resolveUsage(dir, model.Task{Input: "abcd"})
	if usage.PromptTokens != 1 || usage.CompletionTokens != 1 || usage.TotalTokens != 2 {
		t.Fatalf("unexpected fallback usage: %+v", usage)
	}
}

func TestPrepareRemoteUserDataCommandCleansHiddenFiles(t *testing.T) {
	cmd := prepareRemoteUserDataCommand("/tmp/test-dir")
	if !strings.Contains(cmd, "find") || !strings.Contains(cmd, "-mindepth 1") {
		t.Fatalf("unexpected cleanup command: %s", cmd)
	}
}

func TestRenderTaskCommandReplacesPlaceholders(t *testing.T) {
	layout := remoteTaskLayout{
		TaskDir:     "/remote/t1",
		UserDataDir: "/remote/t1/userdata",
		TaskFile:    "/remote/t1/task.json",
		UsageFile:   "/remote/t1/usage.json",
	}
	cmd := renderTaskCommand(
		model.Task{ID: "t1", UserID: "u1", TaskType: "search", Input: "hello"},
		"runner --task {{TASK_FILE}} --usage {{USAGE_FILE}} --dir {{USERDATA_DIR}}",
		layout,
		"",
	)
	if strings.Contains(cmd, "{{TASK_FILE}}") || strings.Contains(cmd, "{{USAGE_FILE}}") {
		t.Fatalf("placeholders were not replaced: %s", cmd)
	}
	if !strings.Contains(cmd, "CLOUDCLAW_TASK_ID='t1'") || !strings.Contains(cmd, "runner --task /remote/t1/task.json") {
		t.Fatalf("unexpected rendered command: %s", cmd)
	}
}
