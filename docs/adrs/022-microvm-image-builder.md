# ADR-022: MicroVM image builder

**Status:** Accepted
**Date:** 2026-04-18
**Context:** M14 — MicroVM runtime

## Context

M14 Phase 4 (ADR-021) wired `pkg/microvm/` into the CLI so users can
boot a microVM with `nix-compose up --microvm --vm-kernel <path>
--vm-rootfs <path>`. However, producing the kernel and rootfs requires
users to manually build a NixOS closure with containerd, CNI plugins,
and the `nix-compose serve` systemd unit. This is error-prone and
creates a steep onboarding barrier.

## Decision

Ship a NixOS module (`nix/microvm-image.nix`) that defines a minimal VM
closure and a rootfs builder (`nix/make-rootfs.nix`) that produces a
read-only erofs image. When `--microvm` is used without explicit
`--vm-kernel` / `--vm-rootfs`, the CLI automatically invokes `nix build`
to produce the image.

### Image composition

The NixOS module produces:

- **Kernel:** uncompressed vmlinux (x86\_64) or Image (aarch64) with
  `virtio_vsock`, `virtiofs`, and `overlay` modules
- **Root filesystem:** erofs image containing the NixOS closure with:
  - systemd as PID 1 (`boot.initrd.systemd.enable = true`)
  - containerd
  - CNI plugins (exposed via `CNI_PATH`)
  - `nix-compose serve --vsock-port 1024` systemd unit
  - Virtiofs mounts: `/nix/store` (ro, tag `nix-store`),
    `/workspace` (rw, tag `project`)
  - Minimal footprint: no SSH, no docs, no polkit, no nix daemon

### Go builder

`pkg/microvm/builder/` wraps the nix build invocation:

```go
type Builder struct {
    Runner    eval.CommandRunner
    FlakeRef  string  // default: "."
    FlakeAttr string  // default: "microvm-image"
}

func (b *Builder) Build(ctx context.Context) (*ImagePaths, error)
```

It runs `nix build <ref>#<attr> --no-link --print-out-paths`, parses
the output path, and resolves `kernel` / `rootfs` symlinks via
`filepath.EvalSymlinks`.

### CLI integration

- `buildVMImage` package-level variable in `pkg/cli/microvm.go`
  (same testability pattern as `newVM` / `dialVsock`)
- `microvmConfig()` auto-triggers the build when kernel or rootfs is
  missing after merging CLI flags and Nix config
- New `--vm-image-flake` flag on `up` for overriding the flake reference

### Flake output

`packages.<system>.microvm-image` is gated on Linux systems and
produces a directory with `kernel` and `rootfs` symlinks.

## Alternatives considered

### microvm.nix flake input

Using the [microvm.nix](https://github.com/astro/microvm.nix) project
as a flake input would provide a richer VM abstraction but introduces
a heavy dependency and opinionated VM management that conflicts with
nix-compose's own lifecycle model.

### Docker-based rootfs

Building the rootfs from a Docker/OCI image would avoid NixOS but
would lose reproducibility and the ability to share `/nix/store` via
virtiofs.

### Pre-built binaries

Distributing pre-built kernel and rootfs binaries would simplify the
user experience but prevents customisation and breaks reproducibility
guarantees that Nix users expect.

## Consequences

- `nix-compose up --microvm` works without manual image preparation
- Users can override the flake reference with `--vm-image-flake`
- The image is reproducible and customisable via standard Nix overrides
- First boot incurs a one-time nix build cost; subsequent runs use the
  cached store path
- The erofs format provides a compact, read-only root that pairs well
  with cloud-hypervisor's disk backend
