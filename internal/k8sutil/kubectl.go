package k8sutil

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type Kubectl struct {
	Namespace string
	Context   string
	Binary    string
}

func (k Kubectl) ListRunningPods(ctx context.Context, labelSelector string) ([]string, error) {
	if strings.TrimSpace(labelSelector) == "" {
		return nil, fmt.Errorf("label selector is required")
	}
	args := k.baseArgs()
	args = append(args,
		"get", "pods",
		"-l", labelSelector,
		"--field-selector", "status.phase=Running",
		"-o", `jsonpath={range .items[*]}{.metadata.name}{"\n"}{end}`,
	)
	out, err := runCmd(ctx, k.binary(), args...)
	if err != nil {
		return nil, err
	}
	pods := []string{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		pods = append(pods, name)
	}
	return pods, nil
}

func (k Kubectl) Exec(ctx context.Context, pod string, remoteCommand string) (string, error) {
	args := k.baseArgs()
	args = append(args, "exec", pod, "--", "sh", "-lc", remoteCommand)
	return runCmd(ctx, k.binary(), args...)
}

func (k Kubectl) CopyToPod(ctx context.Context, srcPath, pod, destPath string) error {
	args := k.baseArgs()
	args = append(args, "cp", srcPath, fmt.Sprintf("%s:%s", pod, destPath))
	_, err := runCmd(ctx, k.binary(), args...)
	return err
}

func (k Kubectl) CopyFromPod(ctx context.Context, pod, srcPath, destPath string) error {
	args := k.baseArgs()
	args = append(args, "cp", fmt.Sprintf("%s:%s", pod, srcPath), destPath)
	_, err := runCmd(ctx, k.binary(), args...)
	return err
}

func (k Kubectl) baseArgs() []string {
	args := make([]string, 0, 4)
	if strings.TrimSpace(k.Context) != "" {
		args = append(args, "--context", k.Context)
	}
	if strings.TrimSpace(k.Namespace) != "" {
		args = append(args, "-n", k.Namespace)
	}
	return args
}

func (k Kubectl) binary() string {
	if strings.TrimSpace(k.Binary) != "" {
		return k.Binary
	}
	return "kubectl"
}

func runCmd(ctx context.Context, bin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s %s failed: %w, output=%s", bin, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
