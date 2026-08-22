package depgraph

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/systemstart/nix-compose/pkg/eval"
)

func testdataPath(name string) string {
	_, f, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(f), "..", "..", "testdata", name)
}

func loadFixture(t *testing.T, name string) *eval.Composition {
	t.Helper()
	data, err := os.ReadFile(testdataPath(name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	comp, err := eval.ParseComposition(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return comp
}

func TestValidate_NoDeps(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx"},
			"api": {Image: "node"},
		},
	}
	errs := Validate(comp)
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestValidate_ValidChain(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"a": {
				Image: "alpine",
				DependsOn: eval.DependsOnValue{
					Entries: map[string]eval.DependsOnEntry{
						"b": {Condition: "service_started"},
					},
				},
			},
			"b": {
				Image: "alpine",
				DependsOn: eval.DependsOnValue{
					Entries: map[string]eval.DependsOnEntry{
						"c": {Condition: "service_started"},
					},
				},
			},
			"c": {Image: "alpine"},
		},
	}
	errs := Validate(comp)
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestValidate_Diamond(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"a": {
				Image: "alpine",
				DependsOn: eval.DependsOnValue{
					Entries: map[string]eval.DependsOnEntry{
						"b": {Condition: "service_started"},
						"c": {Condition: "service_started"},
					},
				},
			},
			"b": {
				Image: "alpine",
				DependsOn: eval.DependsOnValue{
					Entries: map[string]eval.DependsOnEntry{
						"d": {Condition: "service_started"},
					},
				},
			},
			"c": {
				Image: "alpine",
				DependsOn: eval.DependsOnValue{
					Entries: map[string]eval.DependsOnEntry{
						"d": {Condition: "service_started"},
					},
				},
			},
			"d": {Image: "alpine"},
		},
	}
	errs := Validate(comp)
	if len(errs) != 0 {
		t.Errorf("expected no errors for diamond graph, got %v", errs)
	}
}

func TestValidate_MissingRef(t *testing.T) {
	comp := loadFixture(t, "missing-dep.json")
	errs := Validate(comp)
	if len(errs) == 0 {
		t.Fatal("expected errors for missing dependency")
	}
	found := false
	for _, err := range errs {
		if strings.Contains(err.Error(), "nonexistent") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error mentioning 'nonexistent', got %v", errs)
	}
}

func TestValidate_DirectCycle(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"a": {
				Image: "alpine",
				DependsOn: eval.DependsOnValue{
					Entries: map[string]eval.DependsOnEntry{
						"b": {Condition: "service_started"},
					},
				},
			},
			"b": {
				Image: "alpine",
				DependsOn: eval.DependsOnValue{
					Entries: map[string]eval.DependsOnEntry{
						"a": {Condition: "service_started"},
					},
				},
			},
		},
	}
	errs := Validate(comp)
	if len(errs) == 0 {
		t.Fatal("expected cycle error")
	}
	found := false
	for _, err := range errs {
		if strings.Contains(err.Error(), "cycle") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'cycle' in error, got %v", errs)
	}
}

func TestValidate_SelfCycle(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"a": {
				Image: "alpine",
				DependsOn: eval.DependsOnValue{
					Entries: map[string]eval.DependsOnEntry{
						"a": {Condition: "service_started"},
					},
				},
			},
		},
	}
	errs := Validate(comp)
	if len(errs) == 0 {
		t.Fatal("expected cycle error for self-dependency")
	}
}

func TestValidate_IndirectCycle(t *testing.T) {
	comp := loadFixture(t, "cycle.json")
	errs := Validate(comp)
	if len(errs) == 0 {
		t.Fatal("expected cycle error")
	}
	found := false
	for _, err := range errs {
		if strings.Contains(err.Error(), "cycle") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'cycle' in error, got %v", errs)
	}
}

func TestValidate_EmptyComposition(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{},
	}
	errs := Validate(comp)
	if len(errs) != 0 {
		t.Errorf("expected no errors for empty composition, got %v", errs)
	}
}
