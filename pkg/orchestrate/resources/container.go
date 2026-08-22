package resources

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/systemstart/nix-compose/pkg/cri"
	"github.com/systemstart/nix-compose/pkg/eval"
	"github.com/systemstart/nix-compose/pkg/orchestrate/typing"
	"github.com/systemstart/nix-compose/pkg/volumes"
)

// ContainerSpec is the spec for a Container resource.
type ContainerSpec struct {
	Project     string                 `json:"project" yaml:"project"`
	Service     string                 `json:"service" yaml:"service"`
	Version     string                 `json:"version" yaml:"version"`
	Image       string                 `json:"image" yaml:"image"`
	Command     eval.CommandValue      `json:"command,omitempty" yaml:"command,omitempty"`
	Entrypoint  eval.CommandValue      `json:"entrypoint,omitempty" yaml:"entrypoint,omitempty"`
	Environment map[string]string      `json:"environment,omitempty" yaml:"environment,omitempty"`
	Ports       []string               `json:"ports,omitempty" yaml:"ports,omitempty"`
	Volumes     []string               `json:"volumes,omitempty" yaml:"volumes,omitempty"`
	CompVolumes map[string]eval.Volume `json:"compVolumes,omitempty" yaml:"compVolumes,omitempty"`
	NetworkMode string                 `json:"networkMode,omitempty" yaml:"networkMode,omitempty"`
	WorkingDir  string                 `json:"workingDir,omitempty" yaml:"workingDir,omitempty"`
	User        string                 `json:"user,omitempty" yaml:"user,omitempty"`
	Privileged  bool                   `json:"privileged,omitempty" yaml:"privileged,omitempty"`
	Tmpfs       []string               `json:"tmpfs,omitempty" yaml:"tmpfs,omitempty"`
	StopSignal  string                 `json:"stopSignal,omitempty" yaml:"stopSignal,omitempty"`
	Hostname    string                 `json:"hostname,omitempty" yaml:"hostname,omitempty"`
	UseCNI      bool                   `json:"useCNI,omitempty" yaml:"useCNI,omitempty"`
	Healthcheck *eval.Healthcheck      `json:"healthcheck,omitempty" yaml:"healthcheck,omitempty"`
}

// ContainerDefinition handles Container resources.
type ContainerDefinition struct {
	Client   *cri.Client
	VolStore *volumes.Store
}

var _ typing.Definition = &ContainerDefinition{}

func (d *ContainerDefinition) GetKey() typing.DefinitionKey  { return ContainerKey }
func (d *ContainerDefinition) GetMappings() []typing.Mapping { return nil }

func (d *ContainerDefinition) Instantiate(r json.RawMessage) (typing.Instance, error) {
	var spec ContainerSpec
	if err := json.Unmarshal(r, &spec); err != nil {
		return nil, fmt.Errorf("unmarshal container spec: %w", err)
	}
	return &ContainerInstance{
		Spec:     spec,
		client:   d.Client,
		volStore: d.VolStore,
	}, nil
}

func (d *ContainerDefinition) Load(r json.RawMessage) (typing.Instance, error) {
	return d.Instantiate(r)
}

func (d *ContainerDefinition) Delete(r typing.Reference) error {
	if d.Client == nil {
		return nil
	}
	// Parse project/service from the reference ID (format: "project/service")
	id := r.GetId()
	project, service := splitContainerId(id)
	if err := d.Client.ServiceDown(context.Background(), project, service, 10); err != nil {
		return fmt.Errorf("service down %s/%s: %w", project, service, err)
	}
	return nil
}

func (d *ContainerDefinition) GetStatus(r typing.Reference) (typing.Status, error) {
	return d.GetProviderStatus(r) //nolint:wrapcheck // delegates to own method
}

func (d *ContainerDefinition) GetProviderStatus(r typing.Reference) (typing.Status, error) {
	return d.GetProviderStatusWithVersion(r, "")
}

// GetProviderStatusWithVersion checks CRI state for a container, optionally comparing
// against an expected version. Returns fine-grained status:
// RUNNING if container is running, ERROR if exited non-zero, PENDING if no containers,
// DRIFTED if version mismatches, SUCCEEDED if exited cleanly.
func (d *ContainerDefinition) GetProviderStatusWithVersion(r typing.Reference, expectedVersion string) (typing.Status, error) {
	if d.Client == nil {
		return PendingStatus(), nil
	}
	id := r.GetId()
	project, service := splitContainerId(id)

	pods, err := d.Client.ListPodSandboxes(context.Background(), map[string]string{
		cri.LabelProject: project,
		cri.LabelService: service,
	})
	if err != nil {
		return ErrorStatus(err.Error()), nil
	}
	if len(pods) == 0 {
		return PendingStatus(), nil
	}

	// Check version label on the pod for drift detection.
	if expectedVersion != "" {
		podVersion := pods[0].GetLabels()[cri.LabelVersion]
		if podVersion != "" && podVersion != expectedVersion {
			return DriftedStatus("version mismatch"), nil
		}
	}

	// List containers in the first pod to check actual state.
	ctrs, err := d.Client.ListContainers(context.Background(), pods[0].Id)
	if err != nil {
		return ErrorStatus(err.Error()), nil
	}
	if len(ctrs) == 0 {
		return PendingStatus(), nil
	}

	return d.containerStateStatus(int32(ctrs[0].State), ctrs[0].Id)
}

// containerStateStatus maps a container's runtime state to a typing.Status.
func (d *ContainerDefinition) containerStateStatus(state int32, containerID string) (typing.Status, error) {
	switch state {
	case 1: // CONTAINER_RUNNING
		return RunningStatus(), nil
	case 2: // CONTAINER_EXITED
		return d.exitedContainerStatus(containerID)
	default:
		return PendingStatus(), nil
	}
}

// exitedContainerStatus checks the exit code of an exited container.
func (d *ContainerDefinition) exitedContainerStatus(containerID string) (typing.Status, error) {
	statusResp, err := d.Client.ContainerStatus(context.Background(), containerID)
	if err != nil {
		return ErrorStatus(err.Error()), nil
	}
	if statusResp.Status != nil && statusResp.Status.ExitCode != 0 {
		return ErrorStatus(fmt.Sprintf("exited with code %d", statusResp.Status.ExitCode)), nil
	}
	return SucceededStatus(), nil
}

// splitContainerId splits "project/service" into its parts.
func splitContainerId(id string) (string, string) {
	for i := 0; i < len(id); i++ {
		if id[i] == '/' {
			return id[:i], id[i+1:]
		}
	}
	return id, id
}

// ContainerInstance is a concrete Container resource.
type ContainerInstance struct {
	Spec     ContainerSpec `json:"spec"`
	client   *cri.Client
	volStore *volumes.Store
}

var _ typing.Instance = &ContainerInstance{}

func (i *ContainerInstance) GetId() string {
	return fmt.Sprintf("%s/%s", i.Spec.Project, i.Spec.Service)
}

func (i *ContainerInstance) GetKey() typing.DefinitionKey { return ContainerKey }

func (i *ContainerInstance) String() string {
	return fmt.Sprintf("[Container %s/%s]", i.Spec.Project, i.Spec.Service)
}

func (i *ContainerInstance) Apply() error {
	if i.client == nil {
		return fmt.Errorf("no CRI client configured")
	}

	svc := eval.Service{
		Image:       i.Spec.Image,
		Command:     i.Spec.Command,
		Entrypoint:  i.Spec.Entrypoint,
		Environment: i.Spec.Environment,
		Ports:       i.Spec.Ports,
		Volumes:     i.Spec.Volumes,
		NetworkMode: i.Spec.NetworkMode,
		WorkingDir:  i.Spec.WorkingDir,
		User:        i.Spec.User,
		Privileged:  i.Spec.Privileged,
		Tmpfs:       i.Spec.Tmpfs,
		StopSignal:  i.Spec.StopSignal,
		Hostname:    i.Spec.Hostname,
	}

	var volResolver cri.VolumeResolver
	if i.volStore != nil {
		volResolver = i.volStore.Ensure
	}

	opts := cri.ServiceUpOptions{
		Project:        i.Spec.Project,
		Version:        i.Spec.Version,
		CompVolumes:    i.Spec.CompVolumes,
		VolumeResolver: volResolver,
		UseCNI:         i.Spec.UseCNI,
	}

	if err := i.client.ServiceUp(context.Background(), i.Spec.Service, svc, opts); err != nil {
		return fmt.Errorf("service up %s/%s: %w", i.Spec.Project, i.Spec.Service, err)
	}
	return nil
}
