package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/systemstart/nix-compose/pkg/cri"
)

var topCmd = &cobra.Command{
	Use:   "top [service]",
	Short: "Display running processes",
	RunE: func(_ *cobra.Command, args []string) error {
		ctx := context.Background()
		criClient, err := requireCRI(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = criClient.Close() }()
		return runTopCRI(ctx, criClient, resolveProject(), args)
	},
}

// runTopCRI runs "ps aux" inside a container via CRI ExecSync.
func runTopCRI(ctx context.Context, client *cri.Client, project string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("service name required")
	}
	service := args[0]

	containerID, err := lookupContainerID(ctx, client, project, service)
	if err != nil {
		return fmt.Errorf("finding container for %s: %w", service, err)
	}

	resp, err := client.ExecSync(ctx, containerID, []string{"ps", "aux"}, 10)
	if err != nil {
		return fmt.Errorf("exec in %s: %w", service, err)
	}

	_, _ = os.Stdout.Write(resp.Stdout)
	_, _ = os.Stderr.Write(resp.Stderr)
	return nil
}
