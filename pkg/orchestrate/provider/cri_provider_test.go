package provider

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/systemstart/nix-compose/pkg/orchestrate/deploy"
	"github.com/systemstart/nix-compose/pkg/orchestrate/resources"
	"github.com/systemstart/nix-compose/pkg/orchestrate/state"
	"github.com/systemstart/nix-compose/pkg/orchestrate/typing"
)

type mockProviderRef struct {
	id  string
	key typing.DefinitionKey
}

func (r *mockProviderRef) GetId() string                { return r.id }
func (r *mockProviderRef) GetKey() typing.DefinitionKey { return r.key }
func (r *mockProviderRef) String() string               { return "[ref " + r.id + "]" }

func TestNewCRIProvider_NilClients(t *testing.T) {
	p := NewCRIProvider(nil, nil, nil, nil)
	if p == nil {
		t.Fatal("expected non-nil provider")
		return
	}
	defs := p.GetDefinitions()
	if len(defs) != 6 {
		t.Fatalf("expected 6 definitions, got %d", len(defs))
	}
}

func TestNewCRIProvider_Fields(t *testing.T) {
	p := NewCRIProvider(nil, nil, nil, nil)
	if p.CRIClient != nil {
		t.Fatal("expected nil CRIClient")
	}
	if p.CNIStore != nil {
		t.Fatal("expected nil CNIStore")
	}
	if p.VolStore != nil {
		t.Fatal("expected nil VolStore")
	}
	if p.DB != nil {
		t.Fatal("expected nil DB")
	}
}

func TestGetDefinitions_Keys(t *testing.T) {
	p := NewCRIProvider(nil, nil, nil, nil)
	defs := p.GetDefinitions()

	expectedKeys := map[typing.DefinitionKey]bool{
		resources.ImageKey:     false,
		resources.NetworkKey:   false,
		resources.VolumeKey:    false,
		resources.ContainerKey: false,
		resources.ServiceKey:   false,
		resources.ProjectKey:   false,
	}

	for _, d := range defs {
		key := d.GetKey()
		if _, ok := expectedKeys[key]; !ok {
			t.Fatalf("unexpected definition key: %s", key)
		}
		expectedKeys[key] = true
	}

	for key, found := range expectedKeys {
		if !found {
			t.Fatalf("missing definition key: %s", key)
		}
	}
}

func TestGetReferencesTo_NilDB(t *testing.T) {
	p := NewCRIProvider(nil, nil, nil, nil)
	ref := &mockProviderRef{id: "proj/svc", key: resources.ContainerKey}
	rollouts, err := p.GetReferencesTo(ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rollouts != nil {
		t.Fatalf("expected nil rollouts, got %v", rollouts)
	}
}

func TestRemove_NilClient_Container(t *testing.T) {
	p := NewCRIProvider(nil, nil, nil, nil)
	ref := &mockProviderRef{id: "proj/svc", key: resources.ContainerKey}
	err := p.Remove(ref)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
}

func TestRemove_NilClient_Image(t *testing.T) {
	p := NewCRIProvider(nil, nil, nil, nil)
	ref := &mockProviderRef{id: "alpine:latest", key: resources.ImageKey}
	err := p.Remove(ref)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
}

func TestRemove_NilClient_Network(t *testing.T) {
	p := NewCRIProvider(nil, nil, nil, nil)
	ref := &mockProviderRef{id: "testnet", key: resources.NetworkKey}
	err := p.Remove(ref)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
}

func TestRemove_NilClient_Volume(t *testing.T) {
	p := NewCRIProvider(nil, nil, nil, nil)
	ref := &mockProviderRef{id: "p/vol", key: resources.VolumeKey}
	err := p.Remove(ref)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
}

func TestRemove_NilClient_Service(t *testing.T) {
	p := NewCRIProvider(nil, nil, nil, nil)
	ref := &mockProviderRef{id: "proj/svc", key: resources.ServiceKey}
	err := p.Remove(ref)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
}

func TestRemove_NilClient_Project(t *testing.T) {
	p := NewCRIProvider(nil, nil, nil, nil)
	ref := &mockProviderRef{id: "myproject", key: resources.ProjectKey}
	err := p.Remove(ref)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
}

func TestRemove_UnknownKey(t *testing.T) {
	p := NewCRIProvider(nil, nil, nil, nil)
	unknownKey := typing.CreateDefinitionKey("unknown", "v1", "Thing")
	ref := &mockProviderRef{id: "something", key: unknownKey}
	err := p.Remove(ref)
	if err != nil {
		t.Fatalf("expected nil error for unknown key, got: %v", err)
	}
}

func TestGetStatus_NilClient_Container(t *testing.T) {
	p := NewCRIProvider(nil, nil, nil, nil)
	ref := &mockProviderRef{id: "proj/svc", key: resources.ContainerKey}
	status, err := p.GetStatus(ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.GetShort() != typing.RolloutStatusPending {
		t.Fatalf("expected PENDING, got %s", status.GetShort())
	}
}

func TestGetStatus_NilClient_Image(t *testing.T) {
	p := NewCRIProvider(nil, nil, nil, nil)
	ref := &mockProviderRef{id: "alpine:latest", key: resources.ImageKey}
	status, err := p.GetStatus(ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.GetShort() != typing.RolloutStatusPending {
		t.Fatalf("expected PENDING, got %s", status.GetShort())
	}
}

func TestGetStatus_NilClient_Network(t *testing.T) {
	p := NewCRIProvider(nil, nil, nil, nil)
	ref := &mockProviderRef{id: "testnet", key: resources.NetworkKey}
	status, err := p.GetStatus(ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.GetShort() != typing.RolloutStatusSuccess {
		t.Fatalf("expected SUCCEEDED, got %s", status.GetShort())
	}
}

func TestGetStatus_NilClient_Project(t *testing.T) {
	p := NewCRIProvider(nil, nil, nil, nil)
	ref := &mockProviderRef{id: "myproject", key: resources.ProjectKey}
	status, err := p.GetStatus(ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.GetShort() != typing.RolloutStatusSuccess {
		t.Fatalf("expected SUCCEEDED, got %s", status.GetShort())
	}
}

func TestGetStatus_NilClient_Volume(t *testing.T) {
	p := NewCRIProvider(nil, nil, nil, nil)
	ref := &mockProviderRef{id: "p/vol", key: resources.VolumeKey}
	status, err := p.GetStatus(ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.GetShort() != typing.RolloutStatusSuccess {
		t.Fatalf("expected SUCCEEDED, got %s", status.GetShort())
	}
}

func TestGetStatus_NilClient_Service(t *testing.T) {
	p := NewCRIProvider(nil, nil, nil, nil)
	ref := &mockProviderRef{id: "proj/svc", key: resources.ServiceKey}
	status, err := p.GetStatus(ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.GetShort() != typing.RolloutStatusPending {
		t.Fatalf("expected PENDING, got %s", status.GetShort())
	}
}

func TestGetStatus_UnknownKey(t *testing.T) {
	p := NewCRIProvider(nil, nil, nil, nil)
	unknownKey := typing.CreateDefinitionKey("unknown", "v1", "Thing")
	ref := &mockProviderRef{id: "something", key: unknownKey}
	status, err := p.GetStatus(ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.GetShort() != typing.RolloutStatusPending {
		t.Fatalf("expected PENDING for unknown key, got %s", status.GetShort())
	}
}

func TestCRIProvider_ImplementsProvider(t *testing.T) {
	// compile-time check already in source, just verify non-nil
	p := NewCRIProvider(nil, nil, nil, nil)
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
}

func TestGetReferencesTo_WithDB(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.bolt")
	db, err := state.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Create source and target references.
	source := &typing.SimpleReference{
		Id:  "proj/svc",
		Key: resources.ServiceKey,
	}
	target := &typing.SimpleReference{
		Id:  "alpine:latest",
		Key: resources.ImageKey,
	}

	// Add a link: source depends on target.
	link := state.NewLink(source, target)
	if err := db.AddLink(link); err != nil {
		t.Fatalf("failed to add link: %v", err)
	}

	// Save a rollout for the source so LoadRollout can find it.
	rollout := &deploy.Rollout{
		InstanceId:  source.Id,
		InstanceKey: source.Key,
		Body:        json.RawMessage(`{}`),
		Status: &deploy.RolloutStatus{
			Short: typing.RolloutStatusPending,
		},
	}
	if err := db.Save(state.RolloutsById, rollout); err != nil {
		t.Fatalf("failed to save rollout: %v", err)
	}

	// Create the provider with the DB.
	p := NewCRIProvider(nil, nil, nil, db)

	// GetReferencesTo(target) should find source as a dependent.
	rollouts, err := p.GetReferencesTo(target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rollouts) != 1 {
		t.Fatalf("expected 1 rollout, got %d", len(rollouts))
	}
	if rollouts[0].GetId() != "proj/svc" {
		t.Fatalf("expected rollout id 'proj/svc', got %q", rollouts[0].GetId())
	}
}

func TestGetReferencesTo_WithDB_NoLinks(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.bolt")
	db, err := state.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	p := NewCRIProvider(nil, nil, nil, db)

	ref := &mockProviderRef{id: "nonexistent", key: resources.ImageKey}
	rollouts, err := p.GetReferencesTo(ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rollouts) != 0 {
		t.Fatalf("expected 0 rollouts, got %d", len(rollouts))
	}
}

func TestGetReferencesTo_WithDB_NoRolloutStored(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.bolt")
	db, err := state.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Create a link but do NOT save a rollout for the source.
	source := &typing.SimpleReference{
		Id:  "proj/svc",
		Key: resources.ServiceKey,
	}
	target := &typing.SimpleReference{
		Id:  "alpine:latest",
		Key: resources.ImageKey,
	}
	link := state.NewLink(source, target)
	if err := db.AddLink(link); err != nil {
		t.Fatalf("failed to add link: %v", err)
	}

	p := NewCRIProvider(nil, nil, nil, db)

	// GetReferencesTo should gracefully skip missing rollouts.
	rollouts, err := p.GetReferencesTo(target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rollouts) != 0 {
		t.Fatalf("expected 0 rollouts (no stored rollout), got %d", len(rollouts))
	}
}
