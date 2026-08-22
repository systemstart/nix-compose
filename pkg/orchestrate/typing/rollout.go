package typing

import "encoding/json"

// RolloutStatusShort is a short status label for rollouts.
type RolloutStatusShort string

const (
	RolloutStatusFailed  RolloutStatusShort = "FAILED"
	RolloutStatusPending RolloutStatusShort = "PENDING"
	RolloutStatusRunning RolloutStatusShort = "RUNNING"
	RolloutStatusError   RolloutStatusShort = "ERROR"
	RolloutStatusSuccess RolloutStatusShort = "SUCCEEDED"
	RolloutStatusDrifted RolloutStatusShort = "DRIFTED"
	RolloutStatusUnknown RolloutStatusShort = "UNKNOWN"
)

// Rollout is a Reference with status and body tracking.
type Rollout interface {
	Reference
	GetStatus() Status
	GetBody() json.RawMessage
}

// Status describes the current state of a rollout.
type Status interface {
	GetShort() RolloutStatusShort
	GetDetails() json.RawMessage
	String() string
}
