package composition

import (
	"testing"

	"github.com/systemstart/nix-compose/pkg/eval"
)

func TestFilterByProfiles_EmptyProfiles(t *testing.T) {
	comp := loadJSONFixture(t, "with-profiles.json")
	result := FilterByProfiles(comp, nil)
	if len(result.Services) != len(comp.Services) {
		t.Errorf("expected all %d services, got %d", len(comp.Services), len(result.Services))
	}
}

func TestFilterByProfiles_BackendOnly(t *testing.T) {
	comp := loadJSONFixture(t, "with-profiles.json")
	result := FilterByProfiles(comp, []string{"backend"})

	// "api" and "db" match backend, "worker" has no profiles (always included)
	if len(result.Services) != 3 {
		t.Fatalf("expected 3 services, got %d: %v", len(result.Services), serviceNames(result))
	}
	if _, ok := result.Services["api"]; !ok {
		t.Error("expected api service")
	}
	if _, ok := result.Services["db"]; !ok {
		t.Error("expected db service")
	}
	if _, ok := result.Services["worker"]; !ok {
		t.Error("expected worker service (no profile = always included)")
	}
	if _, ok := result.Services["web"]; ok {
		t.Error("web should be excluded (frontend profile)")
	}
}

func TestFilterByProfiles_FrontendOnly(t *testing.T) {
	comp := loadJSONFixture(t, "with-profiles.json")
	result := FilterByProfiles(comp, []string{"frontend"})

	// "web" matches frontend, "worker" has no profiles
	if len(result.Services) != 2 {
		t.Fatalf("expected 2 services, got %d: %v", len(result.Services), serviceNames(result))
	}
	if _, ok := result.Services["web"]; !ok {
		t.Error("expected web service")
	}
	if _, ok := result.Services["worker"]; !ok {
		t.Error("expected worker service")
	}
}

func TestFilterByProfiles_MultipleProfiles(t *testing.T) {
	comp := loadJSONFixture(t, "with-profiles.json")
	result := FilterByProfiles(comp, []string{"frontend", "backend"})
	if len(result.Services) != 4 {
		t.Errorf("expected all 4 services, got %d", len(result.Services))
	}
}

func TestFilterByProfiles_PrunesDanglingDeps(t *testing.T) {
	comp := loadJSONFixture(t, "with-profiles.json")
	result := FilterByProfiles(comp, []string{"frontend"})

	// Verify no dangling depends_on references exist after filtering.
	for _, svc := range result.Services {
		for dep := range svc.DependsOn.Entries {
			if _, ok := result.Services[dep]; !ok {
				t.Errorf("dangling depends_on reference to %q", dep)
			}
		}
	}
}

func TestFilterByProfiles_NoMatchingProfile(t *testing.T) {
	comp := loadJSONFixture(t, "with-profiles.json")
	result := FilterByProfiles(comp, []string{"nonexistent"})

	// Only "worker" (no profiles) should be included
	if len(result.Services) != 1 {
		t.Fatalf("expected 1 service, got %d: %v", len(result.Services), serviceNames(result))
	}
	if _, ok := result.Services["worker"]; !ok {
		t.Error("expected worker service")
	}
}

// --- Top-level vs legacy profiles ---

func TestServiceMatchesProfile_TopLevel(t *testing.T) {
	svc := eval.Service{
		Image:    "nginx",
		Profiles: []string{"frontend", "web"},
	}
	if !serviceMatchesProfile(svc, []string{"web"}) {
		t.Error("expected match on top-level 'web' profile")
	}
}

func TestServiceMatchesProfile_LegacyXNixCompose(t *testing.T) {
	svc := eval.Service{
		Image:       "nginx",
		XNixCompose: &eval.NixComposeExtended{Profiles: []string{"frontend"}},
	}
	if !serviceMatchesProfile(svc, []string{"frontend"}) {
		t.Error("expected match on legacy x-nix-compose profile")
	}
}

func TestServiceMatchesProfile_TopLevelTakesPrecedence(t *testing.T) {
	svc := eval.Service{
		Image:       "nginx",
		Profiles:    []string{"prod"},
		XNixCompose: &eval.NixComposeExtended{Profiles: []string{"dev"}},
	}
	if serviceMatchesProfile(svc, []string{"dev"}) {
		t.Error("top-level profiles should take precedence over x-nix-compose")
	}
	if !serviceMatchesProfile(svc, []string{"prod"}) {
		t.Error("expected match on top-level 'prod' profile")
	}
}

func TestServiceMatchesProfile_NoXNixCompose(t *testing.T) {
	svc := eval.Service{Image: "nginx"}
	if !serviceMatchesProfile(svc, []string{"backend"}) {
		t.Error("service with no profiles should always match")
	}
}

func TestServiceMatchesProfile_EmptyProfiles(t *testing.T) {
	svc := eval.Service{
		Image:       "nginx",
		XNixCompose: &eval.NixComposeExtended{Profiles: []string{}},
	}
	if !serviceMatchesProfile(svc, []string{"backend"}) {
		t.Error("service with empty profiles should always match")
	}
}

func TestServiceMatchesProfile_NoMatch(t *testing.T) {
	svc := eval.Service{
		Image:    "nginx",
		Profiles: []string{"frontend"},
	}
	if serviceMatchesProfile(svc, []string{"backend"}) {
		t.Error("expected no match on 'backend' profile")
	}
}

// --- Transitive dependency activation ---

func TestFilterByProfiles_TransitiveDeps(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {
				Image:    "nginx",
				Profiles: []string{"frontend"},
				DependsOn: eval.DependsOnValue{
					Entries: map[string]eval.DependsOnEntry{
						"api": {Condition: "service_started"},
					},
				},
			},
			"api": {
				Image:    "node",
				Profiles: []string{"backend"},
				DependsOn: eval.DependsOnValue{
					Entries: map[string]eval.DependsOnEntry{
						"db": {Condition: "service_healthy"},
					},
				},
			},
			"db": {
				Image:    "postgres",
				Profiles: []string{"backend"},
			},
		},
	}

	// Activating "frontend" should pull in web → api → db transitively.
	result := FilterByProfiles(comp, []string{"frontend"})
	if len(result.Services) != 3 {
		t.Fatalf("expected 3 services (transitive deps), got %d: %v", len(result.Services), serviceNames(result))
	}
	for _, name := range []string{"web", "api", "db"} {
		if _, ok := result.Services[name]; !ok {
			t.Errorf("expected %q to be included via transitive dependency", name)
		}
	}
}

func TestFilterByProfiles_TransitiveDeps_NoExtraServices(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {
				Image:    "nginx",
				Profiles: []string{"frontend"},
			},
			"monitoring": {
				Image:    "grafana",
				Profiles: []string{"observability"},
			},
			"worker": {Image: "worker"},
		},
	}

	// Activating "frontend" should include web + worker (no profile), not monitoring.
	result := FilterByProfiles(comp, []string{"frontend"})
	if len(result.Services) != 2 {
		t.Fatalf("expected 2 services, got %d: %v", len(result.Services), serviceNames(result))
	}
	if _, ok := result.Services["monitoring"]; ok {
		t.Error("monitoring should not be included")
	}
}

// --- Prune helpers ---

func TestPruneDanglingDeps(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {
				Image: "nginx",
				DependsOn: eval.DependsOnValue{
					Entries: map[string]eval.DependsOnEntry{
						"api":     {Condition: "service_started"},
						"missing": {Condition: "service_started"},
					},
				},
			},
			"api": {Image: "node"},
		},
	}
	pruneDanglingDeps(comp)
	web := comp.Services["web"]
	if len(web.DependsOn.Entries) != 1 {
		t.Fatalf("expected 1 dep, got %d", len(web.DependsOn.Entries))
	}
	if _, ok := web.DependsOn.Entries["api"]; !ok {
		t.Error("expected 'api' dep to remain")
	}
}

func TestPruneDanglingDeps_AllRemoved(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {
				Image: "nginx",
				DependsOn: eval.DependsOnValue{
					Entries: map[string]eval.DependsOnEntry{
						"missing": {Condition: "service_started"},
					},
				},
			},
		},
	}
	pruneDanglingDeps(comp)
	web := comp.Services["web"]
	if !web.DependsOn.IsEmpty() {
		t.Errorf("expected empty depends_on, got %v", web.DependsOn.Entries)
	}
}

func TestWarnDeprecatedProfiles(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"legacy": {
				Image:       "nginx",
				XNixCompose: &eval.NixComposeExtended{Profiles: []string{"frontend"}},
			},
			"modern": {
				Image:    "nginx",
				Profiles: []string{"backend"},
			},
			"both": {
				Image:       "nginx",
				Profiles:    []string{"prod"},
				XNixCompose: &eval.NixComposeExtended{Profiles: []string{"dev"}},
			},
			"none": {
				Image: "nginx",
			},
		},
	}
	// WarnDeprecatedProfiles only prints warnings; verify it does not panic.
	WarnDeprecatedProfiles(comp)
}

func serviceNames(comp *eval.Composition) []string {
	names := make([]string, 0, len(comp.Services))
	for name := range comp.Services {
		names = append(names, name)
	}
	return names
}
