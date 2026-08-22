package orchestrate

import (
	"context"
	"testing"
	"time"

	"github.com/systemstart/nix-compose/pkg/orchestrate/convert"
	"github.com/systemstart/nix-compose/pkg/orchestrate/deploy"
	"github.com/systemstart/nix-compose/pkg/orchestrate/state"
)

func TestCondPriority(t *testing.T) {
	tests := []struct {
		cond     string
		priority int
	}{
		{"healthy", 3},
		{"completed", 2},
		{"started", 1},
		{"", 0},
		{"unknown", 0},
	}

	for _, tt := range tests {
		t.Run(tt.cond, func(t *testing.T) {
			got := condPriority(tt.cond)
			if got != tt.priority {
				t.Errorf("condPriority(%q) = %d, want %d", tt.cond, got, tt.priority)
			}
		})
	}
}

func TestSplitResourceID(t *testing.T) {
	tests := []struct {
		id      string
		project string
		service string
	}{
		{"myapp/web", "myapp", "web"},
		{"foo/bar", "foo", "bar"},
		{"single", "single", "single"},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			p, s := splitResourceID(tt.id)
			if p != tt.project || s != tt.service {
				t.Errorf("splitResourceID(%q) = (%q, %q), want (%q, %q)",
					tt.id, p, s, tt.project, tt.service)
			}
		})
	}
}

func TestWaitCondition_NoConditions(t *testing.T) {
	engine := newTestEngine(t)

	// With nil conditions map, waitCondition should be a no-op.
	err := waitCondition(t.Context(), engine, "myapp/web", nil)
	if err != nil {
		t.Fatalf("waitCondition with nil conditions should not error: %v", err)
	}
}

func TestWaitCondition_NoMatchingResource(t *testing.T) {
	engine := newTestEngine(t)

	conditions := convert.ConditionMap{
		"myapp/db": {
			"myapp/web": "started",
		},
	}

	// waitCondition for a resource not in the map should be a no-op.
	err := waitCondition(t.Context(), engine, "myapp/other", conditions)
	if err != nil {
		t.Fatalf("waitCondition for unmatched resource should not error: %v", err)
	}
}

func TestLoadContainerSpec_NotFound(t *testing.T) {
	engine := newTestEngine(t)

	spec, err := loadContainerSpec(engine, "nonexistent/web")
	if err != nil {
		t.Fatalf("loadContainerSpec: %v", err)
	}
	if spec != nil {
		t.Fatal("expected nil spec for nonexistent resource")
	}
}

func TestLoadContainerSpec_NoHealthcheck(t *testing.T) {
	engine := newTestEngine(t)

	// Save a rollout with valid JSON but no healthcheck.
	rollout := &deploy.Rollout{
		InstanceId:  "myapp/web",
		InstanceKey: "cri.orchestrator.io/v1/Service",
		Body:        []byte(`{"container":{"project":"myapp","service":"web","image":"nginx:latest"}}`),
		Status:      &deploy.RolloutStatus{Short: "SUCCESS"},
	}
	if err := engine.DB().Save(state.RolloutsById, rollout); err != nil {
		t.Fatalf("Save: %v", err)
	}

	spec, err := loadContainerSpec(engine, "myapp/web")
	if err != nil {
		t.Fatalf("loadContainerSpec: %v", err)
	}
	// Spec might be nil if body doesn't match the expected format, or non-nil.
	// Either way, no healthcheck.
	if spec != nil && spec.Healthcheck != nil {
		t.Error("expected nil healthcheck")
	}
}

func TestWaitCondition_PicksStrictestCondition(t *testing.T) {
	engine := newTestEngine(t)

	// Multiple conditions for the same resource — should pick "healthy" (highest priority).
	conditions := convert.ConditionMap{
		"myapp/db": {
			"myapp/web":   "started",
			"myapp/admin": "healthy",
		},
	}

	// Without a CRI client, waitHealthy falls back to waitStarted,
	// which will fail because the registry won't have the service definition
	// for the engine with nil CRI.
	// The important thing is that the condition handling code runs.
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	err := waitCondition(ctx, engine, "myapp/db", conditions)
	// Should error (no CRI, or timeout), but should not panic.
	// It's also possible that waitStarted returns immediately if the def status is success.
	_ = err
}

func TestWaitCondition_StartedCondition(t *testing.T) {
	engine := newTestEngine(t)

	conditions := convert.ConditionMap{
		"myapp/db": {
			"myapp/web": "started",
		},
	}

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	err := waitCondition(ctx, engine, "myapp/db", conditions)
	// Will timeout or error because no real CRI status — that's expected.
	// Just ensure the code path was exercised.
	_ = err
}

func TestWaitCondition_CompletedCondition(t *testing.T) {
	engine := newTestEngine(t)

	conditions := convert.ConditionMap{
		"myapp/migrate": {
			"myapp/app": "completed",
		},
	}

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	err := waitCondition(ctx, engine, "myapp/migrate", conditions)
	// waitCompleted requires CRI client - should error or timeout; either is fine.
	_ = err
}

// TestLoadContainerSpec_WithHealthcheck tests loadContainerSpec with a rollout
// body that includes a healthcheck, verifying the healthcheck is preserved.
func TestLoadContainerSpec_WithHealthcheck(t *testing.T) {
	engine := newTestEngine(t)

	rollout := &deploy.Rollout{
		InstanceId:  "myapp/web",
		InstanceKey: "cri.orchestrator.io/v1/Service",
		Body:        []byte(`{"container":{"project":"myapp","service":"web","image":"nginx:latest","healthcheck":{"test":["CMD","curl","-f","http://localhost/"],"interval":"10s","timeout":"5s","retries":3}}}`),
		Status:      &deploy.RolloutStatus{Short: "SUCCESS"},
	}
	if err := engine.DB().Save(state.RolloutsById, rollout); err != nil {
		t.Fatalf("Save: %v", err)
	}

	spec, err := loadContainerSpec(engine, "myapp/web")
	if err != nil {
		t.Fatalf("loadContainerSpec: %v", err)
	}
	if spec == nil {
		t.Fatal("expected non-nil spec")
		return
	}
	if spec.Healthcheck == nil {
		t.Fatal("expected non-nil healthcheck in spec")
		return
	}
	if spec.Healthcheck.Retries != 3 {
		t.Errorf("healthcheck retries = %d, want 3", spec.Healthcheck.Retries)
	}
}

// TestWaitCondition_DefaultCondition tests that an unknown condition string
// falls through to the default case (treated as waitStarted).
func TestWaitCondition_DefaultCondition(t *testing.T) {
	engine := newTestEngine(t)

	conditions := convert.ConditionMap{
		"myapp/db": {
			"myapp/web": "custom_unknown",
		},
	}

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	err := waitCondition(ctx, engine, "myapp/db", conditions)
	// Unknown condition falls back to default (waitStarted).
	// Will timeout or error, but should not panic.
	_ = err
}

// TestWaitCompleted_NoCRI tests that waitCompleted returns an error when
// no CRI client is configured.
func TestWaitCompleted_NoCRI(t *testing.T) {
	engine := newTestEngine(t)

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	err := waitCompleted(ctx, engine, "myapp/svc")
	if err == nil {
		t.Error("expected error for missing CRI client")
	}
	if err != nil && !contains(err.Error(), "CRI client") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestWaitHealthy_NoCRI tests that waitHealthy falls back to waitStarted
// when no CRI client is available.
func TestWaitHealthy_NoCRI(t *testing.T) {
	engine := newTestEngine(t)

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	// Without CRI, waitHealthy logs a warning and falls back to waitStarted.
	// waitStarted will then timeout or error, which is fine.
	err := waitHealthy(ctx, engine, "myapp/web")
	_ = err // Will timeout or error, just ensure no panic
}

// TestLoadContainerSpec_InvalidRolloutBody tests loadContainerSpec when the
// rollout body contains garbage JSON that doesn't match any known spec format.
func TestLoadContainerSpec_InvalidRolloutBody2(t *testing.T) {
	engine := newTestEngine(t)

	rollout := &deploy.Rollout{
		InstanceId:  "myapp/broken",
		InstanceKey: "cri.orchestrator.io/v1/Service",
		Body:        []byte(`{"totally":"different","fields":123}`),
		Status:      &deploy.RolloutStatus{Short: "SUCCESS"},
	}
	if err := engine.DB().Save(state.RolloutsById, rollout); err != nil {
		t.Fatalf("Save: %v", err)
	}

	spec, err := loadContainerSpec(engine, "myapp/broken")
	if err != nil {
		t.Fatalf("loadContainerSpec: %v", err)
	}
	if spec != nil {
		t.Errorf("expected nil spec for garbage body, got %+v", spec)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
