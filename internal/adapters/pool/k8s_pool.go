package pool

import (
	"context"
	"fmt"
	"sort"

	"cloudclaw/internal/k8sutil"
)

type K8sOptions struct {
	Namespace     string
	Context       string
	KubectlBinary string
	LabelSelector string
}

type K8sPool struct {
	kubectl k8sutil.Kubectl
	label   string
}

func NewK8s(opts K8sOptions) (*K8sPool, error) {
	if opts.LabelSelector == "" {
		return nil, fmt.Errorf("k8s label selector is required")
	}
	return &K8sPool{
		kubectl: k8sutil.Kubectl{
			Namespace: opts.Namespace,
			Context:   opts.Context,
			Binary:    opts.KubectlBinary,
		},
		label: opts.LabelSelector,
	}, nil
}

func (p *K8sPool) Name() string {
	return "k8s"
}

func (p *K8sPool) ContainerIDs(ctx context.Context) ([]string, error) {
	pods, err := p.kubectl.ListRunningPods(ctx, p.label)
	if err != nil {
		return nil, err
	}
	sort.Strings(pods)
	return pods, nil
}

func (p *K8sPool) Reconcile(ctx context.Context) error {
	_, err := p.ContainerIDs(ctx)
	return err
}
