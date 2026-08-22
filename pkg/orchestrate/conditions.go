package orchestrate

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/systemstart/nix-compose/pkg/cri"
	"github.com/systemstart/nix-compose/pkg/eval"
	"github.com/systemstart/nix-compose/pkg/health"
	"github.com/systemstart/nix-compose/pkg/orchestrate/convert"
	"github.com/systemstart/nix-compose/pkg/orchestrate/resources"
	"github.com/systemstart/nix-compose/pkg/orchestrate/state"
	"github.com/systemstart/nix-compose/pkg/orchestrate/typing"
)

// waitCondition blocks until all conditions for resourceID are met.
// ConditionMap semantics: conditions[toID][fromID] = cond
// After creating toID, we wait for the condition to be met.
func waitCondition(
	ctx context.Context, e *Engine, resourceID string,
	conditions convert.ConditionMap,
) error {
	deps, ok := conditions[resourceID]
	if !ok {
		return nil
	}

	timeout := time.Duration(e.conditionTimeout) * time.Second
	condCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Find the strictest condition required of this resource.
	strictest := ""
	for _, cond := range deps {
		if condPriority(cond) > condPriority(strictest) {
			strictest = cond
		}
	}

	switch strictest {
	case "healthy":
		return waitHealthy(condCtx, e, resourceID)
	case "completed":
		return waitCompleted(condCtx, e, resourceID)
	case "started":
		return waitStarted(condCtx, e, resourceID)
	default:
		// No condition or unknown — wait for started as default.
		return waitStarted(condCtx, e, resourceID)
	}
}

// condPriority returns priority of a condition for comparison.
func condPriority(cond string) int {
	switch cond {
	case "healthy":
		return 3
	case "completed":
		return 2
	case "started":
		return 1
	default:
		return 0
	}
}

// waitStarted polls until the container for resourceID is in RUNNING state.
func waitStarted(ctx context.Context, e *Engine, resourceID string) error {
	ref := typing.NewReference(resourceID, resources.ServiceKey)
	pollInterval := 2 * time.Second

	for {
		def, err := e.registry.GetDefinition(resources.ServiceKey)
		if err != nil {
			return fmt.Errorf("no service definition: %w", err)
		}
		status, err := def.GetProviderStatus(ref)
		if err != nil {
			return fmt.Errorf("checking status for %s: %w", resourceID, err)
		}
		short := status.GetShort()
		if short == typing.RolloutStatusRunning || short == typing.RolloutStatusSuccess {
			return nil
		}
		if short == typing.RolloutStatusError {
			return fmt.Errorf("resource %s is in error state", resourceID)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for %s to start: %w", resourceID, ctx.Err())
		case <-time.After(pollInterval):
		}
	}
}

// waitHealthy resolves the health probe for a resource and waits until it passes.
// Falls back to waitStarted if no probe is configured.
func waitHealthy(ctx context.Context, e *Engine, resourceID string) error {
	if e.criClient == nil {
		log.Printf("conditions: no CRI client, falling back to waitStarted for %s", resourceID)
		return waitStarted(ctx, e, resourceID)
	}

	// Extract the ContainerSpec from the rollout body to get the healthcheck.
	spec, err := loadContainerSpec(e, resourceID)
	if err != nil || spec == nil || spec.Healthcheck == nil {
		log.Printf("conditions: no healthcheck for %s, falling back to waitStarted", resourceID)
		return waitStarted(ctx, e, resourceID)
	}

	// First wait for container to be started.
	if err := waitStarted(ctx, e, resourceID); err != nil {
		return err
	}

	project, service := splitResourceID(resourceID)

	// Find containerID and podIP.
	containerID, podIP, err := lookupContainerInfo(ctx, e.criClient, project, service)
	if err != nil {
		return fmt.Errorf("looking up container info for %s: %w", resourceID, err)
	}

	// Build probe config from the healthcheck.
	probe := health.ResolveProbe(evalServiceFromSpec(spec))
	if probe == nil {
		log.Printf("conditions: could not resolve probe for %s, falling back to waitStarted", resourceID)
		return nil // already started
	}

	// Use health.Monitor for probe execution.
	executor := &criHealthExecutor{client: e.criClient}
	monitor := health.NewMonitor(executor)
	monitor.Register(service, containerID, podIP, probe)
	monitor.Start(ctx, service)

	if err := monitor.WaitHealthy(ctx, service); err != nil {
		return fmt.Errorf("health check for %s: %w", resourceID, err)
	}
	return nil
}

// waitCompleted waits for a container to exit with exit code 0.
func waitCompleted(ctx context.Context, e *Engine, resourceID string) error {
	if e.criClient == nil {
		return fmt.Errorf("waitCompleted requires CRI client for %s", resourceID)
	}

	// First wait for container to be started.
	if err := waitStarted(ctx, e, resourceID); err != nil {
		return err
	}

	project, service := splitResourceID(resourceID)

	containerID, _, err := lookupContainerInfo(ctx, e.criClient, project, service)
	if err != nil {
		return fmt.Errorf("looking up container for %s: %w", resourceID, err)
	}

	exitCode, err := e.criClient.WaitExited(ctx, containerID, 2*time.Second)
	if err != nil {
		return fmt.Errorf("waiting for %s to exit: %w", resourceID, err)
	}
	if exitCode != 0 {
		return fmt.Errorf("resource %s exited with code %d", resourceID, exitCode)
	}
	return nil
}

// splitResourceID splits "project/service" into project and service.
func splitResourceID(id string) (string, string) {
	for i := 0; i < len(id); i++ {
		if id[i] == '/' {
			return id[:i], id[i+1:]
		}
	}
	return id, id
}

// lookupContainerInfo finds the containerID and podIP for a service.
func lookupContainerInfo(ctx context.Context, client *cri.Client, project, service string) (string, string, error) {
	pods, err := client.ListPodSandboxes(ctx, map[string]string{
		cri.LabelProject: project,
		cri.LabelService: service,
	})
	if err != nil {
		return "", "", fmt.Errorf("listing pods: %w", err)
	}
	if len(pods) == 0 {
		return "", "", fmt.Errorf("no pods found for %s/%s", project, service)
	}

	ctrs, err := client.ListContainers(ctx, pods[0].Id)
	if err != nil {
		return "", "", fmt.Errorf("listing containers: %w", err)
	}
	if len(ctrs) == 0 {
		return "", "", fmt.Errorf("no containers found for %s/%s", project, service)
	}

	podIP := ""
	status, err := client.PodSandboxStatus(ctx, pods[0].Id)
	if err == nil && status.Status != nil && status.Status.Network != nil {
		podIP = status.Status.Network.Ip
	}

	return ctrs[0].Id, podIP, nil
}

// loadContainerSpec loads the ContainerSpec from a rollout body.
func loadContainerSpec(e *Engine, resourceID string) (*resources.ContainerSpec, error) {
	rollout, err := e.db.Load(state.RolloutsById, resourceID)
	if err != nil {
		return nil, fmt.Errorf("loading rollout for %s: %w", resourceID, err)
	}
	if rollout == nil {
		return nil, nil
	}

	// Parse rollout to extract body.
	var r struct {
		Body json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(rollout, &r); err != nil {
		return nil, fmt.Errorf("unmarshalling rollout for %s: %w", resourceID, err)
	}

	// Try ServiceSpec (container wrapper).
	var sSpec resources.ServiceSpec
	if err := json.Unmarshal(r.Body, &sSpec); err == nil && sSpec.Container.Service != "" {
		return &sSpec.Container, nil
	}

	// Try direct ContainerSpec.
	var cSpec resources.ContainerSpec
	if err := json.Unmarshal(r.Body, &cSpec); err == nil && cSpec.Service != "" {
		return &cSpec, nil
	}

	return nil, nil
}

// evalServiceFromSpec builds a minimal eval.Service for health probe resolution.
func evalServiceFromSpec(spec *resources.ContainerSpec) eval.Service {
	return eval.Service{
		Healthcheck: spec.Healthcheck,
	}
}

// criHealthExecutor adapts cri.Client to health.ContainerExecutor.
type criHealthExecutor struct {
	client *cri.Client
}

func (e *criHealthExecutor) ExecSync(ctx context.Context, containerID string, cmd []string,
	timeoutSecs int64,
) (*health.ExecResult, error) {
	resp, err := e.client.ExecSync(ctx, containerID, cmd, timeoutSecs)
	if err != nil {
		return nil, fmt.Errorf("exec sync: %w", err)
	}
	return &health.ExecResult{ExitCode: resp.ExitCode}, nil
}
