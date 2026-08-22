package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/systemstart/nix-compose/pkg/cni"
	"github.com/systemstart/nix-compose/pkg/cri"
	"github.com/systemstart/nix-compose/pkg/envfrom"
	"github.com/systemstart/nix-compose/pkg/eval"
	"github.com/systemstart/nix-compose/pkg/orchestrate"
	"github.com/systemstart/nix-compose/pkg/orchestrate/client"
	"github.com/systemstart/nix-compose/pkg/orchestrate/convert"
	"github.com/systemstart/nix-compose/pkg/volumes"
)

var planCmd = &cobra.Command{
	Use:   "plan [services...]",
	Short: "Show planned changes without applying",
	Long:  "Evaluate Nix config and diff desired state against current state to show what would change.",
	RunE:  runPlan,
}

func runPlan(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	dir := projectDir()

	// Try remote path.
	if rc := dialRemote(ctx); rc != nil {
		defer func() { _ = rc.Close() }()
		return remotePlan(ctx, rc, dir)
	}

	criClient, err := requireCRI(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = criClient.Close() }()

	plan, err := computeOrchestratePlan(ctx, dir, criClient)
	if err != nil {
		return err
	}

	printPlan(plan)
	return nil
}

// remotePlan delegates the plan command to a remote orchestrate server.
func remotePlan(ctx context.Context, rc *client.Client, dir string) error {
	comp, err := evalForOrchestrate(ctx, dir)
	if err != nil {
		return err
	}

	compJSON, err := json.Marshal(comp)
	if err != nil {
		return fmt.Errorf("marshaling composition: %w", err)
	}

	project := projectNameFor(dir, flagProjectName)

	useCNI := len(cni.NewStore().CheckPlugins()) == 0

	resp, err := rc.Plan(ctx, compJSON, project, useCNI)
	if err != nil {
		return fmt.Errorf("remote plan: %w", err)
	}

	printRemoteActions(resp.Actions)
	printRemoteSummary(resp.Creates, resp.Updates, resp.Destroys, resp.Noops)
	return nil
}

// computeOrchestratePlan runs the full eval → transform → convert → bridge → plan pipeline.
func computeOrchestratePlan(ctx context.Context, dir string, criClient *cri.Client) (*orchestrate.Plan, error) {
	comp, err := evalForOrchestrate(ctx, dir)
	if err != nil {
		return nil, err
	}

	project := projectNameFor(dir, flagProjectName)

	useCNI := len(cni.NewStore().CheckPlugins()) == 0

	result, err := convert.Convert(comp, convert.Options{
		Project: project,
		UseCNI:  useCNI,
	})
	if err != nil {
		return nil, fmt.Errorf("converting to manifests: %w", err)
	}

	volStore, err := volumes.NewStore()
	if err != nil {
		return nil, fmt.Errorf("volume store: %w", err)
	}

	cniStore := cni.NewStore()
	engine, err := orchestrate.New(orchestrate.Config{
		CRIClient: criClient,
		CNIStore:  cniStore,
		VolStore:  volStore,
	})
	if err != nil {
		return nil, fmt.Errorf("starting engine: %w", err)
	}
	defer func() { _ = engine.Close() }()

	deployment, conditions, err := convert.Bridge(result, engine.Registry())
	if err != nil {
		return nil, fmt.Errorf("bridging to deployment: %w", err)
	}

	plan, err := engine.Plan(deployment, conditions)
	if err != nil {
		return nil, fmt.Errorf("computing plan: %w", err)
	}

	return plan, nil
}

// evalForOrchestrate runs Nix evaluation, profile filtering, and envFrom resolution.
func evalForOrchestrate(ctx context.Context, dir string) (*eval.Composition, error) {
	announceEval(dir)
	runner := &eval.ExecRunner{Dir: dir}
	evaluator := &eval.Evaluator{
		Runner:     runner,
		ProjectDir: dir,
		FlakeAttr:  flagFlakeAttr,
		Impure:     flagImpure,
	}

	comp, _, err := evaluator.Eval(ctx)
	if err != nil {
		return nil, fmt.Errorf("evaluation failed: %w", err)
	}

	profiles := mergeProfiles(flagProfiles)
	resolver := &envfrom.Resolver{ProjectDir: dir, Runner: runner}
	comp, err = transformComposition(ctx, comp, profiles, resolver)
	if err != nil {
		return nil, err
	}

	printResourceWarnings(comp)
	return comp, nil
}

// printPlan displays the plan in a human-readable format.
func printPlan(plan *orchestrate.Plan) {
	for _, a := range plan.Actions {
		gvk := a.Key.GetGVK()
		symbol := " "
		switch a.Type {
		case orchestrate.ActionCreate:
			symbol = "+"
		case orchestrate.ActionUpdate:
			symbol = "~"
		case orchestrate.ActionDestroy:
			symbol = "-"
		}
		if a.Type == orchestrate.ActionNoOp {
			continue
		}
		_, _ = fmt.Fprintf(os.Stdout, "  %s %-8s %-20s (%s)\n", symbol, gvk.Kind, a.ResourceID, a.Reason)
	}

	creates, updates, destroys, noops := plan.Summary()
	fmt.Println()
	fmt.Printf("Plan: %d to create, %d to update, %d to destroy, %d unchanged\n",
		creates, updates, destroys, noops)
}
