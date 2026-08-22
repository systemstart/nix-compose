package orchestrate

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/systemstart/nix-compose/pkg/orchestrate/deploy"
	"github.com/systemstart/nix-compose/pkg/orchestrate/resources"
	"github.com/systemstart/nix-compose/pkg/orchestrate/state"
	"github.com/systemstart/nix-compose/pkg/orchestrate/typing"
)

// chainFixture builds a common A→B→C chain for topo sort tests.
func chainFixture() ([]deploy.Request, []*state.Link) {
	key := typing.DefinitionKey("test/v1/Svc")
	requests := []deploy.Request{
		&deploy.CreateRequest{SubjectId: "A", SubjectKey: key},
		&deploy.CreateRequest{SubjectId: "B", SubjectKey: key},
		&deploy.CreateRequest{SubjectId: "C", SubjectKey: key},
	}

	refA := typing.NewReference("A", key)
	refB := typing.NewReference("B", key)
	refC := typing.NewReference("C", key)

	links := []*state.Link{
		state.NewLink(refA, refB), // A depends on B
		state.NewLink(refB, refC), // B depends on C
	}

	return requests, links
}

func TestTopoSort_Chain(t *testing.T) {
	// A depends on B, B depends on C.
	// Forward order: C, B, A.
	requests, links := chainFixture()

	sorted := topoSortRequests(requests, links, false)
	if len(sorted) != 3 {
		t.Fatalf("sorted length: got %d, want 3", len(sorted))
	}

	// C must come before B, B must come before A.
	idxC := indexOf(sorted, "C")
	idxB := indexOf(sorted, "B")
	idxA := indexOf(sorted, "A")

	if idxC > idxB {
		t.Errorf("C (idx=%d) should come before B (idx=%d)", idxC, idxB)
	}
	if idxB > idxA {
		t.Errorf("B (idx=%d) should come before A (idx=%d)", idxB, idxA)
	}
}

func TestTopoSort_Reverse(t *testing.T) {
	requests, links := chainFixture()

	sorted := topoSortRequests(requests, links, true)
	if len(sorted) != 3 {
		t.Fatalf("sorted length: got %d, want 3", len(sorted))
	}

	// Reverse: A, B, C.
	idxA := indexOf(sorted, "A")
	idxB := indexOf(sorted, "B")
	idxC := indexOf(sorted, "C")

	if idxA > idxB {
		t.Errorf("A (idx=%d) should come before B (idx=%d) in reverse", idxA, idxB)
	}
	if idxB > idxC {
		t.Errorf("B (idx=%d) should come before C (idx=%d) in reverse", idxB, idxC)
	}
}

func TestTopoSort_NoDeps(t *testing.T) {
	key := typing.DefinitionKey("test/v1/Svc")
	requests := []deploy.Request{
		&deploy.CreateRequest{SubjectId: "c", SubjectKey: key},
		&deploy.CreateRequest{SubjectId: "a", SubjectKey: key},
		&deploy.CreateRequest{SubjectId: "b", SubjectKey: key},
	}

	sorted := topoSortRequests(requests, nil, false)
	if len(sorted) != 3 {
		t.Fatalf("sorted length: got %d, want 3", len(sorted))
	}

	// With no deps, should be sorted alphabetically by ID.
	if sorted[0].GetSubjectId() != "a" {
		t.Errorf("sorted[0] = %q, want %q", sorted[0].GetSubjectId(), "a")
	}
	if sorted[1].GetSubjectId() != "b" {
		t.Errorf("sorted[1] = %q, want %q", sorted[1].GetSubjectId(), "b")
	}
	if sorted[2].GetSubjectId() != "c" {
		t.Errorf("sorted[2] = %q, want %q", sorted[2].GetSubjectId(), "c")
	}
}

func TestTopoSort_Empty(t *testing.T) {
	sorted := topoSortRequests(nil, nil, false)
	if sorted != nil {
		t.Fatalf("expected nil for empty input, got %v", sorted)
	}
}

func TestSplitRequests(t *testing.T) {
	key := typing.DefinitionKey("test/v1/Svc")
	requests := deploy.RequestList{
		&deploy.CreateRequest{SubjectId: "c1", SubjectKey: key},
		&deploy.DeleteRequest{SubjectId: "d1", SubjectKey: key},
		&deploy.CreateRequest{SubjectId: "c2", SubjectKey: key},
		&deploy.DeleteRequest{SubjectId: "d2", SubjectKey: key},
		&deploy.StatusRequest{SubjectId: "s1", SubjectKey: key},
	}

	deletes, creates := splitRequests(requests)
	if len(deletes) != 2 {
		t.Fatalf("expected 2 deletes, got %d", len(deletes))
	}
	if len(creates) != 2 {
		t.Fatalf("expected 2 creates, got %d", len(creates))
	}
	if deletes[0].GetSubjectId() != "d1" {
		t.Errorf("deletes[0] = %q, want d1", deletes[0].GetSubjectId())
	}
	if creates[0].GetSubjectId() != "c1" {
		t.Errorf("creates[0] = %q, want c1", creates[0].GetSubjectId())
	}
}

func TestSplitRequests_Empty(t *testing.T) {
	deletes, creates := splitRequests(nil)
	if len(deletes) != 0 || len(creates) != 0 {
		t.Fatalf("expected empty results, got %d deletes, %d creates", len(deletes), len(creates))
	}
}

func TestSplitRequests_OnlyCreates(t *testing.T) {
	key := typing.DefinitionKey("test/v1/Svc")
	requests := deploy.RequestList{
		&deploy.CreateRequest{SubjectId: "c1", SubjectKey: key},
	}
	deletes, creates := splitRequests(requests)
	if len(deletes) != 0 {
		t.Fatalf("expected 0 deletes, got %d", len(deletes))
	}
	if len(creates) != 1 {
		t.Fatalf("expected 1 create, got %d", len(creates))
	}
}

func TestExecuteDeletes_Success(t *testing.T) {
	engine := newTestEngine(t)
	ctx := t.Context()

	key := typing.DefinitionKey("cri.orchestrator.io/v1/Service")
	sorted := []deploy.Request{
		&deploy.DeleteRequest{SubjectId: "s1", SubjectKey: key},
	}

	err := executeDeletes(ctx, sorted, engine.ReqCtx())
	if err != nil {
		t.Fatalf("executeDeletes: %v", err)
	}
}

func TestExecuteDeletes_ContextCancelled(t *testing.T) {
	engine := newTestEngine(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	key := typing.DefinitionKey("cri.orchestrator.io/v1/Service")
	sorted := []deploy.Request{
		&deploy.DeleteRequest{SubjectId: "s1", SubjectKey: key},
	}

	err := executeDeletes(ctx, sorted, engine.ReqCtx())
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestRollback_Empty(t *testing.T) {
	engine := newTestEngine(t)
	// Rollback with empty applied list should be a no-op.
	rollback(nil, engine.ReqCtx())
}

func TestLoadLinks_Empty(t *testing.T) {
	engine := newTestEngine(t)
	links, err := engine.loadLinks()
	if err != nil {
		t.Fatalf("loadLinks: %v", err)
	}
	if len(links) != 0 {
		t.Fatalf("expected 0 links, got %d", len(links))
	}
}

func TestLoadLinks_WithLinks(t *testing.T) {
	engine := newTestEngine(t)

	// Add some links to DB.
	refA := typing.NewReference("a", "test/v1/A")
	refB := typing.NewReference("b", "test/v1/B")
	if err := engine.DB().AddLink(state.NewLink(refA, refB)); err != nil {
		t.Fatalf("AddLink: %v", err)
	}

	links, err := engine.loadLinks()
	if err != nil {
		t.Fatalf("loadLinks: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("expected 1 link, got %d", len(links))
	}
}

func TestExecuteCreates_Empty(t *testing.T) {
	engine := newTestEngine(t)
	ctx := t.Context()

	// Empty slice should succeed.
	err := executeCreates(ctx, nil, engine.ReqCtx(), engine, nil)
	if err != nil {
		t.Fatalf("executeCreates: %v", err)
	}
}

func TestExecuteCreates_ContextCancelled(t *testing.T) {
	engine := newTestEngine(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	key := typing.DefinitionKey("cri.orchestrator.io/v1/Project")
	sorted := []deploy.Request{
		&deploy.CreateRequest{SubjectId: "s1", SubjectKey: key, Subject: []byte(`{"project":"s1"}`)},
	}

	err := executeCreates(ctx, sorted, engine.ReqCtx(), engine, nil)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestExecuteCreates_ProjectOnly(t *testing.T) {
	engine := newTestEngine(t)
	ctx := t.Context()

	// Project type Apply is a no-op (returns nil), so this should succeed.
	key := typing.DefinitionKey("cri.orchestrator.io/v1/Project")
	sorted := []deploy.Request{
		&deploy.CreateRequest{SubjectId: "myapp", SubjectKey: key, Subject: []byte(`{"name":"myapp"}`)},
	}

	err := executeCreates(ctx, sorted, engine.ReqCtx(), engine, nil)
	if err != nil {
		t.Fatalf("executeCreates: %v", err)
	}
}

func TestRollback_WithRequests(t *testing.T) {
	engine := newTestEngine(t)
	key := typing.DefinitionKey("cri.orchestrator.io/v1/Service")
	applied := []deploy.Request{
		&deploy.CreateRequest{SubjectId: "s1", SubjectKey: key},
		&deploy.CreateRequest{SubjectId: "s2", SubjectKey: key},
	}

	// Rollback should attempt to delete in reverse order.
	// Should not panic even if delete fails.
	rollback(applied, engine.ReqCtx())
}

// --- helpers ---

func indexOf(requests []deploy.Request, id string) int {
	for i, r := range requests {
		if r.GetSubjectId() == id {
			return i
		}
	}
	return -1
}

// --- New tests for improved coverage ---

// TestBuildGraph_LinksOutsideBatch verifies that buildGraph ignores links where
// either source or target is not in the request batch.
func TestBuildGraph_LinksOutsideBatch(t *testing.T) {
	key := typing.DefinitionKey("test/v1/Svc")
	reqSet := map[string]deploy.Request{
		"A": &deploy.CreateRequest{SubjectId: "A", SubjectKey: key},
		"B": &deploy.CreateRequest{SubjectId: "B", SubjectKey: key},
	}

	// Links reference "C" which is not in the batch.
	links := []*state.Link{
		state.NewLink(
			typing.NewReference("A", key),
			typing.NewReference("C", key), // C not in batch
		),
		state.NewLink(
			typing.NewReference("D", key), // D not in batch
			typing.NewReference("B", key),
		),
	}

	inDegree, adjacency := buildGraph(reqSet, links)

	// Both A and B should have in-degree 0 since links outside the batch are ignored.
	if inDegree["A"] != 0 {
		t.Errorf("inDegree[A] = %d, want 0", inDegree["A"])
	}
	if inDegree["B"] != 0 {
		t.Errorf("inDegree[B] = %d, want 0", inDegree["B"])
	}
	// Adjacency should be empty.
	if len(adjacency) != 0 {
		t.Errorf("expected empty adjacency, got %v", adjacency)
	}
}

// TestBuildGraph_WithBatchLinks verifies that buildGraph correctly records
// in-degree and adjacency for links within the batch.
func TestBuildGraph_WithBatchLinks(t *testing.T) {
	key := typing.DefinitionKey("test/v1/Svc")
	reqSet := map[string]deploy.Request{
		"A": &deploy.CreateRequest{SubjectId: "A", SubjectKey: key},
		"B": &deploy.CreateRequest{SubjectId: "B", SubjectKey: key},
		"C": &deploy.CreateRequest{SubjectId: "C", SubjectKey: key},
	}

	// A depends on B, B depends on C — all within the batch.
	links := []*state.Link{
		state.NewLink(
			typing.NewReference("A", key),
			typing.NewReference("B", key),
		),
		state.NewLink(
			typing.NewReference("B", key),
			typing.NewReference("C", key),
		),
	}

	inDegree, adjacency := buildGraph(reqSet, links)

	// A has in-degree 1 (depends on B), B has in-degree 1 (depends on C), C has in-degree 0.
	if inDegree["A"] != 1 {
		t.Errorf("inDegree[A] = %d, want 1", inDegree["A"])
	}
	if inDegree["B"] != 1 {
		t.Errorf("inDegree[B] = %d, want 1", inDegree["B"])
	}
	if inDegree["C"] != 0 {
		t.Errorf("inDegree[C] = %d, want 0", inDegree["C"])
	}
	// Adjacency: B -> [A], C -> [B]
	if len(adjacency["B"]) != 1 || adjacency["B"][0] != "A" {
		t.Errorf("adjacency[B] = %v, want [A]", adjacency["B"])
	}
	if len(adjacency["C"]) != 1 || adjacency["C"][0] != "B" {
		t.Errorf("adjacency[C] = %v, want [B]", adjacency["C"])
	}
}

// TestBuildGraph_NoLinks verifies that buildGraph with empty links initializes
// all in-degrees to 0.
func TestBuildGraph_NoLinks(t *testing.T) {
	key := typing.DefinitionKey("test/v1/Svc")
	reqSet := map[string]deploy.Request{
		"X": &deploy.CreateRequest{SubjectId: "X", SubjectKey: key},
		"Y": &deploy.CreateRequest{SubjectId: "Y", SubjectKey: key},
	}

	inDegree, adjacency := buildGraph(reqSet, nil)

	if inDegree["X"] != 0 {
		t.Errorf("inDegree[X] = %d, want 0", inDegree["X"])
	}
	if inDegree["Y"] != 0 {
		t.Errorf("inDegree[Y] = %d, want 0", inDegree["Y"])
	}
	if len(adjacency) != 0 {
		t.Errorf("expected empty adjacency, got %v", adjacency)
	}
}

// TestBuildGraph_MixedInsideOutsideLinks verifies that buildGraph handles a mix
// of links inside and outside the batch correctly.
func TestBuildGraph_MixedInsideOutsideLinks(t *testing.T) {
	key := typing.DefinitionKey("test/v1/Svc")
	reqSet := map[string]deploy.Request{
		"A": &deploy.CreateRequest{SubjectId: "A", SubjectKey: key},
		"B": &deploy.CreateRequest{SubjectId: "B", SubjectKey: key},
	}

	links := []*state.Link{
		// This link is within the batch.
		state.NewLink(
			typing.NewReference("A", key),
			typing.NewReference("B", key),
		),
		// Source outside batch.
		state.NewLink(
			typing.NewReference("X", key),
			typing.NewReference("B", key),
		),
		// Target outside batch.
		state.NewLink(
			typing.NewReference("A", key),
			typing.NewReference("Y", key),
		),
	}

	inDegree, adjacency := buildGraph(reqSet, links)

	// Only the A->B link should be counted.
	if inDegree["A"] != 1 {
		t.Errorf("inDegree[A] = %d, want 1", inDegree["A"])
	}
	if inDegree["B"] != 0 {
		t.Errorf("inDegree[B] = %d, want 0", inDegree["B"])
	}
	if len(adjacency["B"]) != 1 {
		t.Errorf("adjacency[B] = %v, want [A]", adjacency["B"])
	}
}

// TestApplySync_ReferenceCheckFail verifies that ApplySync returns an error when
// CheckReferences fails (e.g., a create request references a dependency that
// does not exist and is not being created in the same deployment).
func TestApplySync_ReferenceCheckFail(t *testing.T) {
	engine := newTestEngine(t)

	// Build a deployment with a create request that has a missing dependency.
	d := deploy.NewDeployment()
	serviceBody := json.RawMessage(`{"container":{"project":"myapp","service":"web","version":"v1","image":"nginx:latest"}}`)
	d.Requests = append(d.Requests, &deploy.CreateRequest{
		SubjectId:  "myapp/web",
		SubjectKey: resources.ServiceKey,
		Subject:    serviceBody,
	})
	// Add a dependency on a resource that does not exist.
	d.Dependencies = []*state.Link{
		state.NewLink(
			typing.NewReference("myapp/web", resources.ServiceKey),
			typing.NewReference("myapp/nonexistent", resources.ServiceKey),
		),
	}

	plan := &Plan{
		Deployment: d,
		Actions: []Action{
			{Type: ActionCreate, ResourceID: "myapp/web", Key: resources.ServiceKey},
		},
	}

	err := engine.ApplySync(t.Context(), plan)
	if err == nil {
		t.Fatal("expected error for missing dependency reference")
	}
}

// TestApplySync_MultipleProjects tests ApplySync with multiple project create
// requests that should all succeed (Project Apply is a no-op).
func TestApplySync_MultipleProjects(t *testing.T) {
	engine := newTestEngine(t)

	d := deploy.NewDeployment()
	for _, name := range []string{"proj1", "proj2", "proj3"} {
		d.Requests = append(d.Requests, &deploy.CreateRequest{
			SubjectId:  name,
			SubjectKey: resources.ProjectKey,
			Subject:    json.RawMessage(fmt.Sprintf(`{"name":"%s"}`, name)),
		})
	}

	plan := &Plan{
		Deployment: d,
		Actions: []Action{
			{Type: ActionCreate, ResourceID: "proj1", Key: resources.ProjectKey},
			{Type: ActionCreate, ResourceID: "proj2", Key: resources.ProjectKey},
			{Type: ActionCreate, ResourceID: "proj3", Key: resources.ProjectKey},
		},
	}

	if err := engine.ApplySync(t.Context(), plan); err != nil {
		t.Fatalf("ApplySync failed: %v", err)
	}

	rollouts, err := engine.State()
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if len(rollouts) != 3 {
		t.Fatalf("expected 3 rollouts, got %d", len(rollouts))
	}
}

// TestApplySync_DeleteAndCreate tests ApplySync with both delete and create
// requests (update scenario).
func TestApplySync_DeleteAndCreate(t *testing.T) {
	engine := newTestEngine(t)
	ctx := t.Context()

	// First, create a project.
	d1 := deploy.NewDeployment()
	d1.Requests = append(d1.Requests, &deploy.CreateRequest{
		SubjectId:  "myproj",
		SubjectKey: resources.ProjectKey,
		Subject:    json.RawMessage(`{"name":"myproj"}`),
	})
	plan1 := &Plan{Deployment: d1}
	if err := engine.ApplySync(ctx, plan1); err != nil {
		t.Fatalf("first ApplySync: %v", err)
	}

	// Now "update" by deleting and re-creating.
	d2 := deploy.NewDeployment()
	d2.Requests = append(d2.Requests,
		&deploy.DeleteRequest{
			SubjectId:  "myproj",
			SubjectKey: resources.ProjectKey,
		},
		&deploy.CreateRequest{
			SubjectId:  "myproj",
			SubjectKey: resources.ProjectKey,
			Subject:    json.RawMessage(`{"name":"myproj"}`),
		},
	)
	plan2 := &Plan{Deployment: d2}
	if err := engine.ApplySync(ctx, plan2); err != nil {
		t.Fatalf("second ApplySync: %v", err)
	}

	rollouts, err := engine.State()
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if len(rollouts) != 1 {
		t.Fatalf("expected 1 rollout, got %d", len(rollouts))
	}
}
