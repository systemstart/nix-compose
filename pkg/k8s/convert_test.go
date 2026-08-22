package k8s

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/systemstart/nix-compose/pkg/eval"
)

func testdataPath(name string) string {
	_, f, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(f), "..", "..", "testdata", name)
}

func loadJSONFixture(t *testing.T, name string) *eval.Composition {
	t.Helper()
	data, err := os.ReadFile(testdataPath(name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	comp, err := eval.ParseComposition(data)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return comp
}

func countManifestTypes(manifests []Manifest) map[string]int {
	types := make(map[string]int)
	for _, m := range manifests {
		switch m.Object.(type) {
		case Deployment:
			types["Deployment"]++
		case Job:
			types["Job"]++
		case K8sService:
			types["Service"]++
		case Secret:
			types["Secret"]++
		case PersistentVolumeClaim:
			types["PVC"]++
		}
	}
	return types
}

func TestConvert_FullM3(t *testing.T) {
	comp := loadJSONFixture(t, "full-m3.json")

	secrets := map[string]map[string]string{
		"api": {
			"DATABASE_URL": "postgres://db:5432/app",
			"API_KEY":      "secret123",
		},
	}

	manifests := Convert(comp, secrets, RenderOptions{Namespace: "default"})

	if len(manifests) < 7 {
		t.Fatalf("expected at least 7 manifests, got %d", len(manifests))
	}

	types := countManifestTypes(manifests)

	if types["Deployment"] != 3 {
		t.Errorf("expected 3 Deployments, got %d", types["Deployment"])
	}
	if types["Service"] < 2 {
		t.Errorf("expected at least 2 Services, got %d", types["Service"])
	}
	if types["Secret"] != 1 {
		t.Errorf("expected 1 Secret, got %d", types["Secret"])
	}
	if types["PVC"] != 1 {
		t.Errorf("expected 1 PVC, got %d", types["PVC"])
	}
}

func TestConvert_Minimal(t *testing.T) {
	comp := loadJSONFixture(t, "minimal.json")
	manifests := Convert(comp, nil, RenderOptions{})

	// 1 Deployment + 1 Service (web has ports).
	if len(manifests) != 2 {
		t.Fatalf("expected 2 manifests, got %d", len(manifests))
	}

	// Namespace should default to "default".
	d := manifests[0].Object.(Deployment)
	if d.Metadata.Namespace != "default" {
		t.Errorf("namespace = %q, want default", d.Metadata.Namespace)
	}
}

func TestConvert_DeterministicOrder(t *testing.T) {
	comp := loadJSONFixture(t, "full-m3.json")
	secrets := map[string]map[string]string{
		"api": {"KEY": "val"},
	}

	m1 := Convert(comp, secrets, RenderOptions{Namespace: "default"})
	m2 := Convert(comp, secrets, RenderOptions{Namespace: "default"})

	if len(m1) != len(m2) {
		t.Fatalf("manifest count differs: %d vs %d", len(m1), len(m2))
	}

	for i := range m1 {
		if m1[i].Filename != m2[i].Filename {
			t.Errorf("manifest[%d] filename differs: %q vs %q", i, m1[i].Filename, m2[i].Filename)
		}
	}
}

func TestConvert_CustomNamespace(t *testing.T) {
	comp := loadJSONFixture(t, "minimal.json")
	manifests := Convert(comp, nil, RenderOptions{Namespace: "production"})

	d := manifests[0].Object.(Deployment)
	if d.Metadata.Namespace != "production" {
		t.Errorf("namespace = %q, want production", d.Metadata.Namespace)
	}
}

func TestConvert_InitContainersNative(t *testing.T) {
	comp := loadJSONFixture(t, "full-m3.json")
	manifests := Convert(comp, nil, RenderOptions{Namespace: "default"})

	// Find api deployment.
	var apiDeploy *Deployment
	for _, m := range manifests {
		d, ok := m.Object.(Deployment)
		if ok && d.Metadata.Name == "api" {
			apiDeploy = &d
			break
		}
	}
	if apiDeploy == nil {
		t.Fatal("missing api deployment")
		return
	}

	inits := apiDeploy.Spec.Template.Spec.InitContainers
	if len(inits) != 1 {
		t.Fatalf("expected 1 init container, got %d", len(inits))
	}
	if inits[0].Name != "migrate" {
		t.Errorf("init name = %q, want migrate", inits[0].Name)
	}
}

func TestConvert_FullM3_GoldenFile(t *testing.T) {
	comp := loadJSONFixture(t, "full-m3.json")
	secrets := map[string]map[string]string{
		"api": {
			"API_KEY":      "secret123",
			"DATABASE_URL": "postgres://db:5432/app",
		},
	}

	manifests := Convert(comp, secrets, RenderOptions{Namespace: "default"})
	var buf bytes.Buffer
	if err := WriteMultiDoc(&buf, manifests); err != nil {
		t.Fatalf("WriteMultiDoc: %v", err)
	}

	goldenPath := testdataPath("k8s/full-m3.yaml")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Log("Updated golden file")
		return
	}

	golden, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden file: %v (run with UPDATE_GOLDEN=1 to create)", err)
	}
	if !bytes.Equal(buf.Bytes(), golden) {
		t.Errorf("output differs from golden file.\nGot:\n%s\nWant:\n%s", buf.String(), string(golden))
	}
}

func TestConvert_Minimal_GoldenFile(t *testing.T) {
	comp := loadJSONFixture(t, "minimal.json")

	manifests := Convert(comp, nil, RenderOptions{Namespace: "default"})
	var buf bytes.Buffer
	if err := WriteMultiDoc(&buf, manifests); err != nil {
		t.Fatalf("WriteMultiDoc: %v", err)
	}

	goldenPath := testdataPath("k8s/minimal.yaml")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Log("Updated golden file")
		return
	}

	golden, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden file: %v (run with UPDATE_GOLDEN=1 to create)", err)
	}
	if !bytes.Equal(buf.Bytes(), golden) {
		t.Errorf("output differs from golden file.\nGot:\n%s\nWant:\n%s", buf.String(), string(golden))
	}
}

func TestConvert_OutputContainsAllFeatures(t *testing.T) {
	comp := loadJSONFixture(t, "full-m3.json")
	secrets := map[string]map[string]string{
		"api": {"API_KEY": "secret123"},
	}

	manifests := Convert(comp, secrets, RenderOptions{Namespace: "default"})
	var buf bytes.Buffer
	if err := WriteMultiDoc(&buf, manifests); err != nil {
		t.Fatalf("WriteMultiDoc: %v", err)
	}
	output := buf.String()

	features := []string{
		"apps/v1",                // Deployment apiVersion
		"Deployment",             // Kind
		"Service",                // Kind
		"Secret",                 // Kind
		"PersistentVolumeClaim",  // Kind
		"initContainers:",        // Native init containers
		"livenessProbe:",         // Liveness probe
		"resources:",             // Resource limits
		"secretRef:",             // envFrom secret reference
		"app.kubernetes.io/name", // Standard labels
	}

	for _, f := range features {
		if !strings.Contains(output, f) {
			t.Errorf("output missing feature: %q", f)
		}
	}
}

func TestConvert_RestartNo_ProducesJob(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"init-db": {
				Image:   "flyway:latest",
				Restart: "no",
			},
		},
	}
	manifests := Convert(comp, nil, RenderOptions{Namespace: "default"})

	types := countManifestTypes(manifests)
	if types["Job"] != 1 {
		t.Errorf("expected 1 Job, got %d", types["Job"])
	}
	if types["Deployment"] != 0 {
		t.Errorf("expected 0 Deployments, got %d", types["Deployment"])
	}

	job := manifests[0].Object.(Job)
	if job.Spec.Template.Spec.RestartPolicy != "Never" {
		t.Errorf("restartPolicy = %q, want Never", job.Spec.Template.Spec.RestartPolicy)
	}
}

func TestConvert_RestartOnFailure_ProducesJob(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"worker": {
				Image:   "worker:latest",
				Restart: "on-failure",
			},
		},
	}
	manifests := Convert(comp, nil, RenderOptions{Namespace: "default"})

	types := countManifestTypes(manifests)
	if types["Job"] != 1 {
		t.Errorf("expected 1 Job, got %d", types["Job"])
	}

	job := manifests[0].Object.(Job)
	if job.Spec.Template.Spec.RestartPolicy != "OnFailure" {
		t.Errorf("restartPolicy = %q, want OnFailure", job.Spec.Template.Spec.RestartPolicy)
	}
}

func TestConvert_JobDoesNotEmitService(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"init-db": {
				Image:   "flyway:latest",
				Restart: "no",
				Ports:   []string{"8080:80"},
			},
		},
	}
	manifests := Convert(comp, nil, RenderOptions{Namespace: "default"})

	types := countManifestTypes(manifests)
	if types["Service"] != 0 {
		t.Errorf("expected 0 Services for Job workload, got %d", types["Service"])
	}
	if types["Job"] != 1 {
		t.Errorf("expected 1 Job, got %d", types["Job"])
	}
}
