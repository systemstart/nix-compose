package resources

import (
	"encoding/json"
	"fmt"

	"github.com/systemstart/nix-compose/pkg/cni"
	"github.com/systemstart/nix-compose/pkg/orchestrate/typing"
)

// NetworkSpec is the spec for a Network resource.
type NetworkSpec struct {
	Project string `json:"project" yaml:"project"`
}

// NetworkDefinition handles Network resources.
type NetworkDefinition struct {
	Store *cni.Store
}

var _ typing.Definition = &NetworkDefinition{}

func (d *NetworkDefinition) GetKey() typing.DefinitionKey  { return NetworkKey }
func (d *NetworkDefinition) GetMappings() []typing.Mapping { return nil }

func (d *NetworkDefinition) Instantiate(r json.RawMessage) (typing.Instance, error) {
	var spec NetworkSpec
	if err := json.Unmarshal(r, &spec); err != nil {
		return nil, fmt.Errorf("unmarshal network spec: %w", err)
	}
	return &NetworkInstance{
		Spec:  spec,
		store: d.Store,
	}, nil
}

func (d *NetworkDefinition) Load(r json.RawMessage) (typing.Instance, error) {
	return d.Instantiate(r)
}

func (d *NetworkDefinition) Delete(r typing.Reference) error {
	if d.Store == nil {
		return nil
	}
	if err := d.Store.Remove(r.GetId()); err != nil {
		return fmt.Errorf("removing network %s: %w", r.GetId(), err)
	}
	return nil
}

func (d *NetworkDefinition) GetStatus(_ typing.Reference) (typing.Status, error) {
	return SucceededStatus(), nil
}

func (d *NetworkDefinition) GetProviderStatus(_ typing.Reference) (typing.Status, error) {
	return SucceededStatus(), nil
}

// NetworkInstance is a concrete Network resource.
type NetworkInstance struct {
	Spec  NetworkSpec `json:"spec"`
	store *cni.Store
}

var _ typing.Instance = &NetworkInstance{}

func (i *NetworkInstance) GetId() string                { return i.Spec.Project }
func (i *NetworkInstance) GetKey() typing.DefinitionKey { return NetworkKey }
func (i *NetworkInstance) String() string               { return fmt.Sprintf("[Network %s]", i.Spec.Project) }

func (i *NetworkInstance) Apply() error {
	if i.store == nil {
		return fmt.Errorf("no CNI store configured")
	}
	if err := i.store.Write(i.Spec.Project); err != nil {
		return fmt.Errorf("writing network config for %s: %w", i.Spec.Project, err)
	}
	return nil
}
