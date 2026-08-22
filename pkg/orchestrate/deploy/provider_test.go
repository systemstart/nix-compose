package deploy

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/systemstart/nix-compose/pkg/orchestrate/state"
	"github.com/systemstart/nix-compose/pkg/orchestrate/typing"
)

// stubProvider implements the Provider interface for testing.
type stubProvider struct {
	definitions []typing.Definition
}

func (p *stubProvider) GetReferencesTo(_ typing.Reference) ([]typing.Rollout, error) {
	return nil, nil
}

func (p *stubProvider) Remove(_ typing.Reference) error {
	return nil
}

func (p *stubProvider) GetStatus(_ typing.Reference) (typing.Status, error) {
	return &RolloutStatus{Short: typing.RolloutStatusSuccess}, nil
}

func (p *stubProvider) GetDefinitions() []typing.Definition {
	return p.definitions
}

// stubProviderDef implements typing.Definition for provider testing.
type stubProviderDef struct {
	key typing.DefinitionKey
}

func (d *stubProviderDef) GetKey() typing.DefinitionKey                         { return d.key }
func (d *stubProviderDef) GetMappings() []typing.Mapping                        { return nil }
func (d *stubProviderDef) Instantiate(json.RawMessage) (typing.Instance, error) { return nil, nil }
func (d *stubProviderDef) Load(json.RawMessage) (typing.Instance, error)        { return nil, nil }
func (d *stubProviderDef) Delete(typing.Reference) error                        { return nil }
func (d *stubProviderDef) GetStatus(typing.Reference) (typing.Status, error)    { return nil, nil }
func (d *stubProviderDef) GetProviderStatus(typing.Reference) (typing.Status, error) {
	return nil, nil
}

func TestProviderRegistry_RegisterAndForKey(t *testing.T) {
	reg := typing.NewRegistry()
	pr := NewProviderRegistry(reg)

	def := &stubProviderDef{key: "test/v1/Widget"}
	provider := &stubProvider{definitions: []typing.Definition{def}}
	pr.Register("test-provider", provider)

	got, err := pr.ForKey("test/v1/Widget")
	if err != nil {
		t.Fatalf("ForKey: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil provider")
	}
}

func TestProviderRegistry_ForKey_Unknown(t *testing.T) {
	reg := typing.NewRegistry()
	pr := NewProviderRegistry(reg)

	_, err := pr.ForKey("unknown/v1/X")
	if err == nil {
		t.Fatal("expected error for unknown key")
	}
}

func TestProviderRegistry_All(t *testing.T) {
	reg := typing.NewRegistry()
	pr := NewProviderRegistry(reg)

	def1 := &stubProviderDef{key: "test/v1/A"}
	def2 := &stubProviderDef{key: "test/v1/B"}
	pr.Register("p1", &stubProvider{definitions: []typing.Definition{def1}})
	pr.Register("p2", &stubProvider{definitions: []typing.Definition{def2}})

	all := pr.All()
	if len(all) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(all))
	}
}

func TestProviderRegistry_Clear(t *testing.T) {
	reg := typing.NewRegistry()
	pr := NewProviderRegistry(reg)

	def := &stubProviderDef{key: "test/v1/Widget"}
	pr.Register("p1", &stubProvider{definitions: []typing.Definition{def}})
	pr.Clear()

	all := pr.All()
	if len(all) != 0 {
		t.Fatalf("expected 0 providers after Clear, got %d", len(all))
	}

	_, err := pr.ForKey("test/v1/Widget")
	if err == nil {
		t.Fatal("expected error after Clear")
	}
}

func TestProviderRegistry_DuplicatePanics(t *testing.T) {
	reg := typing.NewRegistry()
	pr := NewProviderRegistry(reg)

	def := &stubProviderDef{key: "test/v1/Widget"}
	pr.Register("p1", &stubProvider{definitions: []typing.Definition{def}})

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for duplicate provider")
		}
	}()
	pr.Register("p1", &stubProvider{})
}

func TestCheckReferences_NoRequests(t *testing.T) {
	d := NewDeployment()
	db := openTestDB(t)
	reg := typing.NewRegistry()
	pr := NewProviderRegistry(reg)

	ctx := &RequestContext{Registry: reg, DB: db, Providers: pr}
	errs := d.CheckReferences(ctx)
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors, got %d", len(errs))
	}
}

func TestCheckCreateReferences_DependencyInDeployment(t *testing.T) {
	d := NewDeployment()
	db := openTestDB(t)
	reg := typing.NewRegistry()
	pr := NewProviderRegistry(reg)

	depInst := &stubInstance{id: "dep-1", key: "test/v1/Dep"}
	mainInst := &stubInstance{id: "main-1", key: "test/v1/Main"}

	d.AddCreation(depInst, json.RawMessage(`{}`), nil)
	d.AddCreation(mainInst, json.RawMessage(`{}`), typing.ReferenceList{depInst})

	ctx := &RequestContext{Registry: reg, DB: db, Providers: pr}
	errs := d.CheckReferences(ctx)
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors (dep in deployment), got %d: %v", len(errs), errs)
	}
}

func TestCheckCreateReferences_MissingDependency(t *testing.T) {
	d := NewDeployment()
	db := openTestDB(t)
	reg := typing.NewRegistry()
	pr := NewProviderRegistry(reg)

	dep := typing.NewReference("dep-1", "test/v1/Dep")
	mainInst := &stubInstance{id: "main-1", key: "test/v1/Main"}

	d.AddCreation(mainInst, json.RawMessage(`{}`), typing.ReferenceList{dep})

	ctx := &RequestContext{Registry: reg, DB: db, Providers: pr}
	errs := d.CheckReferences(ctx)
	if len(errs) == 0 {
		t.Fatal("expected error for missing dependency")
	}
}

func TestCreateRequest_Process(t *testing.T) {
	db := openTestDB(t)
	reg := typing.NewRegistry()

	def := &processableDef{key: "test/v1/Widget"}
	reg.Register(def)

	body := json.RawMessage(`{}`)
	cr := &CreateRequest{
		SubjectId:  "w-1",
		SubjectKey: "test/v1/Widget",
		Subject:    body,
	}

	ctx := &RequestContext{Registry: reg, DB: db}
	err := cr.Process(ctx)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	// Verify rollout was saved
	rollout, err := LoadRollout(db, "w-1")
	if err != nil {
		t.Fatalf("LoadRollout: %v", err)
	}
	if rollout == nil {
		t.Fatal("expected rollout to be saved")
		return
	}
	if rollout.Status.Short != typing.RolloutStatusSuccess {
		t.Fatalf("expected SUCCESS, got %s", rollout.Status.Short)
	}
}

func TestDeleteRequest_Process(t *testing.T) {
	db := openTestDB(t)
	reg := typing.NewRegistry()

	def := &processableDef{key: "test/v1/Widget"}
	reg.Register(def)

	dr := &DeleteRequest{
		SubjectId:  "w-1",
		SubjectKey: "test/v1/Widget",
	}

	ctx := &RequestContext{Registry: reg, DB: db}
	err := dr.Process(ctx)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
}

func TestCreateRequest_GetStatus_NoRollout(t *testing.T) {
	db := openTestDB(t)
	cr := &CreateRequest{SubjectId: "nonexist", SubjectKey: "g/v1/K"}
	ctx := &RequestContext{DB: db}
	status, err := cr.GetStatus(ctx)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status != typing.RolloutStatusPending {
		t.Fatalf("expected PENDING, got %s", status)
	}
}

func TestDeleteRequest_GetStatus_NoRollout(t *testing.T) {
	db := openTestDB(t)
	dr := &DeleteRequest{SubjectId: "nonexist", SubjectKey: "g/v1/K"}
	ctx := &RequestContext{DB: db}
	status, err := dr.GetStatus(ctx)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status != typing.RolloutStatusSuccess {
		t.Fatalf("expected SUCCESS, got %s", status)
	}
}

func TestStatusRequest_Process(t *testing.T) {
	db := openTestDB(t)
	reg := typing.NewRegistry()

	def := &processableDef{key: "test/v1/Widget"}
	reg.Register(def)

	// First create a rollout to update
	rollout := &Rollout{
		InstanceId:  "w-1",
		InstanceKey: "test/v1/Widget",
		Status:      &RolloutStatus{Short: typing.RolloutStatusPending},
	}
	if err := db.Save(state.RolloutsById, rollout); err != nil {
		t.Fatalf("Save rollout: %v", err)
	}

	sr := &StatusRequest{
		SubjectId:  "w-1",
		SubjectKey: "test/v1/Widget",
	}

	pr := NewProviderRegistry(reg)
	ctx := &RequestContext{Registry: reg, DB: db, Providers: pr}
	err := sr.Process(ctx)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
}

// processableDef is a Definition that can be used for Process tests.
type processableDef struct {
	key typing.DefinitionKey
}

func (d *processableDef) GetKey() typing.DefinitionKey  { return d.key }
func (d *processableDef) GetMappings() []typing.Mapping { return nil }
func (d *processableDef) Instantiate(json.RawMessage) (typing.Instance, error) {
	return &stubInstance{id: "w-1", key: d.key}, nil
}

func (d *processableDef) Load(r json.RawMessage) (typing.Instance, error) {
	return d.Instantiate(r)
}
func (d *processableDef) Delete(typing.Reference) error { return nil }
func (d *processableDef) GetStatus(typing.Reference) (typing.Status, error) {
	return &RolloutStatus{Short: typing.RolloutStatusSuccess}, nil
}

func (d *processableDef) GetProviderStatus(typing.Reference) (typing.Status, error) {
	return &RolloutStatus{Short: typing.RolloutStatusSuccess}, nil
}

// referencingProvider is a Provider that returns predefined references from GetReferencesTo.
type referencingProvider struct {
	definitions []typing.Definition
	refs        []typing.Rollout
}

func (p *referencingProvider) GetReferencesTo(_ typing.Reference) ([]typing.Rollout, error) {
	return p.refs, nil
}
func (p *referencingProvider) Remove(_ typing.Reference) error { return nil }
func (p *referencingProvider) GetStatus(_ typing.Reference) (typing.Status, error) {
	return &RolloutStatus{Short: typing.RolloutStatusSuccess}, nil
}

func (p *referencingProvider) GetDefinitions() []typing.Definition {
	return p.definitions
}

func TestCheckDeleteReferences_NoReferences(t *testing.T) {
	db := openTestDB(t)
	reg := typing.NewRegistry()
	pr := NewProviderRegistry(reg)

	def := &stubProviderDef{key: "test/v1/Widget"}
	pr.Register("test", &stubProvider{definitions: []typing.Definition{def}})

	rollout := &Rollout{
		InstanceId:  "w-1",
		InstanceKey: "test/v1/Widget",
		Status:      &RolloutStatus{Short: typing.RolloutStatusSuccess},
	}
	if err := db.Save(state.RolloutsById, rollout); err != nil {
		t.Fatalf("Save: %v", err)
	}

	d := NewDeployment()
	d.AddDeletion(typing.NewReference("w-1", "test/v1/Widget"))

	ctx := &RequestContext{Registry: reg, DB: db, Providers: pr}
	errs := d.CheckReferences(ctx)
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors, got %d: %v", len(errs), errs)
	}
}

func TestCheckDeleteReferences_StillReferenced(t *testing.T) {
	db := openTestDB(t)
	reg := typing.NewRegistry()
	pr := NewProviderRegistry(reg)

	refProvider := &referencingProvider{
		definitions: []typing.Definition{&stubProviderDef{key: "test/v1/Widget"}},
		refs: []typing.Rollout{
			&Rollout{InstanceId: "other-1", InstanceKey: "test/v1/Widget"},
		},
	}
	pr.Register("test", refProvider)

	rollout := &Rollout{
		InstanceId:  "w-1",
		InstanceKey: "test/v1/Widget",
		Status:      &RolloutStatus{Short: typing.RolloutStatusSuccess},
	}
	if err := db.Save(state.RolloutsById, rollout); err != nil {
		t.Fatalf("Save: %v", err)
	}

	d := NewDeployment()
	d.AddDeletion(typing.NewReference("w-1", "test/v1/Widget"))

	ctx := &RequestContext{Registry: reg, DB: db, Providers: pr}
	errs := d.CheckReferences(ctx)
	if len(errs) == 0 {
		t.Fatal("expected errors for still-referenced resource")
	}
}

func TestCheckDeleteReferences_ReferencedButAlsoDeleted(t *testing.T) {
	db := openTestDB(t)
	reg := typing.NewRegistry()
	pr := NewProviderRegistry(reg)

	refProvider := &referencingProvider{
		definitions: []typing.Definition{&stubProviderDef{key: "test/v1/Widget"}},
		refs: []typing.Rollout{
			&Rollout{InstanceId: "other-1", InstanceKey: "test/v1/Widget"},
		},
	}
	pr.Register("test", refProvider)

	rollout := &Rollout{
		InstanceId:  "w-1",
		InstanceKey: "test/v1/Widget",
		Status:      &RolloutStatus{Short: typing.RolloutStatusSuccess},
	}
	if err := db.Save(state.RolloutsById, rollout); err != nil {
		t.Fatalf("Save: %v", err)
	}

	d := NewDeployment()
	d.AddDeletion(typing.NewReference("w-1", "test/v1/Widget"))
	d.AddDeletion(typing.NewReference("other-1", "test/v1/Widget"))

	ctx := &RequestContext{Registry: reg, DB: db, Providers: pr}
	errs := d.CheckReferences(ctx)
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors (both deleted), got %d: %v", len(errs), errs)
	}
}

func TestCheckCreateReferences_DependencyAlreadyDeployed(t *testing.T) {
	db := openTestDB(t)
	reg := typing.NewRegistry()
	pr := NewProviderRegistry(reg)

	// Save a rollout for the dependency.
	rollout := &Rollout{
		InstanceId:  "dep-1",
		InstanceKey: "test/v1/Dep",
		Status:      &RolloutStatus{Short: typing.RolloutStatusSuccess},
	}
	if err := db.Save(state.RolloutsById, rollout); err != nil {
		t.Fatalf("Save: %v", err)
	}

	d := NewDeployment()
	mainInst := &stubInstance{id: "main-1", key: "test/v1/Main"}
	dep := typing.NewReference("dep-1", "test/v1/Dep")
	d.AddCreation(mainInst, json.RawMessage(`{}`), typing.ReferenceList{dep})

	ctx := &RequestContext{Registry: reg, DB: db, Providers: pr}
	errs := d.CheckReferences(ctx)
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors (dep already deployed), got %d: %v", len(errs), errs)
	}
}

// failingApplyDef is a Definition whose instances always fail Apply.
type failingApplyDef struct {
	key typing.DefinitionKey
}

func (d *failingApplyDef) GetKey() typing.DefinitionKey  { return d.key }
func (d *failingApplyDef) GetMappings() []typing.Mapping { return nil }
func (d *failingApplyDef) Instantiate(json.RawMessage) (typing.Instance, error) {
	return &failingInstance{id: "w-1", key: d.key}, nil
}

func (d *failingApplyDef) Load(r json.RawMessage) (typing.Instance, error) {
	return d.Instantiate(r)
}
func (d *failingApplyDef) Delete(typing.Reference) error { return nil }
func (d *failingApplyDef) GetStatus(typing.Reference) (typing.Status, error) {
	return &RolloutStatus{Short: typing.RolloutStatusSuccess}, nil
}

func (d *failingApplyDef) GetProviderStatus(typing.Reference) (typing.Status, error) {
	return &RolloutStatus{Short: typing.RolloutStatusSuccess}, nil
}

// failingInstance is an Instance whose Apply always returns an error.
type failingInstance struct {
	id  string
	key typing.DefinitionKey
}

func (i *failingInstance) GetId() string                { return i.id }
func (i *failingInstance) GetKey() typing.DefinitionKey { return i.key }
func (i *failingInstance) String() string               { return i.id }
func (i *failingInstance) Apply() error                 { return fmt.Errorf("apply failed") }

func TestCreateRequest_Process_ApplyError(t *testing.T) {
	db := openTestDB(t)
	reg := typing.NewRegistry()

	def := &failingApplyDef{key: "test/v1/Widget"}
	reg.Register(def)

	cr := &CreateRequest{
		SubjectId:  "w-1",
		SubjectKey: "test/v1/Widget",
		Subject:    json.RawMessage(`{}`),
	}

	ctx := &RequestContext{Registry: reg, DB: db}
	err := cr.Process(ctx)
	if err == nil {
		t.Fatal("expected error from failed Apply")
	}

	rollout, lerr := LoadRollout(db, "w-1")
	if lerr != nil {
		t.Fatalf("LoadRollout: %v", lerr)
	}
	if rollout == nil {
		t.Fatal("expected rollout to be saved despite error")
		return
	}
	if rollout.Status.Short != typing.RolloutStatusError {
		t.Fatalf("expected ERROR status, got %s", rollout.Status.Short)
	}
	if len(rollout.Errors) == 0 {
		t.Fatal("expected error messages in rollout")
	}
}

func TestStatusRequest_Process_UnknownKey(t *testing.T) {
	db := openTestDB(t)
	reg := typing.NewRegistry()
	pr := NewProviderRegistry(reg)

	sr := &StatusRequest{
		SubjectId:  "w-1",
		SubjectKey: "unknown/v1/Widget",
	}

	ctx := &RequestContext{Registry: reg, DB: db, Providers: pr}
	err := sr.Process(ctx)
	if err == nil {
		t.Fatal("expected error for unknown key")
	}
}
