package dockerutil

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type Docker struct {
	Binary string
}

func (d Docker) ListRunningContainers(ctx context.Context, labelSelector string) ([]string, error) {
	filters := parseLabelSelector(labelSelector)
	args := []string{"ps"}
	for _, f := range filters {
		args = append(args, "--filter", "label="+f)
	}
	args = append(args, "--format", "{{.Names}}")
	out, err := runCmd(ctx, d.binary(), args...)
	if err != nil {
		return nil, err
	}
	containers := []string{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		containers = append(containers, name)
	}
	return containers, nil
}

func (d Docker) Exec(ctx context.Context, container string, remoteCommand string) (string, error) {
	args := []string{"exec", container, "sh", "-lc", remoteCommand}
	return runCmd(ctx, d.binary(), args...)
}

func (d Docker) CopyToContainer(ctx context.Context, srcPath, container, destPath string) error {
	args := []string{"cp", srcPath, fmt.Sprintf("%s:%s", container, destPath)}
	_, err := runCmd(ctx, d.binary(), args...)
	return err
}

func (d Docker) CopyFromContainer(ctx context.Context, container, srcPath, destPath string) error {
	args := []string{"cp", fmt.Sprintf("%s:%s", container, srcPath), destPath}
	_, err := runCmd(ctx, d.binary(), args...)
	return err
}

func (d Docker) binary() string {
	if strings.TrimSpace(d.Binary) != "" {
		return d.Binary
	}
	return "docker"
}

func parseLabelSelector(selector string) []string {
	parts := strings.Split(selector, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		v := strings.TrimSpace(p)
		if v == "" {
			continue
		}
		out = append(out, v)
	}
	return out
}

func runCmd(ctx context.Context, bin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s %s failed: %w, output=%s", bin, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
