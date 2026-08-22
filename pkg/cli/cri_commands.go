package cli

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/systemstart/nix-compose/pkg/cri"
	"github.com/systemstart/nix-compose/pkg/eval"
	runtimev1 "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// containerInfo holds resolved container details for CLI commands.
type containerInfo struct {
	PodID       string
	ContainerID string
	Service     string
	State       runtimev1.ContainerState
	Image       string
	ExitCode    int32
}

// resolveProject returns the CRI project name from flags or the directory basename.
func resolveProject() string {
	return projectNameFor(projectDir(), flagProjectName)
}

// projectNameFor is the single definition of a project's name, in precedence
// order: an explicit --project-name, then the document's `name:`, then the
// directory basename.
//
// Every command must agree on this or they address different projects — `up`
// would create containers `down` cannot find. The basename fallback is
// compose's default and collides whenever two projects sit in like-named
// directories, which `*/test/integration/` does by construction; `name:`
// exists to break that tie.
func projectNameFor(dir, override string) string {
	if override != "" {
		return override
	}
	if name := eval.ProjectNameFromDir(dir); name != "" {
		return name
	}
	return filepath.Base(dir)
}

// resolveContainers finds containers for the given services (or all services if empty).
func resolveContainers(ctx context.Context, client *cri.Client, project string, services []string) ([]containerInfo, error) {
	labels := map[string]string{
		cri.LabelProject: project,
	}
	pods, err := client.ListPodSandboxes(ctx, labels)
	if err != nil {
		return nil, fmt.Errorf("listing pods: %w", err)
	}

	serviceSet := make(map[string]bool, len(services))
	for _, s := range services {
		serviceSet[s] = true
	}

	var result []containerInfo
	for _, pod := range pods {
		svc := pod.Labels[cri.LabelService]
		if len(services) > 0 && !serviceSet[svc] {
			continue
		}

		infos, err := containersForPod(ctx, client, pod.Id, svc)
		if err != nil {
			return nil, err
		}
		result = append(result, infos...)
	}
	return result, nil
}

// containersForPod lists containers in a pod and builds containerInfo entries.
func containersForPod(ctx context.Context, client *cri.Client, podID, service string) ([]containerInfo, error) {
	ctrs, err := client.ListContainers(ctx, podID)
	if err != nil {
		return nil, fmt.Errorf("listing containers for pod %s: %w", podID, err)
	}
	result := make([]containerInfo, 0, len(ctrs))
	for _, ctr := range ctrs {
		info := containerInfo{
			PodID:       podID,
			ContainerID: ctr.Id,
			Service:     service,
			State:       ctr.State,
		}
		if ctr.Image != nil {
			info.Image = ctr.Image.Image
		}
		if ctr.State == runtimev1.ContainerState_CONTAINER_EXITED {
			status, err := client.ContainerStatus(ctx, ctr.Id)
			if err == nil && status.Status != nil {
				info.ExitCode = status.Status.ExitCode
			}
		}
		result = append(result, info)
	}
	return result, nil
}
