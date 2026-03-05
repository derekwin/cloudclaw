package dockerutil

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectContainerReturnsErrorOnUnexpectedRunningState(t *testing.T) {
	bin := writeFakeDocker(t, `
if [ "$1" = "inspect" ]; then
  echo "unknown"
  exit 0
fi
echo "unexpected args" >&2
exit 1
`)

	d := Docker{Binary: bin}
	_, _, err := d.inspectContainer(context.Background(), "c1")
	if err == nil {
		t.Fatal("expected parse error for unexpected inspect output")
	}
	if !strings.Contains(err.Error(), `unexpected running state "unknown"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInspectContainerReturnsNotExistsForMissingContainer(t *testing.T) {
	bin := writeFakeDocker(t, `
if [ "$1" = "inspect" ]; then
  echo "Error: No such object: $4" >&2
  exit 1
fi
echo "unexpected args" >&2
exit 1
`)

	d := Docker{Binary: bin}
	running, exists, err := d.inspectContainer(context.Background(), "c1")
	if err != nil {
		t.Fatalf("inspect should treat missing object as non-error, got: %v", err)
	}
	if running {
		t.Fatal("expected running=false for missing container")
	}
	if exists {
		t.Fatal("expected exists=false for missing container")
	}
}

func writeFakeDocker(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "docker")
	script := "#!/bin/sh\nset -eu\n" + strings.TrimSpace(body) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake docker script: %v", err)
	}
	return path
}
