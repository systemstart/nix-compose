package composition

import (
	"testing"

	"github.com/systemstart/nix-compose/pkg/eval"
)

func TestValidateResources_NoResources(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx"},
		},
	}
	warnings := ValidateResources(comp)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}
}

func TestValidateResources_Valid(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"api": {
				Image: "node:18",
				XNixCompose: &eval.NixComposeExtended{
					Resources: &eval.Resources{
						Limits:   &eval.ResourceSpec{CPU: "1.0", Memory: "512M"},
						Requests: &eval.ResourceSpec{CPU: "0.25", Memory: "128M"},
					},
				},
			},
		},
	}
	warnings := ValidateResources(comp)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}
}

func TestValidateResources_RequestsExceedLimits(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"api": {
				Image: "node:18",
				XNixCompose: &eval.NixComposeExtended{
					Resources: &eval.Resources{
						Limits:   &eval.ResourceSpec{CPU: "0.5", Memory: "128M"},
						Requests: &eval.ResourceSpec{CPU: "1.0", Memory: "512M"},
					},
				},
			},
		},
	}
	warnings := ValidateResources(comp)
	if len(warnings) != 2 {
		t.Fatalf("expected 2 warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestValidateResources_LimitsOnly(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"api": {
				Image: "node:18",
				XNixCompose: &eval.NixComposeExtended{
					Resources: &eval.Resources{
						Limits: &eval.ResourceSpec{CPU: "1.0", Memory: "512M"},
					},
				},
			},
		},
	}
	warnings := ValidateResources(comp)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}
}

func TestValidateResources_MillicoresCPU(t *testing.T) {
	// 25m < 100m — request does NOT exceed limit, no warning expected.
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"alertmanager": {
				Image: "prom/alertmanager",
				XNixCompose: &eval.NixComposeExtended{
					Resources: &eval.Resources{
						Limits:   &eval.ResourceSpec{CPU: "100m"},
						Requests: &eval.ResourceSpec{CPU: "25m"},
					},
				},
			},
		},
	}
	warnings := ValidateResources(comp)
	if len(warnings) != 0 {
		t.Errorf("25m < 100m should not warn, got %v", warnings)
	}
}

func TestValidateResources_MillicoresExceed(t *testing.T) {
	// 200m > 100m — request exceeds limit.
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"api": {
				Image: "node:18",
				XNixCompose: &eval.NixComposeExtended{
					Resources: &eval.Resources{
						Limits:   &eval.ResourceSpec{CPU: "100m"},
						Requests: &eval.ResourceSpec{CPU: "200m"},
					},
				},
			},
		},
	}
	warnings := ValidateResources(comp)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
}

func TestValidateResources_MixedCPUFormats(t *testing.T) {
	// 500m == 0.5 — request does NOT exceed limit.
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"api": {
				Image: "node:18",
				XNixCompose: &eval.NixComposeExtended{
					Resources: &eval.Resources{
						Limits:   &eval.ResourceSpec{CPU: "1.0"},
						Requests: &eval.ResourceSpec{CPU: "500m"},
					},
				},
			},
		},
	}
	warnings := ValidateResources(comp)
	if len(warnings) != 0 {
		t.Errorf("500m < 1.0 should not warn, got %v", warnings)
	}
}

func TestValidateResources_MemorySuffixes(t *testing.T) {
	// 128Mi < 1Gi — no warning.
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"api": {
				Image: "node:18",
				XNixCompose: &eval.NixComposeExtended{
					Resources: &eval.Resources{
						Limits:   &eval.ResourceSpec{Memory: "1Gi"},
						Requests: &eval.ResourceSpec{Memory: "128Mi"},
					},
				},
			},
		},
	}
	warnings := ValidateResources(comp)
	if len(warnings) != 0 {
		t.Errorf("128Mi < 1Gi should not warn, got %v", warnings)
	}
}

func TestValidateResources_MemoryExceed(t *testing.T) {
	// 2Gi > 1Gi — warning.
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"api": {
				Image: "node:18",
				XNixCompose: &eval.NixComposeExtended{
					Resources: &eval.Resources{
						Limits:   &eval.ResourceSpec{Memory: "1Gi"},
						Requests: &eval.ResourceSpec{Memory: "2Gi"},
					},
				},
			},
		},
	}
	warnings := ValidateResources(comp)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
}

func TestParseCPU(t *testing.T) {
	tests := []struct {
		input string
		want  float64
	}{
		{"1.0", 1000},
		{"0.25", 250},
		{"100m", 100},
		{"25m", 25},
		{"500m", 500},
		{"2", 2000},
	}
	for _, tt := range tests {
		got, ok := parseCPU(tt.input)
		if !ok {
			t.Errorf("parseCPU(%q) failed", tt.input)
			continue
		}
		if got != tt.want {
			t.Errorf("parseCPU(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestParseMemory(t *testing.T) {
	tests := []struct {
		input string
		want  float64
	}{
		{"1024", 1024},
		{"1K", 1000},
		{"1Ki", 1024},
		{"512M", 512_000_000},
		{"512Mi", 512 * 1024 * 1024},
		{"1G", 1_000_000_000},
		{"1Gi", 1024 * 1024 * 1024},
		{"2T", 2_000_000_000_000},
		{"2Ti", 2 * 1024 * 1024 * 1024 * 1024},
	}
	for _, tt := range tests {
		got, ok := parseMemory(tt.input)
		if !ok {
			t.Errorf("parseMemory(%q) failed", tt.input)
			continue
		}
		if got != tt.want {
			t.Errorf("parseMemory(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
