package portfwd

import (
	"testing"

	"github.com/systemstart/nix-compose/pkg/eval"
)

func TestExtractPorts_Basic(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Ports: []string{"8080:80"}},
		},
	}

	mappings := ExtractPorts(comp)
	if len(mappings) != 1 {
		t.Fatalf("got %d mappings, want 1", len(mappings))
	}

	m := mappings[0]
	if m.HostPort != 8080 {
		t.Errorf("HostPort = %d, want 8080", m.HostPort)
	}
	if m.VMPort != 8080 {
		t.Errorf("VMPort = %d, want 8080", m.VMPort)
	}
	if m.Protocol != "tcp" {
		t.Errorf("Protocol = %q, want tcp", m.Protocol)
	}
}

func TestExtractPorts_HostIP(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Ports: []string{"127.0.0.1:9090:80"}},
		},
	}

	mappings := ExtractPorts(comp)
	if len(mappings) != 1 {
		t.Fatalf("got %d mappings, want 1", len(mappings))
	}
	if mappings[0].HostIP != "127.0.0.1" {
		t.Errorf("HostIP = %q, want 127.0.0.1", mappings[0].HostIP)
	}
	if mappings[0].HostPort != 9090 {
		t.Errorf("HostPort = %d, want 9090", mappings[0].HostPort)
	}
}

func TestExtractPorts_UDPSkipped(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"dns": {Ports: []string{"53:53/udp"}},
		},
	}

	mappings := ExtractPorts(comp)
	if len(mappings) != 0 {
		t.Fatalf("got %d mappings, want 0 (UDP should be skipped)", len(mappings))
	}
}

func TestExtractPorts_NamedPorts(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"api": {
				XNixCompose: &eval.NixComposeExtended{
					NamedPorts: []eval.NamedPort{
						{Name: "http", HostPort: 8080, ContainerPort: 3000, Protocol: "tcp"},
						{Name: "metrics", HostPort: 9090, ContainerPort: 9090},
					},
				},
			},
		},
	}

	mappings := ExtractPorts(comp)
	if len(mappings) != 2 {
		t.Fatalf("got %d mappings, want 2", len(mappings))
	}

	if mappings[0].HostPort != 8080 && mappings[1].HostPort != 8080 {
		t.Error("expected a mapping with HostPort 8080")
	}
}

func TestExtractPorts_ContainerOnlySkipped(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"internal": {
				XNixCompose: &eval.NixComposeExtended{
					NamedPorts: []eval.NamedPort{
						{Name: "internal", HostPort: 0, ContainerPort: 8082},
					},
				},
			},
		},
	}

	mappings := ExtractPorts(comp)
	if len(mappings) != 0 {
		t.Fatalf("got %d mappings, want 0 (container-only port)", len(mappings))
	}
}

func TestExtractPorts_MultipleServices(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web":   {Ports: []string{"8080:80"}},
			"api":   {Ports: []string{"3000:3000"}},
			"redis": {Ports: []string{"6379:6379"}},
		},
	}

	mappings := ExtractPorts(comp)
	if len(mappings) != 3 {
		t.Fatalf("got %d mappings, want 3", len(mappings))
	}
}

func TestExtractPorts_EmptyComposition(t *testing.T) {
	mappings := ExtractPorts(nil)
	if len(mappings) != 0 {
		t.Fatalf("got %d mappings from nil, want 0", len(mappings))
	}

	mappings = ExtractPorts(&eval.Composition{})
	if len(mappings) != 0 {
		t.Fatalf("got %d mappings from empty, want 0", len(mappings))
	}
}

func TestExtractPorts_SinglePort(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Ports: []string{"80"}},
		},
	}

	mappings := ExtractPorts(comp)
	if len(mappings) != 1 {
		t.Fatalf("got %d mappings, want 1", len(mappings))
	}
	if mappings[0].HostPort != 80 {
		t.Errorf("HostPort = %d, want 80", mappings[0].HostPort)
	}
	if mappings[0].VMPort != 80 {
		t.Errorf("VMPort = %d, want 80", mappings[0].VMPort)
	}
}
