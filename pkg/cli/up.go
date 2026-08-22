package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/systemstart/nix-compose/pkg/cni"
	"github.com/systemstart/nix-compose/pkg/composition"
	"github.com/systemstart/nix-compose/pkg/cri"
	"github.com/systemstart/nix-compose/pkg/depgraph"
	"github.com/systemstart/nix-compose/pkg/envfrom"
	"github.com/systemstart/nix-compose/pkg/eval"
	"github.com/systemstart/nix-compose/pkg/gcroot"
	"github.com/systemstart/nix-compose/pkg/health"
	"github.com/systemstart/nix-compose/pkg/microvm/portfwd"
	"github.com/systemstart/nix-compose/pkg/orchestrate"
	"github.com/systemstart/nix-compose/pkg/orchestrate/client"
	"github.com/systemstart/nix-compose/pkg/orchestrate/convert"
	"github.com/systemstart/nix-compose/pkg/volumes"
	"github.com/systemstart/nix-compose/pkg/watch"
)

var (
	upDetach        bool
	upBuild         bool
	upWatch         bool
	upWait          bool
	upOrchestrate   bool
	upMicroVM       bool
	vmKernel        string
	vmRootFS        string
	vmVCPUs         int
	vmMemoryMB      int
	vmCID           uint32
	vmImageFlakeRef string
	vmPortFwdPort   uint32
)

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Evaluate Nix config and start services",
	RunE:  runUp,
}

func init() {
	upCmd.Flags().BoolVarP(&upDetach, "detach", "d", false, "run in background")
	upCmd.Flags().BoolVar(&upBuild, "build", false, "build images before starting")
	upCmd.Flags().BoolVar(&upWatch, "watch", false, "watch for Nix file changes and restart affected services")
	upCmd.Flags().BoolVar(&upWait, "wait", false, "wait for services to be running/healthy")
	upCmd.Flags().BoolVar(&upOrchestrate, "orchestrate", false, "use declarative plan/apply engine")
	upCmd.Flags().BoolVar(&upMicroVM, "microvm", false, "boot a microVM and delegate orchestration")
	upCmd.Flags().StringVar(&vmKernel, "vm-kernel", "", "path to vmlinux kernel image")
	upCmd.Flags().StringVar(&vmRootFS, "vm-rootfs", "", "path to rootfs image")
	upCmd.Flags().IntVar(&vmVCPUs, "vm-vcpus", 1, "number of vCPUs for the microVM")
	upCmd.Flags().IntVar(&vmMemoryMB, "vm-memory", 512, "memory in MB for the microVM")
	upCmd.Flags().Uint32Var(&vmCID, "vm-cid", 3, "vsock CID for the microVM")
	upCmd.Flags().StringVar(&vmImageFlakeRef, "vm-image-flake", "", "flake reference override for the VM image build")
	upCmd.Flags().Uint32Var(&vmPortFwdPort, "vm-portfwd-port", 1025, "vsock port for VM port forwarding")
}

// GCRootCreator abstracts GC root creation for testability.
type GCRootCreator func(ctx context.Context, runner eval.CommandRunner, projectDir string, storePaths []string) error

// UpDeps holds injectable dependencies for the up command.
type UpDeps struct {
	Evaluator       *eval.Evaluator
	GCRootCreate    GCRootCreator
	ProjectDir      string
	ProjectName     string
	EnvFromResolver *envfrom.Resolver
	CRIClient       *cri.Client
}

// DoUp performs the up workflow:
// eval → filter → synthesize init containers → resolve envFrom → validate resources → validate depgraph → gcroot → write YAML → compose up.
// Returns the final Composition for use by watch mode.
func DoUp(ctx context.Context, deps UpDeps, detach, build, wait bool, profiles []string, args []string) (*eval.Composition, error) {
	comp, rawJSON, err := deps.Evaluator.Eval(ctx)
	if err != nil {
		return nil, fmt.Errorf("evaluation failed: %w", err)
	}

	comp, err = transformComposition(ctx, comp, profiles, deps.EnvFromResolver)
	if err != nil {
		return nil, err
	}

	printResourceWarnings(comp)

	if errs := depgraph.Validate(comp); len(errs) > 0 {
		return nil, fmt.Errorf("dependency validation failed: %w", errs[0])
	}

	// Before anything starts: the GC root keeps the store paths this
	// composition references alive for as long as the project is up. It
	// matters for `useHostStore` and `nixStorePaths`, which bind-mount live
	// store paths into containers — a `nix-collect-garbage` without this
	// can pull the floor out from under a running service.
	tryCreateGCRoot(ctx, deps, rawJSON)

	if deps.CRIClient != nil {
		if err := prepareCRIImages(ctx, deps.Evaluator, comp); err != nil {
			return nil, err
		}
		if useOrchestrate() {
			if err := orchestrateUp(ctx, deps, comp); err != nil {
				return nil, err
			}
			return comp, nil
		}
		if err := criUp(ctx, deps, comp); err != nil {
			return nil, err
		}
		return comp, nil
	}

	return nil, fmt.Errorf("no CRI runtime available")
}

// criServiceOpts builds the CRI ServiceUpOptions from the project deps and composition.
func criServiceOpts(project string, comp *eval.Composition) (cri.ServiceUpOptions, error) {
	store, err := volumes.NewStore()
	if err != nil {
		return cri.ServiceUpOptions{}, fmt.Errorf("volume store: %w", err)
	}
	useCNI := setupCNI(project)
	return cri.ServiceUpOptions{
		Project:        project,
		Version:        "1",
		CompVolumes:    comp.Volumes,
		VolumeResolver: store.Ensure,
		UseCNI:         useCNI,
	}, nil
}

// criProjectName resolves the project name from deps.
func criProjectName(deps UpDeps) string {
	return projectNameFor(deps.ProjectDir, deps.ProjectName)
}

// criUp brings up all services via CRI with topological ordering and health gating.
func criUp(ctx context.Context, deps UpDeps, comp *eval.Composition) error {
	project := criProjectName(deps)

	opts, err := criServiceOpts(project, comp)
	if err != nil {
		return err
	}

	levels, err := depgraph.StartOrder(comp)
	if err != nil {
		return fmt.Errorf("dependency ordering: %w", err)
	}

	executor := &criExecutor{client: deps.CRIClient}
	monitor := health.NewMonitor(executor)

	for _, level := range levels {
		for _, name := range level {
			svc := comp.Services[name]
			if err := deps.CRIClient.ServiceUp(ctx, name, svc, opts); err != nil {
				return fmt.Errorf("service %s: %w", name, err)
			}
			cond := highestConditionOf(name, comp)
			if err := waitCondition(ctx, deps.CRIClient, project, name, cond, svc, monitor); err != nil {
				return err
			}
		}
	}
	return nil
}

// useOrchestrate checks whether the declarative orchestrate path is enabled
// via --orchestrate flag or NIX_COMPOSE_ORCHESTRATE=1 environment variable.
func useOrchestrate() bool {
	return upOrchestrate || os.Getenv("NIX_COMPOSE_ORCHESTRATE") == "1"
}

// orchestrateUp brings up services via the declarative plan/apply engine.
func orchestrateUp(ctx context.Context, deps UpDeps, comp *eval.Composition) error {
	project := criProjectName(deps)

	useCNI := setupCNI(project)

	result, err := convert.Convert(comp, convert.Options{
		Project: project,
		UseCNI:  useCNI,
	})
	if err != nil {
		return fmt.Errorf("converting to manifests: %w", err)
	}

	volStore, err := volumes.NewStore()
	if err != nil {
		return fmt.Errorf("volume store: %w", err)
	}

	cniStore := cni.NewStore()
	engine, err := orchestrate.New(orchestrate.Config{
		CRIClient: deps.CRIClient,
		CNIStore:  cniStore,
		VolStore:  volStore,
	})
	if err != nil {
		return fmt.Errorf("starting engine: %w", err)
	}
	defer func() { _ = engine.Close() }()

	deployment, conditions, err := convert.Bridge(result, engine.Registry())
	if err != nil {
		return fmt.Errorf("bridging to deployment: %w", err)
	}

	plan, err := engine.Plan(deployment, conditions)
	if err != nil {
		return fmt.Errorf("computing plan: %w", err)
	}

	printPlan(plan)

	creates, updates, destroys, _ := plan.Summary()
	if creates+updates+destroys == 0 {
		fmt.Println("No changes to apply.")
		return nil
	}

	fmt.Println("Applying...")
	if err := engine.ApplySync(ctx, plan); err != nil {
		return fmt.Errorf("applying changes: %w", err)
	}
	return nil
}

// criForeground runs in foreground mode: starts a supervisor to watch and
// restart services, blocks on SIGINT/SIGTERM, then shuts down gracefully.
func criForeground(ctx context.Context, deps UpDeps, comp *eval.Composition) error {
	project := criProjectName(deps)

	opts, err := criServiceOpts(project, comp)
	if err != nil {
		return err
	}

	sup := cri.NewSupervisor(cri.SupervisorConfig{
		Client:  deps.CRIClient,
		Project: project,
		Opts:    opts,
	})
	for name, svc := range comp.Services {
		sup.Register(name, svc)
	}

	sigCtx, sigStop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer sigStop()

	supDone := make(chan error, 1)
	go func() { supDone <- sup.Run(sigCtx) }()

	select {
	case <-sigCtx.Done():
		fmt.Println("\nReceived shutdown signal, stopping services...")
		sup.Stop()
		// Give services time to shut down gracefully.
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()
		if err := shutdownServices(shutdownCtx, deps.CRIClient, project, 10, comp); err != nil {
			fmt.Printf("Warning: error during shutdown: %v\n", err)
		}
		// Wait for supervisor goroutines to finish.
		select {
		case <-supDone:
		case <-time.After(5 * time.Second):
		}
		return nil
	case err := <-supDone:
		// All watchers exited naturally (e.g. all restart:no services done).
		return err
	}
}

// setupCNI writes CNI config and returns whether CNI is usable.
func setupCNI(project string) bool {
	cniStore := cni.NewStore()
	if missing := cniStore.CheckPlugins(); len(missing) > 0 {
		fmt.Printf("Warning: CNI plugins missing (%s), using host networking\n",
			strings.Join(missing, ", "))
		return false
	}
	if err := cniStore.Write(project); err != nil {
		fmt.Printf("Warning: failed to write CNI config: %v, using host networking\n", err)
		return false
	}
	return true
}

// waitCondition blocks until the dependency condition for a service is satisfied.
func waitCondition(ctx context.Context, client *cri.Client, project, name, cond string, svc eval.Service, monitor *health.Monitor) error {
	switch cond {
	case "service_completed_successfully":
		return waitExited(ctx, client, project, name)
	case "service_healthy":
		return waitHealthy(ctx, client, project, name, svc, monitor)
	}
	return nil
}

// waitExited waits for a service container to exit with code 0.
func waitExited(ctx context.Context, client *cri.Client, project, name string) error {
	ctrID, err := lookupContainerID(ctx, client, project, name)
	if err != nil {
		return fmt.Errorf("looking up container for %s: %w", name, err)
	}
	exitCode, err := client.WaitExited(ctx, ctrID, 2*time.Second)
	if err != nil {
		return fmt.Errorf("waiting for %s to exit: %w", name, err)
	}
	if exitCode != 0 {
		return fmt.Errorf("service %s exited with code %d", name, exitCode)
	}
	return nil
}

// waitHealthy waits for a service to become healthy via its probe.
func waitHealthy(ctx context.Context, client *cri.Client, project, name string, svc eval.Service, monitor *health.Monitor) error {
	probe := health.ResolveProbe(svc)
	if probe == nil {
		return fmt.Errorf("service %s has no health check but service_healthy condition is required", name)
	}
	ctrID, err := lookupContainerID(ctx, client, project, name)
	if err != nil {
		return fmt.Errorf("looking up container for %s: %w", name, err)
	}
	podIP, err := lookupPodIP(ctx, client, project, name)
	if err != nil {
		return fmt.Errorf("looking up pod IP for %s: %w", name, err)
	}
	monitor.Register(name, ctrID, podIP, probe)
	monitor.Start(ctx, name)
	if err := monitor.WaitHealthy(ctx, name); err != nil {
		return fmt.Errorf("health check for %s: %w", name, err)
	}
	return nil
}

// criExecutor adapts cri.Client to health.ContainerExecutor.
type criExecutor struct {
	client *cri.Client
}

func (e *criExecutor) ExecSync(ctx context.Context, containerID string, cmd []string, timeoutSecs int64) (*health.ExecResult, error) {
	resp, err := e.client.ExecSync(ctx, containerID, cmd, timeoutSecs)
	if err != nil {
		return nil, fmt.Errorf("exec sync: %w", err)
	}
	return &health.ExecResult{ExitCode: resp.ExitCode}, nil
}

// highestConditionOf scans all services' DependsOn for the strictest condition
// required of the named service. Priority: service_healthy > service_completed_successfully > service_started.
func highestConditionOf(name string, comp *eval.Composition) string {
	highest := ""
	for _, svc := range comp.Services {
		entry, ok := svc.DependsOn.Entries[name]
		if !ok {
			continue
		}
		cond := entry.Condition
		if cond == "" {
			cond = "service_started"
		}
		if condPriority(cond) > condPriority(highest) {
			highest = cond
		}
	}
	return highest
}

func condPriority(cond string) int {
	switch cond {
	case "service_healthy":
		return 3
	case "service_completed_successfully":
		return 2
	case "service_started":
		return 1
	default:
		return 0
	}
}

// lookupContainerID finds the first container ID for a service in a project.
func lookupContainerID(ctx context.Context, client *cri.Client, project, service string) (string, error) {
	pods, err := client.ListPodSandboxes(ctx, map[string]string{
		cri.LabelProject: project,
		cri.LabelService: service,
	})
	if err != nil {
		return "", fmt.Errorf("listing pod sandboxes: %w", err)
	}
	if len(pods) == 0 {
		return "", fmt.Errorf("no pods found for service %s", service)
	}
	ctrs, err := client.ListContainers(ctx, pods[0].Id)
	if err != nil {
		return "", fmt.Errorf("listing containers: %w", err)
	}
	if len(ctrs) == 0 {
		return "", fmt.Errorf("no containers found for service %s", service)
	}
	return ctrs[0].Id, nil
}

// lookupPodIP finds the pod IP for a service in a project.
func lookupPodIP(ctx context.Context, client *cri.Client, project, service string) (string, error) {
	pods, err := client.ListPodSandboxes(ctx, map[string]string{
		cri.LabelProject: project,
		cri.LabelService: service,
	})
	if err != nil {
		return "", fmt.Errorf("listing pod sandboxes: %w", err)
	}
	if len(pods) == 0 {
		return "", fmt.Errorf("no pods found for service %s", service)
	}
	status, err := client.PodSandboxStatus(ctx, pods[0].Id)
	if err != nil {
		return "", fmt.Errorf("getting pod status: %w", err)
	}
	if status.Status == nil || status.Status.Network == nil {
		return "", fmt.Errorf("no network info for service %s", service)
	}
	return status.Status.Network.Ip, nil
}

// transformComposition applies profile filtering, init container synthesis, and envFrom resolution.
func transformComposition(ctx context.Context, comp *eval.Composition, profiles []string, resolver *envfrom.Resolver) (*eval.Composition, error) {
	composition.WarnDeprecatedProfiles(comp)
	comp = composition.FilterByProfiles(comp, profiles)
	comp = composition.SynthesizeInitContainers(comp)

	if err := envfrom.ResolveEnvFrom(ctx, comp, resolver); err != nil {
		return nil, fmt.Errorf("envFrom resolution failed: %w", err)
	}
	return comp, nil
}

// prepareCRIImages gets a composition's images ready for the CRI backend:
// every service must name an image CRI can obtain, and any Nix-defined image
// must exist in the store before it can be imported.
//
// Only the CRI paths need this. The compose backend hands image references to
// an external CLI, which has its own idea of where images live and can honour
// `build:` on its own.
func prepareCRIImages(ctx context.Context, evaluator *eval.Evaluator, comp *eval.Composition) error {
	//nolint:wrapcheck // the message names the Nix options to use instead; a prefix only obscures it
	if err := cri.ValidateImages(comp); err != nil {
		return err
	}
	if evaluator == nil {
		return nil
	}
	if err := eval.RealiseImages(ctx, evaluator.Runner, comp); err != nil {
		return fmt.Errorf("realising images: %w", err)
	}
	return nil
}

func printResourceWarnings(comp *eval.Composition) {
	for _, w := range composition.ValidateResources(comp) {
		fmt.Printf("Warning: %s\n", w)
	}
}

func tryCreateGCRoot(ctx context.Context, deps UpDeps, rawJSON []byte) {
	if deps.GCRootCreate == nil {
		return
	}
	storePaths := gcroot.CollectStorePaths(rawJSON)
	if err := deps.GCRootCreate(ctx, deps.Evaluator.Runner, deps.ProjectDir, storePaths); err != nil {
		fmt.Printf("Warning: GC root creation failed: %v\n", err)
	}
}

func runUp(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	dir := projectDir()

	announceEval(dir)
	runner := &eval.ExecRunner{Dir: dir}

	evaluator := &eval.Evaluator{
		Runner:     runner,
		ProjectDir: dir,
		FlakeAttr:  flagFlakeAttr,
		Impure:     flagImpure,
	}

	deps := UpDeps{
		Evaluator:    evaluator,
		GCRootCreate: gcroot.Create,
		ProjectDir:   dir,
		ProjectName:  flagProjectName,
		EnvFromResolver: &envfrom.Resolver{
			ProjectDir: dir,
			Runner:     runner,
		},
	}

	// Try remote path.
	if rc := dialRemote(ctx); rc != nil {
		defer func() { _ = rc.Close() }()
		profiles := mergeProfiles(flagProfiles)
		return remoteUp(ctx, rc, deps, profiles)
	}

	// Try microVM path.
	if upMicroVM {
		return runUpMicroVM(ctx, deps)
	}

	profiles := mergeProfiles(flagProfiles)

	// Compose path (explicit opt-in).

	// CRI path (default).
	return runUpCRI(ctx, deps, dir, evaluator, profiles, args)
}

func runUpMicroVM(ctx context.Context, deps UpDeps) error {
	profiles := mergeProfiles(flagProfiles)
	vm, comp, err := microvmUp(ctx, deps, profiles)
	if err != nil {
		return err
	}

	// Start port forwarding for published ports.
	mappings := portfwd.ExtractPorts(comp)
	if len(mappings) > 0 {
		fwd := portfwd.NewForwarder(vm.CID(), vmPortFwdPort, mappings, dialVsockRaw)
		if err := fwd.Start(ctx); err != nil {
			fmt.Printf("Warning: port forwarding: %v\n", err)
		} else {
			defer fwd.Stop()
			for _, addr := range fwd.ForwardedAddrs() {
				fmt.Printf("  Port forwarding: %s\n", addr)
			}
		}
	}

	if !upDetach {
		return microvmForeground(ctx, vm)
	}
	fmt.Printf("MicroVM started (CID=%d). Use --remote-vsock-cid %d to manage.\n",
		vm.CID(), vm.CID())
	return nil
}

func runUpCRI(ctx context.Context, deps UpDeps, dir string, evaluator *eval.Evaluator, profiles []string, args []string) error {
	criClient, err := requireCRI(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = criClient.Close() }()
	deps.CRIClient = criClient

	comp, err := DoUp(ctx, deps, upDetach, upBuild, upWait, profiles, args)
	if err != nil {
		return err
	}

	if !upDetach {
		return criForeground(ctx, deps, comp)
	}

	if !upWatch {
		return nil
	}

	return runWatcherCRI(ctx, deps.CRIClient, criProjectName(deps), evaluator, comp)
}

// requireCRI connects to a CRI socket using the flag or auto-detection.
// Returns an error when no CRI runtime is available. There is no second
// backend to fall back to, so the message points at `doctor` instead.
// and how to set up a CRI runtime.
func requireCRI(ctx context.Context) (*cri.Client, error) {
	if flagCRISocket != "" {
		c, err := cri.Dial(ctx, flagCRISocket)
		if err != nil {
			return nil, fmt.Errorf("cannot connect to CRI socket %s: %w\n  → check the runtime is running and the socket is readable by this user; `nix-compose doctor` diagnoses both", flagCRISocket, err)
		}
		return c, nil
	}
	c, err := cri.Detect(ctx)
	if err != nil {
		return nil, fmt.Errorf("no CRI runtime found (checked /run/containerd/containerd.sock, /run/crio/crio.sock, /tmp/ctrd/containerd.sock)\n  → start containerd or CRI-O, or pass --cri-socket; `nix-compose doctor` says which\n    prerequisite is missing and how to fix it")
	}
	return c, nil
}

// runWatcherCRI runs watch mode with CRI-backed callbacks.
func runWatcherCRI(ctx context.Context, client *cri.Client, project string,
	evaluator *eval.Evaluator, comp *eval.Composition,
) error {
	sigCtx, sigStop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer sigStop()

	w := &watch.Watcher{
		Config: watch.Config{
			ProjectDir: projectDir(),
		},
		Eval: func(ctx context.Context) (*eval.Composition, []byte, error) {
			return evaluator.Eval(ctx)
		},
		Restart: func(ctx context.Context, services []string) error {
			for _, svc := range services {
				if err := client.ServiceDown(ctx, project, svc, 10); err != nil {
					return fmt.Errorf("stopping %s: %w", svc, err)
				}
				// Re-evaluate to get the latest service definition.
				c, _, err := evaluator.Eval(ctx)
				if err != nil {
					return fmt.Errorf("re-eval for restart: %w", err)
				}
				svcDef, ok := c.Services[svc]
				if !ok {
					continue
				}
				opts, err := criServiceOpts(project, c)
				if err != nil {
					return err
				}
				if err := client.ServiceUp(ctx, svc, svcDef, opts); err != nil {
					return fmt.Errorf("starting %s: %w", svc, err)
				}
			}
			return nil
		},
		Remove: func(ctx context.Context, services []string) error {
			for _, svc := range services {
				if err := client.ServiceDown(ctx, project, svc, 10); err != nil {
					return fmt.Errorf("removing %s: %w", svc, err)
				}
			}
			return nil
		},
	}

	if err := w.Run(sigCtx, comp); err != nil {
		return fmt.Errorf("watch mode: %w", err)
	}
	return nil
}

// remoteUp delegates the up command to a remote orchestrate server.
func remoteUp(ctx context.Context, rc *client.Client, deps UpDeps, profiles []string) error {
	comp, err := evalForOrchestrate(ctx, deps.ProjectDir)
	if err != nil {
		return err
	}

	compJSON, err := json.Marshal(comp)
	if err != nil {
		return fmt.Errorf("marshaling composition: %w", err)
	}

	project := criProjectName(deps)
	useCNI := len(cni.NewStore().CheckPlugins()) == 0
	_ = profiles // profiles already applied by evalForOrchestrate

	fmt.Println("Applying via remote orchestrate server...")
	resp, err := rc.Apply(ctx, compJSON, project, useCNI)
	if err != nil {
		return fmt.Errorf("remote apply: %w", err)
	}

	printRemoteActions(resp.Actions)
	printRemoteSummary(resp.Creates, resp.Updates, resp.Destroys, resp.Noops)
	return nil
}

// mergeProfiles combines --profile flags with the COMPOSE_PROFILES
// environment variable (comma-separated). Duplicates are removed.
func mergeProfiles(flags []string) []string {
	seen := make(map[string]bool, len(flags))
	var result []string
	for _, p := range flags {
		if !seen[p] {
			seen[p] = true
			result = append(result, p)
		}
	}
	if env := os.Getenv("COMPOSE_PROFILES"); env != "" {
		for _, p := range strings.Split(env, ",") {
			p = strings.TrimSpace(p)
			if p != "" && !seen[p] {
				seen[p] = true
				result = append(result, p)
			}
		}
	}
	return result
}
