package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/systemstart/nix-compose/pkg/cri"
	"github.com/systemstart/nix-compose/pkg/volumes"
	runtimev1 "k8s.io/cri-api/pkg/apis/runtime/v1"
)

var (
	rmForce   bool
	rmStop    bool
	rmVolumes bool
)

var rmCmd = &cobra.Command{
	Use:   "rm [service...]",
	Short: "Remove stopped service containers",
	RunE: func(_ *cobra.Command, args []string) error {
		ctx := context.Background()
		criClient, err := requireCRI(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = criClient.Close() }()
		return runRmCRI(ctx, criClient, resolveProject(), args, rmStop, rmVolumes)
	},
}

func init() {
	rmCmd.Flags().BoolVarP(&rmForce, "force", "f", false, "don't ask to confirm removal")
	rmCmd.Flags().BoolVarP(&rmStop, "stop", "s", false, "stop containers before removing")
	rmCmd.Flags().BoolVarP(&rmVolumes, "volumes", "v", false, "remove anonymous volumes")
}

// runRmCRI removes containers for the given services via CRI.
// If stop is true, running containers are stopped first.
// If removeVolumes is true, named volumes for the project are removed.
func runRmCRI(ctx context.Context, client *cri.Client, project string, services []string, stop, removeVolumes bool) error {
	containers, err := resolveContainers(ctx, client, project, services)
	if err != nil {
		return err
	}

	for _, c := range containers {
		if err := removeContainerCRI(ctx, client, c, stop); err != nil {
			return err
		}
	}

	if removeVolumes {
		cleanupVolumes(project)
	}

	return nil
}

func removeContainerCRI(ctx context.Context, client *cri.Client, c containerInfo, stop bool) error {
	if c.State == runtimev1.ContainerState_CONTAINER_RUNNING {
		if !stop {
			return nil
		}
		fmt.Printf("Stopping %s ...\n", c.Service)
		if err := client.StopContainer(ctx, c.ContainerID, 10); err != nil {
			return fmt.Errorf("stopping %s: %w", c.Service, err)
		}
	}
	fmt.Printf("Removing %s ...\n", c.Service)
	if err := client.RemoveContainer(ctx, c.ContainerID); err != nil {
		return fmt.Errorf("removing container %s: %w", c.Service, err)
	}
	if err := client.StopPodSandbox(ctx, c.PodID); err != nil {
		return fmt.Errorf("stopping pod for %s: %w", c.Service, err)
	}
	if err := client.RemovePodSandbox(ctx, c.PodID); err != nil {
		return fmt.Errorf("removing pod for %s: %w", c.Service, err)
	}
	return nil
}

func cleanupVolumes(project string) {
	store, err := volumes.NewStore()
	if err != nil {
		fmt.Printf("Warning: failed to init volume store: %v\n", err)
		return
	}
	if err := store.RemoveAll(project); err != nil {
		fmt.Printf("Warning: failed to remove volumes: %v\n", err)
	}
}
