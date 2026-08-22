package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/systemstart/nix-compose/pkg/cri"
	"github.com/systemstart/nix-compose/pkg/orchestrate"
	"github.com/systemstart/nix-compose/pkg/orchestrate/deploy"
	"github.com/systemstart/nix-compose/pkg/orchestrate/state"
	"github.com/systemstart/nix-compose/pkg/orchestrate/typing"
	runtimev1 "k8s.io/cri-api/pkg/apis/runtime/v1"
)

var (
	stopTimeout int
	stopForce   bool
)

var stopCmd = &cobra.Command{
	Use:   "stop [service...]",
	Short: "Stop services without removing them",
	RunE: func(_ *cobra.Command, args []string) error {
		ctx := context.Background()
		criClient, err := requireCRI(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = criClient.Close() }()
		return runStopCRI(ctx, criClient, resolveProject(), args, int64(stopTimeout))
	},
}

func init() {
	stopCmd.Flags().IntVarP(&stopTimeout, "timeout", "t", 10, "shutdown timeout in seconds")
	stopCmd.Flags().BoolVar(&stopForce, "force", false, "suppress dependency warnings")
}

// runStopCRI stops running containers for the given services via CRI.
func runStopCRI(ctx context.Context, client *cri.Client, project string, services []string, timeout int64) error {
	containers, err := resolveContainers(ctx, client, project, services)
	if err != nil {
		return err
	}

	if !stopForce {
		warnDependents(project, containers)
	}

	for _, c := range containers {
		if c.State != runtimev1.ContainerState_CONTAINER_RUNNING {
			continue
		}
		fmt.Printf("Stopping %s ...\n", c.Service)
		if err := client.StopContainer(ctx, c.ContainerID, timeout); err != nil {
			return fmt.Errorf("stopping %s: %w", c.Service, err)
		}
	}
	return nil
}

// warnDependents checks if any target service has running dependents
// and prints a warning. This is advisory only — it does not block the stop.
func warnDependents(project string, containers []containerInfo) {
	engine, err := openReadOnlyEngine()
	if err != nil {
		return
	}
	defer func() { _ = engine.Close() }()

	// Build set of services being stopped.
	stopping := make(map[string]bool)
	for _, c := range containers {
		if c.State == runtimev1.ContainerState_CONTAINER_RUNNING {
			stopping[c.Service] = true
		}
	}

	for svc := range stopping {
		if running := findRunningDependents(engine, project, svc, stopping); len(running) > 0 {
			fmt.Printf("Warning: %s has running dependents: %v\n", svc, running)
		}
	}
}

// findRunningDependents returns resource IDs of running dependents for a
// service that are not themselves being stopped.
func findRunningDependents(engine *orchestrate.Engine, project, svc string, stopping map[string]bool) []string {
	resourceID := project + "/" + svc
	ref := typing.NewReference(resourceID, "")
	dependents, err := engine.DB().GetDepending(ref)
	if err != nil {
		return nil
	}

	var running []string
	for _, dep := range dependents {
		depID := dep.GetId()
		if isBeingStopped(depID, project, stopping) {
			continue
		}
		if isDepRunning(engine.DB(), depID) {
			running = append(running, depID)
		}
	}
	return running
}

// isDepRunning checks if a dependent resource is in a running or success state.
func isDepRunning(db *state.DB, depID string) bool {
	rollout, err := deploy.LoadRollout(db, depID)
	if err != nil || rollout == nil || rollout.Status == nil {
		return false
	}
	return rollout.Status.Short == typing.RolloutStatusSuccess ||
		rollout.Status.Short == typing.RolloutStatusRunning
}

// isBeingStopped checks if a dependent resource belongs to a service being stopped.
func isBeingStopped(depID, project string, stopping map[string]bool) bool {
	prefix := project + "/"
	if len(depID) > len(prefix) && depID[:len(prefix)] == prefix {
		svcName := depID[len(prefix):]
		return stopping[svcName]
	}
	return false
}
