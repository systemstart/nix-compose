package composition

import (
	"testing"

	"github.com/systemstart/nix-compose/pkg/eval"
)

func TestInitServiceName(t *testing.T) {
	got := initServiceName("api", "migrate")
	want := "api-init-migrate"
	if got != want {
		t.Errorf("initServiceName() = %q, want %q", got, want)
	}
}

func TestSynthesizeInitContainers_NoInits(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx"},
		},
	}
	result := SynthesizeInitContainers(comp)
	if len(result.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(result.Services))
	}
	if _, ok := result.Services["web"]; !ok {
		t.Error("expected web service")
	}
}

func TestSynthesizeInitContainers_SingleInit(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"api": {
				Image: "node:18",
				DependsOn: eval.DependsOnValue{
					Entries: map[string]eval.DependsOnEntry{
						"db": {Condition: "service_healthy"},
					},
				},
				XNixCompose: &eval.NixComposeExtended{
					InitContainers: []eval.InitContainer{
						{
							Name:    "migrate",
							Image:   "flyway:latest",
							Command: eval.CommandValue{Parts: []string{"flyway", "migrate"}},
						},
					},
				},
			},
			"db": {Image: "postgres:15"},
		},
	}

	result := SynthesizeInitContainers(comp)

	// Should have 3 services: api, db, api-init-migrate.
	if len(result.Services) != 3 {
		t.Fatalf("expected 3 services, got %d: %v", len(result.Services), serviceNames(result))
	}

	// Check init service exists.
	initSvc, ok := result.Services["api-init-migrate"]
	if !ok {
		t.Fatal("missing api-init-migrate service")
	}
	if initSvc.Image != "flyway:latest" {
		t.Errorf("init image = %q, want flyway:latest", initSvc.Image)
	}
	if initSvc.Restart != "no" {
		t.Errorf("init restart = %q, want no", initSvc.Restart)
	}

	// Init inherits main service's depends_on.
	if initSvc.DependsOn.IsEmpty() {
		t.Fatal("init service should have depends_on")
	}
	if _, ok := initSvc.DependsOn.Entries["db"]; !ok {
		t.Error("init service should depend on db")
	}

	// Main service depends on last init.
	apiSvc := result.Services["api"]
	if apiSvc.DependsOn.IsEmpty() {
		t.Fatal("api should have depends_on")
	}
	dep, ok := apiSvc.DependsOn.Entries["api-init-migrate"]
	if !ok {
		t.Fatal("api should depend on api-init-migrate")
	}
	if dep.Condition != "service_completed_successfully" {
		t.Errorf("condition = %q, want service_completed_successfully", dep.Condition)
	}
}

func TestSynthesizeInitContainers_ChainedInits(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"api": {
				Image: "node:18",
				XNixCompose: &eval.NixComposeExtended{
					InitContainers: []eval.InitContainer{
						{Name: "migrate", Image: "flyway:latest"},
						{Name: "seed", Image: "node:18"},
					},
				},
			},
		},
	}

	result := SynthesizeInitContainers(comp)

	// Should have 3 services: api, api-init-migrate, api-init-seed.
	if len(result.Services) != 3 {
		t.Fatalf("expected 3 services, got %d: %v", len(result.Services), serviceNames(result))
	}

	// Second init depends on first.
	seedSvc := result.Services["api-init-seed"]
	if seedSvc.DependsOn.IsEmpty() {
		t.Fatal("seed init should have depends_on")
	}
	dep, ok := seedSvc.DependsOn.Entries["api-init-migrate"]
	if !ok {
		t.Fatal("seed should depend on migrate")
	}
	if dep.Condition != "service_completed_successfully" {
		t.Errorf("condition = %q, want service_completed_successfully", dep.Condition)
	}

	// Main service depends on last init (seed).
	apiSvc := result.Services["api"]
	apiDep, ok := apiSvc.DependsOn.Entries["api-init-seed"]
	if !ok {
		t.Fatal("api should depend on api-init-seed")
	}
	if apiDep.Condition != "service_completed_successfully" {
		t.Errorf("condition = %q, want service_completed_successfully", apiDep.Condition)
	}
}

func TestSynthesizeInitContainers_PreservesNetworksAndVolumes(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx"},
		},
		Networks: map[string]eval.Network{
			"default": {Name: "mynet"},
		},
		Volumes: map[string]eval.Volume{
			"data": {},
		},
	}

	result := SynthesizeInitContainers(comp)
	if len(result.Networks) != 1 {
		t.Error("expected networks to be preserved")
	}
	if len(result.Volumes) != 1 {
		t.Error("expected volumes to be preserved")
	}
}

func TestSynthesizeInitContainers_InitWithEnvAndVolumes(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"api": {
				Image: "node:18",
				XNixCompose: &eval.NixComposeExtended{
					InitContainers: []eval.InitContainer{
						{
							Name:        "migrate",
							Image:       "flyway:latest",
							Environment: map[string]string{"DB_URL": "postgres://localhost/db"},
							Volumes:     []string{"/migrations:/flyway/sql"},
						},
					},
				},
			},
		},
	}

	result := SynthesizeInitContainers(comp)
	initSvc := result.Services["api-init-migrate"]

	if initSvc.Environment["DB_URL"] != "postgres://localhost/db" {
		t.Errorf("env DB_URL = %q", initSvc.Environment["DB_URL"])
	}
	if len(initSvc.Volumes) != 1 || initSvc.Volumes[0] != "/migrations:/flyway/sql" {
		t.Errorf("volumes = %v", initSvc.Volumes)
	}
}

func TestSynthesizeInitContainers_FromFixture(t *testing.T) {
	comp := loadJSONFixture(t, "init-containers.json")
	result := SynthesizeInitContainers(comp)

	// Should have: api, db, api-init-migrate, api-init-seed.
	if len(result.Services) != 4 {
		t.Fatalf("expected 4 services, got %d: %v", len(result.Services), serviceNames(result))
	}

	// First init inherits api's depends_on (db).
	migrateSvc := result.Services["api-init-migrate"]
	if _, ok := migrateSvc.DependsOn.Entries["db"]; !ok {
		t.Error("migrate should inherit dependency on db")
	}

	// seed depends on migrate.
	seedSvc := result.Services["api-init-seed"]
	if _, ok := seedSvc.DependsOn.Entries["api-init-migrate"]; !ok {
		t.Error("seed should depend on migrate")
	}

	// api depends on seed (last init).
	apiSvc := result.Services["api"]
	if _, ok := apiSvc.DependsOn.Entries["api-init-seed"]; !ok {
		t.Error("api should depend on seed")
	}
}

// serviceNames is defined in filter_test.go
