package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/systemstart/nix-compose/pkg/cri"
	"github.com/systemstart/nix-compose/pkg/eval"
)

var pullCmd = &cobra.Command{
	Use:   "pull [service...]",
	Short: "Pull service images",
	RunE:  runPull,
}

func runPull(_ *cobra.Command, args []string) error {
	ctx := context.Background()
	criClient, err := requireCRI(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = criClient.Close() }()
	return doPullCRI(ctx, criClient, args)
}

// doPullCRI evaluates the Nix config and pulls images via CRI.
func doPullCRI(ctx context.Context, client *cri.Client, args []string) error {
	dir := projectDir()
	runner := &eval.ExecRunner{Dir: dir}
	evaluator := &eval.Evaluator{
		Runner:     runner,
		ProjectDir: dir,
		FlakeAttr:  flagFlakeAttr,
		Impure:     flagImpure,
	}

	comp, _, err := evaluator.Eval(ctx)
	if err != nil {
		return fmt.Errorf("evaluation failed: %w", err)
	}

	if err := prepareCRIImages(ctx, evaluator, comp); err != nil {
		return err
	}

	images := serviceImages(comp, args)
	for svc, image := range images {
		// An explicit pull always re-contacts the registry, so this does not go
		// through EnsureImage's "already present" shortcut. Local artifacts have
		// no registry to re-contact and are imported instead.
		if cri.IsLocalImageRef(image) {
			fmt.Printf("Importing %s (%s)...\n", svc, image)
			if _, err := client.EnsureImage(ctx, image); err != nil {
				return fmt.Errorf("import %s: %w", svc, err)
			}
			continue
		}
		fmt.Printf("Pulling %s (%s)...\n", svc, image)
		if err := client.PullImage(ctx, image); err != nil {
			return fmt.Errorf("pull %s: %w", svc, err)
		}
	}
	return nil
}

// serviceImages returns a map of service-name → image for the given composition.
// If args is non-empty, only matching services are included.
func serviceImages(comp *eval.Composition, args []string) map[string]string {
	result := make(map[string]string)
	if len(args) == 0 {
		for name, svc := range comp.Services {
			if svc.Image != "" {
				result[name] = svc.Image
			}
		}
		return result
	}
	wanted := make(map[string]bool, len(args))
	for _, a := range args {
		wanted[a] = true
	}
	for name, svc := range comp.Services {
		if wanted[name] && svc.Image != "" {
			result[name] = svc.Image
		}
	}
	return result
}
