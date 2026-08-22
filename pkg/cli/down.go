package cli

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/systemstart/nix-compose/pkg/cni"
	"github.com/systemstart/nix-compose/pkg/cri"
	"github.com/systemstart/nix-compose/pkg/depgraph"
	"github.com/systemstart/nix-compose/pkg/eval"
	"github.com/systemstart/nix-compose/pkg/gcroot"
	"github.com/systemstart/nix-compose/pkg/orchestrate/client"
	"github.com/systemstart/nix-compose/pkg/volumes"
)

var (
	downVolumes       bool
	downTimeout       int
	downRemoveOrphans bool
	downRmi           string
)

var downCmd = &cobra.Command{
	Use:   "down",
	Short: "Stop services and remove GC root",
	RunE:  runDown,
}

func init() {
	downCmd.Flags().BoolVarP(&downVolumes, "volumes", "v", false, "remove named volumes")
	downCmd.Flags().IntVar(&downTimeout, "timeout", 0, "shutdown timeout in seconds")
	downCmd.Flags().BoolVar(&downRemoveOrphans, "remove-orphans", false, "remove containers for services not defined in the config")
	downCmd.Flags().StringVar(&downRmi, "rmi", "", "remove images (all or local)")
}

// CRIDown tears down all pods for a project via CRI, then removes the GC root.
// If removeVolumes is true, named volume directories are also removed.
// When a composition is available, services are stopped in reverse dependency order.
func CRIDown(ctx context.Context, client *cri.Client, project, projectDir string, timeout int, removeVolumes bool) error {
	return CRIDownOrdered(ctx, client, project, projectDir, timeout, removeVolumes, nil)
}

// shutdownServices stops services in reverse dependency order or unordered.
func shutdownServices(ctx context.Context, client *cri.Client, project string, timeout int, comp *eval.Composition) error {
	if comp != nil {
		levels, err := depgraph.StopOrder(comp)
		if err == nil {
			for _, level := range levels {
				for _, name := range level {
					if err := client.ServiceDown(ctx, project, name, int64(timeout)); err != nil {
						return fmt.Errorf("cri down service %s: %w", name, err)
					}
				}
			}
			return nil
		}
	}
	if err := client.ProjectDown(ctx, project, int64(timeout)); err != nil {
		return fmt.Errorf("cri down: %w", err)
	}
	return nil
}

// CRIDownOrdered tears down services in reverse dependency order when a
// composition is provided. Falls back to unordered ProjectDown when comp is nil.
func CRIDownOrdered(ctx context.Context, client *cri.Client, project, projectDir string, timeout int, removeVolumes bool, comp *eval.Composition) error {
	if err := shutdownServices(ctx, client, project, timeout, comp); err != nil {
		return err
	}
	criDownCleanup(project, projectDir, removeVolumes)
	return nil
}

// criDownCleanup removes CNI config, volumes, and GC root.
func criDownCleanup(project, projectDir string, removeVolumes bool) {
	cniStore := cni.NewStore()
	if err := cniStore.Remove(project); err != nil {
		fmt.Printf("Warning: failed to remove CNI config: %v\n", err)
	}
	if removeVolumes {
		store, err := volumes.NewStore()
		if err != nil {
			fmt.Printf("Warning: failed to init volume store: %v\n", err)
		} else if err := store.RemoveAll(project); err != nil {
			fmt.Printf("Warning: failed to remove volumes: %v\n", err)
		}
	}
	if err := gcroot.Remove(projectDir); err != nil {
		fmt.Printf("Warning: failed to remove GC root: %v\n", err)
	}
}

// remoteDown delegates the down command to a remote orchestrate server.
func remoteDown(ctx context.Context, rc *client.Client, dir string) error {
	project := projectNameFor(dir, flagProjectName)
	timeout := int32(downTimeout) //nolint:gosec // timeout fits in int32
	if timeout == 0 {
		timeout = 10
	}

	// Optionally eval composition for ordered shutdown.
	var compJSON []byte
	runner := &eval.ExecRunner{Dir: dir}
	evaluator := &eval.Evaluator{
		Runner:     runner,
		ProjectDir: dir,
		FlakeAttr:  flagFlakeAttr,
		Impure:     flagImpure,
	}
	if comp, _, err := evaluator.Eval(ctx); err == nil {
		compJSON, _ = json.Marshal(comp)
	}

	fmt.Println("Tearing down via remote orchestrate server...")
	if err := rc.Teardown(ctx, project, timeout, downVolumes, compJSON); err != nil {
		return fmt.Errorf("remote teardown: %w", err)
	}
	fmt.Println("Teardown complete.")
	return nil
}

func runDown(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	dir := projectDir()

	// Try remote path.
	if rc := dialRemote(ctx); rc != nil {
		defer func() { _ = rc.Close() }()
		return remoteDown(ctx, rc, dir)
	}

	// Try microVM path.
	if upMicroVM {
		return microvmDown(ctx, dir)
	}

	// CRI path (default).
	criClient, err := requireCRI(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = criClient.Close() }()
	project := projectNameFor(dir, flagProjectName)
	timeout := downTimeout
	if timeout == 0 {
		timeout = 10
	}

	// Try to evaluate the composition for ordered shutdown.
	var comp *eval.Composition
	runner := &eval.ExecRunner{Dir: dir}
	evaluator := &eval.Evaluator{
		Runner:     runner,
		ProjectDir: dir,
		FlakeAttr:  flagFlakeAttr,
		Impure:     flagImpure,
	}
	if c, _, err := evaluator.Eval(ctx); err == nil {
		comp = c
	}

	return CRIDownOrdered(ctx, criClient, project, dir, timeout, downVolumes, comp)
}
