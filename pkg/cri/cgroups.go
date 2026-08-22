package cri

import (
	"context"
	"sync"

	runtimev1 "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// SystemdSlice is the systemd slice pods are placed under when the runtime uses
// the systemd cgroup driver. systemd creates it on demand, which keeps
// nix-compose's containers out of system.slice.
const SystemdSlice = "nix-compose.slice"

// cgroupParent returns the pod cgroup parent this runtime expects.
//
// A runtime configured with the systemd cgroup driver rejects the CRI default
// (an empty parent, which runc renders as a plain "/k8s.io/<id>" path) with
// "expected cgroupsPath to be of format slice:prefix:name". A runtime on the
// cgroupfs driver is happy with the default, so this returns "" for it.
//
// The answer cannot change under a running runtime, so it is resolved once.
func (c *Client) cgroupParent(ctx context.Context) string {
	c.cgroupOnce.Do(func() {
		resp, err := c.runtime.RuntimeConfig(ctx, &runtimev1.RuntimeConfigRequest{})
		if err != nil {
			// Older runtimes do not implement RuntimeConfig. Leaving the parent
			// empty preserves the behaviour they had before this call existed.
			return
		}
		if resp.GetLinux().GetCgroupDriver() == runtimev1.CgroupDriver_SYSTEMD {
			c.cgroupSlice = SystemdSlice
		}
	})
	return c.cgroupSlice
}

// applyCgroupParent fills in a pod config's cgroup parent when the runtime
// needs one and the caller has not set it. It is applied at RunPodSandbox
// rather than in BuildPodConfig because the right value is a property of the
// runtime, which only a connected client can ask about.
func (c *Client) applyCgroupParent(ctx context.Context, config *runtimev1.PodSandboxConfig) {
	if config == nil || config.Linux == nil || config.Linux.CgroupParent != "" {
		return
	}
	config.Linux.CgroupParent = c.cgroupParent(ctx)
}

// CgroupDriver reports which cgroup driver the runtime uses: "systemd",
// "cgroupfs", or "unknown" for a runtime too old to answer.
//
// It is derived from the same call and the same memoised answer that decides
// where pods are actually placed, so what `doctor` prints cannot drift from
// what the runtime does.
func (c *Client) CgroupDriver(ctx context.Context) string {
	switch c.cgroupParent(ctx) {
	case SystemdSlice:
		return "systemd"
	case "":
		// Either cgroupfs, or a runtime that does not implement
		// RuntimeConfig. Ask once more to tell those apart.
		if _, err := c.runtime.RuntimeConfig(ctx, &runtimev1.RuntimeConfigRequest{}); err != nil {
			return "unknown"
		}
		return "cgroupfs"
	default:
		return "unknown"
	}
}

// cgroupState is embedded in Client to memoise the runtime's cgroup driver.
type cgroupState struct {
	cgroupOnce  sync.Once
	cgroupSlice string
}
