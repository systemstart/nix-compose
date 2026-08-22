package resources

import (
	"encoding/json"
	"fmt"

	"github.com/systemstart/nix-compose/pkg/orchestrate/typing"
)

// SimpleStatus is a basic Status implementation.
type SimpleStatus struct {
	Short   typing.RolloutStatusShort `json:"short"`
	Message string                    `json:"message,omitempty"`
}

func (s *SimpleStatus) GetShort() typing.RolloutStatusShort {
	return s.Short
}

func (s *SimpleStatus) GetDetails() json.RawMessage {
	if s.Message == "" {
		return nil
	}
	b, _ := json.Marshal(s.Message)
	return b
}

func (s *SimpleStatus) String() string {
	return fmt.Sprintf("%s: %s", s.Short, s.Message)
}

// SucceededStatus returns a SUCCEEDED status.
func SucceededStatus() typing.Status {
	return &SimpleStatus{Short: typing.RolloutStatusSuccess}
}

// PendingStatus returns a PENDING status.
func PendingStatus() typing.Status {
	return &SimpleStatus{Short: typing.RolloutStatusPending}
}

// ErrorStatus returns an ERROR status with a message.
func ErrorStatus(msg string) typing.Status {
	return &SimpleStatus{Short: typing.RolloutStatusError, Message: msg}
}

// RunningStatus returns a RUNNING status.
func RunningStatus() typing.Status {
	return &SimpleStatus{Short: typing.RolloutStatusRunning}
}

// DriftedStatus returns a DRIFTED status with a reason.
func DriftedStatus(reason string) typing.Status {
	return &SimpleStatus{Short: typing.RolloutStatusDrifted, Message: reason}
}
