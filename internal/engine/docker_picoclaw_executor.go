package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cloudclaw/internal/dockerutil"
	"cloudclaw/internal/model"
)

type DockerPicoclawExecutor struct {
	Docker        dockerutil.Docker
	RemoteBaseDir string
	TaskCommand   string
}

func (e *DockerPicoclawExecutor) Execute(ctx context.Context, containerID string, task model.Task, workspaceDir string) (model.TokenUsage, error) {
	if strings.TrimSpace(containerID) == "" {
		return model.TokenUsage{}, fmt.Errorf("container id is required for docker executor")
	}
	if strings.TrimSpace(e.TaskCommand) == "" {
		return model.TokenUsage{}, fmt.Errorf("docker task command is required")
	}

	remoteBase := e.RemoteBaseDir
	if strings.TrimSpace(remoteBase) == "" {
		remoteBase = "/workspace/cloudclaw"
	}
	remoteTaskDir := fmt.Sprintf("%s/%s", strings.TrimRight(remoteBase, "/"), task.ID)
	remoteUserDataDir := remoteTaskDir + "/userdata"
	remoteTaskFile := remoteTaskDir + "/task.json"
	remoteUsageFile := remoteTaskDir + "/usage.json"

	payloadFile := filepath.Join(workspaceDir, "task.json")
	if err := writeTaskPayload(payloadFile, task); err != nil {
		return model.TokenUsage{}, err
	}

	prepareCmd := fmt.Sprintf("mkdir -p %s && rm -rf %s/*", shellQuote(remoteUserDataDir), shellQuote(remoteUserDataDir))
	if _, err := e.Docker.Exec(ctx, containerID, prepareCmd); err != nil {
		return model.TokenUsage{}, err
	}

	if err := e.Docker.CopyToContainer(ctx, workspaceDir+"/.", containerID, remoteUserDataDir); err != nil {
		return model.TokenUsage{}, fmt.Errorf("copy userdata to container failed: %w", err)
	}
	if err := e.Docker.CopyToContainer(ctx, payloadFile, containerID, remoteTaskFile); err != nil {
		return model.TokenUsage{}, fmt.Errorf("copy task payload to container failed: %w", err)
	}

	runnerCmd := e.renderCommand(task, remoteTaskDir, remoteTaskFile, remoteUsageFile)
	if _, err := e.Docker.Exec(ctx, containerID, runnerCmd); err != nil {
		return model.TokenUsage{}, err
	}

	if err := os.RemoveAll(workspaceDir); err != nil {
		return model.TokenUsage{}, err
	}
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		return model.TokenUsage{}, err
	}
	if err := e.Docker.CopyFromContainer(ctx, containerID, remoteUserDataDir+"/.", workspaceDir); err != nil {
		return model.TokenUsage{}, fmt.Errorf("copy userdata from container failed: %w", err)
	}

	usagePath := filepath.Join(workspaceDir, "usage.json")
	usage := model.TokenUsage{}
	if b, err := os.ReadFile(usagePath); err == nil {
		if err := json.Unmarshal(b, &usage); err == nil {
			usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
			return usage, nil
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
	}, nil
}

func (e *DockerPicoclawExecutor) renderCommand(task model.Task, remoteTaskDir, remoteTaskFile, remoteUsageFile string) string {
	replaced := strings.NewReplacer(
		"{{TASK_DIR}}", remoteTaskDir,
		"{{TASK_FILE}}", remoteTaskFile,
		"{{USAGE_FILE}}", remoteUsageFile,
		"{{USERDATA_DIR}}", remoteTaskDir+"/userdata",
	).Replace(e.TaskCommand)
	envPrefix := []string{
		"CLOUDCLAW_TASK_ID=" + shellQuote(task.ID),
		"CLOUDCLAW_USER_ID=" + shellQuote(task.UserID),
		"CLOUDCLAW_TASK_TYPE=" + shellQuote(task.TaskType),
		"CLOUDCLAW_INPUT=" + shellQuote(task.Input),
		"CLOUDCLAW_TASK_FILE=" + shellQuote(remoteTaskFile),
		"CLOUDCLAW_WORKSPACE=" + shellQuote(remoteTaskDir+"/userdata"),
		"CLOUDCLAW_USAGE_FILE=" + shellQuote(remoteUsageFile),
	}
	return strings.Join(envPrefix, " ") + " " + replaced
}
