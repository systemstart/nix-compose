package orchestrate

import (
	"encoding/json"
	"testing"

	"github.com/systemstart/nix-compose/pkg/orchestrate/convert"
	"github.com/systemstart/nix-compose/pkg/orchestrate/deploy"
	"github.com/systemstart/nix-compose/pkg/orchestrate/typing"
)

func makeCreateRequest(id string, key typing.DefinitionKey, body string) *deploy.CreateRequest {
	return &deploy.CreateRequest{
		SubjectId:  id,
		SubjectKey: key,
		Subject:    json.RawMessage(body),
	}
}

func makeRollout(id string, key typing.DefinitionKey, body string) *deploy.Rollout {
	return &deploy.Rollout{
		InstanceId:  id,
		InstanceKey: key,
		Body:        json.RawMessage(body),
	}
}

var testKey typing.DefinitionKey = "test.io/v1/Resource"

func TestComputePlan_AllCreates(t *testing.T) {
	desired := deploy.NewDeployment()
	desired.Requests = append(desired.Requests,
		makeCreateRequest("a", testKey, `{"name":"a"}`),
		makeCreateRequest("b", testKey, `{"name":"b"}`),
	)

	plan := ComputePlan(desired, nil, nil)

	creates, _, _, _ := plan.Summary()
	if creates != 2 {
		t.Fatalf("creates: got %d, want 2", creates)
	}
	for _, a := range plan.Actions {
		if a.Type != ActionCreate {
			t.Errorf("expected all creates, got %s for %s", a.Type, a.ResourceID)
		}
	}
}

func TestComputePlan_AllNoOps(t *testing.T) {
	body := `{"name":"a"}`
	desired := deploy.NewDeployment()
	desired.Requests = append(desired.Requests,
		makeCreateRequest("a", testKey, body),
	)

	current := []*deploy.Rollout{
		makeRollout("a", testKey, body),
	}

	plan := ComputePlan(desired, current, nil)

	_, _, _, noops := plan.Summary()
	if noops != 1 {
		t.Fatalf("noops: got %d, want 1", noops)
	}
	// Deployment should have no requests (nothing to do).
	if len(plan.Deployment.Requests) != 0 {
		t.Fatalf("deployment requests: got %d, want 0", len(plan.Deployment.Requests))
	}
}

func TestComputePlan_OrphanDestroy(t *testing.T) {
	desired := deploy.NewDeployment()
	desired.Requests = append(desired.Requests,
		makeCreateRequest("a", testKey, `{"name":"a"}`),
	)

	current := []*deploy.Rollout{
		makeRollout("a", testKey, `{"name":"a"}`),
		makeRollout("b", testKey, `{"name":"b"}`),
	}

	plan := ComputePlan(desired, current, nil)

	_, _, destroys, _ := plan.Summary()
	if destroys != 1 {
		t.Fatalf("destroys: got %d, want 1", destroys)
	}

	// Find the destroy action.
	found := false
	for _, a := range plan.Actions {
		if a.Type == ActionDestroy && a.ResourceID == "b" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected destroy action for orphan 'b'")
	}
}

func TestComputePlan_Update(t *testing.T) {
	desired := deploy.NewDeployment()
	desired.Requests = append(desired.Requests,
		makeCreateRequest("a", testKey, `{"name":"a","version":"2"}`),
	)

	current := []*deploy.Rollout{
		makeRollout("a", testKey, `{"name":"a","version":"1"}`),
	}

	plan := ComputePlan(desired, current, nil)

	_, updates, _, _ := plan.Summary()
	if updates != 1 {
		t.Fatalf("updates: got %d, want 1", updates)
	}

	// Deployment should have delete + create.
	deletes := 0
	creates := 0
	for _, req := range plan.Deployment.Requests {
		switch req.GetType() {
		case deploy.RequestTypeDelete:
			deletes++
		case deploy.RequestTypeCreate:
			creates++
		}
	}
	if deletes != 1 || creates != 1 {
		t.Fatalf("update should produce 1 delete + 1 create, got %d deletes + %d creates", deletes, creates)
	}
}

func TestBodyEqual(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{"identical", `{"a":1,"b":2}`, `{"a":1,"b":2}`, true},
		{"reordered keys", `{"b":2,"a":1}`, `{"a":1,"b":2}`, true},
		{"different values", `{"a":1}`, `{"a":2}`, false},
		{"nil both", "", "", true},
		{"nil a", "", `{"a":1}`, false},
		{"nil b", `{"a":1}`, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := bodyEqual(json.RawMessage(tt.a), json.RawMessage(tt.b))
			if got != tt.want {
				t.Errorf("bodyEqual(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestComputePlan_Mixed(t *testing.T) {
	desired := deploy.NewDeployment()
	desired.Requests = append(desired.Requests,
		makeCreateRequest("keep", testKey, `{"v":1}`),   // noop
		makeCreateRequest("change", testKey, `{"v":2}`), // update
		makeCreateRequest("new", testKey, `{"v":1}`),    // create
	)

	current := []*deploy.Rollout{
		makeRollout("keep", testKey, `{"v":1}`),
		makeRollout("change", testKey, `{"v":1}`),
		makeRollout("old", testKey, `{"v":1}`),
	}

	conditions := convert.ConditionMap{
		"keep": {"change": "healthy"},
	}

	plan := ComputePlan(desired, current, conditions)

	creates, updates, destroys, noops := plan.Summary()
	if creates != 1 || updates != 1 || destroys != 1 || noops != 1 {
		t.Fatalf("expected 1/1/1/1, got creates=%d updates=%d destroys=%d noops=%d",
			creates, updates, destroys, noops)
	}

	// Conditions should be passed through.
	if plan.Conditions == nil {
		t.Fatal("expected non-nil conditions")
		return
	}
	if plan.Conditions["keep"]["change"] != "healthy" {
		t.Error("conditions not passed through correctly")
	}
}
