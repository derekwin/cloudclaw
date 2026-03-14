package engine

type DockerOpencodeExecutor struct {
	DockerRuntimeExecutor
}

func (e *DockerOpencodeExecutor) Name() string {
	return "docker-opencode"
}

type DockerOpenclawExecutor struct {
	DockerRuntimeExecutor
}

func (e *DockerOpenclawExecutor) Name() string {
	return "docker-openclaw"
}

type DockerClaudecodeExecutor struct {
	DockerRuntimeExecutor
}

func (e *DockerClaudecodeExecutor) Name() string {
	return "docker-claudecode"
}

type DockerMockExecutor struct {
	DockerRuntimeExecutor
}

func (e *DockerMockExecutor) Name() string {
	return "docker-mock"
}

type K8sOpencodeExecutor struct {
	K8sRuntimeExecutor
}

func (e *K8sOpencodeExecutor) Name() string {
	return "k8s-opencode"
}

type K8sOpenclawExecutor struct {
	K8sRuntimeExecutor
}

func (e *K8sOpenclawExecutor) Name() string {
	return "k8s-openclaw"
}

type K8sClaudecodeExecutor struct {
	K8sRuntimeExecutor
}

func (e *K8sClaudecodeExecutor) Name() string {
	return "k8s-claudecode"
}
