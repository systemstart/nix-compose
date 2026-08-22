package depgraph

import (
	"reflect"
	"testing"

	"github.com/systemstart/nix-compose/pkg/eval"
)

func TestStartOrder_NoDeps(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web":   {Image: "nginx"},
			"api":   {Image: "node"},
			"cache": {Image: "redis"},
		},
	}

	levels, err := StartOrder(comp)
	if err != nil {
		t.Fatalf("StartOrder: %v", err)
	}
	if len(levels) != 1 {
		t.Fatalf("expected 1 level, got %d", len(levels))
	}
	if len(levels[0]) != 3 {
		t.Errorf("expected 3 services in level 0, got %d", len(levels[0]))
	}
}

func TestStartOrder_Chain(t *testing.T) {
	// a -> b -> c  ⟹  start order: [[c], [b], [a]]
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

	levels, err := StartOrder(comp)
	if err != nil {
		t.Fatalf("StartOrder: %v", err)
	}

	want := [][]string{{"c"}, {"b"}, {"a"}}
	if !reflect.DeepEqual(levels, want) {
		t.Errorf("StartOrder = %v, want %v", levels, want)
	}
}

func TestStartOrder_Diamond(t *testing.T) {
	// a -> {b, c} -> d  ⟹  [[d], [b, c], [a]]
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

	levels, err := StartOrder(comp)
	if err != nil {
		t.Fatalf("StartOrder: %v", err)
	}

	want := [][]string{{"d"}, {"b", "c"}, {"a"}}
	if !reflect.DeepEqual(levels, want) {
		t.Errorf("StartOrder = %v, want %v", levels, want)
	}
}

func TestStartOrder_SingleService(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx"},
		},
	}

	levels, err := StartOrder(comp)
	if err != nil {
		t.Fatalf("StartOrder: %v", err)
	}
	if len(levels) != 1 || len(levels[0]) != 1 || levels[0][0] != "web" {
		t.Errorf("expected [[web]], got %v", levels)
	}
}

func TestStartOrder_Cycle(t *testing.T) {
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

	_, err := StartOrder(comp)
	if err == nil {
		t.Fatal("expected error for cycle")
	}
}

func TestStopOrder_Chain(t *testing.T) {
	// a -> b -> c  ⟹  stop order: [[a], [b], [c]]  (reverse of start)
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

	levels, err := StopOrder(comp)
	if err != nil {
		t.Fatalf("StopOrder: %v", err)
	}

	want := [][]string{{"a"}, {"b"}, {"c"}}
	if !reflect.DeepEqual(levels, want) {
		t.Errorf("StopOrder = %v, want %v", levels, want)
	}
}
