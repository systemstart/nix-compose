package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/systemstart/nix-compose/pkg/cri"
	runtimev1 "k8s.io/cri-api/pkg/apis/runtime/v1"
)

var startCmd = &cobra.Command{
	Use:   "start [service...]",
	Short: "Start stopped services",
	RunE: func(_ *cobra.Command, args []string) error {
		ctx := context.Background()
		criClient, err := requireCRI(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = criClient.Close() }()
		return runStartCRI(ctx, criClient, resolveProject(), args)
	},
}

// runStartCRI starts exited containers for the given services via CRI.
func runStartCRI(ctx context.Context, client *cri.Client, project string, services []string) error {
	containers, err := resolveContainers(ctx, client, project, services)
	if err != nil {
		return err
	}
	for _, c := range containers {
		if c.State != runtimev1.ContainerState_CONTAINER_EXITED {
			continue
		}
		fmt.Printf("Starting %s ...\n", c.Service)
		if err := client.StartContainer(ctx, c.ContainerID); err != nil {
			return fmt.Errorf("starting %s: %w", c.Service, err)
		}
	}
	return nil
}
