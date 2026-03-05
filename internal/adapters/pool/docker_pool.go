package pool

import (
	"context"
	"fmt"
	"sort"

	"cloudclaw/internal/dockerutil"
)

type DockerOptions struct {
	Binary                   string
	LabelSelector            string
	ManagePool               bool
	PoolSize                 int
	Image                    string
	NamePrefix               string
	InitCmd                  string
	SharedSkillsHostDir      string
	SharedSkillsContainerDir string
	WorkspaceHostDir         string
	WorkspaceContainerDir    string
}

type DockerPool struct {
	docker dockerutil.Docker
	opts   DockerOptions
}

func NewDocker(opts DockerOptions) (*DockerPool, error) {
	if opts.LabelSelector == "" {
		return nil, fmt.Errorf("docker label selector is required")
	}
	if opts.ManagePool {
		if opts.Image == "" {
			return nil, fmt.Errorf("docker image is required when manage-pool is enabled")
		}
		if opts.PoolSize <= 0 {
			opts.PoolSize = 1
		}
	}
	return &DockerPool{
		docker: dockerutil.Docker{Binary: opts.Binary},
		opts:   opts,
	}, nil
}

func (p *DockerPool) Name() string {
	return "docker"
}

func (p *DockerPool) ContainerIDs(ctx context.Context) ([]string, error) {
	ids, err := p.resolve(ctx)
	if err != nil {
		return nil, err
	}
	sort.Strings(ids)
	return ids, nil
}

func (p *DockerPool) Reconcile(ctx context.Context) error {
	_, err := p.resolve(ctx)
	return err
}

func (p *DockerPool) Docker() dockerutil.Docker {
	return p.docker
}

func (p *DockerPool) resolve(ctx context.Context) ([]string, error) {
	if p.opts.ManagePool {
		return p.docker.EnsurePool(ctx, dockerutil.EnsurePoolOptions{
			Image:                    p.opts.Image,
			NamePrefix:               p.opts.NamePrefix,
			Label:                    p.opts.LabelSelector,
			PoolSize:                 p.opts.PoolSize,
			InitCmd:                  p.opts.InitCmd,
			SharedSkillsHostDir:      p.opts.SharedSkillsHostDir,
			SharedSkillsContainerDir: p.opts.SharedSkillsContainerDir,
			WorkspaceHostDir:         p.opts.WorkspaceHostDir,
			WorkspaceContainerDir:    p.opts.WorkspaceContainerDir,
		})
	}
	return p.docker.ListRunningContainers(ctx, p.opts.LabelSelector)
}
