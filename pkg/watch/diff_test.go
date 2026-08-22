package watch

import (
	"testing"

	"github.com/systemstart/nix-compose/pkg/eval"
)

func TestDiffCompositions_Identical(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx"},
		},
	}
	diff, err := DiffCompositions(comp, comp)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if !diff.IsEmpty() {
		t.Errorf("expected empty diff, got added=%v removed=%v changed=%v",
			diff.Added, diff.Removed, diff.Changed)
	}
}

func TestDiffCompositions_Added(t *testing.T) {
	old := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx"},
		},
	}
	new := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx"},
			"api": {Image: "node"},
		},
	}

	diff, err := DiffCompositions(old, new)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if len(diff.Added) != 1 || diff.Added[0] != "api" {
		t.Errorf("added = %v, want [api]", diff.Added)
	}
	if len(diff.Removed) != 0 {
		t.Errorf("removed = %v, want []", diff.Removed)
	}
}

func TestDiffCompositions_Removed(t *testing.T) {
	old := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx"},
			"api": {Image: "node"},
		},
	}
	new := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx"},
		},
	}

	diff, err := DiffCompositions(old, new)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if len(diff.Removed) != 1 || diff.Removed[0] != "api" {
		t.Errorf("removed = %v, want [api]", diff.Removed)
	}
}

func TestDiffCompositions_Changed(t *testing.T) {
	old := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx:1.24"},
		},
	}
	new := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx:1.25"},
		},
	}

	diff, err := DiffCompositions(old, new)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if len(diff.Changed) != 1 || diff.Changed[0] != "web" {
		t.Errorf("changed = %v, want [web]", diff.Changed)
	}
}

func TestDiffCompositions_Mixed(t *testing.T) {
	old := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx:1.24"},
			"db":  {Image: "postgres:15"},
		},
	}
	new := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx:1.25"},
			"api": {Image: "node"},
		},
	}

	diff, err := DiffCompositions(old, new)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if len(diff.Added) != 1 || diff.Added[0] != "api" {
		t.Errorf("added = %v, want [api]", diff.Added)
	}
	if len(diff.Removed) != 1 || diff.Removed[0] != "db" {
		t.Errorf("removed = %v, want [db]", diff.Removed)
	}
	if len(diff.Changed) != 1 || diff.Changed[0] != "web" {
		t.Errorf("changed = %v, want [web]", diff.Changed)
	}
}

func TestDiffCompositions_EmptyOld(t *testing.T) {
	old := &eval.Composition{
		Services: map[string]eval.Service{},
	}
	new := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx"},
		},
	}

	diff, err := DiffCompositions(old, new)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if len(diff.Added) != 1 {
		t.Errorf("expected 1 added, got %d", len(diff.Added))
	}
}

func TestDiffCompositions_EmptyNew(t *testing.T) {
	old := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx"},
		},
	}
	new := &eval.Composition{
		Services: map[string]eval.Service{},
	}

	diff, err := DiffCompositions(old, new)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if len(diff.Removed) != 1 {
		t.Errorf("expected 1 removed, got %d", len(diff.Removed))
	}
}

func TestDiffResult_IsEmpty(t *testing.T) {
	d := &DiffResult{}
	if !d.IsEmpty() {
		t.Error("expected empty")
	}
	d.Added = []string{"web"}
	if d.IsEmpty() {
		t.Error("expected non-empty")
	}
}

func TestDiffCompositions_CommandChange(t *testing.T) {
	old := &eval.Composition{
		Services: map[string]eval.Service{
			"api": {
				Image:   "node",
				Command: eval.CommandValue{Parts: []string{"node", "server.js"}},
			},
		},
	}
	new := &eval.Composition{
		Services: map[string]eval.Service{
			"api": {
				Image:   "node",
				Command: eval.CommandValue{Parts: []string{"node", "app.js"}},
			},
		},
	}

	diff, err := DiffCompositions(old, new)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if len(diff.Changed) != 1 || diff.Changed[0] != "api" {
		t.Errorf("changed = %v, want [api]", diff.Changed)
	}
}
