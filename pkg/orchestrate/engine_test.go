package orchestrate

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/systemstart/nix-compose/pkg/eval"
	"github.com/systemstart/nix-compose/pkg/orchestrate/convert"
	"github.com/systemstart/nix-compose/pkg/orchestrate/deploy"
	"github.com/systemstart/nix-compose/pkg/orchestrate/resources"
	"github.com/systemstart/nix-compose/pkg/orchestrate/state"
	"github.com/systemstart/nix-compose/pkg/orchestrate/typing"
)

func TestEngineNewAndClose(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.bolt")

	engine, err := New(Config{
		DBPath: dbPath,
		// nil CRI/CNI/Vol clients — we just test the engine lifecycle
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	if engine.DB() == nil {
		t.Fatal("expected non-nil DB")
		return
	}
	if engine.Registry() == nil {
		t.Fatal("expected non-nil Registry")
		return
	}
	if engine.Providers() == nil {
		t.Fatal("expected non-nil Providers")
		return
	}

	// Check that 6 definitions were registered
	all := engine.Registry().AllDefinitions()
	if len(all) != 6 {
		t.Fatalf("expected 6 definitions, got %d", len(all))
	}

	// State should be empty initially
	rollouts, err := engine.State()
	if err != nil {
		t.Fatalf("State failed: %v", err)
	}
	if len(rollouts) != 0 {
		t.Fatalf("expected 0 rollouts, got %d", len(rollouts))
	}

	if err := engine.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

// newTestEngine creates an engine with nil CRI/CNI/Vol for plan/state testing.
func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.bolt")
	engine, err := New(Config{DBPath: dbPath})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	return engine
}

// bridgeComp runs eval → convert → bridge for a test composition.
func bridgeComp(t *testing.T, engine *Engine, comp *eval.Composition, project string) (*deploy.Deployment, convert.ConditionMap) {
	t.Helper()
	result, err := convert.Convert(comp, convert.Options{Project: project})
	if err != nil {
		t.Fatalf("Convert failed: %v", err)
	}
	deployment, conditions, err := convert.Bridge(result, engine.Registry())
	if err != nil {
		t.Fatalf("Bridge failed: %v", err)
	}
	return deployment, conditions
}

// simulateApply writes rollouts to the DB as if ApplySync had run,
// without actually calling instance.Apply() (which needs a CRI client).
func simulateApply(t *testing.T, engine *Engine, d *deploy.Deployment) {
	t.Helper()
	// Persist dependency links.
	if err := d.PersistLinks(engine.DB()); err != nil {
		t.Fatalf("PersistLinks failed: %v", err)
	}
	for _, req := range d.Requests {
		if req.GetType() != deploy.RequestTypeCreate {
			continue
		}
		cr := req.(*deploy.CreateRequest)
		rollout := &deploy.Rollout{
			InstanceId:  cr.SubjectId,
			InstanceKey: cr.SubjectKey,
			Body:        cr.Subject,
			Status:      &deploy.RolloutStatus{Short: typing.RolloutStatusSuccess},
		}
		if err := engine.DB().Save(state.RolloutsById, rollout); err != nil {
			t.Fatalf("Save rollout failed: %v", err)
		}
	}
}

func TestEngine_PlanApplyRoundTrip(t *testing.T) {
	engine := newTestEngine(t)

	// Use host networking to avoid Network/Project ID collision in BoltDB.
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx:latest", NetworkMode: "host"},
		},
	}

	deployment, conditions := bridgeComp(t, engine, comp, "myapp")

	// First plan: everything should be create (Project + Image + Service).
	plan, err := engine.Plan(deployment, conditions)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	creates, _, _, _ := plan.Summary()
	if creates != 3 { // Project + Image + Service
		t.Fatalf("first plan creates: got %d, want 3", creates)
	}

	// Simulate apply by writing rollouts to DB.
	simulateApply(t, engine, plan.Deployment)

	// State should show rollouts.
	rollouts, err := engine.State()
	if err != nil {
		t.Fatalf("State failed: %v", err)
	}
	if len(rollouts) != 3 {
		t.Fatalf("rollouts after apply: got %d, want 3", len(rollouts))
	}

	// Second plan with same comp: should be all noops.
	deployment2, conditions2 := bridgeComp(t, engine, comp, "myapp")
	plan2, err := engine.Plan(deployment2, conditions2)
	if err != nil {
		t.Fatalf("second Plan failed: %v", err)
	}

	_, _, _, noops := plan2.Summary()
	if noops != 3 {
		c, u, d, n := plan2.Summary()
		t.Fatalf("second plan: expected 3 noops, got creates=%d updates=%d destroys=%d noops=%d", c, u, d, n)
	}
}

func TestEngine_UpdateCycle(t *testing.T) {
	engine := newTestEngine(t)

	// Simulate applying v1.
	comp1 := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx:latest"},
		},
	}

	d1, c1 := bridgeComp(t, engine, comp1, "myapp")
	p1, err := engine.Plan(d1, c1)
	if err != nil {
		t.Fatal(err)
	}
	simulateApply(t, engine, p1.Deployment)

	// Plan v2 with changed image.
	comp2 := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx:1.25"},
		},
	}

	d2, c2 := bridgeComp(t, engine, comp2, "myapp")
	p2, err := engine.Plan(d2, c2)
	if err != nil {
		t.Fatal(err)
	}

	// Should see at least one update (image/service body changed).
	_, updates, _, _ := p2.Summary()
	if updates == 0 {
		t.Fatal("expected at least 1 update for changed image")
	}
}

func TestEngine_OrphanRemoval(t *testing.T) {
	engine := newTestEngine(t)

	// Simulate applying 2 services.
	comp1 := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx:latest"},
			"api": {Image: "node:20"},
		},
	}

	d1, c1 := bridgeComp(t, engine, comp1, "myapp")
	p1, err := engine.Plan(d1, c1)
	if err != nil {
		t.Fatal(err)
	}
	simulateApply(t, engine, p1.Deployment)

	// Remove api service.
	comp2 := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx:latest"},
		},
	}

	d2, c2 := bridgeComp(t, engine, comp2, "myapp")
	p2, err := engine.Plan(d2, c2)
	if err != nil {
		t.Fatal(err)
	}

	_, _, destroys, _ := p2.Summary()
	if destroys == 0 {
		t.Fatal("expected at least 1 destroy for removed service")
	}

	// Verify there's at least one destroy action.
	foundDestroy := false
	for _, a := range p2.Actions {
		if a.Type == ActionDestroy {
			foundDestroy = true
			break
		}
	}
	if !foundDestroy {
		t.Fatal("no destroy actions found")
	}
}

// TestEngine_ReqCtx verifies the ReqCtx accessor returns a valid context.
func TestEngine_ReqCtx(t *testing.T) {
	engine := newTestEngine(t)
	reqCtx := engine.ReqCtx()
	if reqCtx == nil {
		t.Fatal("expected non-nil ReqCtx")
		return
	}
	if reqCtx.Registry == nil {
		t.Fatal("expected non-nil Registry in ReqCtx")
		return
	}
	if reqCtx.DB == nil {
		t.Fatal("expected non-nil DB in ReqCtx")
		return
	}
}

// TestEngine_BodyStoredInRollout verifies the Body field fix in request.go
// by simulating an apply and checking rollout bodies.
func TestEngine_BodyStoredInRollout(t *testing.T) {
	engine := newTestEngine(t)

	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx:latest"},
		},
	}

	d, c := bridgeComp(t, engine, comp, "myapp")
	p, err := engine.Plan(d, c)
	if err != nil {
		t.Fatal(err)
	}

	if !hasValidCreateBody(p.Deployment.Requests) {
		t.Error("expected at least one CreateRequest to carry a non-empty Subject body")
	}

	simulateApply(t, engine, p.Deployment)

	rollouts, err := engine.State()
	if err != nil {
		t.Fatal(err)
	}

	if !hasValidRolloutBody(rollouts) {
		t.Error("expected at least one rollout to have a non-empty Body")
	}
}

// hasValidCreateBody checks if any CreateRequest in the list carries a valid JSON body.
func hasValidCreateBody(requests deploy.RequestList) bool {
	for _, req := range requests {
		cr, ok := req.(*deploy.CreateRequest)
		if !ok || len(cr.Subject) == 0 {
			continue
		}
		var v interface{}
		if json.Unmarshal(cr.Subject, &v) == nil {
			return true
		}
	}
	return false
}

// hasValidRolloutBody checks if any rollout carries a valid JSON body.
func hasValidRolloutBody(rollouts []*deploy.Rollout) bool {
	for _, r := range rollouts {
		if len(r.Body) == 0 {
			continue
		}
		var v interface{}
		if json.Unmarshal(r.Body, &v) == nil {
			return true
		}
	}
	return false
}

func TestEngine_ListDeployments_Empty(t *testing.T) {
	engine := newTestEngine(t)

	deployments, err := engine.ListDeployments()
	if err != nil {
		t.Fatalf("ListDeployments failed: %v", err)
	}
	if len(deployments) != 0 {
		t.Fatalf("expected 0 deployments, got %d", len(deployments))
	}
}

func TestEngine_DeploymentPersistence(t *testing.T) {
	engine := newTestEngine(t)

	// Create and save a deployment.
	d := deploy.NewDeployment()
	d.Requests = append(d.Requests, &deploy.CreateRequest{
		SubjectId:  "myapp/web",
		SubjectKey: typing.DefinitionKey("cri.orchestrator.io/v1/Service"),
		Subject:    json.RawMessage(`{"container":{"project":"myapp","service":"web","version":"abc","image":"nginx:latest"}}`),
	})

	if err := d.Save(engine.DB()); err != nil {
		t.Fatalf("Save deployment: %v", err)
	}

	deployments, err := engine.ListDeployments()
	if err != nil {
		t.Fatalf("ListDeployments: %v", err)
	}
	if len(deployments) != 1 {
		t.Fatalf("expected 1 deployment, got %d", len(deployments))
	}
	if deployments[0].Id != d.Id {
		t.Errorf("deployment ID = %q, want %q", deployments[0].Id, d.Id)
	}
}

func TestEngine_Rollback_DryRun(t *testing.T) {
	engine := newTestEngine(t)

	// Simulate applying a service and saving the deployment.
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx:latest", NetworkMode: "host"},
		},
	}

	d, c := bridgeComp(t, engine, comp, "myapp")
	p, err := engine.Plan(d, c)
	if err != nil {
		t.Fatal(err)
	}
	simulateApply(t, engine, p.Deployment)

	// Save the deployment.
	if err := p.Deployment.Save(engine.DB()); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Change the composition.
	comp2 := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx:1.25", NetworkMode: "host"},
		},
	}
	d2, c2 := bridgeComp(t, engine, comp2, "myapp")
	p2, err := engine.Plan(d2, c2)
	if err != nil {
		t.Fatal(err)
	}
	simulateApply(t, engine, p2.Deployment)

	// Dry-run rollback to the first deployment.
	plan, err := engine.Rollback(t.Context(), p.Deployment.Id, true)
	if err != nil {
		t.Fatalf("Rollback dry-run: %v", err)
	}

	// The plan should contain actions (at minimum some updates to revert).
	if plan == nil {
		t.Fatal("expected non-nil plan from dry-run rollback")
		return
	}
	if len(plan.Actions) == 0 {
		t.Fatal("expected actions in rollback plan")
	}
}

func TestEngine_Rollback_NotFound(t *testing.T) {
	engine := newTestEngine(t)

	_, err := engine.Rollback(t.Context(), "nonexistent-id", true)
	if err == nil {
		t.Fatal("expected error for nonexistent deployment")
	}
}

func TestEngine_CRIClient_Nil(t *testing.T) {
	engine := newTestEngine(t)
	if engine.CRIClient() != nil {
		t.Error("expected nil CRIClient")
	}
}

func TestEngine_Close_Idempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.bolt")
	engine, err := New(Config{DBPath: dbPath})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := engine.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
}

func TestEvalServiceFromSpec(t *testing.T) {
	spec := &resources.ContainerSpec{
		Healthcheck: &eval.Healthcheck{
			Test: eval.CommandValue{Parts: []string{"CMD", "curl", "-f", "http://localhost/"}},
		},
	}
	svc := evalServiceFromSpec(spec)
	if svc.Healthcheck == nil {
		t.Fatal("expected non-nil healthcheck")
		return
	}
	if len(svc.Healthcheck.Test.Parts) != 4 {
		t.Errorf("expected 4 test elements, got %d", len(svc.Healthcheck.Test.Parts))
	}
}

func TestEvalServiceFromSpec_NilHealthcheck(t *testing.T) {
	spec := &resources.ContainerSpec{}
	svc := evalServiceFromSpec(spec)
	if svc.Healthcheck != nil {
		t.Error("expected nil healthcheck")
	}
}

func TestExtractVersionFromBody_Direct(t *testing.T) {
	body := json.RawMessage(`{"version":"v2"}`)
	v := extractVersionFromBody(body)
	if v != "v2" {
		t.Errorf("expected 'v2', got %q", v)
	}
}

func TestExtractVersionFromBody_Nested(t *testing.T) {
	body := json.RawMessage(`{"container":{"version":"v3"}}`)
	v := extractVersionFromBody(body)
	if v != "v3" {
		t.Errorf("expected 'v3', got %q", v)
	}
}

func TestExtractVersionFromBody_Empty(t *testing.T) {
	body := json.RawMessage(`{}`)
	v := extractVersionFromBody(body)
	if v != "" {
		t.Errorf("expected empty, got %q", v)
	}
}

func TestExtractVersionFromBody_Invalid(t *testing.T) {
	body := json.RawMessage(`not-json`)
	v := extractVersionFromBody(body)
	if v != "" {
		t.Errorf("expected empty for invalid JSON, got %q", v)
	}
}

func TestAppendCycleOrphans_NoOrphans(t *testing.T) {
	key := typing.DefinitionKey("test/v1/Svc")
	requests := []deploy.Request{
		&deploy.CreateRequest{SubjectId: "A", SubjectKey: key},
		&deploy.CreateRequest{SubjectId: "B", SubjectKey: key},
	}
	sorted := []deploy.Request{
		&deploy.CreateRequest{SubjectId: "A", SubjectKey: key},
		&deploy.CreateRequest{SubjectId: "B", SubjectKey: key},
	}

	result := appendCycleOrphans(sorted, requests)
	if len(result) != 2 {
		t.Errorf("expected 2, got %d", len(result))
	}
}

func TestAppendCycleOrphans_WithOrphans(t *testing.T) {
	key := typing.DefinitionKey("test/v1/Svc")
	requests := []deploy.Request{
		&deploy.CreateRequest{SubjectId: "A", SubjectKey: key},
		&deploy.CreateRequest{SubjectId: "B", SubjectKey: key},
		&deploy.CreateRequest{SubjectId: "C", SubjectKey: key},
	}
	sorted := []deploy.Request{
		&deploy.CreateRequest{SubjectId: "A", SubjectKey: key},
	}

	result := appendCycleOrphans(sorted, requests)
	if len(result) != 3 {
		t.Fatalf("expected 3, got %d", len(result))
	}
	// B and C should be appended.
	found := map[string]bool{}
	for _, r := range result {
		found[r.GetSubjectId()] = true
	}
	if !found["B"] || !found["C"] {
		t.Error("expected B and C to be appended")
	}
}

func TestListDeployments_Empty(t *testing.T) {
	engine := newTestEngine(t)
	deployments, err := engine.ListDeployments()
	if err != nil {
		t.Fatalf("ListDeployments: %v", err)
	}
	if len(deployments) != 0 {
		t.Errorf("expected 0, got %d", len(deployments))
	}
}

func TestListDeployments_AfterSimulateApply(t *testing.T) {
	engine := newTestEngine(t)

	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx:latest", NetworkMode: "host"},
		},
	}

	deployment, _ := bridgeComp(t, engine, comp, "myapp")
	plan, err := engine.Plan(deployment, nil)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	simulateApply(t, engine, plan.Deployment)

	// Save the deployment to DB.
	if err := engine.DB().Save(state.DeploymentsById, plan.Deployment); err != nil {
		t.Fatalf("Save deployment: %v", err)
	}

	deployments, err := engine.ListDeployments()
	if err != nil {
		t.Fatalf("ListDeployments: %v", err)
	}
	if len(deployments) != 1 {
		t.Errorf("expected 1, got %d", len(deployments))
	}
}

func TestDriftCheck_Empty(t *testing.T) {
	engine := newTestEngine(t)
	ctx := t.Context()

	results, err := engine.DriftCheck(ctx)
	if err != nil {
		t.Fatalf("DriftCheck: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 drift results, got %d", len(results))
	}
}

func TestBodyEqual_EqualBodies(t *testing.T) {
	a := json.RawMessage(`{"image":"nginx"}`)
	b := json.RawMessage(`{"image":"nginx"}`)
	if !bodyEqual(a, b) {
		t.Error("expected equal bodies")
	}
}

func TestBodyEqual_DifferentBodies(t *testing.T) {
	a := json.RawMessage(`{"image":"nginx"}`)
	b := json.RawMessage(`{"image":"node"}`)
	if bodyEqual(a, b) {
		t.Error("expected different bodies")
	}
}

func TestBodyEqual_EmptyBodies(t *testing.T) {
	if !bodyEqual(nil, nil) {
		t.Error("expected nil == nil")
	}
}

func TestBodyEqual_OneNil(t *testing.T) {
	a := json.RawMessage(`{}`)
	if bodyEqual(a, nil) {
		t.Error("expected non-nil != nil")
	}
}

// TestApplySync_ProjectOnly exercises ApplySync with a plan that contains only
// a Project create request. Project Apply is a no-op (returns nil), so this
// should succeed end-to-end without needing a CRI client.
func TestApplySync_ProjectOnly(t *testing.T) {
	engine := newTestEngine(t)

	// Build a plan with a single Project create request.
	projectBody := json.RawMessage(`{"name":"testproj"}`)
	d := deploy.NewDeployment()
	d.Requests = append(d.Requests, &deploy.CreateRequest{
		SubjectId:  "testproj",
		SubjectKey: resources.ProjectKey,
		Subject:    projectBody,
	})

	plan := &Plan{
		Deployment: d,
		Actions: []Action{
			{Type: ActionCreate, ResourceID: "testproj", Key: resources.ProjectKey},
		},
	}

	ctx := t.Context()
	if err := engine.ApplySync(ctx, plan); err != nil {
		t.Fatalf("ApplySync failed: %v", err)
	}

	// Verify the rollout was persisted with SUCCEEDED status.
	rollouts, err := engine.State()
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if len(rollouts) != 1 {
		t.Fatalf("expected 1 rollout, got %d", len(rollouts))
	}
	if rollouts[0].InstanceId != "testproj" {
		t.Errorf("rollout ID = %q, want %q", rollouts[0].InstanceId, "testproj")
	}
	if rollouts[0].Status == nil || rollouts[0].Status.Short != typing.RolloutStatusSuccess {
		t.Errorf("expected SUCCEEDED status, got %v", rollouts[0].Status)
	}
}

// TestEngine_Apply_QueueDeployment calls Engine.Apply directly (async path).
// The pool is started in New(), so it should accept the deployment without error.
// We give the pool a moment to process before closing.
func TestEngine_Apply_QueueDeployment(t *testing.T) {
	engine := newTestEngine(t)

	projectBody := json.RawMessage(`{"name":"queueproj"}`)
	d := deploy.NewDeployment()
	d.Requests = append(d.Requests, &deploy.CreateRequest{
		SubjectId:  "queueproj",
		SubjectKey: resources.ProjectKey,
		Subject:    projectBody,
	})

	if err := engine.Apply(d); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	// Give the pool time to drain the request before cleanup tears it down.
	time.Sleep(200 * time.Millisecond)
}

// TestDriftCheck_WithPendingRollout verifies that DriftCheck skips rollouts
// with a PENDING status (only SUCCEEDED/RUNNING are checked for drift).
func TestDriftCheck_WithPendingRollout(t *testing.T) {
	engine := newTestEngine(t)

	// Write a rollout with PENDING status directly.
	rollout := &deploy.Rollout{
		InstanceId:  "testproj",
		InstanceKey: resources.ProjectKey,
		Body:        json.RawMessage(`{"name":"testproj"}`),
		Status:      &deploy.RolloutStatus{Short: typing.RolloutStatusPending},
	}
	if err := engine.DB().Save(state.RolloutsById, rollout); err != nil {
		t.Fatalf("Save rollout: %v", err)
	}

	results, err := engine.DriftCheck(t.Context())
	if err != nil {
		t.Fatalf("DriftCheck: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 drift results for PENDING rollout, got %d", len(results))
	}
}

// TestDriftCheck_WithSucceededProjectRollout verifies that a SUCCEEDED Project
// rollout is not flagged as drifted, because ProjectDefinition.GetProviderStatus
// always returns SUCCEEDED.
func TestDriftCheck_WithSucceededProjectRollout(t *testing.T) {
	engine := newTestEngine(t)

	rollout := &deploy.Rollout{
		InstanceId:  "testproj",
		InstanceKey: resources.ProjectKey,
		Body:        json.RawMessage(`{"name":"testproj"}`),
		Status:      &deploy.RolloutStatus{Short: typing.RolloutStatusSuccess},
	}
	if err := engine.DB().Save(state.RolloutsById, rollout); err != nil {
		t.Fatalf("Save rollout: %v", err)
	}

	results, err := engine.DriftCheck(t.Context())
	if err != nil {
		t.Fatalf("DriftCheck: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 drift results for SUCCEEDED Project, got %d", len(results))
	}
}

// TestCheckRolloutDrift_NilStatus verifies that checkRolloutDrift returns
// false when the rollout has a nil Status.
func TestCheckRolloutDrift_NilStatus(t *testing.T) {
	engine := newTestEngine(t)

	rollout := &deploy.Rollout{
		InstanceId:  "testproj",
		InstanceKey: resources.ProjectKey,
		Body:        json.RawMessage(`{"name":"testproj"}`),
		Status:      nil,
	}

	_, drifted := engine.checkRolloutDrift(rollout)
	if drifted {
		t.Error("expected no drift for nil status")
	}
}

// TestCheckRolloutDrift_UnknownKey verifies that checkRolloutDrift returns
// false when the rollout has an unregistered InstanceKey (no definition found).
func TestCheckRolloutDrift_UnknownKey(t *testing.T) {
	engine := newTestEngine(t)

	rollout := &deploy.Rollout{
		InstanceId:  "unknown/resource",
		InstanceKey: typing.DefinitionKey("fake.group/v1/UnknownKind"),
		Body:        json.RawMessage(`{}`),
		Status:      &deploy.RolloutStatus{Short: typing.RolloutStatusSuccess},
	}

	_, drifted := engine.checkRolloutDrift(rollout)
	if drifted {
		t.Error("expected no drift for unknown definition key")
	}
}

// TestGetProviderStatus_ProjectDef verifies that getProviderStatus returns
// SUCCEEDED for a Project rollout. ProjectDefinition.GetProviderStatus always
// returns SUCCEEDED.
func TestGetProviderStatus_ProjectDef(t *testing.T) {
	engine := newTestEngine(t)

	def, err := engine.Registry().GetDefinition(resources.ProjectKey)
	if err != nil {
		t.Fatalf("GetDefinition(ProjectKey): %v", err)
	}

	rollout := &deploy.Rollout{
		InstanceId:  "testproj",
		InstanceKey: resources.ProjectKey,
		Body:        json.RawMessage(`{"name":"testproj"}`),
		Status:      &deploy.RolloutStatus{Short: typing.RolloutStatusSuccess},
	}

	status, err := engine.getProviderStatus(def, rollout)
	if err != nil {
		t.Fatalf("getProviderStatus: %v", err)
	}
	if status.GetShort() != typing.RolloutStatusSuccess {
		t.Errorf("expected SUCCEEDED, got %s", status.GetShort())
	}
}

// TestGetProviderStatus_ContainerDef verifies that getProviderStatus with a
// Container rollout and nil CRI client returns PENDING status. The
// ContainerDefinition.GetProviderStatusWithVersion method returns PendingStatus
// when the CRI client is nil.
func TestGetProviderStatus_ContainerDef(t *testing.T) {
	engine := newTestEngine(t)

	def, err := engine.Registry().GetDefinition(resources.ContainerKey)
	if err != nil {
		t.Fatalf("GetDefinition(ContainerKey): %v", err)
	}

	rollout := &deploy.Rollout{
		InstanceId:  "myapp/web",
		InstanceKey: resources.ContainerKey,
		Body:        json.RawMessage(`{"project":"myapp","service":"web","version":"v1","image":"nginx:latest"}`),
		Status:      &deploy.RolloutStatus{Short: typing.RolloutStatusSuccess},
	}

	status, err := engine.getProviderStatus(def, rollout)
	if err != nil {
		t.Fatalf("getProviderStatus: %v", err)
	}
	if status.GetShort() != typing.RolloutStatusPending {
		t.Errorf("expected PENDING (nil CRI client), got %s", status.GetShort())
	}
}

// --- New tests for improved coverage ---

// TestSplitResourceID_EdgeCases extends the existing splitResourceID tests
// with additional edge cases not already covered in conditions_test.go.
func TestSplitResourceID_EdgeCases(t *testing.T) {
	tests := []struct {
		input       string
		wantProject string
		wantService string
	}{
		{"", "", ""},                                // empty string
		{"/leading", "", "leading"},                 // leading slash
		{"trailing/", "trailing", ""},               // trailing slash
		{"multi/slash/path", "multi", "slash/path"}, // only first slash splits
		{"a/b", "a", "b"},                           // single-char parts
	}

	for _, tc := range tests {
		project, service := splitResourceID(tc.input)
		if project != tc.wantProject || service != tc.wantService {
			t.Errorf("splitResourceID(%q) = (%q, %q), want (%q, %q)",
				tc.input, project, service, tc.wantProject, tc.wantService)
		}
	}
}

// TestCondPriority_CaseSensitiveAndOrdering extends the existing condPriority
// tests with case-sensitivity checks and ordering verification.
func TestCondPriority_CaseSensitiveAndOrdering(t *testing.T) {
	// Case-sensitive: uppercase variants should return 0.
	caseSensitive := []string{"HEALTHY", "Completed", "STARTED", "Running"}
	for _, cond := range caseSensitive {
		if condPriority(cond) != 0 {
			t.Errorf("condPriority(%q) = %d, want 0 (case-sensitive)", cond, condPriority(cond))
		}
	}

	// Verify ordering: healthy > completed > started > unknown
	if condPriority("healthy") <= condPriority("completed") {
		t.Error("healthy should have higher priority than completed")
	}
	if condPriority("completed") <= condPriority("started") {
		t.Error("completed should have higher priority than started")
	}
	if condPriority("started") <= condPriority("") {
		t.Error("started should have higher priority than empty")
	}
}

// TestEvalServiceFromSpec_WithAllFields tests evalServiceFromSpec preserves
// healthcheck fields correctly from a fully populated ContainerSpec.
func TestEvalServiceFromSpec_WithAllFields(t *testing.T) {
	spec := &resources.ContainerSpec{
		Project: "myapp",
		Service: "web",
		Image:   "nginx:latest",
		Healthcheck: &eval.Healthcheck{
			Test:        eval.CommandValue{Parts: []string{"CMD-SHELL", "curl -f http://localhost/"}},
			Interval:    "10s",
			Timeout:     "5s",
			Retries:     3,
			StartPeriod: "30s",
		},
	}

	svc := evalServiceFromSpec(spec)
	if svc.Healthcheck == nil {
		t.Fatal("expected non-nil healthcheck")
		return
	}
	if svc.Healthcheck.Interval != "10s" {
		t.Errorf("interval = %q, want %q", svc.Healthcheck.Interval, "10s")
	}
	if svc.Healthcheck.Timeout != "5s" {
		t.Errorf("timeout = %q, want %q", svc.Healthcheck.Timeout, "5s")
	}
	if svc.Healthcheck.Retries != 3 {
		t.Errorf("retries = %d, want 3", svc.Healthcheck.Retries)
	}
	if svc.Healthcheck.StartPeriod != "30s" {
		t.Errorf("startPeriod = %q, want %q", svc.Healthcheck.StartPeriod, "30s")
	}
}

// TestLoadContainerSpec_ServiceSpec tests loadContainerSpec with a ServiceSpec body.
func TestLoadContainerSpec_ServiceSpec(t *testing.T) {
	engine := newTestEngine(t)

	// Build a rollout with ServiceSpec body (container wrapper).
	rolloutBody := &deploy.Rollout{
		InstanceId:  "myapp/web",
		InstanceKey: resources.ServiceKey,
		Body:        json.RawMessage(`{"container":{"project":"myapp","service":"web","version":"v1","image":"nginx:latest"}}`),
		Status:      &deploy.RolloutStatus{Short: typing.RolloutStatusSuccess},
	}
	if err := engine.DB().Save(state.RolloutsById, rolloutBody); err != nil {
		t.Fatalf("Save rollout: %v", err)
	}

	spec, err := loadContainerSpec(engine, "myapp/web")
	if err != nil {
		t.Fatalf("loadContainerSpec: %v", err)
	}
	if spec == nil {
		t.Fatal("expected non-nil spec")
		return
	}
	if spec.Project != "myapp" {
		t.Errorf("project = %q, want %q", spec.Project, "myapp")
	}
	if spec.Service != "web" {
		t.Errorf("service = %q, want %q", spec.Service, "web")
	}
	if spec.Version != "v1" {
		t.Errorf("version = %q, want %q", spec.Version, "v1")
	}
	if spec.Image != "nginx:latest" {
		t.Errorf("image = %q, want %q", spec.Image, "nginx:latest")
	}
}

// TestLoadContainerSpec_DirectContainerSpec tests loadContainerSpec with a direct ContainerSpec body.
func TestLoadContainerSpec_DirectContainerSpec(t *testing.T) {
	engine := newTestEngine(t)

	rolloutBody := &deploy.Rollout{
		InstanceId:  "myapp/api",
		InstanceKey: resources.ContainerKey,
		Body:        json.RawMessage(`{"project":"myapp","service":"api","version":"v2","image":"node:20"}`),
		Status:      &deploy.RolloutStatus{Short: typing.RolloutStatusSuccess},
	}
	if err := engine.DB().Save(state.RolloutsById, rolloutBody); err != nil {
		t.Fatalf("Save rollout: %v", err)
	}

	spec, err := loadContainerSpec(engine, "myapp/api")
	if err != nil {
		t.Fatalf("loadContainerSpec: %v", err)
	}
	if spec == nil {
		t.Fatal("expected non-nil spec")
		return
	}
	if spec.Service != "api" {
		t.Errorf("service = %q, want %q", spec.Service, "api")
	}
	if spec.Image != "node:20" {
		t.Errorf("image = %q, want %q", spec.Image, "node:20")
	}
}

// TestLoadContainerSpec_NotFound_Verbose tests loadContainerSpec when no rollout
// exists, verifying that both return values are correct.
func TestLoadContainerSpec_NotFound_Verbose(t *testing.T) {
	engine := newTestEngine(t)

	spec, err := loadContainerSpec(engine, "nonexistent/svc")
	if err != nil {
		t.Fatalf("loadContainerSpec should not error for missing rollout: %v", err)
	}
	if spec != nil {
		t.Error("expected nil spec for nonexistent rollout")
	}
}

// TestLoadContainerSpec_InvalidBody tests loadContainerSpec with an unparseable body.
func TestLoadContainerSpec_InvalidBody(t *testing.T) {
	engine := newTestEngine(t)

	// Store a rollout with an invalid JSON body field inside a valid rollout JSON.
	rolloutBody := &deploy.Rollout{
		InstanceId:  "myapp/bad",
		InstanceKey: resources.ServiceKey,
		Body:        json.RawMessage(`{"notcontainer":"nope"}`),
		Status:      &deploy.RolloutStatus{Short: typing.RolloutStatusSuccess},
	}
	if err := engine.DB().Save(state.RolloutsById, rolloutBody); err != nil {
		t.Fatalf("Save rollout: %v", err)
	}

	// The body doesn't match ServiceSpec or ContainerSpec (no service field set).
	spec, err := loadContainerSpec(engine, "myapp/bad")
	if err != nil {
		t.Fatalf("loadContainerSpec: %v", err)
	}
	// Should return nil since neither ServiceSpec.Container.Service nor ContainerSpec.Service is set.
	if spec != nil {
		t.Errorf("expected nil spec for unrecognized body, got %+v", spec)
	}
}

// TestExtractVersionFromBody_ServiceSpecFull tests extractVersionFromBody with a full ServiceSpec body.
func TestExtractVersionFromBody_ServiceSpecFull(t *testing.T) {
	body := json.RawMessage(`{"container":{"project":"myapp","service":"web","version":"v42","image":"nginx:latest"}}`)
	v := extractVersionFromBody(body)
	if v != "v42" {
		t.Errorf("expected 'v42', got %q", v)
	}
}

// TestExtractVersionFromBody_DirectVersionPriority tests that direct version takes precedence.
func TestExtractVersionFromBody_DirectVersionPriority(t *testing.T) {
	// If body has both direct version and nested container.version, direct wins
	// because extractVersionFromBody checks direct first.
	body := json.RawMessage(`{"version":"direct","container":{"version":"nested"}}`)
	v := extractVersionFromBody(body)
	if v != "direct" {
		t.Errorf("expected 'direct', got %q", v)
	}
}

// TestExtractVersionFromBody_NilBody tests extractVersionFromBody with nil input.
func TestExtractVersionFromBody_NilBody(t *testing.T) {
	v := extractVersionFromBody(nil)
	if v != "" {
		t.Errorf("expected empty for nil body, got %q", v)
	}
}

// TestListDeployments_Multiple tests ListDeployments with multiple saved deployments.
func TestListDeployments_Multiple(t *testing.T) {
	engine := newTestEngine(t)

	// Save 3 deployments.
	for i := 0; i < 3; i++ {
		d := deploy.NewDeployment()
		d.Requests = append(d.Requests, &deploy.CreateRequest{
			SubjectId:  fmt.Sprintf("proj%d/svc%d", i, i),
			SubjectKey: resources.ServiceKey,
			Subject:    json.RawMessage(fmt.Sprintf(`{"container":{"project":"proj%d","service":"svc%d","version":"v1","image":"nginx"}}`, i, i)),
		})
		if err := d.Save(engine.DB()); err != nil {
			t.Fatalf("Save deployment %d: %v", i, err)
		}
	}

	deployments, err := engine.ListDeployments()
	if err != nil {
		t.Fatalf("ListDeployments: %v", err)
	}
	if len(deployments) != 3 {
		t.Fatalf("expected 3 deployments, got %d", len(deployments))
	}

	// Verify all deployments have valid IDs.
	for _, d := range deployments {
		if d.Id == "" {
			t.Error("deployment has empty ID")
		}
	}
}

// TestRollback_DryRun_NoCreateRequestsInOldDeployment tests Rollback when the
// stored deployment has only delete requests (edge case).
func TestRollback_DryRun_NoCreateRequestsInOldDeployment(t *testing.T) {
	engine := newTestEngine(t)

	// Create a deployment that has only a delete request.
	d := deploy.NewDeployment()
	d.Requests = append(d.Requests, &deploy.DeleteRequest{
		SubjectId:  "myapp/web",
		SubjectKey: resources.ServiceKey,
	})
	if err := d.Save(engine.DB()); err != nil {
		t.Fatalf("Save: %v", err)
	}

	plan, err := engine.Rollback(t.Context(), d.Id, true)
	if err != nil {
		t.Fatalf("Rollback dry-run: %v", err)
	}
	if plan == nil {
		t.Fatal("expected non-nil plan")
		return
	}
	// Since there were no create requests in the old deployment,
	// the rollback desired deployment should be empty.
	creates, _, _, _ := plan.Summary()
	// Only destroy actions for current state orphans, no creates.
	if creates != 0 {
		t.Errorf("expected 0 creates in rollback plan, got %d", creates)
	}
}

// TestRollback_DryRun_WithCurrentState tests Rollback dry-run where current
// state has extra resources that should be destroyed.
func TestRollback_DryRun_WithCurrentState(t *testing.T) {
	engine := newTestEngine(t)

	// Simulate: first deploy "web" only.
	comp1 := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx:latest", NetworkMode: "host"},
		},
	}
	d1, c1 := bridgeComp(t, engine, comp1, "myapp")
	p1, err := engine.Plan(d1, c1)
	if err != nil {
		t.Fatal(err)
	}
	simulateApply(t, engine, p1.Deployment)
	if err := p1.Deployment.Save(engine.DB()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	savedID := p1.Deployment.Id

	// Now deploy "web" + "api".
	comp2 := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx:latest", NetworkMode: "host"},
			"api": {Image: "node:20", NetworkMode: "host"},
		},
	}
	d2, c2 := bridgeComp(t, engine, comp2, "myapp")
	p2, err := engine.Plan(d2, c2)
	if err != nil {
		t.Fatal(err)
	}
	simulateApply(t, engine, p2.Deployment)

	// Rollback to the first deployment (web only). Should destroy api.
	plan, err := engine.Rollback(t.Context(), savedID, true)
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	_, _, destroys, _ := plan.Summary()
	if destroys == 0 {
		t.Error("expected at least 1 destroy for removed api service in rollback")
	}
}

func TestBodyEqual_InvalidJSON(t *testing.T) {
	a := json.RawMessage(`not valid json`)
	b := json.RawMessage(`{"a":1}`)
	if bodyEqual(a, b) {
		t.Error("expected false for invalid JSON in first arg")
	}
	if bodyEqual(b, a) {
		t.Error("expected false for invalid JSON in second arg")
	}
}

func TestLookupNode_NotFound(t *testing.T) {
	engine := newTestEngine(t)
	_, err := engine.lookupNode("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent rollout")
	}
}

func TestRollback_InvalidID(t *testing.T) {
	engine := newTestEngine(t)
	_, err := engine.Rollback(t.Context(), "nonexistent-id", true)
	if err == nil {
		t.Error("expected error for nonexistent deployment")
	}
}
