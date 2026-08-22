package convert

import (
	"testing"

	"github.com/systemstart/nix-compose/pkg/eval"
	"github.com/systemstart/nix-compose/pkg/orchestrate/deploy"
	"github.com/systemstart/nix-compose/pkg/orchestrate/manifest"
	"github.com/systemstart/nix-compose/pkg/orchestrate/resources"
	"github.com/systemstart/nix-compose/pkg/orchestrate/typing"
)

func testRegistry() *typing.Registry {
	reg := typing.NewRegistry()
	reg.Register(&resources.ProjectDefinition{})
	reg.Register(&resources.NetworkDefinition{})
	reg.Register(&resources.ImageDefinition{})
	reg.Register(&resources.VolumeDefinition{})
	reg.Register(&resources.ContainerDefinition{})
	reg.Register(&resources.ServiceDefinition{})
	return reg
}

func TestBridge_Minimal(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx:latest"},
		},
	}
	result, err := Convert(comp, Options{Project: "myapp"})
	if err != nil {
		t.Fatal(err)
	}

	reg := testRegistry()
	deployment, conditions, err := Bridge(result, reg)
	if err != nil {
		t.Fatal(err)
	}

	// 4 manifests → 4 CreateRequests: Project, Network, Image, Service.
	creates := countCreates(deployment)
	if creates != 4 {
		t.Fatalf("CreateRequests: got %d, want 4", creates)
	}

	if len(conditions) != 0 {
		t.Errorf("expected empty conditions for no depends_on, got %d", len(conditions))
	}
}

func TestBridge_Edges(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx:latest"},
		},
	}
	result, err := Convert(comp, Options{Project: "myapp"})
	if err != nil {
		t.Fatal(err)
	}

	reg := testRegistry()
	deployment, _, err := Bridge(result, reg)
	if err != nil {
		t.Fatal(err)
	}

	// Service→Project, Service→Image, Service→Network = 3 dependency links.
	if got := len(deployment.Dependencies); got != 3 {
		t.Fatalf("Dependencies: got %d, want 3", got)
	}
}

func TestBridge_ConditionMap(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {
				Image: "nginx:latest",
				DependsOn: eval.DependsOnValue{
					Entries: map[string]eval.DependsOnEntry{
						"db": {Condition: "service_healthy"},
					},
				},
			},
			"db": {Image: "postgres:16"},
		},
	}
	result, err := Convert(comp, Options{Project: "myapp"})
	if err != nil {
		t.Fatal(err)
	}

	reg := testRegistry()
	_, conditions, err := Bridge(result, reg)
	if err != nil {
		t.Fatal(err)
	}

	// The db service (myapp/db) should have a condition from web (myapp/web) = "healthy".
	dbConditions, ok := conditions["myapp/db"]
	if !ok {
		t.Fatal("expected condition map entry for myapp/db")
	}
	cond, ok := dbConditions["myapp/web"]
	if !ok {
		t.Fatal("expected condition from myapp/web → myapp/db")
	}
	if cond != "healthy" {
		t.Errorf("condition = %q, want %q", cond, "healthy")
	}
}

func TestBridge_UnknownKey(t *testing.T) {
	// Create a result with an unregistered kind.
	result := &Result{
		Manifests: []manifest.Manifest{
			{
				APIVersion: "fake.io/v1",
				Kind:       "Unknown",
				Metadata:   manifest.Metadata{Name: "test"},
				Spec:       map[string]string{"foo": "bar"},
			},
		},
	}

	reg := typing.NewRegistry() // empty registry
	_, _, err := Bridge(result, reg)
	if err == nil {
		t.Fatal("expected error for unregistered definition key")
	}
}

// --- helpers ---

func countCreates(d *deploy.Deployment) int {
	n := 0
	for _, req := range d.Requests {
		if req.GetType() == deploy.RequestTypeCreate {
			n++
		}
	}
	return n
}
