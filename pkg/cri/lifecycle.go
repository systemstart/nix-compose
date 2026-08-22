package cri

import (
	"context"
	"fmt"
	"os"

	"github.com/systemstart/nix-compose/pkg/eval"
)

// ServiceUpOptions holds options for bringing up a service.
type ServiceUpOptions struct {
	Project        string
	Version        string
	CompVolumes    map[string]eval.Volume
	VolumeResolver VolumeResolver
	UseCNI         bool
}

// resolveNetworkMode determines the pod network mode for a service.
func resolveNetworkMode(svc eval.Service, useCNI bool) PodNetworkMode {
	if svc.NetworkMode == "host" {
		return PodNetworkHost
	}
	if useCNI {
		return PodNetworkCNI
	}
	return PodNetworkHost
}

// ServiceUp brings up one service: tear down existing → pull → pod → container → start.
func (c *Client) ServiceUp(ctx context.Context, name string, svc eval.Service, opts ServiceUpOptions) error {
	// Tear down any existing pod for this service (idempotent).
	existing, err := c.ListPodSandboxes(ctx, map[string]string{
		LabelProject: opts.Project,
		LabelService: name,
	})
	if err != nil {
		return fmt.Errorf("listing existing pods for %s: %w", name, err)
	}
	for _, pod := range existing {
		if err := c.teardownPod(ctx, pod.Id, 10); err != nil {
			return fmt.Errorf("tearing down old pod %s: %w", pod.Id, err)
		}
	}

	// Ensure log directory exists.
	logDir := fmt.Sprintf("/tmp/nix-compose-logs/%s/%s", opts.Project, name)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return fmt.Errorf("creating log directory: %w", err)
	}

	// Make the image available — imported from the Nix store, or pulled.
	image, err := c.EnsureImage(ctx, svc.Image)
	if err != nil {
		return fmt.Errorf("resolving image for %s: %w", name, err)
	}
	svc.Image = image

	// Create pod sandbox.
	netMode := resolveNetworkMode(svc, opts.UseCNI)
	podConfig := BuildPodConfig(opts.Project, name, svc, opts.Version, netMode)
	podID, err := c.RunPodSandbox(ctx, podConfig)
	if err != nil {
		return fmt.Errorf("running pod for %s: %w", name, err)
	}

	// Build mounts.
	mounts, err := BuildMounts(svc, opts.Project, opts.CompVolumes, opts.VolumeResolver)
	if err != nil {
		return fmt.Errorf("building mounts for %s: %w", name, err)
	}

	// Create and start container.
	ctrConfig := BuildContainerConfig(name, svc, opts.Project, opts.Version, mounts)
	ctrID, err := c.CreateContainer(ctx, podID, ctrConfig, podConfig)
	if err != nil {
		return fmt.Errorf("creating container for %s: %w", name, err)
	}
	if err := c.StartContainer(ctx, ctrID); err != nil {
		return fmt.Errorf("starting container for %s: %w", name, err)
	}

	return nil
}

// ProjectDown tears down all pods belonging to a project.
func (c *Client) ProjectDown(ctx context.Context, project string, timeout int64) error {
	pods, err := c.ListPodSandboxes(ctx, map[string]string{
		LabelProject: project,
	})
	if err != nil {
		return fmt.Errorf("listing pods for project %s: %w", project, err)
	}

	for _, pod := range pods {
		if err := c.teardownPod(ctx, pod.Id, timeout); err != nil {
			return fmt.Errorf("tearing down pod %s: %w", pod.Id, err)
		}
	}
	return nil
}

// CompositionUp brings up all services in a composition.
func (c *Client) CompositionUp(ctx context.Context, comp *eval.Composition, opts ServiceUpOptions) error {
	if opts.CompVolumes == nil {
		opts.CompVolumes = comp.Volumes
	}
	for name, svc := range comp.Services {
		if err := c.ServiceUp(ctx, name, svc, opts); err != nil {
			return fmt.Errorf("service %s: %w", name, err)
		}
	}
	return nil
}

// ServiceDown tears down all pods for a single service in a project.
func (c *Client) ServiceDown(ctx context.Context, project, service string, timeout int64) error {
	pods, err := c.ListPodSandboxes(ctx, map[string]string{
		LabelProject: project,
		LabelService: service,
	})
	if err != nil {
		return fmt.Errorf("listing pods for service %s: %w", service, err)
	}
	for _, pod := range pods {
		if err := c.teardownPod(ctx, pod.Id, timeout); err != nil {
			return fmt.Errorf("tearing down pod %s for service %s: %w", pod.Id, service, err)
		}
	}
	return nil
}

// teardownPod stops and removes all containers in a pod, then stops and removes the pod.
func (c *Client) teardownPod(ctx context.Context, podID string, timeout int64) error {
	ctrs, err := c.ListContainers(ctx, podID)
	if err != nil {
		return err
	}
	for _, ctr := range ctrs {
		if err := c.StopContainer(ctx, ctr.Id, timeout); err != nil {
			return err
		}
		if err := c.RemoveContainer(ctx, ctr.Id); err != nil {
			return err
		}
	}
	if err := c.StopPodSandbox(ctx, podID); err != nil {
		return err
	}
	return c.RemovePodSandbox(ctx, podID)
}
