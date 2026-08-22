package manifest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManifest_Validate_AllValid(t *testing.T) {
	m := Manifest{
		APIVersion: "cri.orchestrator.io/v1",
		Kind:       "Service",
		Metadata:   Metadata{Name: "web"},
	}
	errs := m.Validate()
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors, got %d: %v", len(errs), errs)
	}
}

func TestManifest_Validate_MissingAPIVersion(t *testing.T) {
	m := Manifest{Kind: "Service", Metadata: Metadata{Name: "web"}}
	errs := m.Validate()
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errs))
	}
}

func TestManifest_Validate_MissingKind(t *testing.T) {
	m := Manifest{APIVersion: "v1", Metadata: Metadata{Name: "web"}}
	errs := m.Validate()
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errs))
	}
}

func TestManifest_Validate_MissingName(t *testing.T) {
	m := Manifest{APIVersion: "v1", Kind: "Service"}
	errs := m.Validate()
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errs))
	}
}

func TestManifest_Validate_AllMissing(t *testing.T) {
	m := Manifest{}
	errs := m.Validate()
	if len(errs) != 3 {
		t.Fatalf("expected 3 errors, got %d", len(errs))
	}
}

func TestLoadFile_SingleDocument(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "test.yaml")
	content := `apiVersion: v1
kind: Service
metadata:
  name: web
spec:
  image: nginx
`

	if err := os.WriteFile(f, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	manifests, err := LoadFile(f)
	if err != nil {
		t.Fatalf("LoadFile failed: %v", err)
	}
	if len(manifests) != 1 {
		t.Fatalf("expected 1 manifest, got %d", len(manifests))
	}
	if manifests[0].APIVersion != "v1" {
		t.Errorf("apiVersion = %q", manifests[0].APIVersion)
	}
	if manifests[0].Kind != "Service" {
		t.Errorf("kind = %q", manifests[0].Kind)
	}
	if manifests[0].Metadata.Name != "web" {
		t.Errorf("name = %q", manifests[0].Metadata.Name)
	}
}

func TestLoadFile_MultiDocument(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "multi.yaml")
	content := `apiVersion: v1
kind: Service
metadata:
  name: web
---
apiVersion: v1
kind: Image
metadata:
  name: nginx
`
	if err := os.WriteFile(f, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	manifests, err := LoadFile(f)
	if err != nil {
		t.Fatalf("LoadFile failed: %v", err)
	}
	if len(manifests) != 2 {
		t.Fatalf("expected 2 manifests, got %d", len(manifests))
	}
}

func TestLoadFile_TrailingSeparator(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "trailing.yaml")
	content := `apiVersion: v1
kind: Service
metadata:
  name: web
---
`
	if err := os.WriteFile(f, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	manifests, err := LoadFile(f)
	if err != nil {
		t.Fatalf("LoadFile failed: %v", err)
	}
	if len(manifests) != 1 {
		t.Fatalf("expected 1 manifest (trailing --- skipped), got %d", len(manifests))
	}
}

func TestLoadFile_NotFound(t *testing.T) {
	_, err := LoadFile("/nonexistent/file.yaml")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestLoadFile_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(f, []byte(":\n  :\n    [invalid"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadFile(f)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestLoadPath_SingleFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "test.yaml")
	content := `apiVersion: v1
kind: Service
metadata:
  name: web
`
	if err := os.WriteFile(f, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	manifests, err := LoadPath(f)
	if err != nil {
		t.Fatalf("LoadPath failed: %v", err)
	}
	if len(manifests) != 1 {
		t.Fatalf("expected 1 manifest, got %d", len(manifests))
	}
}

func TestLoadPath_Directory(t *testing.T) {
	dir := t.TempDir()

	for _, name := range []string{"a.yaml", "b.yml"} {
		content := "apiVersion: v1\nkind: Service\nmetadata:\n  name: " + name + "\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Non-YAML file should be skipped
	if err := os.WriteFile(filepath.Join(dir, "c.txt"), []byte("not yaml"), 0o644); err != nil {
		t.Fatal(err)
	}

	manifests, err := LoadPath(dir)
	if err != nil {
		t.Fatalf("LoadPath failed: %v", err)
	}
	if len(manifests) != 2 {
		t.Fatalf("expected 2 manifests, got %d", len(manifests))
	}
}

func TestLoadPath_NotFound(t *testing.T) {
	_, err := LoadPath("/nonexistent/dir")
	if err == nil {
		t.Fatal("expected error for nonexistent path")
	}
}

func TestLoadPath_NestedDirectory(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	content := "apiVersion: v1\nkind: Service\nmetadata:\n  name: nested\n"
	if err := os.WriteFile(filepath.Join(sub, "nested.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	manifests, err := LoadPath(dir)
	if err != nil {
		t.Fatalf("LoadPath failed: %v", err)
	}
	if len(manifests) != 1 {
		t.Fatalf("expected 1 manifest from nested dir, got %d", len(manifests))
	}
}

func TestValidateAll_NoErrors(t *testing.T) {
	manifests := []Manifest{
		{APIVersion: "v1", Kind: "Service", Metadata: Metadata{Name: "web"}},
		{APIVersion: "v1", Kind: "Image", Metadata: Metadata{Name: "nginx"}},
	}
	errs := ValidateAll(manifests)
	if len(errs) != 0 {
		t.Fatalf("expected 0 error groups, got %d", len(errs))
	}
}

func TestValidateAll_WithErrors(t *testing.T) {
	manifests := []Manifest{
		{APIVersion: "v1", Kind: "Service", Metadata: Metadata{Name: "web"}},
		{Kind: "Image"}, // missing apiVersion and name
	}
	errs := ValidateAll(manifests)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error group, got %d", len(errs))
	}
	// The key should be "document 2" since name is empty
	if _, ok := errs["document 2"]; !ok {
		t.Errorf("expected error group for 'document 2', got keys: %v", errs)
	}
}

func TestValidateAll_NamedError(t *testing.T) {
	manifests := []Manifest{
		{Kind: "Service", Metadata: Metadata{Name: "web"}}, // missing apiVersion
	}
	errs := ValidateAll(manifests)
	if _, ok := errs["web"]; !ok {
		t.Errorf("expected error group for 'web', got keys: %v", errs)
	}
}

func TestMetadata_Labels(t *testing.T) {
	m := Manifest{
		APIVersion: "v1",
		Kind:       "Service",
		Metadata: Metadata{
			Name:   "web",
			Labels: map[string]string{"app": "myapp"},
		},
	}
	if m.Metadata.Labels["app"] != "myapp" {
		t.Errorf("label 'app' = %q", m.Metadata.Labels["app"])
	}
}

func TestMetadata_Namespace(t *testing.T) {
	m := Manifest{
		APIVersion: "v1",
		Kind:       "Service",
		Metadata: Metadata{
			Name:      "web",
			Namespace: "production",
		},
	}
	if m.Metadata.Namespace != "production" {
		t.Errorf("namespace = %q", m.Metadata.Namespace)
	}
}
