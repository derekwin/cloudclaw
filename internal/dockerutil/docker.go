package dockerutil

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

type Docker struct {
	Binary string
}

type EnsurePoolOptions struct {
	Image                    string
	NamePrefix               string
	Label                    string
	PoolSize                 int
	InitCmd                  string
	SharedSkillsHostDir      string
	SharedSkillsContainerDir string
	WorkspaceHostDir         string
	WorkspaceContainerDir    string
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

func (d Docker) EnsurePool(ctx context.Context, opts EnsurePoolOptions) ([]string, error) {
	if strings.TrimSpace(opts.Image) == "" {
		return nil, fmt.Errorf("docker pool image is required")
	}
	if strings.TrimSpace(opts.NamePrefix) == "" {
		opts.NamePrefix = "picoclaw-agent"
	}
	if strings.TrimSpace(opts.Label) == "" {
		opts.Label = "app=picoclaw-agent"
	}
	if opts.PoolSize <= 0 {
		opts.PoolSize = 1
	}
	if strings.TrimSpace(opts.InitCmd) == "" {
		opts.InitCmd = "sleep infinity"
	}

	out := make([]string, 0, opts.PoolSize)
	for i := 1; i <= opts.PoolSize; i++ {
		name := fmt.Sprintf("%s-%d", opts.NamePrefix, i)
		running, exists, err := d.inspectContainer(ctx, name)
		if err != nil {
			return nil, err
		}
		if !exists {
			args := []string{
				"run", "-d",
				"--name", name,
				"--label", opts.Label,
				"--entrypoint", "/bin/sh",
			}
			if strings.TrimSpace(opts.SharedSkillsHostDir) != "" && strings.TrimSpace(opts.SharedSkillsContainerDir) != "" {
				args = append(args, "-v", fmt.Sprintf("%s:%s:ro", opts.SharedSkillsHostDir, opts.SharedSkillsContainerDir))
			}
			if strings.TrimSpace(opts.WorkspaceHostDir) != "" && strings.TrimSpace(opts.WorkspaceContainerDir) != "" {
				args = append(args, "-v", fmt.Sprintf("%s:%s", opts.WorkspaceHostDir, opts.WorkspaceContainerDir))
			}
			args = append(args, opts.Image, "-lc", opts.InitCmd)
			if _, err := runCmd(ctx, d.binary(), args...); err != nil {
				return nil, err
			}
		} else if !running {
			if _, err := runCmd(ctx, d.binary(), "start", name); err != nil {
				return nil, err
			}
		}
		out = append(out, name)
	}
	return out, nil
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

func (d Docker) inspectContainer(ctx context.Context, name string) (running bool, exists bool, err error) {
	out, err := runCmd(ctx, d.binary(), "inspect", "-f", "{{.State.Running}}", name)
	if err != nil {
		if strings.Contains(err.Error(), "No such object") {
			return false, false, nil
		}
		return false, false, err
	}
	v := strings.TrimSpace(out)
	parsed, parseErr := strconv.ParseBool(v)
	if parseErr != nil {
		return false, true, fmt.Errorf("unexpected running state %q for container %s", v, name)
	}
	return parsed, true, nil
}

func runCmd(ctx context.Context, bin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s %s failed: %w, output=%s", bin, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
