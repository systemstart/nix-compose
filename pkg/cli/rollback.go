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

var rollbackDryRun bool

var rollbackCmd = &cobra.Command{
	Use:   "rollback [list | <deployment-id>]",
	Short: "List past deployments or rollback to a previous deployment",
	Long: `Rollback manages deployment history:

  nix-compose rollback list              # list past deployments
  nix-compose rollback <deployment-id>   # revert to that state
  nix-compose rollback --dry-run <id>    # preview without applying`,
	Args: cobra.MaximumNArgs(1),
	RunE: runRollback,
}

func init() {
	rollbackCmd.Flags().BoolVar(&rollbackDryRun, "dry-run", false, "preview changes without applying")
}

func runRollback(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	if len(args) == 0 || args[0] == "list" {
		return runRollbackList(ctx)
	}

	return runRollbackApply(ctx, args[0])
}

func runRollbackList(ctx context.Context) error {
	// Try remote path.
	if rc := dialRemote(ctx); rc != nil {
		defer func() { _ = rc.Close() }()
		return remoteRollbackList(ctx, rc)
	}

	engine, cleanup, err := newRollbackEngine()
	if err != nil {
		return err
	}
	defer cleanup()

	deployments, err := engine.ListDeployments()
	if err != nil {
		return fmt.Errorf("listing deployments: %w", err)
	}

	if len(deployments) == 0 {
		fmt.Println("No deployments found.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tRESOURCES")
	for _, d := range deployments {
		createCount := 0
		for _, req := range d.Requests {
			if req.GetType() == "CREATE" {
				createCount++
			}
		}
		_, _ = fmt.Fprintf(w, "%s\t%d\n", d.Id, createCount)
	}
	_ = w.Flush()
	return nil
}

func runRollbackApply(ctx context.Context, deploymentID string) error {
	// Try remote path.
	if rc := dialRemote(ctx); rc != nil {
		defer func() { _ = rc.Close() }()
		return remoteRollbackApply(ctx, rc, deploymentID)
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

	plan, err := engine.Rollback(ctx, deploymentID, rollbackDryRun)
	if err != nil {
		return fmt.Errorf("rollback: %w", err)
	}

	printPlan(plan)

	if rollbackDryRun {
		fmt.Println("\nDry run — no changes applied.")
	} else {
		fmt.Println("\nRollback applied successfully.")
	}
	return nil
}

// newRollbackEngine creates an engine for read-only rollback listing.
func newRollbackEngine() (*orchestrate.Engine, func(), error) {
	engine, err := orchestrate.New(orchestrate.Config{})
	if err != nil {
		return nil, nil, fmt.Errorf("starting engine: %w", err)
	}
	return engine, func() { _ = engine.Close() }, nil
}

// remoteRollbackList delegates rollback list to a remote server.
func remoteRollbackList(ctx context.Context, rc *client.Client) error {
	// Use the State RPC to get rollout info as a proxy for deployment listing.
	// Full deployment listing via gRPC would need a dedicated RPC.
	fmt.Println("Remote rollback list is not yet supported. Use local mode.")
	return nil
}

// remoteRollbackApply delegates rollback apply to a remote server.
func remoteRollbackApply(ctx context.Context, rc *client.Client, deploymentID string) error {
	resp, err := rc.Rollback(ctx, deploymentID, rollbackDryRun)
	if err != nil {
		return fmt.Errorf("remote rollback: %w", err)
	}

	printRemoteActions(resp.Actions)
	printRemoteSummary(resp.Creates, resp.Updates, resp.Destroys, resp.Noops)

	if rollbackDryRun {
		fmt.Println("\nDry run — no changes applied.")
	} else {
		fmt.Println("\nRollback applied successfully.")
	}
	return nil
}
