package k8s

import (
	"testing"

	"github.com/systemstart/nix-compose/pkg/eval"
)

func TestConvertJob_RestartNo(t *testing.T) {
	svc := eval.Service{
		Image:   "flyway:latest",
		Restart: "no",
	}
	m := convertJob("init-db", svc, nil, RenderOptions{Namespace: "test-ns"})

	job, ok := m.Object.(Job)
	if !ok {
		t.Fatal("expected Job")
	}
	if job.APIVersion != "batch/v1" {
		t.Errorf("apiVersion = %q, want batch/v1", job.APIVersion)
	}
	if job.Kind != "Job" {
		t.Errorf("kind = %q, want Job", job.Kind)
	}
	if job.Metadata.Name != "init-db" {
		t.Errorf("name = %q, want init-db", job.Metadata.Name)
	}
	if job.Metadata.Namespace != "test-ns" {
		t.Errorf("namespace = %q, want test-ns", job.Metadata.Namespace)
	}
	if job.Spec.Template.Spec.RestartPolicy != "Never" {
		t.Errorf("restartPolicy = %q, want Never", job.Spec.Template.Spec.RestartPolicy)
	}
	if m.Filename != "init-db-job.yaml" {
		t.Errorf("filename = %q, want init-db-job.yaml", m.Filename)
	}
}

func TestConvertJob_RestartOnFailure(t *testing.T) {
	svc := eval.Service{
		Image:   "worker:latest",
		Restart: "on-failure",
	}
	m := convertJob("worker", svc, nil, RenderOptions{Namespace: "default"})

	job := m.Object.(Job)
	if job.Spec.Template.Spec.RestartPolicy != "OnFailure" {
		t.Errorf("restartPolicy = %q, want OnFailure", job.Spec.Template.Spec.RestartPolicy)
	}
}

func TestConvertJob_Labels(t *testing.T) {
	svc := eval.Service{Image: "busybox", Restart: "no"}
	m := convertJob("task", svc, nil, RenderOptions{Namespace: "default"})

	job := m.Object.(Job)
	if job.Metadata.Labels["app.kubernetes.io/name"] != "task" {
		t.Errorf("missing app.kubernetes.io/name label")
	}
	if job.Metadata.Labels["app.kubernetes.io/managed-by"] != "nix-compose" {
		t.Errorf("missing app.kubernetes.io/managed-by label")
	}
	// Template labels should match.
	if job.Spec.Template.Metadata.Labels["app.kubernetes.io/name"] != "task" {
		t.Errorf("missing template app.kubernetes.io/name label")
	}
}

func TestConvertJob_Volumes(t *testing.T) {
	svc := eval.Service{
		Image:   "busybox",
		Restart: "no",
		Volumes: []string{"data:/mnt/data"},
	}
	compVols := map[string]eval.Volume{"data": {}}
	m := convertJob("task", svc, compVols, RenderOptions{Namespace: "default"})

	job := m.Object.(Job)
	if len(job.Spec.Template.Spec.Volumes) != 1 {
		t.Fatalf("expected 1 pod volume, got %d", len(job.Spec.Template.Spec.Volumes))
	}
	if job.Spec.Template.Spec.Volumes[0].PersistentVolumeClaim == nil {
		t.Error("expected PVC volume source")
	}
	c := job.Spec.Template.Spec.Containers[0]
	if len(c.VolumeMounts) != 1 || c.VolumeMounts[0].MountPath != "/mnt/data" {
		t.Errorf("volume mount = %+v, want /mnt/data", c.VolumeMounts)
	}
}

func TestConvertJob_InitContainers(t *testing.T) {
	svc := eval.Service{
		Image:   "app:latest",
		Restart: "no",
		XNixCompose: &eval.NixComposeExtended{
			InitContainers: []eval.InitContainer{
				{
					Name:    "setup",
					Image:   "busybox",
					Command: eval.CommandValue{Parts: []string{"sh", "-c", "echo hello"}},
				},
			},
		},
	}
	m := convertJob("task", svc, nil, RenderOptions{Namespace: "default"})

	job := m.Object.(Job)
	if len(job.Spec.Template.Spec.InitContainers) != 1 {
		t.Fatalf("expected 1 init container, got %d", len(job.Spec.Template.Spec.InitContainers))
	}
	if job.Spec.Template.Spec.InitContainers[0].Name != "setup" {
		t.Errorf("init name = %q, want setup", job.Spec.Template.Spec.InitContainers[0].Name)
	}
}
