package deploy

import (
	"encoding/json"
	"fmt"

	"github.com/systemstart/nix-compose/pkg/orchestrate/state"
	"github.com/systemstart/nix-compose/pkg/orchestrate/typing"
)

// Rollout tracks the status of a resource instance deployment.
type Rollout struct {
	InstanceId  string               `json:"instanceId"`
	InstanceKey typing.DefinitionKey `json:"instanceKey"`
	Body        json.RawMessage      `json:"body"`
	Messages    []string             `json:"messages"`
	Errors      []string             `json:"errors"`
	Status      *RolloutStatus       `json:"status"`
}

// UpdateStatus updates the rollout status from a typing.Status.
func (r *Rollout) UpdateStatus(s typing.Status) {
	if r.Status == nil {
		r.Status = &RolloutStatus{}
	}
	r.Status.Details = s.GetDetails()
	r.Status.Short = s.GetShort()
}

var _ typing.Rollout = &Rollout{}

func (r *Rollout) String() string {
	return fmt.Sprintf("[rollout for %s %s]", r.InstanceKey, r.InstanceId)
}

func (r *Rollout) GetStatus() typing.Status {
	return r.Status
}

func (r *Rollout) GetId() string {
	return r.InstanceId
}

func (r *Rollout) GetKey() typing.DefinitionKey {
	return r.InstanceKey
}

func (r *Rollout) GetBody() json.RawMessage {
	return r.Body
}

// RolloutStatus is a concrete Status implementation.
type RolloutStatus struct {
	Short   typing.RolloutStatusShort `json:"short"`
	Details json.RawMessage           `json:"details"`
}

func (rs *RolloutStatus) GetShort() typing.RolloutStatusShort {
	if rs == nil {
		return typing.RolloutStatusUnknown
	}
	return rs.Short
}

func (rs *RolloutStatus) GetDetails() json.RawMessage {
	if rs == nil {
		return nil
	}
	return rs.Details
}

func (rs *RolloutStatus) String() string {
	return fmt.Sprintf("status[%s, details: %s]", rs.Short, rs.Details)
}

// LoadRollout loads a Rollout from the database by instance ID.
func LoadRollout(db *state.DB, instanceId string) (*Rollout, error) {
	body, err := db.Load(state.RolloutsById, instanceId)
	if err != nil {
		return nil, fmt.Errorf("loading %s failed: %w", instanceId, err)
	}
	if body == nil {
		return nil, nil
	}
	return DeserializeRollout(body)
}

// DeserializeRollout unmarshals a Rollout from JSON.
func DeserializeRollout(r json.RawMessage) (*Rollout, error) {
	var result Rollout
	err := json.Unmarshal(r, &result)
	if err != nil {
		return nil, fmt.Errorf("unmarshaling failed: %w", err)
	}
	return &result, nil
}
