package cli

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/systemstart/nix-compose/pkg/cni"
	"github.com/systemstart/nix-compose/pkg/orchestrate"
	"github.com/systemstart/nix-compose/pkg/orchestrate/client"
	"github.com/systemstart/nix-compose/pkg/volumes"
)

var driftCmd = &cobra.Command{
	Use:   "drift",
	Short: "Detect drift between desired and actual state",
	Long:  "Compare rollout state in BoltDB against actual CRI container state to detect drift.",
	RunE:  runDrift,
}

func runDrift(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Try remote path.
	if rc := dialRemote(ctx); rc != nil {
		defer func() { _ = rc.Close() }()
		return remoteDrift(ctx, rc)
	}

	criClient, err := requireCRI(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = criClient.Close() }()

	volStore, err := volumes.NewStore()
	if err != nil {
		return fmt.Errorf("volume store: %w", err)
	}

	cniStore := cni.NewStore()
	engine, err := orchestrate.New(orchestrate.Config{
		CRIClient: criClient,
		CNIStore:  cniStore,
		VolStore:  volStore,
	})
	if err != nil {
		return fmt.Errorf("starting engine: %w", err)
	}
	defer func() { _ = engine.Close() }()

	results, err := engine.DriftCheck(ctx)
	if err != nil {
		return fmt.Errorf("drift check: %w", err)
	}

	if len(results) == 0 {
		fmt.Println("No drift detected.")
		return nil
	}

	printDriftResults(results)
	os.Exit(1)
	return nil
}

// remoteDrift delegates the drift command to a remote orchestrate server.
func remoteDrift(ctx context.Context, rc *client.Client) error {
	resp, err := rc.Drift(ctx, "")
	if err != nil {
		return fmt.Errorf("remote drift: %w", err)
	}

	if len(resp.Items) == 0 {
		fmt.Println("No drift detected.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tKIND\tEXPECTED\tACTUAL\tREASON")
	for _, item := range resp.Items {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			item.ResourceId, item.Kind, item.Expected, item.Actual, item.Reason)
	}
	_ = w.Flush()

	os.Exit(1)
	return nil
}

// printDriftResults prints drift results in tabular format.
func printDriftResults(results []orchestrate.DriftResult) {
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tKIND\tEXPECTED\tACTUAL\tREASON")
	for _, r := range results {
		gvk := r.Key.GetGVK()
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			r.ResourceID, gvk.Kind, r.Expected, r.Actual, r.Reason)
	}
	_ = w.Flush()
}
