package k8s

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestWriteKustomization(t *testing.T) {
	dir := t.TempDir()
	filenames := []string{
		"api-secret.yaml",
		"db-data-pvc.yaml",
		"api-deployment.yaml",
		"api-service.yaml",
		"db-deployment.yaml",
		"web-deployment.yaml",
		"web-service.yaml",
	}

	if err := writeKustomization(dir, filenames); err != nil {
		t.Fatalf("writeKustomization: %v", err)
	}

	path := filepath.Join(dir, "kustomization.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read kustomization: %v", err)
	}

	var k Kustomization
	if err := yaml.Unmarshal(data, &k); err != nil {
		t.Fatalf("parse kustomization: %v", err)
	}

	if k.APIVersion != "kustomize.config.k8s.io/v1beta1" {
		t.Errorf("apiVersion = %q, want kustomize.config.k8s.io/v1beta1", k.APIVersion)
	}
	if k.Kind != "Kustomization" {
		t.Errorf("kind = %q, want Kustomization", k.Kind)
	}
	if len(k.Resources) != len(filenames) {
		t.Errorf("expected %d resources, got %d", len(filenames), len(k.Resources))
	}
}

func TestWriteKustomization_Empty(t *testing.T) {
	dir := t.TempDir()
	if err := writeKustomization(dir, nil); err != nil {
		t.Fatalf("writeKustomization: %v", err)
	}

	path := filepath.Join(dir, "kustomization.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var k Kustomization
	if err := yaml.Unmarshal(data, &k); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(k.Resources) != 0 {
		t.Errorf("expected empty resources, got %v", k.Resources)
	}
}
