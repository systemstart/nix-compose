package typing

import (
	"encoding/json"
	"testing"
)

// stubInstance implements Instance for testing.
type stubInstance struct {
	id  string
	key DefinitionKey
}

func (s *stubInstance) GetId() string         { return s.id }
func (s *stubInstance) GetKey() DefinitionKey { return s.key }
func (s *stubInstance) String() string        { return s.id }
func (s *stubInstance) Apply() error          { return nil }

func TestInstanceList_MarshalJSON(t *testing.T) {
	il := InstanceList{
		&stubInstance{id: "a", key: "g/v1/A"},
		&stubInstance{id: "b", key: "g/v1/B"},
	}
	data, err := il.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	// Should serialize as SimpleReferences
	var refs []SimpleReference
	if err := json.Unmarshal(data, &refs); err != nil {
		t.Fatalf("Unmarshal result: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d", len(refs))
	}
	if refs[0].Id != "a" {
		t.Fatalf("expected id 'a', got %q", refs[0].Id)
	}
	if refs[1].Id != "b" {
		t.Fatalf("expected id 'b', got %q", refs[1].Id)
	}
}

func TestInstanceList_MarshalJSON_Empty(t *testing.T) {
	il := InstanceList{}
	data, err := il.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	if string(data) != "[]" {
		t.Fatalf("expected '[]', got %s", data)
	}
}

func TestInstanceList_Unique(t *testing.T) {
	a := &stubInstance{id: "a", key: "g/v1/A"}
	b := &stubInstance{id: "b", key: "g/v1/B"}
	il := InstanceList{a, a, b, a}
	il.Unique()
	if len(il) != 2 {
		t.Fatalf("expected 2 unique instances, got %d", len(il))
	}
	if il[0].GetId() != "a" {
		t.Fatalf("expected 'a', got %q", il[0].GetId())
	}
	if il[1].GetId() != "b" {
		t.Fatalf("expected 'b', got %q", il[1].GetId())
	}
}

func TestInstanceList_Unique_Empty(t *testing.T) {
	il := InstanceList{}
	il.Unique()
	if len(il) != 0 {
		t.Fatalf("expected 0 after Unique on empty, got %d", len(il))
	}
}

func TestInstanceList_Unique_NoDuplicates(t *testing.T) {
	a := &stubInstance{id: "a", key: "g/v1/A"}
	b := &stubInstance{id: "b", key: "g/v1/B"}
	il := InstanceList{a, b}
	il.Unique()
	if len(il) != 2 {
		t.Fatalf("expected 2 after Unique with no dups, got %d", len(il))
	}
}

func TestGVK_GetKey(t *testing.T) {
	gvk := &GVK{Group: "mygroup", Version: "v2", Kind: "MyKind"}
	key := gvk.GetKey()
	if key != "mygroup/v2/MyKind" {
		t.Fatalf("expected 'mygroup/v2/MyKind', got %q", key)
	}
}

func TestRegistryInstantiate(t *testing.T) {
	reg := NewRegistry()
	def := &instantiatingDef{key: "test/v1/Widget"}
	reg.Register(def)

	inst, err := reg.Instantiate("test/v1/Widget", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	if inst == nil {
		t.Fatal("expected non-nil instance")
		return
	}
	if inst.GetId() != "widget-1" {
		t.Fatalf("expected id 'widget-1', got %q", inst.GetId())
	}
}

func TestRegistryInstantiate_UnknownKey(t *testing.T) {
	reg := NewRegistry()
	_, err := reg.Instantiate("unknown/v1/X", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for unknown key")
	}
}

func TestRegistryDelete(t *testing.T) {
	reg := NewRegistry()
	def := &instantiatingDef{key: "test/v1/Widget"}
	reg.Register(def)

	ref := NewReference("widget-1", "test/v1/Widget")
	err := reg.Delete(ref)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestRegistryDelete_UnknownKey(t *testing.T) {
	reg := NewRegistry()
	ref := NewReference("widget-1", "unknown/v1/X")
	err := reg.Delete(ref)
	if err == nil {
		t.Fatal("expected error for unknown key")
	}
}

func TestRegistryLoadInstance(t *testing.T) {
	reg := NewRegistry()
	def := &instantiatingDef{key: "test/v1/Widget"}
	reg.Register(def)

	inst, err := reg.LoadInstance("test/v1/Widget", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("LoadInstance: %v", err)
	}
	if inst == nil {
		t.Fatal("expected non-nil instance")
		return
	}
	if inst.GetId() != "widget-1" {
		t.Fatalf("expected id 'widget-1', got %q", inst.GetId())
	}
}

func TestRegistryLoadInstance_UnknownKey(t *testing.T) {
	reg := NewRegistry()
	_, err := reg.LoadInstance("unknown/v1/X", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for unknown key")
	}
}

func TestRegistryLoadInstance_EmptyId(t *testing.T) {
	reg := NewRegistry()
	def := &instantiatingDef{key: "test/v1/Widget", emptyId: true}
	reg.Register(def)

	_, err := reg.LoadInstance("test/v1/Widget", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for empty id")
	}
}

// instantiatingDef is a stub Definition that returns actual instances.
type instantiatingDef struct {
	key     DefinitionKey
	emptyId bool
}

func (d *instantiatingDef) GetKey() DefinitionKey  { return d.key }
func (d *instantiatingDef) GetMappings() []Mapping { return nil }
func (d *instantiatingDef) Delete(Reference) error { return nil }
func (d *instantiatingDef) GetStatus(Reference) (Status, error) {
	return nil, nil
}

func (d *instantiatingDef) GetProviderStatus(Reference) (Status, error) {
	return nil, nil
}

func (d *instantiatingDef) Instantiate(json.RawMessage) (Instance, error) {
	id := "widget-1"
	if d.emptyId {
		id = ""
	}
	return &stubInstance{id: id, key: d.key}, nil
}

func (d *instantiatingDef) Load(r json.RawMessage) (Instance, error) {
	return d.Instantiate(r)
}
