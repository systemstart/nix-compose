package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	orchestratev1 "github.com/systemstart/nix-compose/api/orchestrate/v1"
	"github.com/systemstart/nix-compose/pkg/orchestrate"
	"github.com/systemstart/nix-compose/pkg/orchestrate/client"
	"github.com/systemstart/nix-compose/pkg/orchestrate/deploy"
)

var stateCmd = &cobra.Command{
	Use:   "state",
	Short: "Inspect orchestration state",
}

var stateListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all rollouts",
	RunE:  runStateList,
}

var stateShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show details for a rollout",
	Args:  cobra.ExactArgs(1),
	RunE:  runStateShow,
}

func init() {
	stateCmd.AddCommand(stateListCmd)
	stateCmd.AddCommand(stateShowCmd)
}

// openReadOnlyEngine opens the orchestrate engine without active CRI/CNI/Vol clients.
func openReadOnlyEngine() (*orchestrate.Engine, error) {
	engine, err := orchestrate.New(orchestrate.Config{})
	if err != nil {
		return nil, fmt.Errorf("opening engine: %w", err)
	}
	return engine, nil
}

func runStateList(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Try remote path.
	if rc := dialRemote(ctx); rc != nil {
		defer func() { _ = rc.Close() }()
		return remoteStateList(ctx, rc)
	}

	engine, err := openReadOnlyEngine()
	if err != nil {
		return fmt.Errorf("opening engine: %w", err)
	}
	defer func() { _ = engine.Close() }()

	rollouts, err := engine.State()
	if err != nil {
		return fmt.Errorf("listing rollouts: %w", err)
	}

	if len(rollouts) == 0 {
		fmt.Println("No rollouts found.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tKIND\tSTATUS")
	for _, r := range rollouts {
		status := "UNKNOWN"
		if r.Status != nil {
			status = string(r.Status.GetShort())
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", r.InstanceId, r.InstanceKey, status)
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("flushing output: %w", err)
	}
	return nil
}

func runStateShow(_ *cobra.Command, args []string) error {
	ctx := context.Background()

	// Try remote path.
	if rc := dialRemote(ctx); rc != nil {
		defer func() { _ = rc.Close() }()
		return remoteStateShow(ctx, rc, args[0])
	}

	engine, err := openReadOnlyEngine()
	if err != nil {
		return fmt.Errorf("opening engine: %w", err)
	}
	defer func() { _ = engine.Close() }()

	rollouts, err := engine.State()
	if err != nil {
		return fmt.Errorf("listing rollouts: %w", err)
	}

	id := args[0]
	for _, r := range rollouts {
		if r.InstanceId != id {
			continue
		}
		printRolloutDetail(engine, r)
		return nil
	}

	return fmt.Errorf("rollout %q not found", id)
}

// remoteStateList lists rollouts from a remote orchestrate server.
func remoteStateList(ctx context.Context, rc *client.Client) error {
	resp, err := rc.State(ctx)
	if err != nil {
		return fmt.Errorf("remote state: %w", err)
	}

	if len(resp.Rollouts) == 0 {
		fmt.Println("No rollouts found.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tKIND\tSTATUS")
	for _, r := range resp.Rollouts {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", r.InstanceId, r.InstanceKey, r.Status)
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("flushing output: %w", err)
	}
	return nil
}

// remoteStateShow shows details for a single rollout from a remote server.
func remoteStateShow(ctx context.Context, rc *client.Client, id string) error {
	resp, err := rc.State(ctx)
	if err != nil {
		return fmt.Errorf("remote state: %w", err)
	}

	for _, r := range resp.Rollouts {
		if r.InstanceId != id {
			continue
		}
		printRemoteRolloutDetail(r)
		return nil
	}

	return fmt.Errorf("rollout %q not found", id)
}

// printRemoteRolloutDetail prints details for a rollout from the remote server.
func printRemoteRolloutDetail(r *orchestratev1.RolloutInfo) {
	fmt.Printf("ID:     %s\n", r.InstanceId)
	fmt.Printf("Kind:   %s\n", r.InstanceKey)
	fmt.Printf("Status: %s\n", r.Status)

	if len(r.Body) == 0 {
		return
	}
	fmt.Println("Spec:")
	var pretty json.RawMessage
	if json.Unmarshal(r.Body, &pretty) == nil {
		formatted, err := json.MarshalIndent(pretty, "  ", "  ")
		if err == nil {
			fmt.Printf("  %s\n", formatted)
		} else {
			fmt.Printf("  %s\n", r.Body)
		}
	}
}

// printRolloutDetail prints full details for a single rollout.
func printRolloutDetail(engine *orchestrate.Engine, r *deploy.Rollout) {
	status := "UNKNOWN"
	if r.Status != nil {
		status = string(r.Status.GetShort())
	}

	fmt.Printf("ID:     %s\n", r.InstanceId)
	fmt.Printf("Kind:   %s\n", r.InstanceKey)
	fmt.Printf("Status: %s\n", status)

	printRolloutLinks(engine, r)
	printRolloutBody(r)
}

// printRolloutLinks prints dependency and dependent links for a rollout.
func printRolloutLinks(engine *orchestrate.Engine, r *deploy.Rollout) {
	deps, err := engine.DB().GetDependencies(r)
	if err == nil && len(deps) > 0 {
		fmt.Println("Dependencies:")
		for _, dep := range deps {
			fmt.Printf("  - %s (%s)\n", dep.GetId(), dep.GetKey())
		}
	}

	dependents, err := engine.DB().GetDepending(r)
	if err == nil && len(dependents) > 0 {
		fmt.Println("Dependents:")
		for _, dep := range dependents {
			fmt.Printf("  - %s (%s)\n", dep.GetId(), dep.GetKey())
		}
	}
}

// printRolloutBody prints the spec body if present.
func printRolloutBody(r *deploy.Rollout) {
	if len(r.Body) == 0 {
		return
	}
	fmt.Println("Spec:")
	var pretty json.RawMessage
	if json.Unmarshal(r.Body, &pretty) == nil {
		formatted, err := json.MarshalIndent(pretty, "  ", "  ")
		if err == nil {
			fmt.Printf("  %s\n", formatted)
		} else {
			fmt.Printf("  %s\n", r.Body)
		}
	}
}
