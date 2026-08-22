package resources

import (
	"encoding/json"
	"testing"

	"github.com/systemstart/nix-compose/pkg/orchestrate/typing"
)

// ---------------------------------------------------------------------------
// Constructor tests
// ---------------------------------------------------------------------------

func TestSucceededStatus(t *testing.T) {
	s := SucceededStatus()
	if s.GetShort() != typing.RolloutStatusSuccess {
		t.Fatalf("expected %s, got %s", typing.RolloutStatusSuccess, s.GetShort())
	}
	if s.GetDetails() != nil {
		t.Fatalf("expected nil details, got %s", string(s.GetDetails()))
	}
}

func TestPendingStatus(t *testing.T) {
	s := PendingStatus()
	if s.GetShort() != typing.RolloutStatusPending {
		t.Fatalf("expected %s, got %s", typing.RolloutStatusPending, s.GetShort())
	}
	if s.GetDetails() != nil {
		t.Fatalf("expected nil details, got %s", string(s.GetDetails()))
	}
}

func TestRunningStatus(t *testing.T) {
	s := RunningStatus()
	if s.GetShort() != typing.RolloutStatusRunning {
		t.Fatalf("expected %s, got %s", typing.RolloutStatusRunning, s.GetShort())
	}
	if s.GetDetails() != nil {
		t.Fatalf("expected nil details, got %s", string(s.GetDetails()))
	}
}

func TestErrorStatus(t *testing.T) {
	msg := "something went wrong"
	s := ErrorStatus(msg)
	if s.GetShort() != typing.RolloutStatusError {
		t.Fatalf("expected %s, got %s", typing.RolloutStatusError, s.GetShort())
	}
	if s.GetDetails() == nil {
		t.Fatal("expected non-nil details for error status with message")
		return
	}
	var detail string
	if err := json.Unmarshal(s.GetDetails(), &detail); err != nil {
		t.Fatalf("failed to unmarshal details: %v", err)
	}
	if detail != msg {
		t.Fatalf("expected detail %q, got %q", msg, detail)
	}
}

func TestErrorStatusEmptyMessage(t *testing.T) {
	s := ErrorStatus("")
	if s.GetShort() != typing.RolloutStatusError {
		t.Fatalf("expected %s, got %s", typing.RolloutStatusError, s.GetShort())
	}
	if s.GetDetails() != nil {
		t.Fatalf("expected nil details for empty message, got %s", string(s.GetDetails()))
	}
}

func TestDriftedStatus(t *testing.T) {
	reason := "config changed"
	s := DriftedStatus(reason)
	if s.GetShort() != typing.RolloutStatusDrifted {
		t.Fatalf("expected %s, got %s", typing.RolloutStatusDrifted, s.GetShort())
	}
	if s.GetDetails() == nil {
		t.Fatal("expected non-nil details for drifted status with reason")
		return
	}
	var detail string
	if err := json.Unmarshal(s.GetDetails(), &detail); err != nil {
		t.Fatalf("failed to unmarshal details: %v", err)
	}
	if detail != reason {
		t.Fatalf("expected detail %q, got %q", reason, detail)
	}
}

func TestDriftedStatusEmptyReason(t *testing.T) {
	s := DriftedStatus("")
	if s.GetShort() != typing.RolloutStatusDrifted {
		t.Fatalf("expected %s, got %s", typing.RolloutStatusDrifted, s.GetShort())
	}
	if s.GetDetails() != nil {
		t.Fatalf("expected nil details for empty reason, got %s", string(s.GetDetails()))
	}
}

// ---------------------------------------------------------------------------
// GetShort table-driven test
// ---------------------------------------------------------------------------

func TestGetShort(t *testing.T) {
	tests := []struct {
		name     string
		status   typing.Status
		expected typing.RolloutStatusShort
	}{
		{"Succeeded", SucceededStatus(), typing.RolloutStatusSuccess},
		{"Pending", PendingStatus(), typing.RolloutStatusPending},
		{"Running", RunningStatus(), typing.RolloutStatusRunning},
		{"Error", ErrorStatus("err"), typing.RolloutStatusError},
		{"Drifted", DriftedStatus("drift"), typing.RolloutStatusDrifted},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.status.GetShort(); got != tc.expected {
				t.Fatalf("expected %s, got %s", tc.expected, got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// GetDetails table-driven test
// ---------------------------------------------------------------------------

func TestGetDetails(t *testing.T) {
	tests := []struct {
		name        string
		status      typing.Status
		expectNil   bool
		expectedMsg string
	}{
		{"Succeeded has no details", SucceededStatus(), true, ""},
		{"Pending has no details", PendingStatus(), true, ""},
		{"Running has no details", RunningStatus(), true, ""},
		{"Error with message", ErrorStatus("boom"), false, "boom"},
		{"Error empty message", ErrorStatus(""), true, ""},
		{"Drifted with reason", DriftedStatus("outdated"), false, "outdated"},
		{"Drifted empty reason", DriftedStatus(""), true, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			details := tc.status.GetDetails()
			if tc.expectNil {
				if details != nil {
					t.Fatalf("expected nil details, got %s", string(details))
				}
				return
			}
			if details == nil {
				t.Fatal("expected non-nil details")
				return
			}
			var msg string
			if err := json.Unmarshal(details, &msg); err != nil {
				t.Fatalf("failed to unmarshal details: %v", err)
			}
			if msg != tc.expectedMsg {
				t.Fatalf("expected %q, got %q", tc.expectedMsg, msg)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// GetDetails returns valid JSON
// ---------------------------------------------------------------------------

func TestGetDetailsIsValidJSON(t *testing.T) {
	statuses := []typing.Status{
		ErrorStatus("simple message"),
		DriftedStatus("quotes \" and backslash \\"),
		ErrorStatus("newline\nin message"),
		DriftedStatus("unicode: \u00e9\u00e8\u00ea"),
	}
	for i, s := range statuses {
		details := s.GetDetails()
		if details == nil {
			t.Fatalf("case %d: expected non-nil details", i)
			return
		}
		if !json.Valid(details) {
			t.Fatalf("case %d: details is not valid JSON: %s", i, string(details))
		}
	}
}

// ---------------------------------------------------------------------------
// String tests
// ---------------------------------------------------------------------------

func TestString(t *testing.T) {
	tests := []struct {
		name     string
		status   typing.Status
		expected string
	}{
		{"Succeeded", SucceededStatus(), "SUCCEEDED: "},
		{"Pending", PendingStatus(), "PENDING: "},
		{"Running", RunningStatus(), "RUNNING: "},
		{"Error", ErrorStatus("disk full"), "ERROR: disk full"},
		{"Error empty", ErrorStatus(""), "ERROR: "},
		{"Drifted", DriftedStatus("config mismatch"), "DRIFTED: config mismatch"},
		{"Drifted empty", DriftedStatus(""), "DRIFTED: "},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.status.String()
			if got != tc.expected {
				t.Fatalf("expected %q, got %q", tc.expected, got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// SimpleStatus direct construction
// ---------------------------------------------------------------------------

func TestSimpleStatusDirect(t *testing.T) {
	s := &SimpleStatus{
		Short:   typing.RolloutStatusFailed,
		Message: "timed out",
	}
	if s.GetShort() != typing.RolloutStatusFailed {
		t.Fatalf("expected %s, got %s", typing.RolloutStatusFailed, s.GetShort())
	}
	var detail string
	if err := json.Unmarshal(s.GetDetails(), &detail); err != nil {
		t.Fatalf("failed to unmarshal details: %v", err)
	}
	if detail != "timed out" {
		t.Fatalf("expected %q, got %q", "timed out", detail)
	}
	expected := "FAILED: timed out"
	if s.String() != expected {
		t.Fatalf("expected %q, got %q", expected, s.String())
	}
}

func TestSimpleStatusUnknown(t *testing.T) {
	s := &SimpleStatus{
		Short: typing.RolloutStatusUnknown,
	}
	if s.GetShort() != typing.RolloutStatusUnknown {
		t.Fatalf("expected %s, got %s", typing.RolloutStatusUnknown, s.GetShort())
	}
	if s.GetDetails() != nil {
		t.Fatalf("expected nil details, got %s", string(s.GetDetails()))
	}
	expected := "UNKNOWN: "
	if s.String() != expected {
		t.Fatalf("expected %q, got %q", expected, s.String())
	}
}

// ---------------------------------------------------------------------------
// Constructors return the typing.Status interface
// ---------------------------------------------------------------------------

func TestConstructorsReturnStatusInterface(t *testing.T) {
	_ = SucceededStatus()
	_ = PendingStatus()
	_ = RunningStatus()
	_ = ErrorStatus("x")
	_ = DriftedStatus("y")
}

// ---------------------------------------------------------------------------
// JSON marshalling of SimpleStatus
// ---------------------------------------------------------------------------

func TestSimpleStatusJSONMarshal(t *testing.T) {
	s := &SimpleStatus{
		Short:   typing.RolloutStatusError,
		Message: "connection refused",
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var got SimpleStatus
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if got.Short != s.Short {
		t.Fatalf("expected short %s, got %s", s.Short, got.Short)
	}
	if got.Message != s.Message {
		t.Fatalf("expected message %q, got %q", s.Message, got.Message)
	}
}

func TestSimpleStatusJSONOmitEmptyMessage(t *testing.T) {
	s := &SimpleStatus{
		Short: typing.RolloutStatusSuccess,
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("failed to unmarshal to map: %v", err)
	}
	if _, exists := raw["message"]; exists {
		t.Fatal("expected 'message' field to be omitted when empty")
	}
}

func TestSimpleStatusJSONIncludesMessage(t *testing.T) {
	s := &SimpleStatus{
		Short:   typing.RolloutStatusDrifted,
		Message: "version changed",
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("failed to unmarshal to map: %v", err)
	}
	if _, exists := raw["message"]; !exists {
		t.Fatal("expected 'message' field to be present when non-empty")
	}
}

func TestSimpleStatusJSONUnmarshal(t *testing.T) {
	input := `{"short":"RUNNING"}`
	var s SimpleStatus
	if err := json.Unmarshal([]byte(input), &s); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if s.Short != typing.RolloutStatusRunning {
		t.Fatalf("expected %s, got %s", typing.RolloutStatusRunning, s.Short)
	}
	if s.Message != "" {
		t.Fatalf("expected empty message, got %q", s.Message)
	}
}
