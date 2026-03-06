package engine

type DockerOpenclawExecutor struct {
	DockerPicoclawExecutor
}

func (e *DockerOpenclawExecutor) Name() string {
	return "docker-openclaw"
}
