package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/systemstart/nix-compose/internal/testsock"

	"github.com/systemstart/nix-compose/pkg/cni"
	"github.com/systemstart/nix-compose/pkg/cri"
	"github.com/systemstart/nix-compose/pkg/orchestrate/typing"
	"github.com/systemstart/nix-compose/pkg/volumes"
	"google.golang.org/grpc"
	runtimev1 "k8s.io/cri-api/pkg/apis/runtime/v1"
)

type testRef struct {
	id  string
	key typing.DefinitionKey
}

func (r *testRef) GetId() string                { return r.id }
func (r *testRef) GetKey() typing.DefinitionKey { return r.key }
func (r *testRef) String() string               { return "[ref " + r.id + "]" }

func newRef(id string, key typing.DefinitionKey) *testRef {
	return &testRef{id: id, key: key}
}

// ===========================================================================
// ContainerDefinition tests
// ===========================================================================

func TestContainerDefinition_GetKey(t *testing.T) {
	d := &ContainerDefinition{}
	if d.GetKey() != ContainerKey {
		t.Fatalf("expected %s, got %s", ContainerKey, d.GetKey())
	}
}

func TestContainerDefinition_GetMappings(t *testing.T) {
	d := &ContainerDefinition{}
	if d.GetMappings() != nil {
		t.Fatal("expected nil mappings")
	}
}

func TestContainerDefinition_Instantiate_ValidJSON(t *testing.T) {
	d := &ContainerDefinition{}
	raw := json.RawMessage(`{"project":"p","service":"s","version":"v1","image":"nginx"}`)
	inst, err := d.Instantiate(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ci, ok := inst.(*ContainerInstance)
	if !ok {
		t.Fatal("expected *ContainerInstance")
	}
	if ci.Spec.Project != "p" {
		t.Fatalf("expected project 'p', got %q", ci.Spec.Project)
	}
	if ci.Spec.Service != "s" {
		t.Fatalf("expected service 's', got %q", ci.Spec.Service)
	}
}

func TestContainerDefinition_Instantiate_InvalidJSON(t *testing.T) {
	d := &ContainerDefinition{}
	_, err := d.Instantiate(json.RawMessage(`{bad json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestContainerDefinition_Load(t *testing.T) {
	d := &ContainerDefinition{}
	raw := json.RawMessage(`{"project":"p","service":"s","version":"v1","image":"nginx"}`)
	inst, err := d.Load(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inst.GetId() != "p/s" {
		t.Fatalf("expected id 'p/s', got %q", inst.GetId())
	}
}

func TestContainerDefinition_Delete_NilClient(t *testing.T) {
	d := &ContainerDefinition{Client: nil}
	ref := newRef("p/s", ContainerKey)
	err := d.Delete(ref)
	if err != nil {
		t.Fatalf("expected nil error for nil client, got: %v", err)
	}
}

func TestContainerDefinition_GetStatus_NilClient(t *testing.T) {
	d := &ContainerDefinition{Client: nil}
	ref := newRef("p/s", ContainerKey)
	status, err := d.GetStatus(ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.GetShort() != typing.RolloutStatusPending {
		t.Fatalf("expected PENDING, got %s", status.GetShort())
	}
}

func TestContainerDefinition_GetProviderStatus_NilClient(t *testing.T) {
	d := &ContainerDefinition{Client: nil}
	ref := newRef("p/s", ContainerKey)
	status, err := d.GetProviderStatus(ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.GetShort() != typing.RolloutStatusPending {
		t.Fatalf("expected PENDING, got %s", status.GetShort())
	}
}

// ===========================================================================
// ContainerInstance tests
// ===========================================================================

func TestContainerInstance_GetId(t *testing.T) {
	ci := &ContainerInstance{Spec: ContainerSpec{Project: "p", Service: "s"}}
	if ci.GetId() != "p/s" {
		t.Fatalf("expected 'p/s', got %q", ci.GetId())
	}
}

func TestContainerInstance_GetKey(t *testing.T) {
	ci := &ContainerInstance{Spec: ContainerSpec{Project: "p", Service: "s"}}
	if ci.GetKey() != ContainerKey {
		t.Fatalf("expected %s, got %s", ContainerKey, ci.GetKey())
	}
}

func TestContainerInstance_String(t *testing.T) {
	ci := &ContainerInstance{Spec: ContainerSpec{Project: "p", Service: "s"}}
	s := ci.String()
	expected := "[Container p/s]"
	if s != expected {
		t.Fatalf("expected %q, got %q", expected, s)
	}
}

func TestContainerInstance_Apply_NilClient(t *testing.T) {
	ci := &ContainerInstance{Spec: ContainerSpec{Project: "p", Service: "s"}, client: nil}
	err := ci.Apply()
	if err == nil {
		t.Fatal("expected error when client is nil")
	}
}

// ===========================================================================
// ServiceDefinition tests
// ===========================================================================

func TestServiceDefinition_GetKey(t *testing.T) {
	d := &ServiceDefinition{}
	if d.GetKey() != ServiceKey {
		t.Fatalf("expected %s, got %s", ServiceKey, d.GetKey())
	}
}

func TestServiceDefinition_Instantiate_ValidJSON(t *testing.T) {
	d := &ServiceDefinition{}
	raw := json.RawMessage(`{"container":{"project":"p","service":"s","version":"v1","image":"nginx"}}`)
	inst, err := d.Instantiate(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	si, ok := inst.(*ServiceInstance)
	if !ok {
		t.Fatal("expected *ServiceInstance")
	}
	if si.Spec.Container.Project != "p" {
		t.Fatalf("expected project 'p', got %q", si.Spec.Container.Project)
	}
}

func TestServiceDefinition_Instantiate_InvalidJSON(t *testing.T) {
	d := &ServiceDefinition{}
	_, err := d.Instantiate(json.RawMessage(`{bad`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestServiceDefinition_Load(t *testing.T) {
	d := &ServiceDefinition{}
	raw := json.RawMessage(`{"container":{"project":"p","service":"s","version":"v1","image":"nginx"}}`)
	inst, err := d.Load(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inst.GetId() != "p/s" {
		t.Fatalf("expected id 'p/s', got %q", inst.GetId())
	}
}

func TestServiceDefinition_GetProviderStatus_NilClient(t *testing.T) {
	d := &ServiceDefinition{Client: nil}
	ref := newRef("p/s", ServiceKey)
	status, err := d.GetProviderStatus(ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.GetShort() != typing.RolloutStatusPending {
		t.Fatalf("expected PENDING, got %s", status.GetShort())
	}
}

// ===========================================================================
// ImageDefinition tests
// ===========================================================================

func TestImageDefinition_GetKey(t *testing.T) {
	d := &ImageDefinition{}
	if d.GetKey() != ImageKey {
		t.Fatalf("expected %s, got %s", ImageKey, d.GetKey())
	}
}

func TestImageDefinition_Instantiate_ValidJSON(t *testing.T) {
	d := &ImageDefinition{}
	raw := json.RawMessage(`{"image":"alpine:latest"}`)
	inst, err := d.Instantiate(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ii, ok := inst.(*ImageInstance)
	if !ok {
		t.Fatal("expected *ImageInstance")
	}
	if ii.Spec.Image != "alpine:latest" {
		t.Fatalf("expected 'alpine:latest', got %q", ii.Spec.Image)
	}
}

func TestImageDefinition_Instantiate_InvalidJSON(t *testing.T) {
	d := &ImageDefinition{}
	_, err := d.Instantiate(json.RawMessage(`{bad`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestImageDefinition_Load(t *testing.T) {
	d := &ImageDefinition{}
	raw := json.RawMessage(`{"image":"redis:7"}`)
	inst, err := d.Load(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inst.GetId() != "redis:7" {
		t.Fatalf("expected id 'redis:7', got %q", inst.GetId())
	}
}

func TestImageDefinition_Delete_NilClient(t *testing.T) {
	d := &ImageDefinition{Client: nil}
	ref := newRef("alpine:latest", ImageKey)
	err := d.Delete(ref)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
}

func TestImageDefinition_GetProviderStatus_NilClient(t *testing.T) {
	d := &ImageDefinition{Client: nil}
	ref := newRef("alpine:latest", ImageKey)
	status, err := d.GetProviderStatus(ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.GetShort() != typing.RolloutStatusPending {
		t.Fatalf("expected PENDING, got %s", status.GetShort())
	}
}

func TestImageInstance_GetId(t *testing.T) {
	ii := &ImageInstance{Spec: ImageSpec{Image: "redis:7"}}
	if ii.GetId() != "redis:7" {
		t.Fatalf("expected 'redis:7', got %q", ii.GetId())
	}
}

func TestImageInstance_GetKey(t *testing.T) {
	ii := &ImageInstance{Spec: ImageSpec{Image: "redis:7"}}
	if ii.GetKey() != ImageKey {
		t.Fatalf("expected %s, got %s", ImageKey, ii.GetKey())
	}
}

func TestImageInstance_String(t *testing.T) {
	ii := &ImageInstance{Spec: ImageSpec{Image: "redis:7"}}
	s := ii.String()
	if !strings.Contains(s, "redis:7") {
		t.Fatalf("expected to contain 'redis:7', got %q", s)
	}
}

func TestImageInstance_Apply_NilClient(t *testing.T) {
	ii := &ImageInstance{Spec: ImageSpec{Image: "redis:7"}, client: nil}
	err := ii.Apply()
	if err == nil {
		t.Fatal("expected error when client is nil")
	}
}

// ===========================================================================
// NetworkDefinition tests
// ===========================================================================

func TestNetworkDefinition_GetKey(t *testing.T) {
	d := &NetworkDefinition{}
	if d.GetKey() != NetworkKey {
		t.Fatalf("expected %s, got %s", NetworkKey, d.GetKey())
	}
}

func TestNetworkDefinition_Instantiate_ValidJSON(t *testing.T) {
	d := &NetworkDefinition{}
	raw := json.RawMessage(`{"project":"mynet"}`)
	inst, err := d.Instantiate(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ni, ok := inst.(*NetworkInstance)
	if !ok {
		t.Fatal("expected *NetworkInstance")
	}
	if ni.Spec.Project != "mynet" {
		t.Fatalf("expected project 'mynet', got %q", ni.Spec.Project)
	}
}

func TestNetworkDefinition_Instantiate_InvalidJSON(t *testing.T) {
	d := &NetworkDefinition{}
	_, err := d.Instantiate(json.RawMessage(`{bad`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestNetworkDefinition_Load(t *testing.T) {
	d := &NetworkDefinition{}
	raw := json.RawMessage(`{"project":"testnet"}`)
	inst, err := d.Load(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inst.GetId() != "testnet" {
		t.Fatalf("expected id 'testnet', got %q", inst.GetId())
	}
}

func TestNetworkDefinition_Delete_NilStore(t *testing.T) {
	d := &NetworkDefinition{Store: nil}
	ref := newRef("testnet", NetworkKey)
	err := d.Delete(ref)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
}

func TestNetworkDefinition_GetStatus_NilStore(t *testing.T) {
	d := &NetworkDefinition{Store: nil}
	ref := newRef("testnet", NetworkKey)
	status, err := d.GetStatus(ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.GetShort() != typing.RolloutStatusSuccess {
		t.Fatalf("expected SUCCEEDED, got %s", status.GetShort())
	}
}

func TestNetworkDefinition_GetProviderStatus_NilStore(t *testing.T) {
	d := &NetworkDefinition{Store: nil}
	ref := newRef("testnet", NetworkKey)
	status, err := d.GetProviderStatus(ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.GetShort() != typing.RolloutStatusSuccess {
		t.Fatalf("expected SUCCEEDED, got %s", status.GetShort())
	}
}

func TestNetworkInstance_GetId(t *testing.T) {
	ni := &NetworkInstance{Spec: NetworkSpec{Project: "myproject"}}
	if ni.GetId() != "myproject" {
		t.Fatalf("expected 'myproject', got %q", ni.GetId())
	}
}

func TestNetworkInstance_GetKey(t *testing.T) {
	ni := &NetworkInstance{Spec: NetworkSpec{Project: "myproject"}}
	if ni.GetKey() != NetworkKey {
		t.Fatalf("expected %s, got %s", NetworkKey, ni.GetKey())
	}
}

func TestNetworkInstance_Apply_NilStore(t *testing.T) {
	ni := &NetworkInstance{Spec: NetworkSpec{Project: "myproject"}, store: nil}
	err := ni.Apply()
	if err == nil {
		t.Fatal("expected error when store is nil")
	}
}

// ===========================================================================
// ProjectDefinition tests
// ===========================================================================

func TestProjectDefinition_GetKey(t *testing.T) {
	d := &ProjectDefinition{}
	if d.GetKey() != ProjectKey {
		t.Fatalf("expected %s, got %s", ProjectKey, d.GetKey())
	}
}

func TestProjectDefinition_Instantiate_ValidJSON(t *testing.T) {
	d := &ProjectDefinition{}
	raw := json.RawMessage(`{"name":"myproject"}`)
	inst, err := d.Instantiate(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pi, ok := inst.(*ProjectInstance)
	if !ok {
		t.Fatal("expected *ProjectInstance")
	}
	if pi.Spec.Name != "myproject" {
		t.Fatalf("expected name 'myproject', got %q", pi.Spec.Name)
	}
}

func TestProjectDefinition_Instantiate_InvalidJSON(t *testing.T) {
	d := &ProjectDefinition{}
	_, err := d.Instantiate(json.RawMessage(`!!!invalid!!!`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestProjectDefinition_Load(t *testing.T) {
	d := &ProjectDefinition{}
	raw := json.RawMessage(`{"name":"loadtest"}`)
	inst, err := d.Load(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inst.GetId() != "loadtest" {
		t.Fatalf("expected id 'loadtest', got %q", inst.GetId())
	}
}

func TestProjectDefinition_Delete_NilClient(t *testing.T) {
	d := &ProjectDefinition{Client: nil}
	ref := newRef("myproject", ProjectKey)
	err := d.Delete(ref)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
}

func TestProjectDefinition_GetStatus_NilClient(t *testing.T) {
	d := &ProjectDefinition{Client: nil}
	ref := newRef("myproject", ProjectKey)
	status, err := d.GetStatus(ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.GetShort() != typing.RolloutStatusSuccess {
		t.Fatalf("expected SUCCEEDED, got %s", status.GetShort())
	}
}

func TestProjectDefinition_GetProviderStatus_NilClient(t *testing.T) {
	d := &ProjectDefinition{Client: nil}
	ref := newRef("myproject", ProjectKey)
	status, err := d.GetProviderStatus(ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.GetShort() != typing.RolloutStatusSuccess {
		t.Fatalf("expected SUCCEEDED, got %s", status.GetShort())
	}
}

func TestProjectInstance_GetId(t *testing.T) {
	pi := &ProjectInstance{Spec: ProjectSpec{Name: "test-proj"}}
	if pi.GetId() != "test-proj" {
		t.Fatalf("expected 'test-proj', got %q", pi.GetId())
	}
}

func TestProjectInstance_GetKey(t *testing.T) {
	pi := &ProjectInstance{Spec: ProjectSpec{Name: "p"}}
	if pi.GetKey() != ProjectKey {
		t.Fatalf("expected %s, got %s", ProjectKey, pi.GetKey())
	}
}

func TestProjectInstance_Apply_NilClient(t *testing.T) {
	pi := &ProjectInstance{Spec: ProjectSpec{Name: "noop"}, client: nil}
	err := pi.Apply()
	if err != nil {
		t.Fatalf("expected nil error for project Apply, got: %v", err)
	}
}

// ===========================================================================
// VolumeDefinition tests
// ===========================================================================

func TestVolumeDefinition_GetKey(t *testing.T) {
	d := &VolumeDefinition{}
	if d.GetKey() != VolumeKey {
		t.Fatalf("expected %s, got %s", VolumeKey, d.GetKey())
	}
}

func TestVolumeDefinition_Instantiate_ValidJSON(t *testing.T) {
	d := &VolumeDefinition{}
	raw := json.RawMessage(`{"project":"proj","name":"data"}`)
	inst, err := d.Instantiate(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	vi, ok := inst.(*VolumeInstance)
	if !ok {
		t.Fatal("expected *VolumeInstance")
	}
	if vi.Spec.Project != "proj" {
		t.Fatalf("expected project 'proj', got %q", vi.Spec.Project)
	}
}

func TestVolumeDefinition_Instantiate_InvalidJSON(t *testing.T) {
	d := &VolumeDefinition{}
	_, err := d.Instantiate(json.RawMessage(`{broken`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestVolumeDefinition_Load(t *testing.T) {
	d := &VolumeDefinition{}
	raw := json.RawMessage(`{"project":"p","name":"vol1"}`)
	inst, err := d.Load(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inst.GetId() != "p/vol1" {
		t.Fatalf("expected id 'p/vol1', got %q", inst.GetId())
	}
}

func TestVolumeDefinition_GetStatus_NilStore(t *testing.T) {
	d := &VolumeDefinition{Store: nil}
	ref := newRef("p/vol1", VolumeKey)
	status, err := d.GetStatus(ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.GetShort() != typing.RolloutStatusSuccess {
		t.Fatalf("expected SUCCEEDED, got %s", status.GetShort())
	}
}

func TestVolumeDefinition_GetProviderStatus_NilStore(t *testing.T) {
	d := &VolumeDefinition{Store: nil}
	ref := newRef("p/vol1", VolumeKey)
	status, err := d.GetProviderStatus(ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.GetShort() != typing.RolloutStatusSuccess {
		t.Fatalf("expected SUCCEEDED, got %s", status.GetShort())
	}
}

func TestVolumeInstance_GetId(t *testing.T) {
	vi := &VolumeInstance{Spec: VolumeSpec{Project: "proj", Name: "data"}}
	if vi.GetId() != "proj/data" {
		t.Fatalf("expected 'proj/data', got %q", vi.GetId())
	}
}

func TestVolumeInstance_GetKey(t *testing.T) {
	vi := &VolumeInstance{Spec: VolumeSpec{Project: "p", Name: "v"}}
	if vi.GetKey() != VolumeKey {
		t.Fatalf("expected %s, got %s", VolumeKey, vi.GetKey())
	}
}

func TestVolumeInstance_Apply_NilStore(t *testing.T) {
	vi := &VolumeInstance{Spec: VolumeSpec{Project: "p", Name: "v"}, store: nil}
	err := vi.Apply()
	if err == nil {
		t.Fatal("expected error when store is nil")
	}
}

// ===========================================================================
// Interface compliance tests
// ===========================================================================

func TestDefinitionInterfaceCompliance(t *testing.T) {
	var _ typing.Definition = &ContainerDefinition{}
	var _ typing.Definition = &ServiceDefinition{}
	var _ typing.Definition = &ImageDefinition{}
	var _ typing.Definition = &NetworkDefinition{}
	var _ typing.Definition = &ProjectDefinition{}
	var _ typing.Definition = &VolumeDefinition{}
}

func TestInstanceInterfaceCompliance(t *testing.T) {
	var _ typing.Instance = &ContainerInstance{}
	var _ typing.Instance = &ServiceInstance{}
	var _ typing.Instance = &ImageInstance{}
	var _ typing.Instance = &NetworkInstance{}
	var _ typing.Instance = &ProjectInstance{}
	var _ typing.Instance = &VolumeInstance{}
}

// Test that Instantiate and Load produce equivalent IDs.
func TestInstantiateAndLoadEquivalence(t *testing.T) {
	tests := []struct {
		name string
		def  typing.Definition
		raw  json.RawMessage
	}{
		{"Container", &ContainerDefinition{}, json.RawMessage(`{"project":"p","service":"s","version":"v1","image":"img"}`)},
		{"Service", &ServiceDefinition{}, json.RawMessage(`{"container":{"project":"p","service":"s","version":"v1","image":"img"}}`)},
		{"Image", &ImageDefinition{}, json.RawMessage(`{"image":"alpine"}`)},
		{"Network", &NetworkDefinition{}, json.RawMessage(`{"project":"netproj"}`)},
		{"Project", &ProjectDefinition{}, json.RawMessage(`{"name":"proj"}`)},
		{"Volume", &VolumeDefinition{}, json.RawMessage(`{"project":"p","name":"vol"}`)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inst1, err := tc.def.Instantiate(tc.raw)
			if err != nil {
				t.Fatalf("Instantiate: %v", err)
			}
			inst2, err := tc.def.Load(tc.raw)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if inst1.GetId() != inst2.GetId() {
				t.Fatalf("IDs differ: %q vs %q", inst1.GetId(), inst2.GetId())
			}
			if inst1.GetKey() != inst2.GetKey() {
				t.Fatalf("Keys differ: %s vs %s", inst1.GetKey(), inst2.GetKey())
			}
		})
	}
}

// ===========================================================================
// String() method tests (all instance types)
// ===========================================================================

func TestNetworkInstance_String(t *testing.T) {
	ni := &NetworkInstance{Spec: NetworkSpec{Project: "myproject"}}
	s := ni.String()
	expected := "[Network myproject]"
	if s != expected {
		t.Fatalf("expected %q, got %q", expected, s)
	}
}

func TestProjectInstance_String(t *testing.T) {
	pi := &ProjectInstance{Spec: ProjectSpec{Name: "myproject"}}
	s := pi.String()
	expected := "[Project myproject]"
	if s != expected {
		t.Fatalf("expected %q, got %q", expected, s)
	}
}

func TestServiceInstance_String(t *testing.T) {
	si := &ServiceInstance{Spec: ServiceSpec{Container: ContainerSpec{Project: "p", Service: "s"}}}
	s := si.String()
	expected := "[Service p/s]"
	if s != expected {
		t.Fatalf("expected %q, got %q", expected, s)
	}
}

func TestServiceInstance_GetId(t *testing.T) {
	si := &ServiceInstance{Spec: ServiceSpec{Container: ContainerSpec{Project: "myproj", Service: "mysvc"}}}
	if si.GetId() != "myproj/mysvc" {
		t.Fatalf("expected 'myproj/mysvc', got %q", si.GetId())
	}
}

func TestServiceInstance_GetKey(t *testing.T) {
	si := &ServiceInstance{Spec: ServiceSpec{Container: ContainerSpec{Project: "p", Service: "s"}}}
	if si.GetKey() != ServiceKey {
		t.Fatalf("expected %s, got %s", ServiceKey, si.GetKey())
	}
}

func TestVolumeInstance_String(t *testing.T) {
	vi := &VolumeInstance{Spec: VolumeSpec{Project: "p", Name: "vol"}}
	s := vi.String()
	expected := "[Volume p/vol]"
	if s != expected {
		t.Fatalf("expected %q, got %q", expected, s)
	}
}

// ===========================================================================
// GetMappings() tests for all definitions
// ===========================================================================

func TestNetworkDefinition_GetMappings(t *testing.T) {
	d := &NetworkDefinition{}
	if d.GetMappings() != nil {
		t.Fatal("expected nil mappings")
	}
}

func TestProjectDefinition_GetMappings(t *testing.T) {
	d := &ProjectDefinition{}
	if d.GetMappings() != nil {
		t.Fatal("expected nil mappings")
	}
}

func TestServiceDefinition_GetMappings(t *testing.T) {
	d := &ServiceDefinition{}
	if d.GetMappings() != nil {
		t.Fatal("expected nil mappings")
	}
}

func TestVolumeDefinition_GetMappings(t *testing.T) {
	d := &VolumeDefinition{}
	if d.GetMappings() != nil {
		t.Fatal("expected nil mappings")
	}
}

// ===========================================================================
// ServiceDefinition.Delete/GetStatus (nil client delegation tests)
// ===========================================================================

func TestServiceDefinition_Delete_NilClient(t *testing.T) {
	d := &ServiceDefinition{Client: nil}
	ref := newRef("p/s", ServiceKey)
	err := d.Delete(ref)
	if err != nil {
		t.Fatalf("expected nil error for nil client, got: %v", err)
	}
}

func TestServiceDefinition_GetStatus_NilClient(t *testing.T) {
	d := &ServiceDefinition{Client: nil}
	ref := newRef("p/s", ServiceKey)
	status, err := d.GetStatus(ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.GetShort() != typing.RolloutStatusPending {
		t.Fatalf("expected PENDING, got %s", status.GetShort())
	}
}

func TestServiceInstance_Apply_NilClient(t *testing.T) {
	si := &ServiceInstance{
		Spec:   ServiceSpec{Container: ContainerSpec{Project: "p", Service: "s"}},
		client: nil,
	}
	err := si.Apply()
	if err == nil {
		t.Fatal("expected error when client is nil")
	}
}

// ===========================================================================
// VolumeInstance.GetHostPath
// ===========================================================================

func TestVolumeInstance_GetHostPath_NilStore(t *testing.T) {
	vi := &VolumeInstance{Spec: VolumeSpec{Project: "p", Name: "v"}, store: nil}
	path := vi.GetHostPath()
	if path != "" {
		t.Fatalf("expected empty path for nil store, got %q", path)
	}
}

// ===========================================================================
// ImageDefinition.GetStatus (delegates to GetProviderStatus)
// ===========================================================================

func TestImageDefinition_GetStatus_NilClient(t *testing.T) {
	d := &ImageDefinition{Client: nil}
	ref := newRef("alpine", ImageKey)
	status, err := d.GetStatus(ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.GetShort() != typing.RolloutStatusPending {
		t.Fatalf("expected PENDING, got %s", status.GetShort())
	}
}

func TestSplitContainerId(t *testing.T) {
	tests := []struct {
		id, project, service string
	}{
		{"a/b", "a", "b"},
		{"a/b/c", "a", "b/c"},
		{"single", "single", "single"},
		{"", "", ""},
		{"/", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			p, s := splitContainerId(tt.id)
			if p != tt.project || s != tt.service {
				t.Fatalf("splitContainerId(%q) = (%q, %q), want (%q, %q)",
					tt.id, p, s, tt.project, tt.service)
			}
		})
	}
}

// ===========================================================================
// containerStateStatus tests
// ===========================================================================

func TestContainerStateStatus(t *testing.T) {
	// containerStateStatus maps an int32 container state to a typing.Status.
	// State 1 = CONTAINER_RUNNING, State 2 = CONTAINER_EXITED (requires client),
	// anything else (0 = CONTAINER_CREATED, 3+ = CONTAINER_UNKNOWN) = PENDING.
	d := &ContainerDefinition{Client: nil}

	tests := []struct {
		name           string
		state          int32
		expectedStatus typing.RolloutStatusShort
	}{
		{
			name:           "CONTAINER_CREATED returns PENDING",
			state:          0,
			expectedStatus: typing.RolloutStatusPending,
		},
		{
			name:           "CONTAINER_RUNNING returns RUNNING",
			state:          1,
			expectedStatus: typing.RolloutStatusRunning,
		},
		// State 2 (CONTAINER_EXITED) is not tested here because it delegates
		// to exitedContainerStatus which requires a CRI client.
		{
			name:           "CONTAINER_UNKNOWN returns PENDING",
			state:          3,
			expectedStatus: typing.RolloutStatusPending,
		},
		{
			name:           "negative state returns PENDING",
			state:          -1,
			expectedStatus: typing.RolloutStatusPending,
		},
		{
			name:           "large state returns PENDING",
			state:          999,
			expectedStatus: typing.RolloutStatusPending,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, err := d.containerStateStatus(tc.state, "test-container-id")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if status.GetShort() != tc.expectedStatus {
				t.Fatalf("expected %s, got %s", tc.expectedStatus, status.GetShort())
			}
		})
	}
}

// ===========================================================================
// exitedContainerStatus tests
// ===========================================================================

func TestExitedContainerStatus_NilClient(t *testing.T) {
	// When Client is nil, exitedContainerStatus will panic or fail because
	// it dereferences d.Client. We verify that containerStateStatus with
	// state=2 (EXITED) delegates to exitedContainerStatus by testing it
	// indirectly through GetProviderStatusWithVersion with a nil client,
	// which returns PENDING before reaching container state logic.

	// For a nil client, GetProviderStatusWithVersion returns PENDING early,
	// so exitedContainerStatus is never reached. This is already covered by
	// TestContainerDefinition_GetProviderStatus_NilClient.

	// Direct test: exitedContainerStatus requires a non-nil client.
	// We document this constraint by verifying the nil-client path returns PENDING
	// via containerStateStatus state=2 (which would call exitedContainerStatus).
	// Since we cannot call exitedContainerStatus without a real CRI client,
	// we verify the status helper functions that exitedContainerStatus uses.

	// Test SucceededStatus (exit code 0 path)
	status := SucceededStatus()
	if status.GetShort() != typing.RolloutStatusSuccess {
		t.Fatalf("expected SUCCEEDED, got %s", status.GetShort())
	}

	// Test ErrorStatus (non-zero exit code path)
	errStatus := ErrorStatus("exited with code 1")
	if errStatus.GetShort() != typing.RolloutStatusError {
		t.Fatalf("expected ERROR, got %s", errStatus.GetShort())
	}
	details := errStatus.GetDetails()
	if details == nil {
		t.Fatal("expected non-nil details for error status")
		return
	}
	var msg string
	if err := json.Unmarshal(details, &msg); err != nil {
		t.Fatalf("failed to unmarshal details: %v", err)
	}
	if !strings.Contains(msg, "exited with code 1") {
		t.Fatalf("expected details to contain 'exited with code 1', got %q", msg)
	}
}

// ===========================================================================
// VolumeInstance with real Store
// ===========================================================================

func TestVolumeInstance_GetHostPath_WithStore(t *testing.T) {
	store := &volumes.Store{Root: t.TempDir()}
	vi := &VolumeInstance{
		Spec:  VolumeSpec{Project: "proj", Name: "data"},
		store: store,
	}
	path := vi.GetHostPath()
	if path == "" {
		t.Fatal("expected non-empty path from store")
	}
	expected := filepath.Join(store.Root, "proj", "data")
	if path != expected {
		t.Fatalf("expected %q, got %q", expected, path)
	}
}

func TestVolumeInstance_Apply_WithStore(t *testing.T) {
	store := &volumes.Store{Root: t.TempDir()}
	vi := &VolumeInstance{
		Spec:  VolumeSpec{Project: "proj", Name: "data"},
		store: store,
	}
	err := vi.Apply()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Verify the directory was created.
	expected := filepath.Join(store.Root, "proj", "data")
	info, err := os.Stat(expected)
	if err != nil {
		t.Fatalf("volume directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected directory at %s, got file", expected)
	}
}

// ===========================================================================
// VolumeDefinition.Delete no-op
// ===========================================================================

func TestVolumeDefinition_Delete_NoOp(t *testing.T) {
	store := &volumes.Store{Root: t.TempDir()}
	d := &VolumeDefinition{Store: store}
	ref := newRef("proj/data", VolumeKey)
	err := d.Delete(ref)
	if err != nil {
		t.Fatalf("expected nil error from no-op Delete, got: %v", err)
	}
}

// ===========================================================================
// NetworkInstance.String formatted output
// ===========================================================================

func TestNetworkInstance_String_Formatted(t *testing.T) {
	ni := &NetworkInstance{Spec: NetworkSpec{Project: "webapp"}}
	got := ni.String()
	expected := "[Network webapp]"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

// ===========================================================================
// NetworkInstance.Apply with real Store
// ===========================================================================

func TestNetworkInstance_Apply_WithStore(t *testing.T) {
	confDir := t.TempDir()
	store := &cni.Store{ConfDir: confDir}
	ni := &NetworkInstance{
		Spec:  NetworkSpec{Project: "testproj"},
		store: store,
	}
	err := ni.Apply()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Verify the conflist file was created.
	confPath := filepath.Join(confDir, cni.ConflistName("testproj"))
	info, err := os.Stat(confPath)
	if err != nil {
		t.Fatalf("conflist file not created: %v", err)
	}
	if info.IsDir() {
		t.Fatalf("expected file at %s, got directory", confPath)
	}
}

// ===========================================================================
// NetworkDefinition.Delete with real Store
// ===========================================================================

func TestNetworkDefinition_Delete_WithStore(t *testing.T) {
	confDir := t.TempDir()
	store := &cni.Store{ConfDir: confDir}
	// Write a conflist first, then delete it.
	if err := store.Write("testproj"); err != nil {
		t.Fatalf("setup: write conflist: %v", err)
	}
	confPath := filepath.Join(confDir, cni.ConflistName("testproj"))
	if _, err := os.Stat(confPath); err != nil {
		t.Fatalf("setup: conflist not found: %v", err)
	}
	d := &NetworkDefinition{Store: store}
	ref := newRef("testproj", NetworkKey)
	err := d.Delete(ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Verify the file was removed.
	if _, err := os.Stat(confPath); !os.IsNotExist(err) {
		t.Fatalf("expected conflist to be removed, but got err: %v", err)
	}
}

// ===========================================================================
// ContainerDefinition.GetProviderStatusWithVersion with nil Client
// ===========================================================================

func TestContainerDefinition_GetProviderStatusWithVersion_NilClient(t *testing.T) {
	d := &ContainerDefinition{Client: nil}
	ref := newRef("p/s", ContainerKey)
	status, err := d.GetProviderStatusWithVersion(ref, "v2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.GetShort() != typing.RolloutStatusPending {
		t.Fatalf("expected PENDING, got %s", status.GetShort())
	}
}

// ===========================================================================
// VolumeInstance.GetHostPath returns correct path for different inputs
// ===========================================================================

func TestVolumeInstance_GetHostPath_SubPath(t *testing.T) {
	store := &volumes.Store{Root: t.TempDir()}
	vi := &VolumeInstance{
		Spec:  VolumeSpec{Project: "myproj", Name: "cache"},
		store: store,
	}
	path := vi.GetHostPath()
	if !strings.HasSuffix(path, filepath.Join("myproj", "cache")) {
		t.Fatalf("expected path ending in myproj/cache, got %q", path)
	}
}

// ===========================================================================
// VolumeDefinition.Instantiate with real Store propagates store
// ===========================================================================

func TestVolumeDefinition_Instantiate_WithStore(t *testing.T) {
	store := &volumes.Store{Root: t.TempDir()}
	d := &VolumeDefinition{Store: store}
	raw := json.RawMessage(`{"project":"proj","name":"data"}`)
	inst, err := d.Instantiate(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	vi, ok := inst.(*VolumeInstance)
	if !ok {
		t.Fatal("expected *VolumeInstance")
	}
	// Verify the store was propagated to the instance.
	path := vi.GetHostPath()
	if path == "" {
		t.Fatal("expected non-empty path, store should be propagated")
	}
}

// ===========================================================================
// VolumeInstance.Apply error path (invalid store root)
// ===========================================================================

func TestVolumeInstance_Apply_ErrorPath(t *testing.T) {
	// Use a store root that cannot be created (nested under a file).
	tmpDir := t.TempDir()
	blockingFile := filepath.Join(tmpDir, "blocker")
	if err := os.WriteFile(blockingFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	store := &volumes.Store{Root: filepath.Join(blockingFile, "impossible")}
	vi := &VolumeInstance{
		Spec:  VolumeSpec{Project: "proj", Name: "data"},
		store: store,
	}
	err := vi.Apply()
	if err == nil {
		t.Fatal("expected error for invalid store root")
	}
	if !strings.Contains(err.Error(), "ensuring volume") {
		t.Fatalf("expected error to mention 'ensuring volume', got: %v", err)
	}
}

// ===========================================================================
// NetworkInstance.Apply error path (invalid conf dir)
// ===========================================================================

func TestNetworkInstance_Apply_ErrorPath(t *testing.T) {
	tmpDir := t.TempDir()
	blockingFile := filepath.Join(tmpDir, "blocker")
	if err := os.WriteFile(blockingFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	store := &cni.Store{ConfDir: filepath.Join(blockingFile, "impossible")}
	ni := &NetworkInstance{
		Spec:  NetworkSpec{Project: "testproj"},
		store: store,
	}
	err := ni.Apply()
	if err == nil {
		t.Fatal("expected error for invalid conf dir")
	}
	if !strings.Contains(err.Error(), "writing network config") {
		t.Fatalf("expected error to mention 'writing network config', got: %v", err)
	}
}

// ===========================================================================
// NetworkDefinition.Delete non-existent file (no-op, should succeed)
// ===========================================================================

func TestNetworkDefinition_Delete_NonExistentFile(t *testing.T) {
	confDir := t.TempDir()
	store := &cni.Store{ConfDir: confDir}
	d := &NetworkDefinition{Store: store}
	ref := newRef("nonexistent", NetworkKey)
	// Deleting a non-existent conflist should succeed silently.
	err := d.Delete(ref)
	if err != nil {
		t.Fatalf("expected nil error for non-existent file, got: %v", err)
	}
}

// ===========================================================================
// NetworkDefinition.Instantiate with Store propagates store
// ===========================================================================

func TestNetworkDefinition_Instantiate_WithStore(t *testing.T) {
	confDir := t.TempDir()
	store := &cni.Store{ConfDir: confDir}
	d := &NetworkDefinition{Store: store}
	raw := json.RawMessage(`{"project":"proj"}`)
	inst, err := d.Instantiate(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ni, ok := inst.(*NetworkInstance)
	if !ok {
		t.Fatal("expected *NetworkInstance")
	}
	// Verify the store was propagated by checking Apply works.
	err = ni.Apply()
	if err != nil {
		t.Fatalf("unexpected error from Apply: %v", err)
	}
}

// ===========================================================================
// Mock CRI server for resource-level tests
// ===========================================================================

// resourceMockCRI implements RuntimeService and ImageService for testing
// resources that need a CRI client.
type resourceMockCRI struct {
	runtimev1.UnimplementedRuntimeServiceServer
	runtimev1.UnimplementedImageServiceServer

	mu         sync.Mutex
	pods       map[string]*runtimev1.PodSandbox
	containers map[string]*runtimev1.Container
	// ctrStatuses maps container ID to its ContainerStatus (exit code, state).
	ctrStatuses map[string]*runtimev1.ContainerStatus
	// images tracks which images are "present" in the mock.
	images    map[string]*runtimev1.Image
	nextPodID int
	nextCtrID int
}

func newResourceMockCRI() *resourceMockCRI {
	return &resourceMockCRI{
		pods:        make(map[string]*runtimev1.PodSandbox),
		containers:  make(map[string]*runtimev1.Container),
		ctrStatuses: make(map[string]*runtimev1.ContainerStatus),
		images:      make(map[string]*runtimev1.Image),
	}
}

func (m *resourceMockCRI) Version(_ context.Context, _ *runtimev1.VersionRequest) (*runtimev1.VersionResponse, error) {
	return &runtimev1.VersionResponse{
		Version:           "0.1.0",
		RuntimeName:       "mock-runtime",
		RuntimeVersion:    "1.0.0",
		RuntimeApiVersion: "v1",
	}, nil
}

func (m *resourceMockCRI) PullImage(_ context.Context, req *runtimev1.PullImageRequest) (*runtimev1.PullImageResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	img := req.Image.Image
	m.images[img] = &runtimev1.Image{
		Id:   "sha256:" + img,
		Spec: &runtimev1.ImageSpec{Image: img},
	}
	return &runtimev1.PullImageResponse{ImageRef: img}, nil
}

func (m *resourceMockCRI) ImageStatus(_ context.Context, req *runtimev1.ImageStatusRequest) (*runtimev1.ImageStatusResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	img, ok := m.images[req.Image.Image]
	if !ok {
		return &runtimev1.ImageStatusResponse{Image: nil}, nil
	}
	return &runtimev1.ImageStatusResponse{Image: img}, nil
}

func (m *resourceMockCRI) RemoveImage(_ context.Context, req *runtimev1.RemoveImageRequest) (*runtimev1.RemoveImageResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.images, req.Image.Image)
	return &runtimev1.RemoveImageResponse{}, nil
}

func (m *resourceMockCRI) RunPodSandbox(_ context.Context, req *runtimev1.RunPodSandboxRequest) (*runtimev1.RunPodSandboxResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextPodID++
	id := fmt.Sprintf("pod-%d", m.nextPodID)
	m.pods[id] = &runtimev1.PodSandbox{
		Id:       id,
		State:    runtimev1.PodSandboxState_SANDBOX_READY,
		Metadata: req.Config.Metadata,
		Labels:   req.Config.Labels,
	}
	return &runtimev1.RunPodSandboxResponse{PodSandboxId: id}, nil
}

func (m *resourceMockCRI) StopPodSandbox(_ context.Context, req *runtimev1.StopPodSandboxRequest) (*runtimev1.StopPodSandboxResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if pod, ok := m.pods[req.PodSandboxId]; ok {
		pod.State = runtimev1.PodSandboxState_SANDBOX_NOTREADY
	}
	return &runtimev1.StopPodSandboxResponse{}, nil
}

func (m *resourceMockCRI) RemovePodSandbox(_ context.Context, req *runtimev1.RemovePodSandboxRequest) (*runtimev1.RemovePodSandboxResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pods, req.PodSandboxId)
	return &runtimev1.RemovePodSandboxResponse{}, nil
}

func (m *resourceMockCRI) ListPodSandbox(_ context.Context, req *runtimev1.ListPodSandboxRequest) (*runtimev1.ListPodSandboxResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var items []*runtimev1.PodSandbox
	selector := req.Filter.GetLabelSelector()
	for _, pod := range m.pods {
		if resourceMatchLabels(pod.Labels, selector) {
			items = append(items, pod)
		}
	}
	return &runtimev1.ListPodSandboxResponse{Items: items}, nil
}

func (m *resourceMockCRI) CreateContainer(_ context.Context, req *runtimev1.CreateContainerRequest) (*runtimev1.CreateContainerResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextCtrID++
	id := fmt.Sprintf("ctr-%d", m.nextCtrID)
	m.containers[id] = &runtimev1.Container{
		Id:           id,
		PodSandboxId: req.PodSandboxId,
		State:        runtimev1.ContainerState_CONTAINER_CREATED,
		Metadata:     req.Config.Metadata,
		Labels:       req.Config.Labels,
		Image:        req.Config.Image,
	}
	return &runtimev1.CreateContainerResponse{ContainerId: id}, nil
}

func (m *resourceMockCRI) StartContainer(_ context.Context, req *runtimev1.StartContainerRequest) (*runtimev1.StartContainerResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctr, ok := m.containers[req.ContainerId]; ok {
		ctr.State = runtimev1.ContainerState_CONTAINER_RUNNING
	}
	return &runtimev1.StartContainerResponse{}, nil
}

func (m *resourceMockCRI) StopContainer(_ context.Context, req *runtimev1.StopContainerRequest) (*runtimev1.StopContainerResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctr, ok := m.containers[req.ContainerId]; ok {
		ctr.State = runtimev1.ContainerState_CONTAINER_EXITED
	}
	return &runtimev1.StopContainerResponse{}, nil
}

func (m *resourceMockCRI) RemoveContainer(_ context.Context, req *runtimev1.RemoveContainerRequest) (*runtimev1.RemoveContainerResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.containers, req.ContainerId)
	delete(m.ctrStatuses, req.ContainerId)
	return &runtimev1.RemoveContainerResponse{}, nil
}

func (m *resourceMockCRI) ListContainers(_ context.Context, req *runtimev1.ListContainersRequest) (*runtimev1.ListContainersResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var items []*runtimev1.Container
	for _, ctr := range m.containers {
		if req.Filter.GetPodSandboxId() == "" || ctr.PodSandboxId == req.Filter.GetPodSandboxId() {
			items = append(items, ctr)
		}
	}
	return &runtimev1.ListContainersResponse{Containers: items}, nil
}

func (m *resourceMockCRI) ContainerStatus(_ context.Context, req *runtimev1.ContainerStatusRequest) (*runtimev1.ContainerStatusResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if status, ok := m.ctrStatuses[req.ContainerId]; ok {
		return &runtimev1.ContainerStatusResponse{Status: status}, nil
	}
	// Default: return a status with exit code 0
	return &runtimev1.ContainerStatusResponse{
		Status: &runtimev1.ContainerStatus{
			Id:       req.ContainerId,
			State:    runtimev1.ContainerState_CONTAINER_EXITED,
			ExitCode: 0,
		},
	}, nil
}

func resourceMatchLabels(podLabels, selector map[string]string) bool {
	for k, v := range selector {
		if podLabels[k] != v {
			return false
		}
	}
	return true
}

// startResourceMockCRI starts a gRPC CRI mock on a unix socket and returns
// the socket path and the mock instance (for state manipulation).
func startResourceMockCRI(t *testing.T) (string, *resourceMockCRI) {
	t.Helper()
	sock := testsock.Path(t, "cri.sock")
	lis, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	mock := newResourceMockCRI()
	runtimev1.RegisterRuntimeServiceServer(srv, mock)
	runtimev1.RegisterImageServiceServer(srv, mock)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.GracefulStop)
	return sock, mock
}

// dialTestCRI starts a mock CRI and returns a connected cri.Client.
func dialTestCRI(t *testing.T) (*cri.Client, *resourceMockCRI) {
	t.Helper()
	sock, mock := startResourceMockCRI(t)
	ctx := context.Background()
	client, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("cri.Dial: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client, mock
}

// ===========================================================================
// GetProviderStatusWithVersion tests (with CRI mock)
// ===========================================================================

func TestCtrDef_StatusWithVer_NoPods(t *testing.T) {
	client, _ := dialTestCRI(t)
	d := &ContainerDefinition{Client: client}
	ref := newRef("proj/svc", ContainerKey)
	status, err := d.GetProviderStatusWithVersion(ref, "v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.GetShort() != typing.RolloutStatusPending {
		t.Fatalf("expected PENDING (no pods), got %s", status.GetShort())
	}
}

func TestCtrDef_StatusWithVer_Running(t *testing.T) {
	client, mock := dialTestCRI(t)

	// Inject a pod and a running container.
	mock.mu.Lock()
	mock.pods["pod-test"] = &runtimev1.PodSandbox{
		Id:    "pod-test",
		State: runtimev1.PodSandboxState_SANDBOX_READY,
		Labels: map[string]string{
			cri.LabelProject: "proj",
			cri.LabelService: "svc",
			cri.LabelVersion: "v1",
		},
	}
	mock.containers["ctr-test"] = &runtimev1.Container{
		Id:           "ctr-test",
		PodSandboxId: "pod-test",
		State:        runtimev1.ContainerState_CONTAINER_RUNNING,
	}
	mock.mu.Unlock()

	d := &ContainerDefinition{Client: client}
	ref := newRef("proj/svc", ContainerKey)
	status, err := d.GetProviderStatusWithVersion(ref, "v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.GetShort() != typing.RolloutStatusRunning {
		t.Fatalf("expected RUNNING, got %s", status.GetShort())
	}
}

func TestCtrDef_StatusWithVer_Drift(t *testing.T) {
	client, mock := dialTestCRI(t)

	// Inject a pod with version "v1" but expect "v2".
	mock.mu.Lock()
	mock.pods["pod-drift"] = &runtimev1.PodSandbox{
		Id:    "pod-drift",
		State: runtimev1.PodSandboxState_SANDBOX_READY,
		Labels: map[string]string{
			cri.LabelProject: "proj",
			cri.LabelService: "svc",
			cri.LabelVersion: "v1",
		},
	}
	mock.containers["ctr-drift"] = &runtimev1.Container{
		Id:           "ctr-drift",
		PodSandboxId: "pod-drift",
		State:        runtimev1.ContainerState_CONTAINER_RUNNING,
	}
	mock.mu.Unlock()

	d := &ContainerDefinition{Client: client}
	ref := newRef("proj/svc", ContainerKey)
	status, err := d.GetProviderStatusWithVersion(ref, "v2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.GetShort() != typing.RolloutStatusDrifted {
		t.Fatalf("expected DRIFTED, got %s", status.GetShort())
	}
}

func TestCtrDef_StatusWithVer_NoCtr(t *testing.T) {
	client, mock := dialTestCRI(t)

	// Pod exists but no containers in it.
	mock.mu.Lock()
	mock.pods["pod-empty"] = &runtimev1.PodSandbox{
		Id:    "pod-empty",
		State: runtimev1.PodSandboxState_SANDBOX_READY,
		Labels: map[string]string{
			cri.LabelProject: "proj",
			cri.LabelService: "svc",
		},
	}
	mock.mu.Unlock()

	d := &ContainerDefinition{Client: client}
	ref := newRef("proj/svc", ContainerKey)
	status, err := d.GetProviderStatusWithVersion(ref, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.GetShort() != typing.RolloutStatusPending {
		t.Fatalf("expected PENDING (no containers), got %s", status.GetShort())
	}
}

func TestCtrDef_StatusWithVer_NoExpVer(t *testing.T) {
	client, mock := dialTestCRI(t)

	// expectedVersion is "" so version check is skipped.
	mock.mu.Lock()
	mock.pods["pod-noexp"] = &runtimev1.PodSandbox{
		Id:    "pod-noexp",
		State: runtimev1.PodSandboxState_SANDBOX_READY,
		Labels: map[string]string{
			cri.LabelProject: "proj",
			cri.LabelService: "svc",
			cri.LabelVersion: "v99",
		},
	}
	mock.containers["ctr-noexp"] = &runtimev1.Container{
		Id:           "ctr-noexp",
		PodSandboxId: "pod-noexp",
		State:        runtimev1.ContainerState_CONTAINER_RUNNING,
	}
	mock.mu.Unlock()

	d := &ContainerDefinition{Client: client}
	ref := newRef("proj/svc", ContainerKey)
	status, err := d.GetProviderStatusWithVersion(ref, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No drift expected since expectedVersion is empty.
	if status.GetShort() != typing.RolloutStatusRunning {
		t.Fatalf("expected RUNNING, got %s", status.GetShort())
	}
}

// ===========================================================================
// containerStateStatus tests with CRI mock (state=2 EXITED path)
// ===========================================================================

func TestCtrStateStatus_ExitCodeZero(t *testing.T) {
	client, mock := dialTestCRI(t)

	// Set up container status with exit code 0.
	mock.mu.Lock()
	mock.ctrStatuses["ctr-exited-ok"] = &runtimev1.ContainerStatus{
		Id:       "ctr-exited-ok",
		State:    runtimev1.ContainerState_CONTAINER_EXITED,
		ExitCode: 0,
	}
	mock.mu.Unlock()

	d := &ContainerDefinition{Client: client}
	status, err := d.containerStateStatus(2, "ctr-exited-ok")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.GetShort() != typing.RolloutStatusSuccess {
		t.Fatalf("expected SUCCEEDED for exit code 0, got %s", status.GetShort())
	}
}

func TestCtrStateStatus_NonZeroExit(t *testing.T) {
	client, mock := dialTestCRI(t)

	// Set up container status with non-zero exit code.
	mock.mu.Lock()
	mock.ctrStatuses["ctr-exited-fail"] = &runtimev1.ContainerStatus{
		Id:       "ctr-exited-fail",
		State:    runtimev1.ContainerState_CONTAINER_EXITED,
		ExitCode: 137,
	}
	mock.mu.Unlock()

	d := &ContainerDefinition{Client: client}
	status, err := d.containerStateStatus(2, "ctr-exited-fail")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.GetShort() != typing.RolloutStatusError {
		t.Fatalf("expected ERROR for non-zero exit code, got %s", status.GetShort())
	}
	details := status.GetDetails()
	if details == nil {
		t.Fatal("expected non-nil details")
		return
	}
	var msg string
	if err := json.Unmarshal(details, &msg); err != nil {
		t.Fatalf("unmarshal details: %v", err)
	}
	if !strings.Contains(msg, "137") {
		t.Fatalf("expected exit code 137 in message, got %q", msg)
	}
}

// ===========================================================================
// exitedContainerStatus tests (with CRI mock)
// ===========================================================================

func TestExitedCtrStatus_CodeZero(t *testing.T) {
	client, mock := dialTestCRI(t)

	mock.mu.Lock()
	mock.ctrStatuses["ctr-ok"] = &runtimev1.ContainerStatus{
		Id:       "ctr-ok",
		State:    runtimev1.ContainerState_CONTAINER_EXITED,
		ExitCode: 0,
	}
	mock.mu.Unlock()

	d := &ContainerDefinition{Client: client}
	status, err := d.exitedContainerStatus("ctr-ok")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.GetShort() != typing.RolloutStatusSuccess {
		t.Fatalf("expected SUCCEEDED, got %s", status.GetShort())
	}
}

func TestExitedCtrStatus_CodeNonZero(t *testing.T) {
	client, mock := dialTestCRI(t)

	mock.mu.Lock()
	mock.ctrStatuses["ctr-fail"] = &runtimev1.ContainerStatus{
		Id:       "ctr-fail",
		State:    runtimev1.ContainerState_CONTAINER_EXITED,
		ExitCode: 1,
	}
	mock.mu.Unlock()

	d := &ContainerDefinition{Client: client}
	status, err := d.exitedContainerStatus("ctr-fail")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.GetShort() != typing.RolloutStatusError {
		t.Fatalf("expected ERROR, got %s", status.GetShort())
	}
}

func TestExitedCtrStatus_Default(t *testing.T) {
	client, mock := dialTestCRI(t)

	// ContainerStatus returns a status with ExitCode 0 and non-nil Status
	// by default when there's no override in ctrStatuses. The default
	// handler returns ExitCode=0 which means SUCCEEDED.
	_ = mock

	d := &ContainerDefinition{Client: client}
	status, err := d.exitedContainerStatus("nonexistent-ctr")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Default mock returns exit code 0 -> SUCCEEDED.
	if status.GetShort() != typing.RolloutStatusSuccess {
		t.Fatalf("expected SUCCEEDED, got %s", status.GetShort())
	}
}

// ===========================================================================
// ContainerDefinition.Delete tests (with CRI mock)
// ===========================================================================

func TestCtrDef_Delete_WithClient(t *testing.T) {
	client, mock := dialTestCRI(t)

	// Create a pod+container via the mock so ServiceDown has something to remove.
	mock.mu.Lock()
	mock.pods["pod-del"] = &runtimev1.PodSandbox{
		Id:    "pod-del",
		State: runtimev1.PodSandboxState_SANDBOX_READY,
		Labels: map[string]string{
			cri.LabelProject: "delproj",
			cri.LabelService: "delsvc",
		},
	}
	mock.containers["ctr-del"] = &runtimev1.Container{
		Id:           "ctr-del",
		PodSandboxId: "pod-del",
		State:        runtimev1.ContainerState_CONTAINER_RUNNING,
	}
	mock.mu.Unlock()

	d := &ContainerDefinition{Client: client}
	ref := newRef("delproj/delsvc", ContainerKey)
	err := d.Delete(ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the pod and container were removed.
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if _, ok := mock.pods["pod-del"]; ok {
		t.Fatal("expected pod to be removed")
	}
	if _, ok := mock.containers["ctr-del"]; ok {
		t.Fatal("expected container to be removed")
	}
}

func TestCtrDef_Delete_NoPods(t *testing.T) {
	client, _ := dialTestCRI(t)
	d := &ContainerDefinition{Client: client}
	ref := newRef("noproj/nosvc", ContainerKey)
	// Delete with no matching pods should succeed (idempotent).
	err := d.Delete(ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ===========================================================================
// ContainerInstance.Apply tests (with CRI mock)
// ===========================================================================

func TestCtrInst_Apply_WithClient(t *testing.T) {
	client, mock := dialTestCRI(t)

	ci := &ContainerInstance{
		Spec: ContainerSpec{
			Project:     "applyproj",
			Service:     "applysvc",
			Version:     "v1",
			Image:       "nginx:latest",
			NetworkMode: "host",
		},
		client: client,
	}
	err := ci.Apply()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify that the mock now has a pod and container.
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.pods) == 0 {
		t.Fatal("expected at least one pod after Apply")
	}
	if len(mock.containers) == 0 {
		t.Fatal("expected at least one container after Apply")
	}

	// Verify the container is running.
	for _, ctr := range mock.containers {
		if ctr.State != runtimev1.ContainerState_CONTAINER_RUNNING {
			t.Fatalf("expected container state RUNNING, got %v", ctr.State)
		}
	}
}

func TestCtrInst_Apply_WithVolStore(t *testing.T) {
	client, _ := dialTestCRI(t)
	volStore := &volumes.Store{Root: t.TempDir()}

	ci := &ContainerInstance{
		Spec: ContainerSpec{
			Project:     "volproj",
			Service:     "volsvc",
			Version:     "v1",
			Image:       "nginx:latest",
			NetworkMode: "host",
		},
		client:   client,
		volStore: volStore,
	}
	err := ci.Apply()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ===========================================================================
// ImageDefinition.Delete tests (with CRI mock)
// ===========================================================================

func TestImgDef_Delete_WithClient(t *testing.T) {
	client, mock := dialTestCRI(t)

	// Pre-populate an image.
	mock.mu.Lock()
	mock.images["alpine:latest"] = &runtimev1.Image{
		Id:   "sha256:alpine",
		Spec: &runtimev1.ImageSpec{Image: "alpine:latest"},
	}
	mock.mu.Unlock()

	d := &ImageDefinition{Client: client}
	ref := newRef("alpine:latest", ImageKey)
	err := d.Delete(ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the image was removed.
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if _, ok := mock.images["alpine:latest"]; ok {
		t.Fatal("expected image to be removed")
	}
}

func TestImgDef_Delete_NonExistent(t *testing.T) {
	client, _ := dialTestCRI(t)
	d := &ImageDefinition{Client: client}
	ref := newRef("nonexistent:latest", ImageKey)
	// Deleting a non-existent image should succeed (idempotent per CRI spec).
	err := d.Delete(ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ===========================================================================
// ImageDefinition.GetProviderStatus tests (with CRI mock)
// ===========================================================================

func TestImgDef_ProviderStatus_Present(t *testing.T) {
	client, mock := dialTestCRI(t)

	// Pre-populate an image.
	mock.mu.Lock()
	mock.images["redis:7"] = &runtimev1.Image{
		Id:   "sha256:redis7",
		Spec: &runtimev1.ImageSpec{Image: "redis:7"},
	}
	mock.mu.Unlock()

	d := &ImageDefinition{Client: client}
	ref := newRef("redis:7", ImageKey)
	status, err := d.GetProviderStatus(ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.GetShort() != typing.RolloutStatusSuccess {
		t.Fatalf("expected SUCCEEDED for present image, got %s", status.GetShort())
	}
}

func TestImgDef_ProviderStatus_Absent(t *testing.T) {
	client, _ := dialTestCRI(t)
	d := &ImageDefinition{Client: client}
	ref := newRef("absent:latest", ImageKey)
	status, err := d.GetProviderStatus(ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.GetShort() != typing.RolloutStatusPending {
		t.Fatalf("expected PENDING for absent image, got %s", status.GetShort())
	}
}

func TestImgDef_GetStatus_Present(t *testing.T) {
	client, mock := dialTestCRI(t)

	mock.mu.Lock()
	mock.images["mysql:8"] = &runtimev1.Image{
		Id:   "sha256:mysql8",
		Spec: &runtimev1.ImageSpec{Image: "mysql:8"},
	}
	mock.mu.Unlock()

	d := &ImageDefinition{Client: client}
	ref := newRef("mysql:8", ImageKey)
	status, err := d.GetStatus(ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.GetShort() != typing.RolloutStatusSuccess {
		t.Fatalf("expected SUCCEEDED, got %s", status.GetShort())
	}
}

// ===========================================================================
// ImageInstance.Apply tests (with CRI mock)
// ===========================================================================

func TestImgInst_Apply_WithClient(t *testing.T) {
	client, mock := dialTestCRI(t)

	ii := &ImageInstance{
		Spec:   ImageSpec{Image: "busybox:latest"},
		client: client,
	}
	err := ii.Apply()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the image was pulled.
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if _, ok := mock.images["busybox:latest"]; !ok {
		t.Fatal("expected image to be present after PullImage")
	}
}

// ===========================================================================
// ProjectDefinition.Delete tests (with CRI mock)
// ===========================================================================

func TestProjDef_Delete_WithClient(t *testing.T) {
	client, mock := dialTestCRI(t)

	// Create two pods in the project.
	mock.mu.Lock()
	mock.pods["pod-p1"] = &runtimev1.PodSandbox{
		Id:    "pod-p1",
		State: runtimev1.PodSandboxState_SANDBOX_READY,
		Labels: map[string]string{
			cri.LabelProject: "myproject",
			cri.LabelService: "web",
		},
	}
	mock.containers["ctr-p1"] = &runtimev1.Container{
		Id:           "ctr-p1",
		PodSandboxId: "pod-p1",
		State:        runtimev1.ContainerState_CONTAINER_RUNNING,
	}
	mock.pods["pod-p2"] = &runtimev1.PodSandbox{
		Id:    "pod-p2",
		State: runtimev1.PodSandboxState_SANDBOX_READY,
		Labels: map[string]string{
			cri.LabelProject: "myproject",
			cri.LabelService: "db",
		},
	}
	mock.containers["ctr-p2"] = &runtimev1.Container{
		Id:           "ctr-p2",
		PodSandboxId: "pod-p2",
		State:        runtimev1.ContainerState_CONTAINER_RUNNING,
	}
	mock.mu.Unlock()

	d := &ProjectDefinition{Client: client}
	ref := newRef("myproject", ProjectKey)
	err := d.Delete(ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify all pods and containers were removed.
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.pods) != 0 {
		t.Fatalf("expected all pods removed, got %d", len(mock.pods))
	}
	if len(mock.containers) != 0 {
		t.Fatalf("expected all containers removed, got %d", len(mock.containers))
	}
}

func TestProjDef_Delete_NoPods(t *testing.T) {
	client, _ := dialTestCRI(t)
	d := &ProjectDefinition{Client: client}
	ref := newRef("emptyproject", ProjectKey)
	// Delete with no pods should succeed (idempotent).
	err := d.Delete(ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ===========================================================================
// GetProviderStatusWithVersion - full pipeline with state=2 (EXITED)
// ===========================================================================

func TestCtrDef_StatusWithVer_Exited(t *testing.T) {
	tests := []struct {
		name       string
		exitCode   int32
		project    string
		service    string
		wantStatus typing.RolloutStatusShort
	}{
		{"ExitCodeZero", 0, "ep", "es", typing.RolloutStatusSuccess},
		{"ExitCodeNonZero", 42, "ep2", "es2", typing.RolloutStatusError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, mock := dialTestCRI(t)

			podID := "pod-" + tt.name
			ctrID := "ctr-" + tt.name

			mock.mu.Lock()
			mock.pods[podID] = &runtimev1.PodSandbox{
				Id:    podID,
				State: runtimev1.PodSandboxState_SANDBOX_READY,
				Labels: map[string]string{
					cri.LabelProject: tt.project,
					cri.LabelService: tt.service,
					cri.LabelVersion: "v1",
				},
			}
			mock.containers[ctrID] = &runtimev1.Container{
				Id:           ctrID,
				PodSandboxId: podID,
				State:        runtimev1.ContainerState_CONTAINER_EXITED,
			}
			mock.ctrStatuses[ctrID] = &runtimev1.ContainerStatus{
				Id:       ctrID,
				State:    runtimev1.ContainerState_CONTAINER_EXITED,
				ExitCode: tt.exitCode,
			}
			mock.mu.Unlock()

			d := &ContainerDefinition{Client: client}
			ref := newRef(tt.project+"/"+tt.service, ContainerKey)
			status, err := d.GetProviderStatusWithVersion(ref, "v1")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if status.GetShort() != tt.wantStatus {
				t.Fatalf("expected %s for exit code %d, got %s", tt.wantStatus, tt.exitCode, status.GetShort())
			}
		})
	}
}

// ===========================================================================
// GetProviderStatus (which delegates to GetProviderStatusWithVersion)
// ===========================================================================

func TestCtrDef_ProviderStatus_Running(t *testing.T) {
	client, mock := dialTestCRI(t)

	mock.mu.Lock()
	mock.pods["pod-ps"] = &runtimev1.PodSandbox{
		Id:    "pod-ps",
		State: runtimev1.PodSandboxState_SANDBOX_READY,
		Labels: map[string]string{
			cri.LabelProject: "psproj",
			cri.LabelService: "pssvc",
		},
	}
	mock.containers["ctr-ps"] = &runtimev1.Container{
		Id:           "ctr-ps",
		PodSandboxId: "pod-ps",
		State:        runtimev1.ContainerState_CONTAINER_RUNNING,
	}
	mock.mu.Unlock()

	d := &ContainerDefinition{Client: client}
	ref := newRef("psproj/pssvc", ContainerKey)
	status, err := d.GetProviderStatus(ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.GetShort() != typing.RolloutStatusRunning {
		t.Fatalf("expected RUNNING, got %s", status.GetShort())
	}
}

// ===========================================================================
// GetStatus (which delegates to GetProviderStatus)
// ===========================================================================

func TestCtrDef_GetStatus_WithClient(t *testing.T) {
	client, mock := dialTestCRI(t)

	mock.mu.Lock()
	mock.pods["pod-gs"] = &runtimev1.PodSandbox{
		Id:    "pod-gs",
		State: runtimev1.PodSandboxState_SANDBOX_READY,
		Labels: map[string]string{
			cri.LabelProject: "gsproj",
			cri.LabelService: "gssvc",
		},
	}
	mock.containers["ctr-gs"] = &runtimev1.Container{
		Id:           "ctr-gs",
		PodSandboxId: "pod-gs",
		State:        runtimev1.ContainerState_CONTAINER_RUNNING,
	}
	mock.mu.Unlock()

	d := &ContainerDefinition{Client: client}
	ref := newRef("gsproj/gssvc", ContainerKey)
	status, err := d.GetStatus(ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.GetShort() != typing.RolloutStatusRunning {
		t.Fatalf("expected RUNNING, got %s", status.GetShort())
	}
}

// ===========================================================================
// Container CREATED state (state 0) through full pipeline
// ===========================================================================

func TestCtrDef_StatusWithVer_Created(t *testing.T) {
	client, mock := dialTestCRI(t)

	mock.mu.Lock()
	mock.pods["pod-created"] = &runtimev1.PodSandbox{
		Id:    "pod-created",
		State: runtimev1.PodSandboxState_SANDBOX_READY,
		Labels: map[string]string{
			cri.LabelProject: "cp",
			cri.LabelService: "cs",
		},
	}
	mock.containers["ctr-created"] = &runtimev1.Container{
		Id:           "ctr-created",
		PodSandboxId: "pod-created",
		State:        runtimev1.ContainerState_CONTAINER_CREATED,
	}
	mock.mu.Unlock()

	d := &ContainerDefinition{Client: client}
	ref := newRef("cp/cs", ContainerKey)
	status, err := d.GetProviderStatusWithVersion(ref, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.GetShort() != typing.RolloutStatusPending {
		t.Fatalf("expected PENDING for created container, got %s", status.GetShort())
	}
}

// ===========================================================================
// ServiceDefinition.Delete with CRI mock (delegates to ContainerDefinition)
// ===========================================================================

func TestSvcDef_Delete_WithClient(t *testing.T) {
	client, mock := dialTestCRI(t)

	mock.mu.Lock()
	mock.pods["pod-svcdel"] = &runtimev1.PodSandbox{
		Id:    "pod-svcdel",
		State: runtimev1.PodSandboxState_SANDBOX_READY,
		Labels: map[string]string{
			cri.LabelProject: "svcproj",
			cri.LabelService: "svcsvc",
		},
	}
	mock.containers["ctr-svcdel"] = &runtimev1.Container{
		Id:           "ctr-svcdel",
		PodSandboxId: "pod-svcdel",
		State:        runtimev1.ContainerState_CONTAINER_RUNNING,
	}
	mock.mu.Unlock()

	d := &ServiceDefinition{Client: client}
	ref := newRef("svcproj/svcsvc", ServiceKey)
	err := d.Delete(ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.pods) != 0 {
		t.Fatal("expected pod to be removed via service delete")
	}
}

// ===========================================================================
// ServiceInstance.Apply with CRI mock (delegates to ContainerInstance.Apply)
// ===========================================================================

func TestSvcInst_Apply_WithClient(t *testing.T) {
	client, mock := dialTestCRI(t)

	si := &ServiceInstance{
		Spec: ServiceSpec{
			Container: ContainerSpec{
				Project:     "svcapplyproj",
				Service:     "svcapplysvc",
				Version:     "v1",
				Image:       "nginx:latest",
				NetworkMode: "host",
			},
		},
		client: client,
	}
	err := si.Apply()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.pods) == 0 {
		t.Fatal("expected pod after service Apply")
	}
	if len(mock.containers) == 0 {
		t.Fatal("expected container after service Apply")
	}
}

// ===========================================================================
// ServiceDefinition.GetProviderStatus with CRI mock
// ===========================================================================

func TestSvcDef_ProviderStatus_Client(t *testing.T) {
	client, mock := dialTestCRI(t)

	mock.mu.Lock()
	mock.pods["pod-svcps"] = &runtimev1.PodSandbox{
		Id:    "pod-svcps",
		State: runtimev1.PodSandboxState_SANDBOX_READY,
		Labels: map[string]string{
			cri.LabelProject: "svcpsproj",
			cri.LabelService: "svcpssvc",
		},
	}
	mock.containers["ctr-svcps"] = &runtimev1.Container{
		Id:           "ctr-svcps",
		PodSandboxId: "pod-svcps",
		State:        runtimev1.ContainerState_CONTAINER_RUNNING,
	}
	mock.mu.Unlock()

	d := &ServiceDefinition{Client: client}
	ref := newRef("svcpsproj/svcpssvc", ServiceKey)
	status, err := d.GetProviderStatus(ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.GetShort() != typing.RolloutStatusRunning {
		t.Fatalf("expected RUNNING, got %s", status.GetShort())
	}
}

// ===========================================================================
// ServiceDefinition.GetStatus with CRI mock
// ===========================================================================

func TestSvcDef_GetStatus_Client(t *testing.T) {
	client, mock := dialTestCRI(t)

	mock.mu.Lock()
	mock.pods["pod-svcgs"] = &runtimev1.PodSandbox{
		Id:    "pod-svcgs",
		State: runtimev1.PodSandboxState_SANDBOX_READY,
		Labels: map[string]string{
			cri.LabelProject: "svcgsproj",
			cri.LabelService: "svcgssvc",
		},
	}
	mock.containers["ctr-svcgs"] = &runtimev1.Container{
		Id:           "ctr-svcgs",
		PodSandboxId: "pod-svcgs",
		State:        runtimev1.ContainerState_CONTAINER_RUNNING,
	}
	mock.mu.Unlock()

	d := &ServiceDefinition{Client: client}
	ref := newRef("svcgsproj/svcgssvc", ServiceKey)
	status, err := d.GetStatus(ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.GetShort() != typing.RolloutStatusRunning {
		t.Fatalf("expected RUNNING, got %s", status.GetShort())
	}
}
