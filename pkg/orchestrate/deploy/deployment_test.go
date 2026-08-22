package deploy

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/systemstart/nix-compose/pkg/orchestrate/state"
	"github.com/systemstart/nix-compose/pkg/orchestrate/typing"
)

// stubInstance implements typing.Instance for testing.
type stubInstance struct {
	id  string
	key typing.DefinitionKey
}

func (s *stubInstance) GetId() string                { return s.id }
func (s *stubInstance) GetKey() typing.DefinitionKey { return s.key }
func (s *stubInstance) String() string               { return s.id }
func (s *stubInstance) Apply() error                 { return nil }

func openTestDB(t *testing.T) *state.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := state.Open(filepath.Join(dir, "test.bolt"))
	if err != nil {
		t.Fatalf("Open DB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// ===========================================================================
// Deployment tests
// ===========================================================================

func TestNewDeployment(t *testing.T) {
	d := NewDeployment()
	if d.Id == "" {
		t.Fatal("expected non-empty UUID")
	}
	if len(d.Requests) != 0 {
		t.Fatal("expected empty requests")
	}
}

func TestDeployment_GetId(t *testing.T) {
	d := NewDeployment()
	if d.GetId() == "" {
		t.Fatal("GetId() should return non-empty id")
	}
	if d.GetId() != d.Id {
		t.Fatalf("GetId() = %q, d.Id = %q", d.GetId(), d.Id)
	}
}

func TestAddCreation(t *testing.T) {
	d := NewDeployment()
	inst := &stubInstance{id: "res-1", key: "test/v1/Widget"}
	body, _ := json.Marshal(map[string]string{"name": "test"})
	deps := typing.ReferenceList{typing.NewReference("dep-1", "test/v1/Other")}

	d.AddCreation(inst, body, deps)

	if len(d.Requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(d.Requests))
	}
	if d.Requests[0].GetType() != RequestTypeCreate {
		t.Fatalf("expected CREATE request, got %s", d.Requests[0].GetType())
	}
	if len(d.Dependencies) != 1 {
		t.Fatalf("expected 1 dependency, got %d", len(d.Dependencies))
	}
	if !d.HasCreate(inst) {
		t.Fatal("HasCreate should return true")
	}
}

func TestAddCreation_NoDeps(t *testing.T) {
	d := NewDeployment()
	inst := &stubInstance{id: "no-dep", key: "test/v1/Widget"}
	d.AddCreation(inst, json.RawMessage(`{}`), nil)

	if len(d.Dependencies) != 0 {
		t.Fatalf("expected 0 dependencies, got %d", len(d.Dependencies))
	}
	if len(d.References) != 1 {
		t.Fatalf("expected 1 reference, got %d", len(d.References))
	}
}

func TestAddCreation_MultipleDeps(t *testing.T) {
	d := NewDeployment()
	inst := &stubInstance{id: "multi-dep", key: "test/v1/Widget"}
	deps := typing.ReferenceList{
		typing.NewReference("dep-a", "test/v1/A"),
		typing.NewReference("dep-b", "test/v1/B"),
	}
	d.AddCreation(inst, json.RawMessage(`{}`), deps)

	if len(d.Dependencies) != 2 {
		t.Fatalf("expected 2 dependencies, got %d", len(d.Dependencies))
	}
}

func TestAddDeletion(t *testing.T) {
	d := NewDeployment()
	ref := typing.NewReference("res-1", "test/v1/Widget")
	d.AddDeletion(ref)

	if len(d.Requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(d.Requests))
	}
	if d.Requests[0].GetType() != RequestTypeDelete {
		t.Fatalf("expected DELETE request, got %s", d.Requests[0].GetType())
	}
}

func TestHasCreate_False(t *testing.T) {
	d := NewDeployment()
	ref := typing.NewReference("nonexist", "test/v1/Widget")
	if d.HasCreate(ref) {
		t.Fatal("HasCreate should return false for empty deployment")
	}
}

func TestHasDelete(t *testing.T) {
	d := NewDeployment()
	ref := typing.NewReference("res-1", "test/v1/Widget")
	d.AddDeletion(ref)

	rollout := &Rollout{InstanceId: "res-1", InstanceKey: "test/v1/Widget"}
	if !d.HasDelete(rollout) {
		t.Fatal("HasDelete should return true")
	}

	otherRollout := &Rollout{InstanceId: "res-2", InstanceKey: "test/v1/Widget"}
	if d.HasDelete(otherRollout) {
		t.Fatal("HasDelete should return false for non-matching id")
	}
}

func TestDeployment_Save_LoadDeployment(t *testing.T) {
	db := openTestDB(t)
	d := NewDeployment()

	body, _ := json.Marshal(map[string]string{"image": "nginx"})
	inst := &stubInstance{id: "inst-1", key: "test/v1/Container"}
	d.AddCreation(inst, body, nil)
	d.AddDeletion(typing.NewReference("inst-2", "test/v1/Container"))

	if err := d.Save(db); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := LoadDeployment(db, d.Id)
	if err != nil {
		t.Fatalf("LoadDeployment failed: %v", err)
	}
	if loaded.Id != d.Id {
		t.Fatalf("Id mismatch: got %s, want %s", loaded.Id, d.Id)
	}
	if len(loaded.Requests) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(loaded.Requests))
	}
	if loaded.Requests[0].GetType() != RequestTypeCreate {
		t.Fatalf("first request type = %s, want CREATE", loaded.Requests[0].GetType())
	}
	if loaded.Requests[1].GetType() != RequestTypeDelete {
		t.Fatalf("second request type = %s, want DELETE", loaded.Requests[1].GetType())
	}
}

func TestLoadDeployment_NotFound(t *testing.T) {
	db := openTestDB(t)
	_, err := LoadDeployment(db, "nonexistent-deployment")
	if err == nil {
		t.Fatal("expected error loading nonexistent deployment")
	}
}

func TestDeployment_Save_EmptyDeployment(t *testing.T) {
	db := openTestDB(t)
	d := NewDeployment()

	if err := d.Save(db); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := LoadDeployment(db, d.Id)
	if err != nil {
		t.Fatalf("LoadDeployment failed: %v", err)
	}
	if loaded.Id != d.Id {
		t.Fatalf("Id mismatch: got %q, want %q", loaded.Id, d.Id)
	}
	if len(loaded.Requests) != 0 {
		t.Fatalf("expected 0 requests, got %d", len(loaded.Requests))
	}
}

func TestDeployment_PersistLinks(t *testing.T) {
	db := openTestDB(t)
	d := NewDeployment()
	inst := &stubInstance{id: "s-1", key: "test/v1/Svc"}
	dep := typing.NewReference("d-1", "test/v1/Dep")
	d.AddCreation(inst, json.RawMessage(`{}`), typing.ReferenceList{dep})

	if err := d.PersistLinks(db); err != nil {
		t.Fatalf("PersistLinks: %v", err)
	}
}

func TestRequestSerializationRoundTrip(t *testing.T) {
	d := NewDeployment()
	body, _ := json.Marshal(map[string]string{"image": "nginx"})
	d.Requests = append(d.Requests, &CreateRequest{
		SubjectId: "res-1", SubjectKey: "test/v1/Widget", Subject: body,
	})
	d.Requests = append(d.Requests, &DeleteRequest{
		SubjectId: "res-2", SubjectKey: "test/v1/Other",
	})
	d.Requests = append(d.Requests, &StatusRequest{
		SubjectId: "res-3", SubjectKey: "test/v1/Thing",
	})

	serialized, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var loaded Deployment
	if err := json.Unmarshal(serialized, &loaded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if len(loaded.Requests) != 3 {
		t.Fatalf("expected 3 requests, got %d", len(loaded.Requests))
	}
	if loaded.Requests[0].GetType() != RequestTypeCreate {
		t.Fatalf("expected CREATE, got %s", loaded.Requests[0].GetType())
	}
	if loaded.Requests[1].GetType() != RequestTypeDelete {
		t.Fatalf("expected DELETE, got %s", loaded.Requests[1].GetType())
	}
	if loaded.Requests[2].GetType() != RequestTypeStatus {
		t.Fatalf("expected STATUS, got %s", loaded.Requests[2].GetType())
	}
}

// ===========================================================================
// Rollout tests
// ===========================================================================

func TestRollout_GetId(t *testing.T) {
	r := &Rollout{InstanceId: "test-id", InstanceKey: "g/v1/K"}
	if r.GetId() != "test-id" {
		t.Fatalf("GetId() = %q", r.GetId())
	}
}

func TestRollout_GetKey(t *testing.T) {
	r := &Rollout{InstanceId: "test-id", InstanceKey: "g/v1/K"}
	if r.GetKey() != "g/v1/K" {
		t.Fatalf("GetKey() = %q", r.GetKey())
	}
}

func TestRollout_GetBody(t *testing.T) {
	body := json.RawMessage(`{"x":1}`)
	r := &Rollout{InstanceId: "id", InstanceKey: "g/v1/K", Body: body}
	if string(r.GetBody()) != `{"x":1}` {
		t.Fatalf("GetBody() = %s", r.GetBody())
	}
}

func TestRollout_GetStatus(t *testing.T) {
	r := &Rollout{
		InstanceId: "id", InstanceKey: "g/v1/K",
		Status: &RolloutStatus{Short: typing.RolloutStatusSuccess},
	}
	if r.GetStatus().GetShort() != typing.RolloutStatusSuccess {
		t.Fatalf("GetStatus().GetShort() = %q", r.GetStatus().GetShort())
	}
}

func TestRollout_String(t *testing.T) {
	r := &Rollout{InstanceId: "test-id", InstanceKey: "g/v1/K"}
	s := r.String()
	if !strings.Contains(s, "g/v1/K") || !strings.Contains(s, "test-id") {
		t.Fatalf("String() = %q", s)
	}
}

func TestRollout_UpdateStatus(t *testing.T) {
	r := &Rollout{InstanceId: "id", InstanceKey: "g/v1/K"}
	r.UpdateStatus(&RolloutStatus{Short: typing.RolloutStatusSuccess, Details: json.RawMessage(`{"ok":true}`)})
	if r.Status.Short != typing.RolloutStatusSuccess {
		t.Fatalf("Status.Short = %q", r.Status.Short)
	}
	if string(r.Status.Details) != `{"ok":true}` {
		t.Fatalf("Status.Details = %s", r.Status.Details)
	}
}

func TestRollout_UpdateStatus_NilInitial(t *testing.T) {
	r := &Rollout{InstanceId: "id", InstanceKey: "g/v1/K"}
	if r.Status != nil {
		t.Fatal("initial status should be nil")
	}
	r.UpdateStatus(&RolloutStatus{Short: typing.RolloutStatusPending})
	if r.Status == nil {
		t.Fatal("status should be non-nil after update")
		return
	}
}

func TestRollout_SaveAndLoad(t *testing.T) {
	db := openTestDB(t)
	r := &Rollout{
		InstanceId:  "roll-1",
		InstanceKey: "g/v1/K",
		Body:        json.RawMessage(`{"a":"b"}`),
		Status:      &RolloutStatus{Short: typing.RolloutStatusSuccess},
		Messages:    []string{"applied"},
	}
	if err := db.Save(state.RolloutsById, r); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := LoadRollout(db, "roll-1")
	if err != nil {
		t.Fatalf("LoadRollout: %v", err)
	}
	if loaded.InstanceId != "roll-1" {
		t.Fatalf("InstanceId = %q", loaded.InstanceId)
	}
	if loaded.Status.Short != typing.RolloutStatusSuccess {
		t.Fatalf("Status.Short = %q", loaded.Status.Short)
	}
	if len(loaded.Messages) != 1 || loaded.Messages[0] != "applied" {
		t.Fatalf("Messages = %v", loaded.Messages)
	}
}

func TestLoadRollout_NotFound(t *testing.T) {
	db := openTestDB(t)
	r, err := LoadRollout(db, "nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r != nil {
		t.Fatal("expected nil rollout for nonexistent id")
	}
}

func TestDeserializeRollout_Valid(t *testing.T) {
	raw := json.RawMessage(`{"instanceId":"id-1","instanceKey":"g/v1/K","body":{},"status":{"short":"SUCCEEDED"}}`)
	r, err := DeserializeRollout(raw)
	if err != nil {
		t.Fatalf("DeserializeRollout: %v", err)
	}
	if r.InstanceId != "id-1" {
		t.Fatalf("InstanceId = %q", r.InstanceId)
	}
	if r.Status.Short != typing.RolloutStatusSuccess {
		t.Fatalf("Status.Short = %q", r.Status.Short)
	}
}

func TestDeserializeRollout_Invalid(t *testing.T) {
	_, err := DeserializeRollout(json.RawMessage(`{invalid`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// ===========================================================================
// RolloutStatus tests
// ===========================================================================

func TestRolloutStatus_GetShort(t *testing.T) {
	rs := &RolloutStatus{Short: typing.RolloutStatusError}
	if rs.GetShort() != typing.RolloutStatusError {
		t.Fatalf("GetShort() = %q", rs.GetShort())
	}
}

func TestRolloutStatus_GetShort_NilReceiver(t *testing.T) {
	var rs *RolloutStatus
	if rs.GetShort() != typing.RolloutStatusUnknown {
		t.Fatalf("nil.GetShort() = %q", rs.GetShort())
	}
}

func TestRolloutStatus_GetDetails(t *testing.T) {
	details := json.RawMessage(`{"key":"val"}`)
	rs := &RolloutStatus{Details: details}
	if string(rs.GetDetails()) != `{"key":"val"}` {
		t.Fatalf("GetDetails() = %s", rs.GetDetails())
	}
}

func TestRolloutStatus_GetDetails_NilReceiver(t *testing.T) {
	var rs *RolloutStatus
	if rs.GetDetails() != nil {
		t.Fatal("nil.GetDetails() should be nil")
	}
}

func TestRolloutStatus_String(t *testing.T) {
	rs := &RolloutStatus{Short: typing.RolloutStatusSuccess, Details: json.RawMessage(`{"ok":true}`)}
	s := rs.String()
	if !strings.Contains(s, string(typing.RolloutStatusSuccess)) {
		t.Fatalf("String() = %q", s)
	}
}

func TestRolloutStatus_ImplementsStatus(t *testing.T) {
	var _ typing.Status = &RolloutStatus{}
}

func TestRollout_ImplementsRollout(t *testing.T) {
	var _ typing.Rollout = &Rollout{}
}

// ===========================================================================
// CreateRequest tests
// ===========================================================================

func TestCreateRequest_Accessors(t *testing.T) {
	cr := &CreateRequest{SubjectId: "c-1", SubjectKey: "g/v1/K", Subject: json.RawMessage(`{}`)}
	if cr.GetType() != RequestTypeCreate {
		t.Fatalf("GetType() = %q", cr.GetType())
	}
	if cr.GetSubjectId() != "c-1" {
		t.Fatalf("GetSubjectId() = %q", cr.GetSubjectId())
	}
	if cr.GetSubjectKey() != "g/v1/K" {
		t.Fatalf("GetSubjectKey() = %q", cr.GetSubjectKey())
	}
}

func TestCreateRequest_String(t *testing.T) {
	cr := &CreateRequest{SubjectId: "c-1", SubjectKey: "g/v1/K", Subject: json.RawMessage(`{"a":1}`)}
	s := cr.String()
	if !strings.Contains(s, "create-request") || !strings.Contains(s, "c-1") {
		t.Fatalf("String() = %q", s)
	}
}

func TestCreateRequest_String_EmptyBody(t *testing.T) {
	cr := &CreateRequest{SubjectId: "c-2", SubjectKey: "g/v1/K"}
	s := cr.String()
	if !strings.Contains(s, "body length: 0") {
		t.Fatalf("String() = %q", s)
	}
}

// ===========================================================================
// DeleteRequest tests
// ===========================================================================

func TestDeleteRequest_Accessors(t *testing.T) {
	dr := &DeleteRequest{SubjectId: "d-1", SubjectKey: "g/v1/K"}
	if dr.GetType() != RequestTypeDelete {
		t.Fatalf("GetType() = %q", dr.GetType())
	}
	if dr.GetSubjectId() != "d-1" {
		t.Fatalf("GetSubjectId() = %q", dr.GetSubjectId())
	}
	if dr.GetSubjectKey() != "g/v1/K" {
		t.Fatalf("GetSubjectKey() = %q", dr.GetSubjectKey())
	}
}

func TestDeleteRequest_String(t *testing.T) {
	dr := &DeleteRequest{SubjectId: "d-1", SubjectKey: "g/v1/K"}
	s := dr.String()
	if !strings.Contains(s, "delete-request") || !strings.Contains(s, "d-1") {
		t.Fatalf("String() = %q", s)
	}
}

// ===========================================================================
// StatusRequest tests
// ===========================================================================

func TestStatusRequest_Accessors(t *testing.T) {
	sr := &StatusRequest{SubjectId: "s-1", SubjectKey: "g/v1/K"}
	if sr.GetType() != RequestTypeStatus {
		t.Fatalf("GetType() = %q", sr.GetType())
	}
	if sr.GetSubjectId() != "s-1" {
		t.Fatalf("GetSubjectId() = %q", sr.GetSubjectId())
	}
	if sr.GetSubjectKey() != "g/v1/K" {
		t.Fatalf("GetSubjectKey() = %q", sr.GetSubjectKey())
	}
}

func TestStatusRequest_String(t *testing.T) {
	sr := &StatusRequest{SubjectId: "s-1", SubjectKey: "g/v1/K"}
	s := sr.String()
	if !strings.Contains(s, "status-request") || !strings.Contains(s, "s-1") {
		t.Fatalf("String() = %q", s)
	}
}

func TestStatusRequest_GetStatus(t *testing.T) {
	sr := &StatusRequest{SubjectId: "s-1", SubjectKey: "g/v1/K"}
	status, err := sr.GetStatus(nil)
	if err != nil {
		t.Fatalf("GetStatus() error: %v", err)
	}
	if status != typing.RolloutStatusRunning {
		t.Fatalf("GetStatus() = %q", status)
	}
}

// ===========================================================================
// RequestList tests
// ===========================================================================

func TestRequestList_MarshalUnmarshal_Empty(t *testing.T) {
	rl := RequestList{}
	data, err := rl.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	var loaded RequestList
	if err := loaded.UnmarshalJSON(data); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("expected 0 requests, got %d", len(loaded))
	}
}

func TestRequestList_MarshalUnmarshal_AllTypes(t *testing.T) {
	rl := RequestList{
		&CreateRequest{SubjectId: "c-1", SubjectKey: "g/v1/A", Subject: json.RawMessage(`{"img":"nginx"}`)},
		&DeleteRequest{SubjectId: "d-1", SubjectKey: "g/v1/B"},
		&StatusRequest{SubjectId: "s-1", SubjectKey: "g/v1/C"},
	}

	data, err := rl.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}

	var loaded RequestList
	if err := loaded.UnmarshalJSON(data); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	if len(loaded) != 3 {
		t.Fatalf("expected 3 requests, got %d", len(loaded))
	}

	types := []RequestType{RequestTypeCreate, RequestTypeDelete, RequestTypeStatus}
	for i, expected := range types {
		if loaded[i].GetType() != expected {
			t.Fatalf("request[%d].GetType() = %q, want %q", i, loaded[i].GetType(), expected)
		}
	}
}

func TestRequestList_UnmarshalJSON_Invalid(t *testing.T) {
	var rl RequestList
	err := rl.UnmarshalJSON([]byte(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestCreateRequest_GetStatus_WithRollout(t *testing.T) {
	db := openTestDB(t)

	rollout := &Rollout{
		InstanceId:  "w-1",
		InstanceKey: "test/v1/Widget",
		Status:      &RolloutStatus{Short: typing.RolloutStatusSuccess},
	}
	if err := db.Save(state.RolloutsById, rollout); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cr := &CreateRequest{SubjectId: "w-1", SubjectKey: "test/v1/Widget"}
	ctx := &RequestContext{DB: db}
	status, err := cr.GetStatus(ctx)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status != typing.RolloutStatusSuccess {
		t.Fatalf("expected SUCCESS, got %s", status)
	}
}

func TestDeleteRequest_GetStatus_WithRollout(t *testing.T) {
	db := openTestDB(t)

	rollout := &Rollout{
		InstanceId:  "w-1",
		InstanceKey: "test/v1/Widget",
		Status:      &RolloutStatus{Short: typing.RolloutStatusSuccess},
	}
	if err := db.Save(state.RolloutsById, rollout); err != nil {
		t.Fatalf("Save: %v", err)
	}

	dr := &DeleteRequest{SubjectId: "w-1", SubjectKey: "test/v1/Widget"}
	ctx := &RequestContext{DB: db}
	status, err := dr.GetStatus(ctx)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status != typing.RolloutStatusSuccess {
		t.Fatalf("expected SUCCESS, got %s", status)
	}
}

func TestDeleteRequest_Process_UnknownKey(t *testing.T) {
	db := openTestDB(t)
	reg := typing.NewRegistry()

	dr := &DeleteRequest{
		SubjectId:  "w-1",
		SubjectKey: "unknown/v1/Widget",
	}

	ctx := &RequestContext{Registry: reg, DB: db}
	err := dr.Process(ctx)
	if err == nil {
		t.Fatal("expected error for unknown key")
	}
}

func TestCreateRequest_Process_UnknownKey(t *testing.T) {
	db := openTestDB(t)
	reg := typing.NewRegistry()

	cr := &CreateRequest{
		SubjectId:  "w-1",
		SubjectKey: "unknown/v1/Widget",
		Subject:    json.RawMessage(`{}`),
	}

	ctx := &RequestContext{Registry: reg, DB: db}
	err := cr.Process(ctx)
	if err == nil {
		t.Fatal("expected error for unknown key")
	}
}

// ===========================================================================
// Additional tests to improve coverage
// ===========================================================================

func TestDeployment_Save_WithStatusRequests(t *testing.T) {
	db := openTestDB(t)
	d := NewDeployment()

	// Add a mix of all request types.
	body, _ := json.Marshal(map[string]string{"image": "nginx"})
	inst := &stubInstance{id: "inst-1", key: "test/v1/Container"}
	d.AddCreation(inst, body, nil)
	d.AddDeletion(typing.NewReference("inst-2", "test/v1/Container"))
	d.Requests = append(d.Requests, &StatusRequest{
		SubjectId:  "inst-3",
		SubjectKey: "test/v1/Container",
	})

	// Save the deployment.
	if err := d.Save(db); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Load it back and verify all request types survived the round-trip.
	loaded, err := LoadDeployment(db, d.Id)
	if err != nil {
		t.Fatalf("LoadDeployment failed: %v", err)
	}
	if loaded.Id != d.Id {
		t.Fatalf("Id mismatch: got %s, want %s", loaded.Id, d.Id)
	}
	if len(loaded.Requests) != 3 {
		t.Fatalf("expected 3 requests, got %d", len(loaded.Requests))
	}
	if loaded.Requests[0].GetType() != RequestTypeCreate {
		t.Fatalf("request[0] type = %s, want CREATE", loaded.Requests[0].GetType())
	}
	if loaded.Requests[1].GetType() != RequestTypeDelete {
		t.Fatalf("request[1] type = %s, want DELETE", loaded.Requests[1].GetType())
	}
	if loaded.Requests[2].GetType() != RequestTypeStatus {
		t.Fatalf("request[2] type = %s, want STATUS", loaded.Requests[2].GetType())
	}

	// Verify the StatusRequest fields survived.
	if loaded.Requests[2].GetSubjectId() != "inst-3" {
		t.Fatalf("StatusRequest SubjectId = %s, want inst-3", loaded.Requests[2].GetSubjectId())
	}
	if loaded.Requests[2].GetSubjectKey() != "test/v1/Container" {
		t.Fatalf("StatusRequest SubjectKey = %s, want test/v1/Container", loaded.Requests[2].GetSubjectKey())
	}
}

func TestDeployment_SaveAndLoad_WithDependencies(t *testing.T) {
	db := openTestDB(t)
	d := NewDeployment()

	inst := &stubInstance{id: "svc-1", key: "test/v1/Service"}
	dep1 := typing.NewReference("db-1", "test/v1/Database")
	dep2 := typing.NewReference("cache-1", "test/v1/Cache")
	body := json.RawMessage(`{"name":"my-service"}`)
	d.AddCreation(inst, body, typing.ReferenceList{dep1, dep2})

	// Save.
	if err := d.Save(db); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Load and verify.
	loaded, err := LoadDeployment(db, d.Id)
	if err != nil {
		t.Fatalf("LoadDeployment: %v", err)
	}

	if len(loaded.Dependencies) != 2 {
		t.Fatalf("expected 2 dependencies, got %d", len(loaded.Dependencies))
	}
	if len(loaded.References) != 1 {
		t.Fatalf("expected 1 reference, got %d", len(loaded.References))
	}
	if loaded.References[0].GetId() != "svc-1" {
		t.Fatalf("reference id = %s, want svc-1", loaded.References[0].GetId())
	}

	// Verify the create request body survived.
	cr, ok := loaded.Requests[0].(*CreateRequest)
	if !ok {
		t.Fatalf("expected *CreateRequest, got %T", loaded.Requests[0])
	}
	if string(cr.Subject) != `{"name":"my-service"}` {
		t.Fatalf("Subject = %s, want {\"name\":\"my-service\"}", string(cr.Subject))
	}
}

func TestDeployment_Save_Overwrite(t *testing.T) {
	db := openTestDB(t)
	d := NewDeployment()

	// Save initially with one request.
	d.AddDeletion(typing.NewReference("r-1", "test/v1/Widget"))
	if err := d.Save(db); err != nil {
		t.Fatalf("first Save: %v", err)
	}

	// Add another request and save again (overwrite).
	d.Requests = append(d.Requests, &CreateRequest{
		SubjectId:  "r-2",
		SubjectKey: "test/v1/Widget",
		Subject:    json.RawMessage(`{}`),
	})
	if err := d.Save(db); err != nil {
		t.Fatalf("second Save: %v", err)
	}

	// Load and verify the latest state.
	loaded, err := LoadDeployment(db, d.Id)
	if err != nil {
		t.Fatalf("LoadDeployment: %v", err)
	}
	if len(loaded.Requests) != 2 {
		t.Fatalf("expected 2 requests after overwrite, got %d", len(loaded.Requests))
	}
}
