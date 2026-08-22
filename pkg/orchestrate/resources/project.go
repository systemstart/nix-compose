package resources

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/systemstart/nix-compose/pkg/cri"
	"github.com/systemstart/nix-compose/pkg/orchestrate/typing"
)

// ProjectSpec is the spec for a Project resource.
type ProjectSpec struct {
	Name string `json:"name" yaml:"name"`
}

// ProjectDefinition handles Project resources.
// Apply is a no-op. Delete tears down all project resources.
type ProjectDefinition struct {
	Client *cri.Client
}

var _ typing.Definition = &ProjectDefinition{}

func (d *ProjectDefinition) GetKey() typing.DefinitionKey  { return ProjectKey }
func (d *ProjectDefinition) GetMappings() []typing.Mapping { return nil }

func (d *ProjectDefinition) Instantiate(r json.RawMessage) (typing.Instance, error) {
	var spec ProjectSpec
	if err := json.Unmarshal(r, &spec); err != nil {
		return nil, fmt.Errorf("unmarshal project spec: %w", err)
	}
	return &ProjectInstance{
		Spec:   spec,
		client: d.Client,
	}, nil
}

func (d *ProjectDefinition) Load(r json.RawMessage) (typing.Instance, error) {
	return d.Instantiate(r)
}

func (d *ProjectDefinition) Delete(r typing.Reference) error {
	if d.Client == nil {
		return nil
	}
	if err := d.Client.ProjectDown(context.Background(), r.GetId(), 10); err != nil {
		return fmt.Errorf("project down %s: %w", r.GetId(), err)
	}
	return nil
}

func (d *ProjectDefinition) GetStatus(_ typing.Reference) (typing.Status, error) {
	return SucceededStatus(), nil
}

func (d *ProjectDefinition) GetProviderStatus(_ typing.Reference) (typing.Status, error) {
	return SucceededStatus(), nil
}

// ProjectInstance is a concrete Project resource.
type ProjectInstance struct {
	Spec   ProjectSpec `json:"spec"`
	client *cri.Client
}

var _ typing.Instance = &ProjectInstance{}

func (i *ProjectInstance) GetId() string                { return i.Spec.Name }
func (i *ProjectInstance) GetKey() typing.DefinitionKey { return ProjectKey }
func (i *ProjectInstance) String() string               { return fmt.Sprintf("[Project %s]", i.Spec.Name) }

// Apply is a no-op for projects — they are just logical groupings.
func (i *ProjectInstance) Apply() error {
	return nil
}
