package cri

import (
	"context"
	"fmt"
	"time"

	runtimev1 "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// PullImage pulls a container image via the CRI ImageService.
func (c *Client) PullImage(ctx context.Context, image string) error {
	_, err := c.image.PullImage(ctx, &runtimev1.PullImageRequest{
		Image: &runtimev1.ImageSpec{Image: image},
	})
	if err != nil {
		return fmt.Errorf("cri: pull image %s: %w", image, err)
	}
	return nil
}

// RunPodSandbox creates and starts a pod sandbox.
func (c *Client) RunPodSandbox(ctx context.Context, config *runtimev1.PodSandboxConfig) (string, error) {
	c.applyCgroupParent(ctx, config)

	resp, err := c.runtime.RunPodSandbox(ctx, &runtimev1.RunPodSandboxRequest{
		Config: config,
	})
	if err != nil {
		return "", fmt.Errorf("cri: run pod sandbox: %w", err)
	}
	return resp.GetPodSandboxId(), nil
}

// StopPodSandbox stops a running pod sandbox.
func (c *Client) StopPodSandbox(ctx context.Context, podID string) error {
	_, err := c.runtime.StopPodSandbox(ctx, &runtimev1.StopPodSandboxRequest{
		PodSandboxId: podID,
	})
	if err != nil {
		return fmt.Errorf("cri: stop pod sandbox %s: %w", podID, err)
	}
	return nil
}

// RemovePodSandbox removes a pod sandbox.
func (c *Client) RemovePodSandbox(ctx context.Context, podID string) error {
	_, err := c.runtime.RemovePodSandbox(ctx, &runtimev1.RemovePodSandboxRequest{
		PodSandboxId: podID,
	})
	if err != nil {
		return fmt.Errorf("cri: remove pod sandbox %s: %w", podID, err)
	}
	return nil
}

// ListPodSandboxes returns pods matching the given label selector.
func (c *Client) ListPodSandboxes(ctx context.Context, labels map[string]string) ([]*runtimev1.PodSandbox, error) {
	resp, err := c.runtime.ListPodSandbox(ctx, &runtimev1.ListPodSandboxRequest{
		Filter: &runtimev1.PodSandboxFilter{
			LabelSelector: labels,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("cri: list pod sandboxes: %w", err)
	}
	return resp.GetItems(), nil
}

// CreateContainer creates a container within a pod sandbox.
func (c *Client) CreateContainer(ctx context.Context, podID string, config *runtimev1.ContainerConfig, sandboxConfig *runtimev1.PodSandboxConfig) (string, error) {
	resp, err := c.runtime.CreateContainer(ctx, &runtimev1.CreateContainerRequest{
		PodSandboxId:  podID,
		Config:        config,
		SandboxConfig: sandboxConfig,
	})
	if err != nil {
		return "", fmt.Errorf("cri: create container: %w", err)
	}
	return resp.GetContainerId(), nil
}

// StartContainer starts a created container.
func (c *Client) StartContainer(ctx context.Context, containerID string) error {
	_, err := c.runtime.StartContainer(ctx, &runtimev1.StartContainerRequest{
		ContainerId: containerID,
	})
	if err != nil {
		return fmt.Errorf("cri: start container %s: %w", containerID, err)
	}
	return nil
}

// StopContainer stops a running container with the given timeout (seconds).
func (c *Client) StopContainer(ctx context.Context, containerID string, timeout int64) error {
	_, err := c.runtime.StopContainer(ctx, &runtimev1.StopContainerRequest{
		ContainerId: containerID,
		Timeout:     timeout,
	})
	if err != nil {
		return fmt.Errorf("cri: stop container %s: %w", containerID, err)
	}
	return nil
}

// RemoveContainer removes a container.
func (c *Client) RemoveContainer(ctx context.Context, containerID string) error {
	_, err := c.runtime.RemoveContainer(ctx, &runtimev1.RemoveContainerRequest{
		ContainerId: containerID,
	})
	if err != nil {
		return fmt.Errorf("cri: remove container %s: %w", containerID, err)
	}
	return nil
}

// ListContainers returns containers in the given pod sandbox.
func (c *Client) ListContainers(ctx context.Context, podID string) ([]*runtimev1.Container, error) {
	resp, err := c.runtime.ListContainers(ctx, &runtimev1.ListContainersRequest{
		Filter: &runtimev1.ContainerFilter{
			PodSandboxId: podID,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("cri: list containers in pod %s: %w", podID, err)
	}
	return resp.GetContainers(), nil
}

// ListContainersByLabels returns containers matching the given label selector.
func (c *Client) ListContainersByLabels(ctx context.Context, labels map[string]string) ([]*runtimev1.Container, error) {
	resp, err := c.runtime.ListContainers(ctx, &runtimev1.ListContainersRequest{
		Filter: &runtimev1.ContainerFilter{
			LabelSelector: labels,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("cri: list containers by labels: %w", err)
	}
	return resp.GetContainers(), nil
}

// ExecSync executes a command in a container synchronously.
func (c *Client) ExecSync(ctx context.Context, containerID string, cmd []string, timeoutSecs int64) (*runtimev1.ExecSyncResponse, error) {
	resp, err := c.runtime.ExecSync(ctx, &runtimev1.ExecSyncRequest{
		ContainerId: containerID,
		Cmd:         cmd,
		Timeout:     timeoutSecs,
	})
	if err != nil {
		return nil, fmt.Errorf("cri: exec sync in %s: %w", containerID, err)
	}
	return resp, nil
}

// ContainerStatus returns the status of a container.
func (c *Client) ContainerStatus(ctx context.Context, containerID string) (*runtimev1.ContainerStatusResponse, error) {
	resp, err := c.runtime.ContainerStatus(ctx, &runtimev1.ContainerStatusRequest{
		ContainerId: containerID,
	})
	if err != nil {
		return nil, fmt.Errorf("cri: container status %s: %w", containerID, err)
	}
	return resp, nil
}

// PodSandboxStatus returns the status of a pod sandbox.
func (c *Client) PodSandboxStatus(ctx context.Context, podID string) (*runtimev1.PodSandboxStatusResponse, error) {
	resp, err := c.runtime.PodSandboxStatus(ctx, &runtimev1.PodSandboxStatusRequest{
		PodSandboxId: podID,
	})
	if err != nil {
		return nil, fmt.Errorf("cri: pod sandbox status %s: %w", podID, err)
	}
	return resp, nil
}

// WaitExited polls ContainerStatus until the container reaches CONTAINER_EXITED
// or the context is cancelled. Returns the exit code.
func (c *Client) WaitExited(ctx context.Context, containerID string, pollInterval time.Duration) (int32, error) {
	for {
		resp, err := c.ContainerStatus(ctx, containerID)
		if err != nil {
			return -1, fmt.Errorf("polling container status: %w", err)
		}
		if resp.Status != nil && resp.Status.State == runtimev1.ContainerState_CONTAINER_EXITED {
			return resp.Status.ExitCode, nil
		}
		select {
		case <-ctx.Done():
			return -1, fmt.Errorf("waiting for container exit: %w", ctx.Err())
		case <-time.After(pollInterval):
		}
	}
}
