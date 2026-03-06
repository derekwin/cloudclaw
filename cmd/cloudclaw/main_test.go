package main

import "testing"

func TestApplyExecutorRuntimeDefaultsForClaudecode(t *testing.T) {
	k8sLabel := defaultK8sLabelSelector
	dockerLabel := defaultDockerLabelSelector
	dockerImage := defaultDockerImage
	dockerNamePrefix := defaultDockerNamePrefix

	applyExecutorRuntimeDefaults("docker-claudecode", &k8sLabel, &dockerLabel, &dockerImage, &dockerNamePrefix)

	if k8sLabel != "app=claudecode-agent" {
		t.Fatalf("unexpected k8s label: %s", k8sLabel)
	}
	if dockerLabel != "app=claudecode-agent" {
		t.Fatalf("unexpected docker label: %s", dockerLabel)
	}
	if dockerImage != "claudecode:latest" {
		t.Fatalf("unexpected docker image: %s", dockerImage)
	}
	if dockerNamePrefix != "claudecode-agent" {
		t.Fatalf("unexpected docker name prefix: %s", dockerNamePrefix)
	}
}

func TestApplyExecutorRuntimeDefaultsKeepsCustomValues(t *testing.T) {
	k8sLabel := "app=custom-k8s"
	dockerLabel := "app=custom-docker"
	dockerImage := "custom/image:1"
	dockerNamePrefix := "custom-prefix"

	applyExecutorRuntimeDefaults("docker-claudecode", &k8sLabel, &dockerLabel, &dockerImage, &dockerNamePrefix)

	if k8sLabel != "app=custom-k8s" {
		t.Fatalf("k8s label should not be overridden: %s", k8sLabel)
	}
	if dockerLabel != "app=custom-docker" {
		t.Fatalf("docker label should not be overridden: %s", dockerLabel)
	}
	if dockerImage != "custom/image:1" {
		t.Fatalf("docker image should not be overridden: %s", dockerImage)
	}
	if dockerNamePrefix != "custom-prefix" {
		t.Fatalf("docker name prefix should not be overridden: %s", dockerNamePrefix)
	}
}

func TestApplyExecutorRuntimeDefaultsForOpencodeNoChange(t *testing.T) {
	k8sLabel := defaultK8sLabelSelector
	dockerLabel := defaultDockerLabelSelector
	dockerImage := defaultDockerImage
	dockerNamePrefix := defaultDockerNamePrefix

	applyExecutorRuntimeDefaults("docker-opencode", &k8sLabel, &dockerLabel, &dockerImage, &dockerNamePrefix)

	if k8sLabel != defaultK8sLabelSelector {
		t.Fatalf("unexpected k8s label: %s", k8sLabel)
	}
	if dockerLabel != defaultDockerLabelSelector {
		t.Fatalf("unexpected docker label: %s", dockerLabel)
	}
	if dockerImage != defaultDockerImage {
		t.Fatalf("unexpected docker image: %s", dockerImage)
	}
	if dockerNamePrefix != defaultDockerNamePrefix {
		t.Fatalf("unexpected docker name prefix: %s", dockerNamePrefix)
	}
}

func TestRuntimeNameForExecutor(t *testing.T) {
	if got := runtimeNameForExecutor("docker-opencode"); got != "opencode" {
		t.Fatalf("unexpected runtime for docker-opencode: %s", got)
	}
	if got := runtimeNameForExecutor("k8s-claudecode"); got != "claudecode" {
		t.Fatalf("unexpected runtime for k8s-claudecode: %s", got)
	}
	if got := runtimeNameForExecutor(""); got != "opencode" {
		t.Fatalf("unexpected default runtime: %s", got)
	}
}
