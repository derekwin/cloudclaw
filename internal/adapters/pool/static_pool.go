package pool

import (
	"context"
	"fmt"
	"strings"
)

type StaticPool struct {
	IDs []string
}

func NewStatic(ids []string) (*StaticPool, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("static pool requires at least one id")
	}
	seen := make(map[string]struct{}, len(ids))
	cp := make([]string, 0, len(ids))
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		cp = append(cp, id)
	}
	if len(cp) == 0 {
		return nil, fmt.Errorf("static pool requires at least one non-empty id")
	}
	return &StaticPool{IDs: cp}, nil
}

func (p *StaticPool) Name() string {
	return "static"
}

func (p *StaticPool) ContainerIDs(ctx context.Context) ([]string, error) {
	_ = ctx
	cp := make([]string, len(p.IDs))
	copy(cp, p.IDs)
	return cp, nil
}

func (p *StaticPool) Reconcile(ctx context.Context) error {
	_ = ctx
	return nil
}
