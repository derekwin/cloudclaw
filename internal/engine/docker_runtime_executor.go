package engine

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"cloudclaw/internal/dockerutil"
	"cloudclaw/internal/model"
)

type DockerRuntimeExecutor struct {
	Docker              dockerutil.Docker
	RemoteBaseDir       string
	TaskCommand         string
	SharedSkillsDir     string
	WorkspaceMode       string // copy | mount
	RunDirHostBase      string
	RunDirContainerBase string
}

func (e *DockerRuntimeExecutor) Name() string {
	return "docker-runtime"
}

func (e *DockerRuntimeExecutor) Execute(ctx context.Context, containerID string, task model.Task, workspaceDir string) (model.TokenUsage, error) {
	if strings.TrimSpace(containerID) == "" {
		return model.TokenUsage{}, fmt.Errorf("container id is required for docker executor")
	}
	if strings.TrimSpace(e.TaskCommand) == "" {
		return model.TokenUsage{}, fmt.Errorf("docker task command is required")
	}
	payloadFile := filepath.Join(workspaceDir, "task.json")
	if err := writeTaskPayload(payloadFile, task); err != nil {
		return model.TokenUsage{}, err
	}

	if isWorkspaceModeMount(e.WorkspaceMode) {
		layout, err := e.layoutForMountedWorkspace(workspaceDir)
		if err != nil {
			return model.TokenUsage{}, err
		}
		runnerCmd := e.renderCommand(task, layout)
		if _, err := e.Docker.Exec(ctx, containerID, runnerCmd); err != nil {
			return model.TokenUsage{}, err
		}
		return resolveUsage(workspaceDir, task), nil
	}

	layout := buildRemoteTaskLayout(e.RemoteBaseDir, task.ID)

	prepareCmd := prepareRemoteTaskLayoutCommand(layout)
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

func (e *DockerRuntimeExecutor) renderCommand(task model.Task, layout remoteTaskLayout) string {
	return renderTaskCommand(task, e.TaskCommand, layout, e.SharedSkillsDir)
}

func isWorkspaceModeMount(mode string) bool {
	return strings.EqualFold(strings.TrimSpace(mode), "mount")
}

func (e *DockerRuntimeExecutor) layoutForMountedWorkspace(workspaceDir string) (remoteTaskLayout, error) {
	hostBase := strings.TrimSpace(e.RunDirHostBase)
	containerBase := strings.TrimSpace(e.RunDirContainerBase)
	if hostBase == "" || containerBase == "" {
		return remoteTaskLayout{}, fmt.Errorf("run dir mount paths are required when workspace mode is mount")
	}
	absHostBase, err := filepath.Abs(hostBase)
	if err != nil {
		return remoteTaskLayout{}, err
	}
	absRunDir, err := filepath.Abs(workspaceDir)
	if err != nil {
		return remoteTaskLayout{}, err
	}
	rel, err := filepath.Rel(absHostBase, absRunDir)
	if err != nil {
		return remoteTaskLayout{}, err
	}
	if rel == "." {
		rel = ""
	}
	if rel == "" || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return remoteTaskLayout{}, fmt.Errorf("workspace %s is outside mounted host base %s", workspaceDir, hostBase)
	}
	relUnix := filepath.ToSlash(rel)
	remoteRunDir := path.Clean(strings.TrimRight(containerBase, "/") + "/" + relUnix)
	return remoteTaskLayout{
		TaskDir:     remoteRunDir,
		UserDataDir: remoteRunDir,
		TaskFile:    remoteRunDir + "/task.json",
		UsageFile:   remoteRunDir + "/usage.json",
	}, nil
}
