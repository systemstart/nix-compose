# Running nix-compose in CI

nix-compose is daemonless. There is no root daemon to hide behind, so the
privilege a container runtime normally absorbs has to come from somewhere
else — and on a CI runner, "somewhere else" is usually the thing you are
not allowed to have.

This document is the operational counterpart to the privilege analysis in
[ADR-010](adrs/010-three-mode-privilege-model.md).
That one explains *why* each operation needs what it needs. This one
answers a narrower question: **given a CI runner, can nix-compose run on
it, and what would it take?**

## The three host modes

| Mode | Runner must provide | Gets you | Blast radius |
|---|---|---|---|
| **A — host CRI** | A writable CRI socket (`/run/containerd/containerd.sock`) | Everything, immediately | Socket access is root-equivalent on the node |
| **B — rootless CRI** | User namespaces, cgroup v2 delegation, a rootless containerd per job | Everything, in principle | Contained |
| **C — microVM** | `/dev/kvm` + `CAP_SETGID` | Everything, isolated | One VM per job |

Mode A is what a developer laptop uses and what `nix-compose doctor`
expects to find. It is also the one a hardened CI runner is least likely
to grant, because a job that can talk to the node's CRI socket can create
a privileged container on that node — the same trade-off Docker makes with
the `docker` group, and for the same reason it is documented as
root-equivalent.

Mode B is the appealing answer and the one that keeps not working. See
[Rootless](#rootless-is-not-currently-a-viable-ci-mode) below.

Mode C is the mode designed for exactly this shape of problem, and it is
the one worth pursuing for a locked-down runner. Its requirements are
smaller and much better contained than mode A's, but they are not zero —
see [MicroVM requirements](#microvm-mode-requirements).

## Deciding which mode you need

Run `nix-compose doctor` **as the CI job's user, inside the CI job**, not
on the runner host. The distinction matters: on a Kubernetes-based runner
the host has containerd and the job does not, so a doctor run in the wrong
place reports a working environment that no job can actually use.

If doctor reports a usable CRI socket, you are in mode A and there is
nothing further to do.

## MicroVM mode requirements

`--microvm` boots a NixOS guest under cloud-hypervisor and runs the whole
orchestrator inside it, sharing the host `/nix/store` read-only over
virtiofs. The host side needs:

- **`/dev/kvm`, readable and writable by the job's user.** Without it
  cloud-hypervisor cannot create a VM.
- **`CAP_SETGID`, for virtiofsd.** This one is easy to miss because it is
  not obviously a VM requirement at all. virtiofsd's `Sandbox::enter()`
  calls `drop_supplemental_groups()` → `setgroups(0, NULL)` *before* it
  checks which sandbox mode was requested, so it fails with

  ```
  [ERROR virtiofsd] Error entering sandbox: DropSupplementalGroups(
    Os { code: 1, kind: PermissionDenied, message: "Operation not permitted" })
  ```

  in any environment that lacks the capability — even running as root, and
  even with `--sandbox=none`. Upstream:
  [virtiofsd#36](https://gitlab.com/virtio-fs/virtiofsd/-/issues/36).
  nix-compose passes `--sandbox=none` only for read-only shares
  (`pkg/microvm/hypervisor.go`), and it would not help if it passed it
  everywhere.

If the runner cannot grant `CAP_SETGID`, the workable fix is to patch
virtiofsd in a Nix overlay so the `setgroups` EPERM is non-fatal
(`overrideAttrs` + a `postPatch` rewriting `drop_supplemental_groups` in
`src/sandbox.rs`). This costs a virtiofsd rebuild, and it is the same
patch the standalone CI-VM runner needs — one overlay unblocks both.

## Rootless is not currently a viable CI mode

Rootless containerd has been attempted and reached roughly 90% before
dying on cgroup v2 controller delegation: `cgroup.subtree_control` cannot
be written while processes remain in the cgroup-namespace root, surfacing
as `cpu.weight: no such file or directory`.

Two things make this worse rather than better on a CI runner:

- containerd 2.x removed `disable_cgroup` from the CRI plugin, so the
  usual rootless escape hatch is gone.
- A CI job in a container typically has a *harder* cgroup environment than
  a login shell, not an easier one — the delegation boundary is already
  spent on the outer container.

Treat mode B as unavailable until someone lands a fix, and do not design a
CI plan around it.

## Networking: what you lose without CNI

CNI is a separate axis from the three modes above. If the CNI plugins are
missing (`doctor` reports which), containers fall back to host networking:

- `ports:` is **not** mapped — the key is accepted and does nothing.
- Services find each other on `localhost`, not by service name.

For the common CI shape — one or more services under test, plus a test
process on the host dialling them — this is survivable and sometimes
simpler. The test process and the containers share a network namespace, so
`localhost:<port>` reaches the service directly and no mapping is needed.
Multi-service projects work too, as long as the services are configured to
find each other on `localhost` rather than by name.

What you cannot do without CNI is run two projects with overlapping ports
side by side, or rely on service-name DNS. On a runner with `capacity: 1`
the first rarely bites; the second requires rewriting the project's
addressing, which is a real cost for an imported compose file.

CNI additionally needs `iptables` **on the runtime's PATH**, not the
shell's — the runtime is what executes the plugin. `doctor` reports this
as an unknown rather than a failure, because reading the runtime's
environment requires the runtime's own uid.

## Worked example: a Kubernetes-hosted CI runner

A shape worth walking through, because it is both common and hardened
against exactly the privileges nix-compose needs. The runner is a
`StatefulSet` of Nix-based pods whose jobs execute directly inside the
runner container rather than in a per-job container of their own — so
whatever the pod is denied, the job is denied:

```yaml
securityContext:
  privileged: false
  allowPrivilegeEscalation: false
  capabilities:
    drop: [ALL]
    add: [CHOWN, DAC_OVERRIDE, FOWNER]
  seccompProfile: {type: RuntimeDefault}
```

Volumes are emptyDirs, secrets, and a configMap — no `hostPath`, no CRI
socket, no `/dev/kvm`.

Reading that against the table:

- **Mode A** would mean mounting the node's containerd socket via
  `hostPath`. It works in one line and grants every CI job the ability to
  create privileged containers on the node. If the cluster runs any kind of
  gated supply chain for its images, this quietly reintroduces the hole that
  gate exists to close.
- **Mode B** is out, per above — and this pod is a harder case than an
  ordinary login shell, where it already fails.
- **Mode C** needs two grants. `/dev/kvm` can be exposed to the pod without
  `privileged: true` by adding a device group to a device plugin, if the
  cluster already runs one; a cluster provisioned for KubeVirt will already
  have KVM available on its nodes. `CAP_SETGID` must then be added back to
  the container's capability list — or avoided entirely with the virtiofsd
  overlay above, which is preferable, since it keeps `drop: [ALL]` intact
  and fixes the standalone CI-VM path at the same time.

The conclusion generalises: **on a hardened runner, microVM mode plus a
patched virtiofsd is the only path that does not trade the runner's
security posture for the ability to run tests.** That makes the virtiofsd
overlay the highest-leverage piece of work for CI support, because it is
the single blocker shared by both VM-based paths.

## Related

- [ADR-010](adrs/010-three-mode-privilege-model.md)
  — per-operation privilege analysis and the three orchestrator modes
- [limitations.md](limitations.md) — what does not work, and why
- [install.md](install.md#requirements) — host requirements for normal use
