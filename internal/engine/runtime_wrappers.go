package engine

type DockerOpencodeExecutor struct {
	DockerRuntimeExecutor
}

func (e *DockerOpencodeExecutor) Name() string {
	return "docker-opencode"
}

type DockerClaudecodeExecutor struct {
	DockerRuntimeExecutor
}

func (e *DockerClaudecodeExecutor) Name() string {
	return "docker-claudecode"
}

type K8sOpencodeExecutor struct {
	K8sRuntimeExecutor
}

func (e *K8sOpencodeExecutor) Name() string {
	return "k8s-opencode"
}

type K8sClaudecodeExecutor struct {
	K8sRuntimeExecutor
}

func (e *K8sClaudecodeExecutor) Name() string {
	return "k8s-claudecode"
}
