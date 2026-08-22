package deploy

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/google/uuid"
	"github.com/systemstart/nix-compose/pkg/orchestrate/state"
	"github.com/systemstart/nix-compose/pkg/orchestrate/typing"
)

// Deployment bundles a set of requests with dependency tracking.
type Deployment struct {
	Id           string               `json:"id"`
	Requests     RequestList          `json:"requests"`
	Dependencies []*state.Link        `json:"dependencies"`
	Depending    []*state.Link        `json:"depending"`
	References   typing.ReferenceList `json:"references"`
}

// NewDeployment creates a Deployment with a new UUID.
func NewDeployment() *Deployment {
	return &Deployment{
		Id:       uuid.New().String(),
		Requests: make(RequestList, 0),
	}
}

func (d *Deployment) GetId() string {
	return d.Id
}

// AddCreation adds a create request and records dependency links.
func (d *Deployment) AddCreation(instance typing.Instance, body json.RawMessage, dependencies typing.ReferenceList) {
	request := &CreateRequest{
		SubjectId:  instance.GetId(),
		SubjectKey: instance.GetKey(),
		Subject:    body,
	}
	d.Requests = append(d.Requests, request)

	for _, dep := range dependencies {
		d.Dependencies = append(d.Dependencies, state.NewLink(instance, dep))
	}

	d.References = append(d.References, instance)
}

// AddDeletion adds a delete request.
func (d *Deployment) AddDeletion(r typing.Reference) {
	request := &DeleteRequest{
		SubjectId:  r.GetId(),
		SubjectKey: r.GetKey(),
	}
	d.Requests = append(d.Requests, request)
}

// HasCreate checks if a create request exists for the given reference.
func (d *Deployment) HasCreate(r typing.Reference) bool {
	for _, req := range d.Requests {
		if req.GetType() == RequestTypeCreate && req.GetSubjectId() == r.GetId() {
			return true
		}
	}
	return false
}

// HasDelete checks if a delete request exists for the given reference.
func (d *Deployment) HasDelete(r typing.Rollout) bool {
	for _, req := range d.Requests {
		if req.GetType() == RequestTypeDelete && req.GetSubjectId() == r.GetId() {
			return true
		}
	}
	return false
}

// CheckReferences validates that all dependency references are satisfied.
func (d *Deployment) CheckReferences(ctx *RequestContext) []error {
	var result []error

	for _, request := range d.Requests {
		switch request.GetType() {
		case RequestTypeCreate:
			result = append(result, d.checkCreateReferences(ctx, request)...)
		case RequestTypeDelete:
			result = append(result, d.checkDeleteReferences(ctx, request)...)
		}
	}
	return result
}

// checkCreateReferences validates that create dependencies are either already deployed or included.
func (d *Deployment) checkCreateReferences(ctx *RequestContext, request Request) []error {
	var errs []error
	log.Printf("deployment %s, checking create reference %s %s",
		d.Id, request.GetSubjectKey(), request.GetSubjectId())

	for _, dep := range d.Dependencies {
		if dep.Source.GetId() != request.GetSubjectId() {
			continue
		}
		sourceReference := dep.Target
		rollout, err := LoadRollout(ctx.DB, sourceReference.GetId())
		if err == nil && rollout != nil {
			continue
		}
		if !d.HasCreate(sourceReference) {
			errs = append(errs, fmt.Errorf("missing dependency: %s", sourceReference.GetId()))
		}
	}
	return errs
}

// checkDeleteReferences validates that no un-deleted resources still reference the target.
func (d *Deployment) checkDeleteReferences(ctx *RequestContext, request Request) []error {
	var errs []error
	log.Printf("deployment %s, checking delete reference %s %s",
		d.Id, request.GetSubjectKey(), request.GetSubjectId())

	rollout, err := LoadRollout(ctx.DB, request.GetSubjectId())
	if err != nil {
		return []error{fmt.Errorf("can't load rollout for %s: %w", request.GetSubjectId(), err)}
	}

	var mayDepends []typing.Rollout
	for _, p := range ctx.Providers.All() {
		refs, err := p.GetReferencesTo(rollout)
		if err != nil {
			errs = append(errs, fmt.Errorf("can't get references to %s %s from provider: %w",
				request.GetSubjectKey(), request.GetSubjectId(), err))
			continue
		}
		mayDepends = append(mayDepends, refs...)
	}

	for _, mayDepend := range mayDepends {
		if !d.HasDelete(mayDepend) {
			errs = append(errs, fmt.Errorf("still referenced: %s", mayDepend.GetId()))
		}
	}
	return errs
}

// Save persists the deployment to BoltDB.
func (d *Deployment) Save(db *state.DB) error {
	if err := db.Save(state.DeploymentsById, d); err != nil {
		return fmt.Errorf("saving deployment %s: %w", d.Id, err)
	}
	return nil
}

// PersistLinks saves all dependency links to BoltDB.
func (d *Deployment) PersistLinks(db *state.DB) error {
	var err error
	for _, link := range d.Dependencies {
		err = db.AddLink(link)
		if err != nil {
			return fmt.Errorf("persisting dependency link %s failed: %w", link, err)
		}
	}
	for _, link := range d.Depending {
		err = db.AddLink(link)
		if err != nil {
			return fmt.Errorf("persisting depending link %s failed: %w", link, err)
		}
	}
	return nil
}

// LoadDeployment loads a Deployment from BoltDB by ID.
func LoadDeployment(db *state.DB, deploymentId string) (*Deployment, error) {
	body, err := db.Load(state.DeploymentsById, deploymentId)
	if err != nil {
		return nil, fmt.Errorf("load %s failed: %w", deploymentId, err)
	}
	var result Deployment
	err = json.Unmarshal(body, &result)
	if err != nil {
		return nil, fmt.Errorf("unmarshaling %s failed: %w", deploymentId, err)
	}

	return &result, nil
}
