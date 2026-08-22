package k8s

import (
	"testing"
)

func TestConvertPVC_Default(t *testing.T) {
	m := convertPVC("db-data", RenderOptions{Namespace: "prod"})
	pvc, ok := m.Object.(PersistentVolumeClaim)
	if !ok {
		t.Fatal("expected PersistentVolumeClaim")
	}
	if pvc.Metadata.Name != "db-data" {
		t.Errorf("name = %q, want db-data", pvc.Metadata.Name)
	}
	if pvc.Metadata.Namespace != "prod" {
		t.Errorf("namespace = %q, want prod", pvc.Metadata.Namespace)
	}
	if pvc.Kind != "PersistentVolumeClaim" {
		t.Errorf("kind = %q, want PersistentVolumeClaim", pvc.Kind)
	}
	if pvc.APIVersion != "v1" {
		t.Errorf("apiVersion = %q, want v1", pvc.APIVersion)
	}
	if len(pvc.Spec.AccessModes) != 1 || pvc.Spec.AccessModes[0] != "ReadWriteOnce" {
		t.Errorf("accessModes = %v, want [ReadWriteOnce]", pvc.Spec.AccessModes)
	}
	if pvc.Spec.Resources.Requests["storage"] != "1Gi" {
		t.Errorf("storage = %q, want 1Gi", pvc.Spec.Resources.Requests["storage"])
	}
	if m.Filename != "db-data-pvc.yaml" {
		t.Errorf("filename = %q, want db-data-pvc.yaml", m.Filename)
	}
}

func TestConvertPVC_Labels(t *testing.T) {
	m := convertPVC("data", RenderOptions{Namespace: "default"})
	pvc := m.Object.(PersistentVolumeClaim)
	if pvc.Metadata.Labels["app.kubernetes.io/managed-by"] != "nix-compose" {
		t.Error("missing managed-by label")
	}
}
