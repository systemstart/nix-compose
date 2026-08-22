package resources

import (
	"testing"
)

func TestDefinitionKeys(t *testing.T) {
	keys := []struct {
		name string
		key  string
	}{
		{"ImageKey", string(ImageKey)},
		{"NetworkKey", string(NetworkKey)},
		{"VolumeKey", string(VolumeKey)},
		{"ContainerKey", string(ContainerKey)},
		{"ServiceKey", string(ServiceKey)},
		{"ProjectKey", string(ProjectKey)},
	}

	for _, k := range keys {
		t.Run(k.name, func(t *testing.T) {
			gvk := ImageKey.GetGVK()
			if gvk.Group != "cri.orchestrator.io" {
				t.Fatalf("unexpected group: %s", gvk.Group)
			}
		})
	}

	// Verify all keys are distinct
	seen := make(map[string]bool)
	for _, k := range keys {
		if seen[k.key] {
			t.Fatalf("duplicate key: %s", k.key)
		}
		seen[k.key] = true
	}
}

func TestImageDefinitionNilClient(t *testing.T) {
	d := &ImageDefinition{Client: nil}
	if d.GetKey() != ImageKey {
		t.Fatalf("unexpected key: %s", d.GetKey())
	}
	if d.GetMappings() != nil {
		t.Fatal("expected nil mappings")
	}
}

func TestVolumeDeleteIsNoop(t *testing.T) {
	d := &VolumeDefinition{Store: nil}
	err := d.Delete(nil)
	if err != nil {
		t.Fatalf("VolumeDefinition.Delete should be a no-op, got: %v", err)
	}
}

func TestProjectApplyIsNoop(t *testing.T) {
	inst := &ProjectInstance{
		Spec:   ProjectSpec{Name: "test-project"},
		client: nil,
	}
	err := inst.Apply()
	if err != nil {
		t.Fatalf("ProjectInstance.Apply should be a no-op, got: %v", err)
	}
	if inst.GetId() != "test-project" {
		t.Fatalf("unexpected id: %s", inst.GetId())
	}
}
