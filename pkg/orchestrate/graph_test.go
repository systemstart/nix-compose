package orchestrate

import (
	"encoding/json"
	"testing"

	"github.com/systemstart/nix-compose/pkg/eval"
	"github.com/systemstart/nix-compose/pkg/orchestrate/deploy"
	"github.com/systemstart/nix-compose/pkg/orchestrate/state"
	"github.com/systemstart/nix-compose/pkg/orchestrate/typing"
)

func TestGraph_Empty(t *testing.T) {
	engine := newTestEngine(t)

	nodes, edges, err := engine.Graph()
	if err != nil {
		t.Fatalf("Graph: %v", err)
	}
	if len(nodes) != 0 {
		t.Errorf("expected 0 nodes, got %d", len(nodes))
	}
	if len(edges) != 0 {
		t.Errorf("expected 0 edges, got %d", len(edges))
	}
}

func TestGraph_WithRollouts(t *testing.T) {
	engine := newTestEngine(t)

	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx:latest", DependsOn: eval.DependsOnValue{
				Entries: map[string]eval.DependsOnEntry{
					"api": {Condition: "service_started"},
				},
			}},
			"api": {Image: "node:20"},
		},
	}

	d, _ := bridgeComp(t, engine, comp, "myapp")
	simulateApply(t, engine, d)

	nodes, edges, err := engine.Graph()
	if err != nil {
		t.Fatalf("Graph: %v", err)
	}

	if len(nodes) == 0 {
		t.Fatal("expected nodes, got 0")
	}

	if len(edges) == 0 {
		t.Fatal("expected edges, got 0")
	}

	// Verify we have nodes for the expected resources.
	nodeIDs := make(map[string]bool)
	for _, n := range nodes {
		nodeIDs[n.ID] = true
	}
	// Should have project, images, network, and services.
	if !nodeIDs["myapp"] {
		t.Error("expected project node 'myapp'")
	}
}

func TestTransitiveDeps_SingleLevel(t *testing.T) {
	engine := newTestEngine(t)

	// web depends on image + project (implicit deps from convert).
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx:latest", NetworkMode: "host"},
		},
	}

	d, _ := bridgeComp(t, engine, comp, "myapp")
	simulateApply(t, engine, d)

	// Service depends on image and project.
	deps, err := engine.TransitiveDeps("myapp/web")
	if err != nil {
		t.Fatalf("TransitiveDeps: %v", err)
	}

	if len(deps) == 0 {
		t.Fatal("expected transitive deps, got 0")
	}

	depIDs := make(map[string]bool)
	for _, d := range deps {
		depIDs[d.ID] = true
	}
	if !depIDs["myapp"] {
		t.Error("expected project 'myapp' in transitive deps")
	}
}

func TestTransitiveDeps_MultiLevel(t *testing.T) {
	engine := newTestEngine(t)

	// Manually create a chain: A → B → C.
	addTestLink(t, engine, "A", "B")
	addTestLink(t, engine, "B", "C")

	// Add rollouts for B and C so they can be looked up.
	addTestRollout(t, engine, "A", "test/v1/Service")
	addTestRollout(t, engine, "B", "test/v1/Service")
	addTestRollout(t, engine, "C", "test/v1/Service")

	deps, err := engine.TransitiveDeps("A")
	if err != nil {
		t.Fatalf("TransitiveDeps: %v", err)
	}

	depIDs := make(map[string]bool)
	for _, d := range deps {
		depIDs[d.ID] = true
	}

	if !depIDs["B"] {
		t.Error("expected B in transitive deps of A")
	}
	if !depIDs["C"] {
		t.Error("expected C in transitive deps of A")
	}
	if len(deps) != 2 {
		t.Errorf("expected 2 transitive deps, got %d", len(deps))
	}
}

func TestTransitiveDeps_NotFound(t *testing.T) {
	engine := newTestEngine(t)

	deps, err := engine.TransitiveDeps("nonexistent")
	if err != nil {
		t.Fatalf("TransitiveDeps: %v", err)
	}
	if len(deps) != 0 {
		t.Errorf("expected 0 deps for nonexistent resource, got %d", len(deps))
	}
}

func TestTransitiveDependents_SingleLevel(t *testing.T) {
	engine := newTestEngine(t)

	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx:latest", NetworkMode: "host"},
		},
	}

	d, _ := bridgeComp(t, engine, comp, "myapp")
	simulateApply(t, engine, d)

	// Project should have web as a dependent.
	dependents, err := engine.TransitiveDependents("myapp")
	if err != nil {
		t.Fatalf("TransitiveDependents: %v", err)
	}

	if len(dependents) == 0 {
		t.Fatal("expected dependents for project 'myapp', got 0")
	}

	foundWeb := false
	for _, dep := range dependents {
		if dep.ID == "myapp/web" {
			foundWeb = true
		}
	}
	if !foundWeb {
		t.Error("expected 'myapp/web' in dependents of 'myapp'")
	}
}

func TestTransitiveDependents_MultiLevel(t *testing.T) {
	engine := newTestEngine(t)

	// Chain: A → B → C. C's dependents should include B and A.
	addTestLink(t, engine, "A", "B")
	addTestLink(t, engine, "B", "C")

	addTestRollout(t, engine, "A", "test/v1/Service")
	addTestRollout(t, engine, "B", "test/v1/Service")
	addTestRollout(t, engine, "C", "test/v1/Service")

	dependents, err := engine.TransitiveDependents("C")
	if err != nil {
		t.Fatalf("TransitiveDependents: %v", err)
	}

	depIDs := make(map[string]bool)
	for _, d := range dependents {
		depIDs[d.ID] = true
	}

	if !depIDs["B"] {
		t.Error("expected B in transitive dependents of C")
	}
	if !depIDs["A"] {
		t.Error("expected A in transitive dependents of C")
	}
	if len(dependents) != 2 {
		t.Errorf("expected 2 transitive dependents, got %d", len(dependents))
	}
}

func TestTransitiveDeps_CycleProtection(t *testing.T) {
	engine := newTestEngine(t)

	// Create a cycle: A → B → C → A.
	addTestLink(t, engine, "A", "B")
	addTestLink(t, engine, "B", "C")
	addTestLink(t, engine, "C", "A")

	addTestRollout(t, engine, "A", "test/v1/Service")
	addTestRollout(t, engine, "B", "test/v1/Service")
	addTestRollout(t, engine, "C", "test/v1/Service")

	// Should not hang — cycle is handled by visited set.
	deps, err := engine.TransitiveDeps("A")
	if err != nil {
		t.Fatalf("TransitiveDeps: %v", err)
	}

	// Should find B and C (but not A again).
	if len(deps) != 2 {
		t.Errorf("expected 2 transitive deps despite cycle, got %d", len(deps))
	}

	depIDs := make(map[string]bool)
	for _, d := range deps {
		depIDs[d.ID] = true
	}
	if !depIDs["B"] || !depIDs["C"] {
		t.Error("expected B and C in cycle traversal")
	}
}

func TestRolloutToNode(t *testing.T) {
	key := typing.CreateDefinitionKey("cri.orchestrator.io", "v1", "Service")
	node := kindFromKey(key)
	if node != "Service" {
		t.Errorf("expected Kind 'Service', got %q", node)
	}
}

// addTestLink adds a dependency link: source depends on target.
func addTestLink(t *testing.T, engine *Engine, sourceID, targetID string) {
	t.Helper()
	link := state.NewLink(
		typing.NewReference(sourceID, ""),
		typing.NewReference(targetID, ""),
	)
	if err := engine.DB().AddLink(link); err != nil {
		t.Fatalf("AddLink %s → %s: %v", sourceID, targetID, err)
	}
}

// addTestRollout writes a minimal rollout to the DB.
func addTestRollout(t *testing.T, engine *Engine, id string, key string) {
	t.Helper()
	rollout := &deploy.Rollout{
		InstanceId:  id,
		InstanceKey: typing.DefinitionKey(key),
		Body:        json.RawMessage(`{}`),
		Status:      &deploy.RolloutStatus{Short: typing.RolloutStatusSuccess},
	}
	if err := engine.DB().Save(state.RolloutsById, rollout); err != nil {
		t.Fatalf("Save rollout %s: %v", id, err)
	}
}
