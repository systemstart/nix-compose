package k8s

import (
	"testing"

	"github.com/systemstart/nix-compose/pkg/eval"
)

func TestConvertK8sService_NoPorts(t *testing.T) {
	svc := eval.Service{Image: "worker:latest"}
	m := convertK8sService("worker", svc, RenderOptions{Namespace: "default"})
	if m != nil {
		t.Error("expected nil for service with no ports")
	}
}

func TestConvertK8sService_NamedPorts(t *testing.T) {
	svc := eval.Service{
		Image: "nginx",
		XNixCompose: &eval.NixComposeExtended{
			NamedPorts: []eval.NamedPort{
				{Name: "http", ContainerPort: 80},
				{Name: "metrics", ContainerPort: 9090},
			},
		},
	}
	m := convertK8sService("web", svc, RenderOptions{Namespace: "default"})
	if m == nil {
		t.Fatal("expected non-nil manifest")
		return
	}

	k8sSvc, ok := m.Object.(K8sService)
	if !ok {
		t.Fatal("expected K8sService")
	}
	if k8sSvc.Metadata.Name != "web" {
		t.Errorf("name = %q, want web", k8sSvc.Metadata.Name)
	}
	if k8sSvc.Spec.Selector["app.kubernetes.io/name"] != "web" {
		t.Error("missing selector")
	}
	if len(k8sSvc.Spec.Ports) != 2 {
		t.Fatalf("expected 2 ports, got %d", len(k8sSvc.Spec.Ports))
	}
	if k8sSvc.Spec.Ports[0].Name != "http" {
		t.Errorf("port[0].name = %q, want http", k8sSvc.Spec.Ports[0].Name)
	}
	if k8sSvc.Spec.Ports[0].TargetPort != "http" {
		t.Errorf("port[0].targetPort = %q, want http", k8sSvc.Spec.Ports[0].TargetPort)
	}
	if k8sSvc.Spec.Ports[0].Port != 80 {
		t.Errorf("port[0].port = %d, want 80", k8sSvc.Spec.Ports[0].Port)
	}
	if m.Filename != "web-service.yaml" {
		t.Errorf("filename = %q, want web-service.yaml", m.Filename)
	}
}

func TestConvertK8sService_StringPorts(t *testing.T) {
	svc := eval.Service{
		Image: "nginx",
		Ports: []string{"8080:80", "53:53/udp"},
	}
	m := convertK8sService("web", svc, RenderOptions{Namespace: "default"})
	if m == nil {
		t.Fatal("expected non-nil manifest")
		return
	}

	k8sSvc := m.Object.(K8sService)
	if len(k8sSvc.Spec.Ports) != 2 {
		t.Fatalf("expected 2 ports, got %d", len(k8sSvc.Spec.Ports))
	}
	if k8sSvc.Spec.Ports[0].Port != 80 {
		t.Errorf("port[0].port = %d, want 80", k8sSvc.Spec.Ports[0].Port)
	}
	if k8sSvc.Spec.Ports[0].TargetPort != "80" {
		t.Errorf("port[0].targetPort = %q, want 80", k8sSvc.Spec.Ports[0].TargetPort)
	}
	if k8sSvc.Spec.Ports[1].Protocol != "UDP" {
		t.Errorf("port[1].protocol = %q, want UDP", k8sSvc.Spec.Ports[1].Protocol)
	}
}

func TestConvertK8sService_Labels(t *testing.T) {
	svc := eval.Service{
		Image: "nginx",
		Ports: []string{"80:80"},
	}
	m := convertK8sService("web", svc, RenderOptions{Namespace: "prod"})
	if m == nil {
		t.Fatal("expected non-nil manifest")
		return
	}

	k8sSvc := m.Object.(K8sService)
	if k8sSvc.Metadata.Namespace != "prod" {
		t.Errorf("namespace = %q, want prod", k8sSvc.Metadata.Namespace)
	}
	if k8sSvc.Metadata.Labels["app.kubernetes.io/name"] != "web" {
		t.Error("missing app.kubernetes.io/name label")
	}
}

func TestConvertK8sService_NamedPortUDP(t *testing.T) {
	svc := eval.Service{
		Image: "dns-server",
		XNixCompose: &eval.NixComposeExtended{
			NamedPorts: []eval.NamedPort{
				{Name: "dns", ContainerPort: 53, Protocol: "udp"},
			},
		},
	}
	m := convertK8sService("dns", svc, RenderOptions{Namespace: "default"})
	if m == nil {
		t.Fatal("expected non-nil manifest")
		return
	}
	k8sSvc := m.Object.(K8sService)
	if k8sSvc.Spec.Ports[0].Protocol != "UDP" {
		t.Errorf("protocol = %q, want UDP", k8sSvc.Spec.Ports[0].Protocol)
	}
}
