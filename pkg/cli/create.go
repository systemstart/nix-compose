package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/systemstart/nix-compose/pkg/cri"
	"github.com/systemstart/nix-compose/pkg/eval"
)

var createCmd = &cobra.Command{
	Use:   "create [service...]",
	Short: "Create service containers without starting them",
	RunE: func(_ *cobra.Command, args []string) error {
		ctx := context.Background()
		criClient, err := requireCRI(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = criClient.Close() }()
		return runCreateCRI(ctx, criClient, args)
	},
}

// runCreateCRI creates containers via CRI without starting them.
// This requires evaluating the Nix composition to get service definitions.
func runCreateCRI(ctx context.Context, client *cri.Client, services []string) error {
	dir := projectDir()
	project := projectNameFor(dir, flagProjectName)

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

	opts, err := criServiceOpts(project, comp)
	if err != nil {
		return err
	}

	serviceSet := make(map[string]bool, len(services))
	for _, s := range services {
		serviceSet[s] = true
	}

	for name, svc := range comp.Services {
		if len(services) > 0 && !serviceSet[name] {
			continue
		}
		if err := createServiceCRI(ctx, client, project, name, svc, opts); err != nil {
			return err
		}
	}

	return nil
}

// createServiceCRI creates a single service's pod and container without starting it.
func createServiceCRI(ctx context.Context, client *cri.Client, project, name string, svc eval.Service, opts cri.ServiceUpOptions) error {
	fmt.Printf("Creating %s ...\n", name)

	image, err := client.EnsureImage(ctx, svc.Image)
	if err != nil {
		return fmt.Errorf("resolving image for %s: %w", name, err)
	}
	svc.Image = image

	logDir := fmt.Sprintf("/tmp/nix-compose-logs/%s/%s", project, name)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return fmt.Errorf("creating log directory: %w", err)
	}

	netMode := cri.PodNetworkHost
	if opts.UseCNI && svc.NetworkMode != "host" {
		netMode = cri.PodNetworkCNI
	}
	podConfig := cri.BuildPodConfig(project, name, svc, opts.Version, netMode)
	podID, err := client.RunPodSandbox(ctx, podConfig)
	if err != nil {
		return fmt.Errorf("creating pod for %s: %w", name, err)
	}

	mounts, err := cri.BuildMounts(svc, project, opts.CompVolumes, opts.VolumeResolver)
	if err != nil {
		return fmt.Errorf("building mounts for %s: %w", name, err)
	}

	ctrConfig := cri.BuildContainerConfig(name, svc, project, opts.Version, mounts)
	if _, err := client.CreateContainer(ctx, podID, ctrConfig, podConfig); err != nil {
		return fmt.Errorf("creating container for %s: %w", name, err)
	}

	return nil
}
