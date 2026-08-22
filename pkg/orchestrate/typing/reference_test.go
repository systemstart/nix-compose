package typing

import (
	"encoding/json"
	"testing"
)

func TestNewReference(t *testing.T) {
	ref := NewReference("my-id", "group/v1/Kind")
	if ref.GetId() != "my-id" {
		t.Fatalf("GetId() = %q", ref.GetId())
	}
	if ref.GetKey() != "group/v1/Kind" {
		t.Fatalf("GetKey() = %q", ref.GetKey())
	}
}

func TestSimpleReference_String(t *testing.T) {
	ref := NewReference("my-id", "group/v1/Kind")
	s := ref.String()
	if s == "" {
		t.Fatal("expected non-empty string")
	}
}

func TestSimpleReference_GetBody(t *testing.T) {
	sr := &SimpleReference{
		Id:   "id-1",
		Key:  "g/v1/K",
		Body: json.RawMessage(`{"x":1}`),
	}
	if string(sr.GetBody()) != `{"x":1}` {
		t.Fatalf("GetBody() = %s", sr.GetBody())
	}
}

func TestReferenceList_MarshalUnmarshal(t *testing.T) {
	rl := ReferenceList{
		NewReference("a", "g/v1/A"),
		NewReference("b", "g/v1/B"),
	}

	data, err := rl.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}

	var loaded ReferenceList
	if err := loaded.UnmarshalJSON(data); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 refs, got %d", len(loaded))
	}
	if loaded[0].GetId() != "a" {
		t.Fatalf("loaded[0].GetId() = %q", loaded[0].GetId())
	}
	if loaded[1].GetId() != "b" {
		t.Fatalf("loaded[1].GetId() = %q", loaded[1].GetId())
	}
}

func TestReferenceList_UnmarshalJSON_Invalid(t *testing.T) {
	var rl ReferenceList
	err := rl.UnmarshalJSON([]byte(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestReferenceList_Unique(t *testing.T) {
	a := NewReference("a", "g/v1/A")
	rl := ReferenceList{a, a, NewReference("b", "g/v1/B")}
	unique := rl.Unique()
	if len(unique) != 2 {
		t.Fatalf("expected 2 unique refs, got %d", len(unique))
	}
}

func TestReferenceList_Without(t *testing.T) {
	a := NewReference("a", "g/v1/A")
	b := NewReference("b", "g/v1/B")
	rl := ReferenceList{a, b}

	result := rl.Without(a)
	if len(result) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(result))
	}
	if result[0].GetId() != "b" {
		t.Fatalf("expected 'b', got %q", result[0].GetId())
	}
}

func TestReferenceList_Without_NotFound(t *testing.T) {
	a := NewReference("a", "g/v1/A")
	c := NewReference("c", "g/v1/C")
	rl := ReferenceList{a}

	result := rl.Without(c)
	if len(result) != 1 {
		t.Fatalf("expected 1 ref (unchanged), got %d", len(result))
	}
}

func TestReferenceList_Without_Empty(t *testing.T) {
	rl := ReferenceList{}
	c := NewReference("c", "g/v1/C")
	result := rl.Without(c)
	if len(result) != 0 {
		t.Fatalf("expected 0, got %d", len(result))
	}
}

func TestReferenceList_First(t *testing.T) {
	a := NewReference("a", "g/v1/A")
	b := NewReference("b", "g/v1/B")
	rl := ReferenceList{a, b}

	if rl.First().GetId() != "a" {
		t.Fatalf("First().GetId() = %q", rl.First().GetId())
	}
}

func TestReferenceList_First_Empty(t *testing.T) {
	rl := ReferenceList{}
	if rl.First() != nil {
		t.Fatal("expected nil for empty list")
	}
}

func TestReferenceList_ByKey(t *testing.T) {
	rl := ReferenceList{
		NewReference("a", "g/v1/A"),
		NewReference("b", "g/v1/B"),
		NewReference("c", "g/v1/A"),
	}

	filtered := rl.ByKey("g/v1/A")
	if len(filtered) != 2 {
		t.Fatalf("expected 2 refs with key g/v1/A, got %d", len(filtered))
	}
}

func TestReferenceList_ByKey_NoMatch(t *testing.T) {
	rl := ReferenceList{NewReference("a", "g/v1/A")}
	filtered := rl.ByKey("g/v1/Z")
	if len(filtered) != 0 {
		t.Fatalf("expected 0, got %d", len(filtered))
	}
}

func TestDefinitionKey_GetGVK_TwoParts(t *testing.T) {
	key := CreateDefinitionKey("group", "v1", "Kind")
	gvk := key.GetGVK()
	if gvk.Group != "group" || gvk.Version != "v1" || gvk.Kind != "Kind" {
		t.Fatalf("unexpected GVK: %+v", gvk)
	}
}

func TestDefinitionKey_GetGVK_SinglePart_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for single-part key")
		}
	}()
	key := DefinitionKey("single")
	_ = key.GetGVK() // should panic
}
