package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"cloudclaw/internal/k8sutil"
	"cloudclaw/internal/model"
)

type K8sRuntimeExecutor struct {
	Kubectl         k8sutil.Kubectl
	RemoteBaseDir   string
	TaskCommand     string
	SharedSkillsDir string
}

func (e *K8sRuntimeExecutor) Name() string {
	return "k8s-runtime"
}

func (e *K8sRuntimeExecutor) Execute(ctx context.Context, containerID string, task model.Task, workspaceDir string) (model.TokenUsage, error) {
	if strings.TrimSpace(containerID) == "" {
		return model.TokenUsage{}, fmt.Errorf("container id is required for k8s executor")
	}
	if strings.TrimSpace(e.TaskCommand) == "" {
		return model.TokenUsage{}, fmt.Errorf("k8s task command is required")
	}

	layout := buildRemoteTaskLayout(e.RemoteBaseDir, task.ID)

	payloadFile := filepath.Join(workspaceDir, "task.json")
	if err := writeTaskPayload(payloadFile, task); err != nil {
		return model.TokenUsage{}, err
	}

	prepareCmd := prepareRemoteTaskLayoutCommand(layout)
	if _, err := e.Kubectl.Exec(ctx, containerID, prepareCmd); err != nil {
		return model.TokenUsage{}, err
	}

	if err := e.Kubectl.CopyToPod(ctx, workspaceDir+"/.", containerID, layout.UserDataDir); err != nil {
		return model.TokenUsage{}, fmt.Errorf("copy userdata to pod failed: %w", err)
	}
	if err := e.Kubectl.CopyToPod(ctx, payloadFile, containerID, layout.TaskFile); err != nil {
		return model.TokenUsage{}, fmt.Errorf("copy task payload to pod failed: %w", err)
	}

	runnerCmd := e.renderCommand(task, layout)
	if _, err := e.Kubectl.Exec(ctx, containerID, runnerCmd); err != nil {
		return model.TokenUsage{}, err
	}

	if err := resetWorkspaceDir(workspaceDir); err != nil {
		return model.TokenUsage{}, err
	}
	if err := e.Kubectl.CopyFromPod(ctx, containerID, layout.UserDataDir+"/.", workspaceDir); err != nil {
		return model.TokenUsage{}, fmt.Errorf("copy userdata from pod failed: %w", err)
	}

	return resolveUsage(workspaceDir, task), nil
}

func (e *K8sRuntimeExecutor) renderCommand(task model.Task, layout remoteTaskLayout) string {
	return renderTaskCommand(task, e.TaskCommand, layout, e.SharedSkillsDir)
}
