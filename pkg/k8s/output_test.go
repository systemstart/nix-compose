package k8s

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func testManifests() []Manifest {
	return []Manifest{
		{
			Object: Secret{
				TypeMeta: TypeMeta{APIVersion: "v1", Kind: "Secret"},
				Metadata: ObjectMeta{Name: "api-secrets", Namespace: "default"},
				StringData: map[string]string{
					"API_KEY": "test",
				},
			},
			Filename: "api-secret.yaml",
		},
		{
			Object: Deployment{
				TypeMeta: TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
				Metadata: ObjectMeta{Name: "web", Namespace: "default"},
				Spec: DeploymentSpec{
					Replicas: 1,
					Selector: &LabelSelector{MatchLabels: map[string]string{"app": "web"}},
					Template: PodTemplateSpec{
						Spec: PodSpec{
							Containers: []Container{{Name: "web", Image: "nginx"}},
						},
					},
				},
			},
			Filename: "web-deployment.yaml",
		},
	}
}

func TestWriteMultiDoc(t *testing.T) {
	manifests := testManifests()
	var buf bytes.Buffer
	if err := WriteMultiDoc(&buf, manifests); err != nil {
		t.Fatalf("WriteMultiDoc: %v", err)
	}

	output := buf.String()

	// Should have document separator between manifests.
	if !strings.Contains(output, "---") {
		t.Error("missing document separator")
	}

	// Should contain both manifests.
	if !strings.Contains(output, "Secret") {
		t.Error("missing Secret manifest")
	}
	if !strings.Contains(output, "Deployment") {
		t.Error("missing Deployment manifest")
	}

	// First manifest should NOT be preceded by ---.
	if strings.HasPrefix(output, "---") {
		t.Error("should not start with ---")
	}
}

func TestWriteMultiDoc_Single(t *testing.T) {
	manifests := []Manifest{testManifests()[0]}
	var buf bytes.Buffer
	if err := WriteMultiDoc(&buf, manifests); err != nil {
		t.Fatalf("WriteMultiDoc: %v", err)
	}
	if strings.Contains(buf.String(), "---") {
		t.Error("single manifest should not have separator")
	}
}

func TestWriteMultiDoc_Empty(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteMultiDoc(&buf, nil); err != nil {
		t.Fatalf("WriteMultiDoc: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected empty output, got %q", buf.String())
	}
}

func TestWriteDirectory(t *testing.T) {
	dir := t.TempDir()
	outDir := filepath.Join(dir, "k8s")
	manifests := testManifests()

	if err := WriteDirectory(outDir, manifests); err != nil {
		t.Fatalf("WriteDirectory: %v", err)
	}

	// Verify each file was written.
	for _, m := range manifests {
		path := filepath.Join(outDir, m.Filename)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("missing file %s: %v", m.Filename, err)
		}
		// Verify it's valid YAML.
		var obj map[string]interface{}
		if err := yaml.Unmarshal(data, &obj); err != nil {
			t.Errorf("invalid YAML in %s: %v", m.Filename, err)
		}
	}

	// Verify kustomization.yaml was generated.
	kustPath := filepath.Join(outDir, "kustomization.yaml")
	data, err := os.ReadFile(kustPath)
	if err != nil {
		t.Fatalf("missing kustomization.yaml: %v", err)
	}
	var k Kustomization
	if err := yaml.Unmarshal(data, &k); err != nil {
		t.Fatalf("invalid kustomization.yaml: %v", err)
	}
	if len(k.Resources) != len(manifests) {
		t.Errorf("expected %d resources, got %d", len(manifests), len(k.Resources))
	}
}

func TestWriteDirectory_CreatesDir(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "a", "b", "c")
	manifests := []Manifest{testManifests()[0]}

	if err := WriteDirectory(nested, manifests); err != nil {
		t.Fatalf("WriteDirectory: %v", err)
	}

	if _, err := os.Stat(filepath.Join(nested, "api-secret.yaml")); err != nil {
		t.Errorf("expected file to exist: %v", err)
	}
}

func TestMarshalManifest(t *testing.T) {
	m := testManifests()[0]
	data, err := MarshalManifest(m)
	if err != nil {
		t.Fatalf("MarshalManifest: %v", err)
	}
	if !strings.Contains(string(data), "Secret") {
		t.Error("expected Secret in output")
	}
	if !strings.Contains(string(data), "api-secrets") {
		t.Error("expected api-secrets in output")
	}
}
