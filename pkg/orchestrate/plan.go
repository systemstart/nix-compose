package orchestrate

import (
	"encoding/json"
	"sort"

	"github.com/systemstart/nix-compose/pkg/orchestrate/convert"
	"github.com/systemstart/nix-compose/pkg/orchestrate/deploy"
	"github.com/systemstart/nix-compose/pkg/orchestrate/typing"
)

// ActionType classifies a planned action.
type ActionType string

const (
	ActionCreate  ActionType = "create"
	ActionUpdate  ActionType = "update"
	ActionDestroy ActionType = "destroy"
	ActionNoOp    ActionType = "noop"
)

// Action represents a single planned change.
type Action struct {
	Type       ActionType
	ResourceID string
	Key        typing.DefinitionKey
	Reason     string
}

// Plan holds the diff result and ready-to-apply deployment.
type Plan struct {
	Actions    []Action
	Deployment *deploy.Deployment
	Conditions convert.ConditionMap
}

// ComputePlan diffs a desired deployment against current rollouts to produce
// a plan with the minimal set of create/update/destroy actions.
func ComputePlan(desired *deploy.Deployment, currentRollouts []*deploy.Rollout,
	conditions convert.ConditionMap,
) *Plan {
	// Index current rollouts by InstanceId.
	current := make(map[string]*deploy.Rollout, len(currentRollouts))
	for _, r := range currentRollouts {
		current[r.InstanceId] = r
	}

	plan := &Plan{
		Deployment: deploy.NewDeployment(),
		Conditions: conditions,
	}

	// Walk desired create requests.
	for _, req := range desired.Requests {
		if req.GetType() != deploy.RequestTypeCreate {
			continue
		}
		cr := req.(*deploy.CreateRequest)
		id := cr.SubjectId
		key := cr.SubjectKey

		existing, found := current[id]
		if !found {
			// New resource → create.
			plan.Actions = append(plan.Actions, Action{
				Type:       ActionCreate,
				ResourceID: id,
				Key:        key,
				Reason:     "new resource",
			})
			plan.Deployment.Requests = append(plan.Deployment.Requests, cr)
		} else if !bodyEqual(cr.Subject, existing.Body) {
			// Exists but body differs → update (delete + create).
			plan.Actions = append(plan.Actions, Action{
				Type:       ActionUpdate,
				ResourceID: id,
				Key:        key,
				Reason:     "configuration changed",
			})
			plan.Deployment.AddDeletion(typing.NewReference(id, key))
			plan.Deployment.Requests = append(plan.Deployment.Requests, cr)
			delete(current, id)
		} else {
			// Exists and body matches → noop.
			plan.Actions = append(plan.Actions, Action{
				Type:       ActionNoOp,
				ResourceID: id,
				Key:        key,
				Reason:     "unchanged",
			})
			delete(current, id)
		}
	}

	// Remaining current rollouts are orphans → destroy.
	orphanIDs := make([]string, 0, len(current))
	for id := range current {
		orphanIDs = append(orphanIDs, id)
	}
	sort.Strings(orphanIDs)
	for _, id := range orphanIDs {
		r := current[id]
		plan.Actions = append(plan.Actions, Action{
			Type:       ActionDestroy,
			ResourceID: id,
			Key:        r.InstanceKey,
			Reason:     "orphaned resource",
		})
		plan.Deployment.AddDeletion(typing.NewReference(id, r.InstanceKey))
	}

	// Copy dependency metadata from desired deployment.
	plan.Deployment.Dependencies = desired.Dependencies
	plan.Deployment.References = desired.References

	return plan
}

// bodyEqual compares two JSON bodies for equality after normalizing key ordering.
func bodyEqual(a, b json.RawMessage) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) == 0 || len(b) == 0 {
		return false
	}

	var av, bv interface{}
	if err := json.Unmarshal(a, &av); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		return false
	}

	an, err := json.Marshal(av)
	if err != nil {
		return false
	}
	bn, err := json.Marshal(bv)
	if err != nil {
		return false
	}

	return string(an) == string(bn)
}

// Summary returns a summary of plan actions by type.
func (p *Plan) Summary() (creates, updates, destroys, noops int) {
	for _, a := range p.Actions {
		switch a.Type {
		case ActionCreate:
			creates++
		case ActionUpdate:
			updates++
		case ActionDestroy:
			destroys++
		case ActionNoOp:
			noops++
		}
	}
	return
}
