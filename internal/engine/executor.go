package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"cloudclaw/internal/model"
)

type Executor interface {
	Execute(ctx context.Context, containerID string, task model.Task, workspaceDir string) (model.TokenUsage, error)
}

type MockExecutor struct{}

func (m *MockExecutor) Execute(ctx context.Context, containerID string, task model.Task, workspaceDir string) (model.TokenUsage, error) {
	_ = ctx
	_ = containerID
	output := fmt.Sprintf("task_id=%s\nuser_id=%s\ntask_type=%s\ninput=%s\n", task.ID, task.UserID, task.TaskType, task.Input)
	if err := os.WriteFile(filepath.Join(workspaceDir, "result.txt"), []byte(output), 0o644); err != nil {
		return model.TokenUsage{}, err
	}
	prompt := estimateTokens(task.Input)
	completion := estimateTokens(output)
	return model.TokenUsage{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      prompt + completion,
	}, nil
}

type CommandExecutor struct {
	Command string
}

func (e *CommandExecutor) Execute(ctx context.Context, containerID string, task model.Task, workspaceDir string) (model.TokenUsage, error) {
	if strings.TrimSpace(e.Command) == "" {
		return model.TokenUsage{}, fmt.Errorf("executor command is empty")
	}
	usagePath := filepath.Join(workspaceDir, "usage.json")
	sharedSkillsPath := filepath.Join(workspaceDir, ".cloudclaw_shared_skills")
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", e.Command)
	cmd.Dir = workspaceDir
	cmd.Env = append(os.Environ(),
		"CLOUDCLAW_TASK_ID="+task.ID,
		"CLOUDCLAW_USER_ID="+task.UserID,
		"CLOUDCLAW_TASK_TYPE="+task.TaskType,
		"CLOUDCLAW_INPUT="+task.Input,
		"CLOUDCLAW_WORKSPACE="+workspaceDir,
		"CLOUDCLAW_SHARED_SKILLS_DIR="+sharedSkillsPath,
		"CLOUDCLAW_CONTAINER_ID="+containerID,
		"CLOUDCLAW_USAGE_FILE="+usagePath,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return model.TokenUsage{}, fmt.Errorf("executor failed: %w, output=%s", err, strings.TrimSpace(string(out)))
	}

	usage := model.TokenUsage{}
	if b, err := os.ReadFile(usagePath); err == nil {
		if err := json.Unmarshal(b, &usage); err == nil {
			usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
			return usage, nil
		}
	}

	stdoutTokens := estimateTokens(string(out))
	return model.TokenUsage{
		PromptTokens:     estimateTokens(task.Input),
		CompletionTokens: stdoutTokens,
		TotalTokens:      estimateTokens(task.Input) + stdoutTokens,
	}, nil
}

func estimateTokens(s string) int {
	n := len(strings.TrimSpace(s))
	if n == 0 {
		return 0
	}
	// Simple fallback estimator used when executor does not provide usage details.
	return (n + 3) / 4
}
