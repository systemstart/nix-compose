package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/systemstart/nix-compose/pkg/cri"
	runtimev1 "k8s.io/cri-api/pkg/apis/runtime/v1"
)

var killSignal string

var killCmd = &cobra.Command{
	Use:   "kill [service...]",
	Short: "Force stop services",
	RunE: func(_ *cobra.Command, args []string) error {
		ctx := context.Background()
		criClient, err := requireCRI(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = criClient.Close() }()
		return runKillCRI(ctx, criClient, resolveProject(), args)
	},
}

func init() {
	killCmd.Flags().StringVarP(&killSignal, "signal", "s", "", "signal to send (default: SIGKILL)")
}

// runKillCRI force-stops running containers for the given services via CRI (timeout=0 → immediate).
func runKillCRI(ctx context.Context, client *cri.Client, project string, services []string) error {
	containers, err := resolveContainers(ctx, client, project, services)
	if err != nil {
		return err
	}
	for _, c := range containers {
		if c.State != runtimev1.ContainerState_CONTAINER_RUNNING {
			continue
		}
		fmt.Printf("Killing %s ...\n", c.Service)
		if err := client.StopContainer(ctx, c.ContainerID, 0); err != nil {
			return fmt.Errorf("killing %s: %w", c.Service, err)
		}
	}
	return nil
}
