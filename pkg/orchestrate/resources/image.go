package resources

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/systemstart/nix-compose/pkg/cri"
	"github.com/systemstart/nix-compose/pkg/orchestrate/typing"
)

// ImageSpec is the spec for an Image resource.
type ImageSpec struct {
	Image string `json:"image" yaml:"image"`
}

// ImageDefinition handles Image resources.
type ImageDefinition struct {
	Client *cri.Client
}

var _ typing.Definition = &ImageDefinition{}

func (d *ImageDefinition) GetKey() typing.DefinitionKey  { return ImageKey }
func (d *ImageDefinition) GetMappings() []typing.Mapping { return nil }

func (d *ImageDefinition) Instantiate(r json.RawMessage) (typing.Instance, error) {
	var spec ImageSpec
	if err := json.Unmarshal(r, &spec); err != nil {
		return nil, fmt.Errorf("unmarshal image spec: %w", err)
	}
	return &ImageInstance{
		Spec:   spec,
		client: d.Client,
	}, nil
}

func (d *ImageDefinition) Load(r json.RawMessage) (typing.Instance, error) {
	return d.Instantiate(r)
}

func (d *ImageDefinition) Delete(r typing.Reference) error {
	if d.Client == nil {
		return nil
	}
	if err := d.Client.RemoveImage(context.Background(), r.GetId()); err != nil {
		return fmt.Errorf("removing image %s: %w", r.GetId(), err)
	}
	return nil
}

func (d *ImageDefinition) GetStatus(r typing.Reference) (typing.Status, error) {
	return d.GetProviderStatus(r) //nolint:wrapcheck // delegates to own method
}

func (d *ImageDefinition) GetProviderStatus(r typing.Reference) (typing.Status, error) {
	if d.Client == nil {
		return PendingStatus(), nil
	}
	img, err := d.Client.ImageStatus(context.Background(), r.GetId())
	if err != nil {
		return ErrorStatus(err.Error()), nil
	}
	if img == nil {
		return PendingStatus(), nil
	}
	return SucceededStatus(), nil
}

// ImageInstance is a concrete Image resource.
type ImageInstance struct {
	Spec   ImageSpec `json:"spec"`
	client *cri.Client
}

var _ typing.Instance = &ImageInstance{}

// GetId returns the reference the runtime knows this image by. For a local
// artifact that is the reference it is imported under, not the store path —
// the store path is what the user wrote, but ImageStatus has never heard of it.
func (i *ImageInstance) GetId() string                { return cri.ResolvedImageRef(i.Spec.Image) }
func (i *ImageInstance) GetKey() typing.DefinitionKey { return ImageKey }
func (i *ImageInstance) String() string               { return fmt.Sprintf("[Image %s]", i.Spec.Image) }

func (i *ImageInstance) Apply() error {
	if i.client == nil {
		return fmt.Errorf("no CRI client configured")
	}
	if _, err := i.client.EnsureImage(context.Background(), i.Spec.Image); err != nil {
		return fmt.Errorf("resolving image %s: %w", i.Spec.Image, err)
	}
	return nil
}
