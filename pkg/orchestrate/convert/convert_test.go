package convert

import (
	"testing"

	"github.com/systemstart/nix-compose/pkg/eval"
	"github.com/systemstart/nix-compose/pkg/orchestrate/resources"
	"github.com/systemstart/nix-compose/pkg/orchestrate/typing"
)

func TestConvert_Minimal(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx:latest"},
		},
	}
	res, err := Convert(comp, Options{Project: "myapp"})
	if err != nil {
		t.Fatal(err)
	}

	// Expect 4 manifests: Project, Network, Image, Service.
	if got := len(res.Manifests); got != 4 {
		t.Fatalf("manifests: got %d, want 4", got)
	}
	assertManifest(t, res, 0, "Project", "myapp")
	assertManifest(t, res, 1, "Network", "myapp")
	assertManifest(t, res, 2, "Image", "nginx:latest")
	assertManifest(t, res, 3, "Service", "myapp/web")

	// Expect 3 implicit edges: Service→Project, Service→Image, Service→Network.
	if got := len(res.Edges); got != 3 {
		t.Fatalf("edges: got %d, want 3", got)
	}
	assertEdge(t, res.Edges[0], "myapp/web", resources.ServiceKey, "myapp", resources.ProjectKey, "")
	assertEdge(t, res.Edges[1], "myapp/web", resources.ServiceKey, "nginx:latest", resources.ImageKey, "")
	assertEdge(t, res.Edges[2], "myapp/web", resources.ServiceKey, "myapp", resources.NetworkKey, "")
}

func TestConvert_TwoServicesWithDeps(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {
				Image:   "nginx:latest",
				Volumes: []string{"data:/var/data"},
				DependsOn: eval.DependsOnValue{
					Entries: map[string]eval.DependsOnEntry{
						"db": {Condition: "service_healthy"},
					},
				},
			},
			"db": {
				Image: "postgres:16",
			},
		},
		Volumes: map[string]eval.Volume{
			"data": {},
		},
	}
	res, err := Convert(comp, Options{Project: "myapp"})
	if err != nil {
		t.Fatal(err)
	}

	// Expect: Project, Network, 2 Images, 1 Volume, 2 Services = 7 manifests.
	if got := len(res.Manifests); got != 7 {
		t.Fatalf("manifests: got %d, want 7", got)
	}

	requireEdge(t, res.Edges, "myapp/web", "myapp/db", "healthy", "")
	requireEdge(t, res.Edges, "myapp/web", "myapp/data", "", string(resources.VolumeKey))
}

func TestConvert_HostNetworkMode(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx:latest", NetworkMode: "host"},
		},
	}
	res, err := Convert(comp, Options{Project: "myapp", UseCNI: true})
	if err != nil {
		t.Fatal(err)
	}

	// No Network manifest: Project, Image, Service = 3.
	if got := len(res.Manifests); got != 3 {
		t.Fatalf("manifests: got %d, want 3", got)
	}
	for _, m := range res.Manifests {
		if m.Kind == "Network" {
			t.Fatal("unexpected Network manifest for host-mode-only composition")
		}
	}

	// No Network edge.
	for _, e := range res.Edges {
		if e.To.GetKey() == resources.NetworkKey {
			t.Fatal("unexpected Network edge for host-mode service")
		}
	}

	// UseCNI should be false in the ContainerSpec despite opts.UseCNI=true.
	svcSpec := res.Manifests[2].Spec.(resources.ServiceSpec)
	if svcSpec.Container.UseCNI {
		t.Error("UseCNI should be false for host network mode")
	}
}

func TestConvert_UseCNI(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx:latest"},
		},
	}
	res, err := Convert(comp, Options{Project: "myapp", UseCNI: true})
	if err != nil {
		t.Fatal(err)
	}

	svcSpec := res.Manifests[3].Spec.(resources.ServiceSpec)
	if !svcSpec.Container.UseCNI {
		t.Error("UseCNI should be true when opts.UseCNI=true and not host mode")
	}
}

func TestConvert_WithInitContainers(t *testing.T) {
	// Simulate pre-synthesized init containers:
	// web-init-0 runs first, web depends on web-init-0 with completed condition.
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {
				Image: "nginx:latest",
				DependsOn: eval.DependsOnValue{
					Entries: map[string]eval.DependsOnEntry{
						"web-init-0": {Condition: "service_completed_successfully"},
					},
				},
			},
			"web-init-0": {
				Image:   "busybox:latest",
				Restart: "no",
			},
		},
	}
	res, err := Convert(comp, Options{Project: "myapp"})
	if err != nil {
		t.Fatal(err)
	}

	// Both services should produce Service manifests.
	svcCount := 0
	for _, m := range res.Manifests {
		if m.Kind == "Service" {
			svcCount++
		}
	}
	if svcCount != 2 {
		t.Fatalf("Service manifests: got %d, want 2", svcCount)
	}

	// Find the "completed" edge from web → web-init-0.
	found := false
	for _, e := range res.Edges {
		if e.From.GetId() == "myapp/web" && e.To.GetId() == "myapp/web-init-0" && e.Condition == "completed" {
			found = true
			break
		}
	}
	if !found {
		t.Error("missing completed edge from web → web-init-0")
	}
}

func TestComputeVersion_Stable(t *testing.T) {
	svc := eval.Service{Image: "nginx:latest"}

	v1 := computeVersion(svc)
	v2 := computeVersion(svc)
	if v1 != v2 {
		t.Fatalf("version not stable: %s != %s", v1, v2)
	}
	if len(v1) != 16 { // 8 bytes = 16 hex chars
		t.Fatalf("version length: got %d, want 16", len(v1))
	}

	// Image change → hash change.
	svc2 := eval.Service{Image: "nginx:1.25"}
	v3 := computeVersion(svc2)
	if v1 == v3 {
		t.Error("different images should produce different versions")
	}

	// DependsOn change → no hash change.
	svc3 := eval.Service{
		Image: "nginx:latest",
		DependsOn: eval.DependsOnValue{
			Entries: map[string]eval.DependsOnEntry{
				"db": {Condition: "service_started"},
			},
		},
	}
	v4 := computeVersion(svc3)
	if v1 != v4 {
		t.Errorf("depends_on should not affect version: %s != %s", v1, v4)
	}

	// Profiles change → no hash change.
	svc4 := eval.Service{
		Image:    "nginx:latest",
		Profiles: []string{"dev"},
	}
	v5 := computeVersion(svc4)
	if v1 != v5 {
		t.Errorf("profiles should not affect version: %s != %s", v1, v5)
	}
}

func TestExtractVolumeName(t *testing.T) {
	tests := []struct {
		mount string
		want  string
	}{
		{"data:/var/data", "data"},
		{"myvolume:/app/data:ro", "myvolume"},
		{"/host/path:/container/path", ""},
		{"./relative:/container/path", ""},
		{"~/home:/container/home", ""},
		{"../parent:/container/parent", ""},
		{"/nix/store/abc123:/nix/store/abc123:ro", ""},
	}
	for _, tt := range tests {
		got := extractVolumeName(tt.mount)
		if got != tt.want {
			t.Errorf("extractVolumeName(%q) = %q, want %q", tt.mount, got, tt.want)
		}
	}
}

func TestConvert_NixStoreVolumes(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {
				Image: "nginx:latest",
				XNixCompose: &eval.NixComposeExtended{
					UseHostStore:  true,
					NixStorePaths: []string{"/nix/store/abc123", "/nix/store/def456"},
				},
			},
		},
	}
	res, err := Convert(comp, Options{Project: "myapp"})
	if err != nil {
		t.Fatal(err)
	}

	// Should not create any Volume manifests.
	for _, m := range res.Manifests {
		if m.Kind == "Volume" {
			t.Fatal("unexpected Volume manifest for nix store paths")
		}
	}

	// Nix store paths should appear as bind mounts in ContainerSpec.Volumes.
	svcSpec := res.Manifests[3].Spec.(resources.ServiceSpec)
	expected := []string{
		"/nix/store/abc123:/nix/store/abc123:ro",
		"/nix/store/def456:/nix/store/def456:ro",
	}
	if got := len(svcSpec.Container.Volumes); got != len(expected) {
		t.Fatalf("volumes: got %d, want %d", got, len(expected))
	}
	for i, want := range expected {
		if svcSpec.Container.Volumes[i] != want {
			t.Errorf("volume[%d] = %q, want %q", i, svcSpec.Container.Volumes[i], want)
		}
	}

	// No volume edges.
	for _, e := range res.Edges {
		if e.To.GetKey() == resources.VolumeKey {
			t.Fatal("unexpected Volume edge for nix store bind mounts")
		}
	}
}

func TestConvert_EmptyComposition(t *testing.T) {
	comp := &eval.Composition{}
	res, err := Convert(comp, Options{Project: "myapp"})
	if err != nil {
		t.Fatal(err)
	}

	// Only the Project manifest.
	if got := len(res.Manifests); got != 1 {
		t.Fatalf("manifests: got %d, want 1", got)
	}
	assertManifest(t, res, 0, "Project", "myapp")

	if got := len(res.Edges); got != 0 {
		t.Fatalf("edges: got %d, want 0", got)
	}
}

func TestConvert_NamedPorts(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {
				Image: "nginx:latest",
				Ports: []string{"80:80"},
				XNixCompose: &eval.NixComposeExtended{
					NamedPorts: []eval.NamedPort{
						{Name: "metrics", ContainerPort: 9090, HostPort: 9090},
						{Name: "debug", ContainerPort: 5005, HostPort: 0, Protocol: "udp"},
					},
				},
			},
		},
	}
	res, err := Convert(comp, Options{Project: "myapp"})
	if err != nil {
		t.Fatal(err)
	}

	svcSpec := res.Manifests[3].Spec.(resources.ServiceSpec)
	wantPorts := []string{"80:80", "9090:9090", "5005/udp"}
	if got := len(svcSpec.Container.Ports); got != len(wantPorts) {
		t.Fatalf("ports: got %d, want %d", got, len(wantPorts))
	}
	for i, want := range wantPorts {
		if svcSpec.Container.Ports[i] != want {
			t.Errorf("port[%d] = %q, want %q", i, svcSpec.Container.Ports[i], want)
		}
	}
}

func TestConvert_MissingProject(t *testing.T) {
	comp := &eval.Composition{}
	_, err := Convert(comp, Options{})
	if err == nil {
		t.Fatal("expected error for empty project name")
	}
}

func TestMapCondition(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"service_started", "started"},
		{"service_healthy", "healthy"},
		{"service_completed_successfully", "completed"},
		{"unknown", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := mapCondition(tt.input)
		if got != tt.want {
			t.Errorf("mapCondition(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestConvert_DeterministicOrder(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"zeta": {Image: "img-z:latest"},
			"alpha": {
				Image: "img-a:latest",
				DependsOn: eval.DependsOnValue{
					Entries: map[string]eval.DependsOnEntry{
						"zeta": {Condition: "service_started"},
					},
				},
			},
		},
		Volumes: map[string]eval.Volume{
			"vol-b": {},
			"vol-a": {},
		},
	}
	opts := Options{Project: "myapp"}

	res1, err := Convert(comp, opts)
	if err != nil {
		t.Fatal(err)
	}
	res2, err := Convert(comp, opts)
	if err != nil {
		t.Fatal(err)
	}

	assertResultsEqual(t, res1, res2)
}

func TestConvert_ManifestsValidate(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx:latest"},
			"db":  {Image: "postgres:16"},
		},
		Volumes: map[string]eval.Volume{
			"data": {},
		},
	}
	res, err := Convert(comp, Options{Project: "myapp"})
	if err != nil {
		t.Fatal(err)
	}

	for i, m := range res.Manifests {
		errs := m.Validate()
		if len(errs) > 0 {
			t.Errorf("manifest[%d] %s/%s validation errors: %v", i, m.Kind, m.Metadata.Name, errs)
		}
	}
}

// --- helpers ---

func assertManifest(t *testing.T, res *Result, idx int, kind, name string) {
	t.Helper()
	if idx >= len(res.Manifests) {
		t.Fatalf("manifest[%d]: index out of range (len=%d)", idx, len(res.Manifests))
	}
	m := res.Manifests[idx]
	if m.Kind != kind {
		t.Errorf("manifest[%d].Kind = %q, want %q", idx, m.Kind, kind)
	}
	if m.Metadata.Name != name {
		t.Errorf("manifest[%d].Metadata.Name = %q, want %q", idx, m.Metadata.Name, name)
	}
	if m.APIVersion != apiVersion {
		t.Errorf("manifest[%d].APIVersion = %q, want %q", idx, m.APIVersion, apiVersion)
	}
}

func assertEdge(t *testing.T, e Edge, fromID string, fromKey typing.DefinitionKey, toID string, toKey typing.DefinitionKey, cond string) {
	t.Helper()
	if e.From.GetId() != fromID {
		t.Errorf("edge.From.ID = %q, want %q", e.From.GetId(), fromID)
	}
	if e.From.GetKey() != fromKey {
		t.Errorf("edge.From.Key = %q, want %q", e.From.GetKey(), fromKey)
	}
	if e.To.GetId() != toID {
		t.Errorf("edge.To.ID = %q, want %q", e.To.GetId(), toID)
	}
	if e.To.GetKey() != toKey {
		t.Errorf("edge.To.Key = %q, want %q", e.To.GetKey(), toKey)
	}
	if e.Condition != cond {
		t.Errorf("edge.Condition = %q, want %q", e.Condition, cond)
	}
}

// requireEdge asserts that an edge exists matching from/to IDs and optional condition/toKey filters.
func requireEdge(t *testing.T, edges []Edge, fromID, toID, condition, toKey string) {
	t.Helper()
	for _, e := range edges {
		if e.From.GetId() != fromID || e.To.GetId() != toID {
			continue
		}
		if condition != "" && e.Condition != condition {
			continue
		}
		if toKey != "" && string(e.To.GetKey()) != toKey {
			continue
		}
		return
	}
	t.Errorf("missing edge from %s to %s (condition=%q, toKey=%q)", fromID, toID, condition, toKey)
}

// assertResultsEqual checks that two conversion results are identical.
func assertResultsEqual(t *testing.T, res1, res2 *Result) {
	t.Helper()
	if len(res1.Manifests) != len(res2.Manifests) {
		t.Fatal("manifest count differs between runs")
	}
	for i := range res1.Manifests {
		m1, m2 := res1.Manifests[i], res2.Manifests[i]
		if m1.Kind != m2.Kind || m1.Metadata.Name != m2.Metadata.Name {
			t.Errorf("manifest[%d] differs: %s/%s vs %s/%s",
				i, m1.Kind, m1.Metadata.Name, m2.Kind, m2.Metadata.Name)
		}
	}
	if len(res1.Edges) != len(res2.Edges) {
		t.Fatal("edge count differs between runs")
	}
	for i := range res1.Edges {
		e1, e2 := res1.Edges[i], res2.Edges[i]
		if e1.From.GetId() != e2.From.GetId() || e1.To.GetId() != e2.To.GetId() || e1.Condition != e2.Condition {
			t.Errorf("edge[%d] differs", i)
		}
	}
}
