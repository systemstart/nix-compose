package resources

import (
	"encoding/json"
	"fmt"

	"github.com/systemstart/nix-compose/pkg/cri"
	"github.com/systemstart/nix-compose/pkg/orchestrate/typing"
	"github.com/systemstart/nix-compose/pkg/volumes"
)

// ServiceSpec is the spec for a Service resource.
// For M11, Service is 1:1 with Container — replica support is deferred.
type ServiceSpec struct {
	Container ContainerSpec `json:"container" yaml:"container"`
}

// ServiceDefinition handles Service resources.
type ServiceDefinition struct {
	Client   *cri.Client
	VolStore *volumes.Store
}

var _ typing.Definition = &ServiceDefinition{}

func (d *ServiceDefinition) GetKey() typing.DefinitionKey  { return ServiceKey }
func (d *ServiceDefinition) GetMappings() []typing.Mapping { return nil }

func (d *ServiceDefinition) Instantiate(r json.RawMessage) (typing.Instance, error) {
	var spec ServiceSpec
	if err := json.Unmarshal(r, &spec); err != nil {
		return nil, fmt.Errorf("unmarshal service spec: %w", err)
	}
	return &ServiceInstance{
		Spec:     spec,
		client:   d.Client,
		volStore: d.VolStore,
	}, nil
}

func (d *ServiceDefinition) Load(r json.RawMessage) (typing.Instance, error) {
	return d.Instantiate(r)
}

func (d *ServiceDefinition) Delete(r typing.Reference) error {
	// Delegate to the underlying container
	cDef := &ContainerDefinition{Client: d.Client, VolStore: d.VolStore}
	return cDef.Delete(r)
}

func (d *ServiceDefinition) GetStatus(r typing.Reference) (typing.Status, error) {
	cDef := &ContainerDefinition{Client: d.Client, VolStore: d.VolStore}
	return cDef.GetStatus(r)
}

func (d *ServiceDefinition) GetProviderStatus(r typing.Reference) (typing.Status, error) {
	cDef := &ContainerDefinition{Client: d.Client, VolStore: d.VolStore}
	return cDef.GetProviderStatus(r)
}

// ServiceInstance is a concrete Service resource, wrapping a single Container.
type ServiceInstance struct {
	Spec     ServiceSpec `json:"spec"`
	client   *cri.Client
	volStore *volumes.Store
}

var _ typing.Instance = &ServiceInstance{}

func (i *ServiceInstance) GetId() string {
	return fmt.Sprintf("%s/%s", i.Spec.Container.Project, i.Spec.Container.Service)
}
func (i *ServiceInstance) GetKey() typing.DefinitionKey { return ServiceKey }
func (i *ServiceInstance) String() string {
	return fmt.Sprintf("[Service %s/%s]", i.Spec.Container.Project, i.Spec.Container.Service)
}

func (i *ServiceInstance) Apply() error {
	// Delegate to ContainerInstance
	ci := &ContainerInstance{
		Spec:     i.Spec.Container,
		client:   i.client,
		volStore: i.volStore,
	}
	return ci.Apply()
}
