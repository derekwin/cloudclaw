package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cloudclaw/internal/model"
)

type taskPayload struct {
	TaskID   string `json:"task_id"`
	UserID   string `json:"user_id"`
	TaskType string `json:"task_type"`
	Input    string `json:"input"`
}

type remoteTaskLayout struct {
	TaskDir     string
	UserDataDir string
	TaskFile    string
	UsageFile   string
}

func buildRemoteTaskLayout(baseDir, taskID string) remoteTaskLayout {
	base := strings.TrimSpace(baseDir)
	if base == "" {
		base = "/workspace/cloudclaw"
	}
	taskDir := fmt.Sprintf("%s/%s", strings.TrimRight(base, "/"), taskID)
	return remoteTaskLayout{
		TaskDir:     taskDir,
		UserDataDir: taskDir + "/userdata",
		TaskFile:    taskDir + "/task.json",
		UsageFile:   taskDir + "/usage.json",
	}
}

func prepareRemoteTaskLayoutCommand(layout remoteTaskLayout) string {
	taskDir := shellQuote(layout.TaskDir)
	userDataDir := shellQuote(layout.UserDataDir)
	taskFile := shellQuote(layout.TaskFile)
	usageFile := shellQuote(layout.UsageFile)
	return fmt.Sprintf(
		"mkdir -p %s %s && find %s -mindepth 1 -maxdepth 1 -exec rm -rf {} + && rm -f %s %s",
		taskDir,
		userDataDir,
		userDataDir,
		taskFile,
		usageFile,
	)
}

func resetWorkspaceDir(workspaceDir string) error {
	if err := os.RemoveAll(workspaceDir); err != nil {
		return err
	}
	return os.MkdirAll(workspaceDir, 0o755)
}

func resolveUsage(workspaceDir string, task model.Task) model.TokenUsage {
	usagePath := filepath.Join(workspaceDir, "usage.json")
	usage := model.TokenUsage{}
	if b, err := os.ReadFile(usagePath); err == nil {
		if err := json.Unmarshal(b, &usage); err == nil {
			if usage.TotalTokens <= 0 {
				usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
			}
			return usage
		}
	}

	resultPath := filepath.Join(workspaceDir, "result.txt")
	completion := 0
	if b, err := os.ReadFile(resultPath); err == nil {
		completion = estimateTokens(string(b))
	}
	prompt := estimateTokens(task.Input)
	return model.TokenUsage{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      prompt + completion,
	}
}

func renderTaskCommand(task model.Task, taskCommand string, layout remoteTaskLayout, sharedSkillsDir string) string {
	replaced := strings.NewReplacer(
		"{{TASK_DIR}}", layout.TaskDir,
		"{{TASK_FILE}}", layout.TaskFile,
		"{{USAGE_FILE}}", layout.UsageFile,
		"{{USERDATA_DIR}}", layout.UserDataDir,
	).Replace(taskCommand)
	if strings.TrimSpace(sharedSkillsDir) == "" {
		sharedSkillsDir = layout.UserDataDir + "/.cloudclaw_shared_skills"
	}
	envPrefix := []string{
		"CLOUDCLAW_TASK_ID=" + shellQuote(task.ID),
		"CLOUDCLAW_USER_ID=" + shellQuote(task.UserID),
		"CLOUDCLAW_TASK_TYPE=" + shellQuote(task.TaskType),
		"CLOUDCLAW_INPUT=" + shellQuote(task.Input),
		"CLOUDCLAW_TASK_FILE=" + shellQuote(layout.TaskFile),
		"CLOUDCLAW_WORKSPACE=" + shellQuote(layout.UserDataDir),
		"CLOUDCLAW_SHARED_SKILLS_DIR=" + shellQuote(sharedSkillsDir),
		"CLOUDCLAW_USAGE_FILE=" + shellQuote(layout.UsageFile),
	}
	return strings.Join(envPrefix, " ") + " " + replaced
}

func writeTaskPayload(path string, task model.Task) error {
	payload := taskPayload{
		TaskID:   task.ID,
		UserID:   task.UserID,
		TaskType: task.TaskType,
		Input:    task.Input,
	}
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func shellQuote(v string) string {
	if v == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(v, "'", "'\"'\"'") + "'"
}
