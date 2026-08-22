package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/systemstart/nix-compose/pkg/cri"
	runtimev1 "k8s.io/cri-api/pkg/apis/runtime/v1"
)

var restartTimeout int

var restartCmd = &cobra.Command{
	Use:   "restart [service...]",
	Short: "Restart services",
	RunE: func(_ *cobra.Command, args []string) error {
		ctx := context.Background()
		criClient, err := requireCRI(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = criClient.Close() }()
		return runRestartCRI(ctx, criClient, resolveProject(), args, int64(restartTimeout))
	},
}

func init() {
	restartCmd.Flags().IntVarP(&restartTimeout, "timeout", "t", 10, "shutdown timeout in seconds")
}

// runRestartCRI stops then starts running containers for the given services via CRI.
func runRestartCRI(ctx context.Context, client *cri.Client, project string, services []string, timeout int64) error {
	containers, err := resolveContainers(ctx, client, project, services)
	if err != nil {
		return err
	}
	for _, c := range containers {
		if c.State != runtimev1.ContainerState_CONTAINER_RUNNING {
			continue
		}
		fmt.Printf("Restarting %s ...\n", c.Service)
		if err := client.StopContainer(ctx, c.ContainerID, timeout); err != nil {
			return fmt.Errorf("stopping %s: %w", c.Service, err)
		}
		if err := client.StartContainer(ctx, c.ContainerID); err != nil {
			return fmt.Errorf("starting %s: %w", c.Service, err)
		}
	}
	return nil
}
