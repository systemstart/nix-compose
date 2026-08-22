package cri

import (
	"testing"

	"github.com/systemstart/nix-compose/pkg/eval"
	runtimev1 "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// TestBuildContainerConfig_PidNamespace guards a default that is easy to
// regress by deletion: NamespaceMode_POD is the CRI zero value, so dropping
// the explicit Pid setting silently puts the entrypoint in the sandbox's PID
// namespace behind the pause process. s6-overlay images then die at start with
// "can only run as pid 1", and nothing in the compose file explains why.
func TestBuildContainerConfig_PidNamespace(t *testing.T) {
	cfg := BuildContainerConfig("web", eval.Service{Image: "alpine"}, "proj", "v1", nil)

	opts := cfg.Linux.GetSecurityContext().GetNamespaceOptions()
	if opts == nil {
		t.Fatal("NamespaceOptions is nil; Pid would default to NamespaceMode_POD")
	}
	if opts.Pid != runtimev1.NamespaceMode_CONTAINER {
		t.Errorf("Pid namespace = %v, want %v — the entrypoint must be PID 1",
			opts.Pid, runtimev1.NamespaceMode_CONTAINER)
	}
}
