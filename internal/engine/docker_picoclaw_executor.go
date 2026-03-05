package engine

import (
	"context"
	"fmt"
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

func (e *DockerPicoclawExecutor) Name() string {
	return "docker-picoclaw"
}

func (e *DockerPicoclawExecutor) Execute(ctx context.Context, containerID string, task model.Task, workspaceDir string) (model.TokenUsage, error) {
	if strings.TrimSpace(containerID) == "" {
		return model.TokenUsage{}, fmt.Errorf("container id is required for docker executor")
	}
	if strings.TrimSpace(e.TaskCommand) == "" {
		return model.TokenUsage{}, fmt.Errorf("docker task command is required")
	}

	layout := buildRemoteTaskLayout(e.RemoteBaseDir, task.ID)

	payloadFile := filepath.Join(workspaceDir, "task.json")
	if err := writeTaskPayload(payloadFile, task); err != nil {
		return model.TokenUsage{}, err
	}

	prepareCmd := prepareRemoteUserDataCommand(layout.UserDataDir)
	if _, err := e.Docker.Exec(ctx, containerID, prepareCmd); err != nil {
		return model.TokenUsage{}, err
	}

	if err := e.Docker.CopyToContainer(ctx, workspaceDir+"/.", containerID, layout.UserDataDir); err != nil {
		return model.TokenUsage{}, fmt.Errorf("copy userdata to container failed: %w", err)
	}
	if err := e.Docker.CopyToContainer(ctx, payloadFile, containerID, layout.TaskFile); err != nil {
		return model.TokenUsage{}, fmt.Errorf("copy task payload to container failed: %w", err)
	}

	runnerCmd := e.renderCommand(task, layout)
	if _, err := e.Docker.Exec(ctx, containerID, runnerCmd); err != nil {
		return model.TokenUsage{}, err
	}

	if err := resetWorkspaceDir(workspaceDir); err != nil {
		return model.TokenUsage{}, err
	}
	if err := e.Docker.CopyFromContainer(ctx, containerID, layout.UserDataDir+"/.", workspaceDir); err != nil {
		return model.TokenUsage{}, fmt.Errorf("copy userdata from container failed: %w", err)
	}

	return resolveUsage(workspaceDir, task), nil
}

func (e *DockerPicoclawExecutor) renderCommand(task model.Task, layout remoteTaskLayout) string {
	return renderTaskCommand(task, e.TaskCommand, layout)
}
