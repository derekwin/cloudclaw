package engine

import (
	"path/filepath"
	"testing"
)

func TestLayoutForMountedWorkspace(t *testing.T) {
	hostBase := t.TempDir()
	runDir := filepath.Join(hostBase, "tsk_1-attempt-1")

	e := DockerPicoclawExecutor{
		RunDirHostBase:      hostBase,
		RunDirContainerBase: "/workspace/cloudclaw/runs",
	}
	layout, err := e.layoutForMountedWorkspace(runDir)
	if err != nil {
		t.Fatalf("layoutForMountedWorkspace returned error: %v", err)
	}
	if layout.UserDataDir != "/workspace/cloudclaw/runs/tsk_1-attempt-1" {
		t.Fatalf("unexpected UserDataDir: %s", layout.UserDataDir)
	}
	if layout.TaskFile != "/workspace/cloudclaw/runs/tsk_1-attempt-1/task.json" {
		t.Fatalf("unexpected TaskFile: %s", layout.TaskFile)
	}
	if layout.UsageFile != "/workspace/cloudclaw/runs/tsk_1-attempt-1/usage.json" {
		t.Fatalf("unexpected UsageFile: %s", layout.UsageFile)
	}
}

func TestLayoutForMountedWorkspaceRejectsOutsideHostBase(t *testing.T) {
	e := DockerPicoclawExecutor{
		RunDirHostBase:      filepath.Join(t.TempDir(), "runs"),
		RunDirContainerBase: "/workspace/cloudclaw/runs",
	}
	_, err := e.layoutForMountedWorkspace(t.TempDir())
	if err == nil {
		t.Fatal("expected error when workspace is outside mounted host base")
	}
}
