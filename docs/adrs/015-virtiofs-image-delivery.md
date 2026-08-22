# ADR-015: Virtiofs image delivery for Nix-built images

**Status:** Accepted
**Date:** 2026-04-13
**Context:** MicroVM runtime design (M14)

## Context

When the orchestrate engine runs inside a microVM, container images
must reach containerd inside the VM. There are three categories of
images:

1. **Registry images** (`postgres:16`) — pulled from a registry
2. **Nix-built images** (`dockerTools.buildImage`) — OCI tarballs
   in `/nix/store`
3. **Host store closures** (`useHostStore`) — Nix closures
   bind-mounted directly into the container, no image at all

Registry images are straightforward (containerd pulls over the
network). The question is how Nix-built tarballs reach the VM.

## Decision

Deliver Nix-built images via the virtiofs `/nix/store` share that
already exists for the host store mount. No additional transport.

### Image delivery paths

| Image source | Delivery mechanism |
|---|---|
| Registry (`postgres:16`) | CRI `PullImage` — containerd pulls over the network |
| Nix-built tarball | virtiofs `/nix/store` — already on the VM filesystem; `containerd.Import()` loads into content store |
| `useHostStore` closure | virtiofs bind mount — no image, no import, binary runs directly |

### How Nix-built import works

```
Host: nix build → /nix/store/xxx-image.tar
VM:   virtiofs   → /nix/store/xxx-image.tar (read-only)
VM:   containerd.Import(ctx, file) → image in content store
VM:   CRI PullImage / ImageStatus sees it normally
```

CRI's `ImageService.PullImage` only supports registry pulls — it has
no local file import. The import uses containerd's native Go API
(`containerd.Client.Import()`), the same mechanism as `ctr images
import` and `nerdctl load`. After import, the image is visible to CRI
normally.

## Consequences

- No image copying, no streaming over vsock, no registry proxy
- Zero additional latency for Nix-built images — the tarball is
  already present on the VM filesystem via virtiofs
- The orchestrate engine needs `containerd.Client` (native API) in
  addition to the CRI gRPC client for the import path
- The `Image` resource definition must detect whether the image ref
  is a Nix store path (local import) or a registry tag (CRI pull)
- virtiofs read-only mount means the VM cannot modify `/nix/store`
  — correct, since images are consumed not produced

## Implementation

Implemented 2026-08-18 in `pkg/cri/import.go`, and not only for the microVM:
the same path is what lets any `nix-compose up` consume a Nix-built image
without a registry, so the local-import decision here applies host-side too.

- `EnsureImage` replaces the unconditional `PullImage` at every call site. A
  reference beginning with `/` is a local artifact; anything else is a registry
  tag, and is pulled only when `ImageStatus` reports it missing.
- Both shapes nix-oci emits are accepted: an OCI layout directory (tarred on
  the fly) and an oci-archive tarball.
- The import registers the image as `nix-compose.local/<name>:<store-hash>`,
  because the store path is not a reference CRI can resolve. The store hash as
  the tag makes the reference content-addressed: a rebuilt image lands under a
  new reference, and an unchanged one short-circuits on `ImageStatus`.
- The import must target the **`k8s.io`** containerd namespace. The CRI plugin
  sees no other namespace, so importing into containerd's `default` leaves the
  image invisible to `crictl` and to nix-compose.
- CRI learns of the new image from a containerd event, so `ImportLocalImage`
  polls `ImageStatus` briefly rather than assuming it is immediately visible.

## Alternatives Considered

1. **Stream over vsock** — host sends tarball bytes over the gRPC
   connection to the VM. Rejected: adds a custom RPC, duplicates
   data that is already shared via virtiofs, and is slower than a
   local filesystem read.

2. **Local registry proxy** — run a registry inside the VM, push
   Nix-built images to it. Rejected: unnecessary complexity, adds a
   registry daemon, and Nix tarballs would need to be pushed before
   every deployment.

3. **containerd image store share** — share containerd's content
   store directory between host and VM. Rejected: containerd's
   internal storage format is not a stable API, and concurrent
   access from two containerd instances would corrupt state.

4. **`skopeo copy`** — copy images into the VM's containerd.
   Rejected: adds a dependency, slower than direct import, and
   requires the image to be available as a registry or
   `docker-daemon:` source on the host.
