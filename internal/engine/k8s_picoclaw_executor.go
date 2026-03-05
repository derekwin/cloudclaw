package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cloudclaw/internal/k8sutil"
	"cloudclaw/internal/model"
)

type K8sPicoclawExecutor struct {
	Kubectl       k8sutil.Kubectl
	RemoteBaseDir string
	TaskCommand   string
}

type taskPayload struct {
	TaskID   string `json:"task_id"`
	UserID   string `json:"user_id"`
	TaskType string `json:"task_type"`
	Input    string `json:"input"`
}

func (e *K8sPicoclawExecutor) Execute(ctx context.Context, containerID string, task model.Task, workspaceDir string) (model.TokenUsage, error) {
	if strings.TrimSpace(containerID) == "" {
		return model.TokenUsage{}, fmt.Errorf("container id is required for k8s executor")
	}
	if strings.TrimSpace(e.TaskCommand) == "" {
		return model.TokenUsage{}, fmt.Errorf("k8s task command is required")
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
	if _, err := e.Kubectl.Exec(ctx, containerID, prepareCmd); err != nil {
		return model.TokenUsage{}, err
	}

	if err := e.Kubectl.CopyToPod(ctx, workspaceDir+"/.", containerID, remoteUserDataDir); err != nil {
		return model.TokenUsage{}, fmt.Errorf("copy userdata to pod failed: %w", err)
	}
	if err := e.Kubectl.CopyToPod(ctx, payloadFile, containerID, remoteTaskFile); err != nil {
		return model.TokenUsage{}, fmt.Errorf("copy task payload to pod failed: %w", err)
	}

	runnerCmd := e.renderCommand(task, remoteTaskDir, remoteTaskFile, remoteUsageFile)
	if _, err := e.Kubectl.Exec(ctx, containerID, runnerCmd); err != nil {
		return model.TokenUsage{}, err
	}

	if err := os.RemoveAll(workspaceDir); err != nil {
		return model.TokenUsage{}, err
	}
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		return model.TokenUsage{}, err
	}
	if err := e.Kubectl.CopyFromPod(ctx, containerID, remoteUserDataDir+"/.", workspaceDir); err != nil {
		return model.TokenUsage{}, fmt.Errorf("copy userdata from pod failed: %w", err)
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

func (e *K8sPicoclawExecutor) renderCommand(task model.Task, remoteTaskDir, remoteTaskFile, remoteUsageFile string) string {
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
		"CLOUDCLAW_SHARED_SKILLS_DIR=" + shellQuote(remoteTaskDir+"/userdata/.cloudclaw_shared_skills"),
		"CLOUDCLAW_USAGE_FILE=" + shellQuote(remoteUsageFile),
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
