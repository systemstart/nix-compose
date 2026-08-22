package typing

import (
	"encoding/json"
	"testing"
)

// stubDefinition is a minimal Definition for testing.
type stubDefinition struct {
	key DefinitionKey
}

func (d *stubDefinition) GetKey() DefinitionKey                         { return d.key }
func (d *stubDefinition) GetMappings() []Mapping                        { return nil }
func (d *stubDefinition) Instantiate(json.RawMessage) (Instance, error) { return nil, nil }
func (d *stubDefinition) Load(json.RawMessage) (Instance, error)        { return nil, nil }
func (d *stubDefinition) Delete(Reference) error                        { return nil }
func (d *stubDefinition) GetStatus(Reference) (Status, error)           { return nil, nil }
func (d *stubDefinition) GetProviderStatus(Reference) (Status, error)   { return nil, nil }

func TestRegistryRegisterAndLookup(t *testing.T) {
	reg := NewRegistry()
	def := &stubDefinition{key: "test/v1/Widget"}
	reg.Register(def)

	got, err := reg.GetDefinition("test/v1/Widget")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != def {
		t.Error("GetDefinition returned wrong definition")
	}
}

func TestRegistryUnknownKey(t *testing.T) {
	reg := NewRegistry()
	_, err := reg.GetDefinition("unknown/v1/Thing")
	if err == nil {
		t.Fatal("expected error for unknown key")
	}
}

func TestRegistryAllDefinitions(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&stubDefinition{key: "a/v1/A"})
	reg.Register(&stubDefinition{key: "b/v1/B"})

	all := reg.AllDefinitions()
	if len(all) != 2 {
		t.Fatalf("expected 2 definitions, got %d", len(all))
	}
}

func TestRegistryDuplicatePanics(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&stubDefinition{key: "a/v1/A"})

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for duplicate registration")
		}
	}()
	reg.Register(&stubDefinition{key: "a/v1/A"})
}

func TestRegistryClear(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&stubDefinition{key: "a/v1/A"})
	reg.Clear()

	all := reg.AllDefinitions()
	if len(all) != 0 {
		t.Fatalf("expected 0 definitions after Clear, got %d", len(all))
	}
}

func TestDefinitionKeyGVK(t *testing.T) {
	key := CreateDefinitionKey("group", "v1", "Kind")
	gvk := key.GetGVK()
	if gvk.Group != "group" || gvk.Version != "v1" || gvk.Kind != "Kind" {
		t.Fatalf("unexpected GVK: %+v", gvk)
	}
}
