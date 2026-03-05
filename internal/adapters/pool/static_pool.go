package pool

import (
	"context"
	"fmt"
)

type StaticPool struct {
	IDs []string
}

func NewStatic(ids []string) (*StaticPool, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("static pool requires at least one id")
	}
	cp := make([]string, len(ids))
	copy(cp, ids)
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
