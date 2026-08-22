package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/systemstart/nix-compose/pkg/composition"
	"github.com/systemstart/nix-compose/pkg/envfrom"
	"github.com/systemstart/nix-compose/pkg/eval"
	"github.com/systemstart/nix-compose/pkg/k8s"
)

var (
	renderTarget    string
	renderOutput    string
	renderNamespace string
	renderDryRun    bool
)

var renderCmd = &cobra.Command{
	Use:   "render",
	Short: "Render K8s manifests from Nix config",
	Long:  "Evaluate the Nix configuration and emit Kubernetes manifests.",
	RunE:  runRender,
}

func init() {
	renderCmd.Flags().StringVar(&renderTarget, "target", "", "output format (required: k8s)")
	renderCmd.Flags().StringVar(&renderOutput, "output", "", "write individual files to directory (default: stdout)")
	renderCmd.Flags().StringVar(&renderNamespace, "namespace", "default", "Kubernetes namespace")
	renderCmd.Flags().BoolVar(&renderDryRun, "dry-run", false, "validate via kubectl apply --dry-run=client")
}

func runRender(_ *cobra.Command, _ []string) error {
	if renderTarget != "k8s" {
		return fmt.Errorf("unsupported target %q (supported: k8s)", renderTarget)
	}
	return renderK8s(context.Background(), projectDir())
}

func renderK8s(ctx context.Context, dir string) error {
	comp, err := evalAndFilter(ctx, dir)
	if err != nil {
		return err
	}

	secrets, err := resolveSecrets(ctx, comp, dir)
	if err != nil {
		return err
	}

	printResourceWarnings(comp)

	opts := k8s.RenderOptions{Namespace: renderNamespace}
	manifests := k8s.Convert(comp, secrets, opts)

	return outputManifests(ctx, manifests)
}

// evalAndFilter runs Nix evaluation and profile filtering.
func evalAndFilter(ctx context.Context, dir string) (*eval.Composition, error) {
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

	comp = composition.FilterByProfiles(comp, flagProfiles)
	return comp, nil
}

// resolveSecrets resolves envFrom for each service, returning per-service secret maps.
func resolveSecrets(ctx context.Context, comp *eval.Composition, dir string) (map[string]map[string]string, error) {
	runner := &eval.ExecRunner{Dir: dir}
	resolver := &envfrom.Resolver{ProjectDir: dir, Runner: runner}
	secrets := make(map[string]map[string]string)

	for name, svc := range comp.Services {
		if svc.XNixCompose == nil || len(svc.XNixCompose.EnvFrom) == 0 {
			continue
		}
		resolved, err := resolver.Resolve(ctx, svc.XNixCompose.EnvFrom)
		if err != nil {
			return nil, fmt.Errorf("service %q envFrom: %w", name, err)
		}
		if len(resolved) > 0 {
			secrets[name] = resolved
		}
	}
	return secrets, nil
}

// outputManifests writes manifests to stdout or directory, with optional dry-run.
func outputManifests(ctx context.Context, manifests []k8s.Manifest) error {
	if renderOutput != "" {
		if err := k8s.WriteDirectory(renderOutput, manifests); err != nil {
			return fmt.Errorf("writing directory: %w", err)
		}
		fmt.Printf("Wrote %d manifests to %s\n", len(manifests), renderOutput)
		return dryRunIfRequested(ctx, manifests)
	}

	if err := k8s.WriteMultiDoc(os.Stdout, manifests); err != nil {
		return fmt.Errorf("writing multi-doc YAML: %w", err)
	}
	return dryRunIfRequested(ctx, manifests)
}

// dryRunIfRequested pipes manifests through kubectl dry-run validation if --dry-run is set.
func dryRunIfRequested(ctx context.Context, manifests []k8s.Manifest) error {
	if !renderDryRun {
		return nil
	}
	return kubectlDryRun(ctx, manifests)
}

// kubectlDryRun validates manifests via kubectl apply --dry-run=client.
func kubectlDryRun(_ context.Context, manifests []k8s.Manifest) error {
	var buf bytes.Buffer
	if err := k8s.WriteMultiDoc(&buf, manifests); err != nil {
		return fmt.Errorf("marshaling for dry-run: %w", err)
	}

	tmpFile, err := os.CreateTemp("", "nix-compose-*.yaml")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	if _, err := tmpFile.Write(buf.Bytes()); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}

	runner := &eval.ExecRunner{}
	stdout, stderr, err := runner.Run(context.Background(), "kubectl", "apply", "--dry-run=client", "-f", tmpFile.Name())
	if err != nil {
		return fmt.Errorf("kubectl dry-run failed: %s: %w", string(stderr), err)
	}
	fmt.Print(string(stdout))
	return nil
}
