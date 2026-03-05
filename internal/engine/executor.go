package engine

import (
	"context"
	"strings"

	"cloudclaw/internal/model"
)

type Executor interface {
	Name() string
	Execute(ctx context.Context, containerID string, task model.Task, workspaceDir string) (model.TokenUsage, error)
}

func estimateTokens(s string) int {
	n := len(strings.TrimSpace(s))
	if n == 0 {
		return 0
	}
	// Simple fallback estimator used when executor does not provide usage details.
	return (n + 3) / 4
}
