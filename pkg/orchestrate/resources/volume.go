package resources

import (
	"encoding/json"
	"fmt"

	"github.com/systemstart/nix-compose/pkg/orchestrate/typing"
	"github.com/systemstart/nix-compose/pkg/volumes"
)

// VolumeSpec is the spec for a Volume resource.
type VolumeSpec struct {
	Project string `json:"project" yaml:"project"`
	Name    string `json:"name" yaml:"name"`
}

// VolumeDefinition handles Volume resources.
// Delete is a no-op by default — named volumes persist across plan/apply cycles.
// Only removed on explicit --volumes flag.
type VolumeDefinition struct {
	Store *volumes.Store
}

var _ typing.Definition = &VolumeDefinition{}

func (d *VolumeDefinition) GetKey() typing.DefinitionKey  { return VolumeKey }
func (d *VolumeDefinition) GetMappings() []typing.Mapping { return nil }

func (d *VolumeDefinition) Instantiate(r json.RawMessage) (typing.Instance, error) {
	var spec VolumeSpec
	if err := json.Unmarshal(r, &spec); err != nil {
		return nil, fmt.Errorf("unmarshal volume spec: %w", err)
	}
	return &VolumeInstance{
		Spec:  spec,
		store: d.Store,
	}, nil
}

func (d *VolumeDefinition) Load(r json.RawMessage) (typing.Instance, error) {
	return d.Instantiate(r)
}

// Delete is a no-op for volumes. Named volumes survive container recreation.
// Volume removal requires explicit user action (e.g. nix-compose down --volumes).
func (d *VolumeDefinition) Delete(_ typing.Reference) error {
	return nil
}

func (d *VolumeDefinition) GetStatus(_ typing.Reference) (typing.Status, error) {
	return SucceededStatus(), nil
}

func (d *VolumeDefinition) GetProviderStatus(_ typing.Reference) (typing.Status, error) {
	return SucceededStatus(), nil
}

// VolumeInstance is a concrete Volume resource.
type VolumeInstance struct {
	Spec  VolumeSpec `json:"spec"`
	store *volumes.Store
}

var _ typing.Instance = &VolumeInstance{}

func (i *VolumeInstance) GetId() string {
	return fmt.Sprintf("%s/%s", i.Spec.Project, i.Spec.Name)
}
func (i *VolumeInstance) GetKey() typing.DefinitionKey { return VolumeKey }
func (i *VolumeInstance) String() string {
	return fmt.Sprintf("[Volume %s/%s]", i.Spec.Project, i.Spec.Name)
}

func (i *VolumeInstance) Apply() error {
	if i.store == nil {
		return fmt.Errorf("no volume store configured")
	}
	if _, err := i.store.Ensure(i.Spec.Project, i.Spec.Name); err != nil {
		return fmt.Errorf("ensuring volume %s/%s: %w", i.Spec.Project, i.Spec.Name, err)
	}
	return nil
}

// GetHostPath returns the resolved host path for this volume.
func (i *VolumeInstance) GetHostPath() string {
	if i.store == nil {
		return ""
	}
	return i.store.Path(i.Spec.Project, i.Spec.Name)
}
