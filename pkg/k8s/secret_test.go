package k8s

import (
	"testing"
)

func TestConvertSecret_Empty(t *testing.T) {
	m := convertSecret("api", nil, RenderOptions{Namespace: "default"})
	if m != nil {
		t.Error("expected nil for empty secrets")
	}
}

func TestConvertSecret_EmptyMap(t *testing.T) {
	m := convertSecret("api", map[string]string{}, RenderOptions{Namespace: "default"})
	if m != nil {
		t.Error("expected nil for empty map")
	}
}

func TestConvertSecret_WithSecrets(t *testing.T) {
	secrets := map[string]string{
		"API_KEY":      "secret123",
		"DATABASE_URL": "postgres://localhost/db",
	}
	m := convertSecret("api", secrets, RenderOptions{Namespace: "prod"})
	if m == nil {
		t.Fatal("expected non-nil manifest")
		return
	}

	s, ok := m.Object.(Secret)
	if !ok {
		t.Fatal("expected Secret")
	}
	if s.Metadata.Name != "api-secrets" {
		t.Errorf("name = %q, want api-secrets", s.Metadata.Name)
	}
	if s.Metadata.Namespace != "prod" {
		t.Errorf("namespace = %q, want prod", s.Metadata.Namespace)
	}
	if s.Kind != "Secret" {
		t.Errorf("kind = %q, want Secret", s.Kind)
	}
	if s.APIVersion != "v1" {
		t.Errorf("apiVersion = %q, want v1", s.APIVersion)
	}
	if len(s.StringData) != 2 {
		t.Errorf("expected 2 stringData entries, got %d", len(s.StringData))
	}
	if s.StringData["API_KEY"] != "secret123" {
		t.Errorf("API_KEY = %q, want secret123", s.StringData["API_KEY"])
	}
	if m.Filename != "api-secret.yaml" {
		t.Errorf("filename = %q, want api-secret.yaml", m.Filename)
	}
}

func TestConvertSecret_Labels(t *testing.T) {
	secrets := map[string]string{"KEY": "val"}
	m := convertSecret("web", secrets, RenderOptions{Namespace: "default"})
	if m == nil {
		t.Fatal("expected non-nil manifest")
		return
	}
	s := m.Object.(Secret)
	if s.Metadata.Labels["app.kubernetes.io/managed-by"] != "nix-compose" {
		t.Error("missing managed-by label")
	}
}
