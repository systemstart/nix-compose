package provider

import (
	"fmt"

	"github.com/systemstart/nix-compose/pkg/cni"
	"github.com/systemstart/nix-compose/pkg/cri"
	"github.com/systemstart/nix-compose/pkg/orchestrate/deploy"
	"github.com/systemstart/nix-compose/pkg/orchestrate/resources"
	"github.com/systemstart/nix-compose/pkg/orchestrate/state"
	"github.com/systemstart/nix-compose/pkg/orchestrate/typing"
	"github.com/systemstart/nix-compose/pkg/volumes"
)

// CRIProvider implements deploy.Provider for CRI container resources.
type CRIProvider struct {
	CRIClient *cri.Client
	CNIStore  *cni.Store
	VolStore  *volumes.Store
	DB        *state.DB

	defs []typing.Definition
}

var _ deploy.Provider = &CRIProvider{}

// NewCRIProvider creates a CRIProvider with all 6 resource definitions.
func NewCRIProvider(criClient *cri.Client, cniStore *cni.Store, volStore *volumes.Store, db *state.DB) *CRIProvider {
	p := &CRIProvider{
		CRIClient: criClient,
		CNIStore:  cniStore,
		VolStore:  volStore,
		DB:        db,
	}

	p.defs = []typing.Definition{
		&resources.ImageDefinition{Client: criClient},
		&resources.NetworkDefinition{Store: cniStore},
		&resources.VolumeDefinition{Store: volStore},
		&resources.ContainerDefinition{Client: criClient, VolStore: volStore},
		&resources.ServiceDefinition{Client: criClient, VolStore: volStore},
		&resources.ProjectDefinition{Client: criClient},
	}

	return p
}

func (p *CRIProvider) GetDefinitions() []typing.Definition {
	return p.defs
}

func (p *CRIProvider) GetReferencesTo(r typing.Reference) ([]typing.Rollout, error) {
	if p.DB == nil {
		return nil, nil
	}

	deps, err := p.DB.GetDepending(r)
	if err != nil {
		return nil, fmt.Errorf("get depending for %s: %w", r.GetId(), err)
	}

	var rollouts []typing.Rollout
	for _, dep := range deps {
		rollout, err := deploy.LoadRollout(p.DB, dep.GetId())
		if err != nil || rollout == nil {
			continue
		}
		rollouts = append(rollouts, rollout)
	}
	return rollouts, nil
}

func (p *CRIProvider) Remove(r typing.Reference) error {
	for _, d := range p.defs {
		if d.GetKey() == r.GetKey() {
			if err := d.Delete(r); err != nil {
				return fmt.Errorf("delete %s %s: %w", r.GetKey(), r.GetId(), err)
			}
			return nil
		}
	}
	return nil
}

func (p *CRIProvider) GetStatus(r typing.Reference) (typing.Status, error) {
	for _, d := range p.defs {
		if d.GetKey() == r.GetKey() {
			status, err := d.GetProviderStatus(r)
			if err != nil {
				return nil, fmt.Errorf("provider status for %s %s: %w", r.GetKey(), r.GetId(), err)
			}
			return status, nil
		}
	}
	return resources.PendingStatus(), nil
}
