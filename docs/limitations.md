# Known limitations

## Nix evaluation

### Eval performance on first run

The first `nix-compose up` in a project fetches flake inputs and evaluates
the full Nix expression, which can take 10-30 seconds. Subsequent runs
reuse the cached evaluation and are near-instant.

**Workaround:** Run `nix flake update` and `nix eval` once to warm caches.

### IFD (import-from-derivation) not supported

Nix configs that use `import-from-derivation` (building a derivation
during evaluation) are not supported. `nix eval --json` blocks on IFD
and may hang or fail.

**Workaround:** Pre-build derivations and reference their store paths
directly via `nixStorePaths`.

## YAML projects

These apply to `nix-compose.yaml` projects
([ADR-024](adrs/024-yaml-project-format.md)). None apply to flake or
`compose.nix` projects, which bring their own nixpkgs.

### `package:` versions are pinned by the nix-compose version, not the file

`image: nginx:1.27` states which nginx you get. `package: nginx` does not — it
resolves against the nixpkgs revision the binary carries (`pkg/nixpins`). Two
machines running different nix-compose versions therefore get different
packages from a byte-identical document, and upgrading nix-compose rebuilds and
re-imports every image a YAML project builds.

**Workaround:** Set a top-level `nixpkgs:` key to pin the revision in the file:

```yaml
nixpkgs: github:NixOS/nixpkgs/nixos-24.11
services:
  web:
    package: nginx
```

A generated lock file is the intended fix; see [ROADMAP.md](../ROADMAP.md).

### YAML evaluation is impure

Resolving `package:` runs `nix eval --impure`, because the expression reads
nix-compose's embedded Nix sources by absolute path. Nothing else about it is
impure: the system is passed in explicitly and the flake references are full
revisions. It does mean YAML projects will not evaluate where `pure-eval` or
`restrict-eval` is enforced.

**Workaround:** Use a flake project in those environments.

### No `eject` to Nix

A YAML project that outgrows the format — needing overlays, custom packages, or
shared configuration — has to be rewritten as a flake by hand. The YAML is a
strict subset of what `mkComposition` accepts, so nothing is lost, but the
conversion is manual.

### `suggest` only reads YAML projects

`nix-compose suggest` requires a `nix-compose.yaml`. For a flake, set
`package = pkgs.<name>;` directly; suggesting into Nix would mean parsing it.

### `import` is one-way

`nix-compose import` does not preserve hand edits on a re-run. It refuses to
overwrite an existing `nix-compose.yaml` without `--force`, which is what
protects them.

## Compose feature gaps

### `build:` is not honoured on the CRI backend

CRI has no build API. A service that declares `build:` and no `image` is
rejected with an error naming the alternatives, rather than silently starting
nothing.

**Workaround:** Build the image from a Nix closure instead —
`services.<name>.package` or `services.<name>.ociImage`, via `mkComposition`
([ADR-006](adrs/006-no-image-builds.md)). To keep a Dockerfile-based build,
run it externally — `docker build`, `podman build`, or your CI — and
reference the resulting image by tag.

### Locally built images need containerd

Importing a Nix-built image uses containerd's native API, which CRI's
`ImageService` does not expose ([ADR-015](adrs/015-virtiofs-image-delivery.md)).
CRI-O can pull registry images but cannot import local ones, so
`services.<name>.package` / `.ociImage` fail there.

**Workaround:** Use containerd, or push the image to a registry and reference
it by tag.

### `networks.ipam.config` not serialized

The `ipam.config` block for custom network subnets and gateways is parsed
but not passed through to the CNI network configuration.

**Workaround:** Use published host ports for service-to-service
communication, or define the network externally and reference it with
`external: true`.

### `--wait` fails on one-shot container exits

The `up --wait` flag expects all services to reach "running" or "healthy"
status. Services that exit immediately after completing their work
(e.g. migration containers) cause `--wait` to report failure.

**Workaround:** Use health-check polling on long-lived services instead,
or skip `--wait` when the composition includes one-shot containers.

### No override files

A project is one document. There is no mechanism to layer additional
override files on top, as `docker-compose.override.yaml` does.

**Workaround:** Express all config in Nix. Use Nix's module system or
attribute merging for environment-specific overrides.

### Container runtime differences

containerd is the primary test target. CRI-O implements the same
interface but differs in places — most visibly, it cannot import locally
built images (above). Behaviour under CRI-O is less exercised.

**Workaround:** Test against the runtime you deploy on.

### Named volumes inherit `$HOME`'s filesystem, which may reject `chown`

Named volumes are directories under
`$HOME/.local/share/nix-compose/volumes/<project>/<name>`, bind-mounted into
the container. That puts them on whatever filesystem backs the home
directory — and if that is virtiofs, 9p, or certain network filesystems,
`chown(2)` inside the container fails with `EINVAL`.

Plenty of images chown their data directory on first start, so this
surfaces as a service that exits immediately with a message like:

```
chown: /var/lib/postgresql/18/docker: Invalid argument
```

A bind mount from the project directory has the same problem when the
project lives on such a filesystem. It is not specific to named volumes.

**Workaround:** For test fixtures, drop the volume — the container's
writable layer sits on the runtime's own storage, which does support
`chown`, and starting from empty state each run is usually what a fixture
wants anyway. For real data, put the volume somewhere on a local
filesystem and reference it as an explicit bind mount rather than using a
named volume.

### Unix socket paths are limited to 108 bytes

Not a nix-compose limitation so much as one it runs into: a unix socket path
is copied into `sockaddr_un.sun_path`, a fixed 108-byte buffer on Linux
(104 on macOS). Exceeding it fails `bind(2)` with `EINVAL`, which Go reports
as `bind: invalid argument` — a message that says nothing about length.

This bites tests rather than users: `t.TempDir()` builds its path from
`TMPDIR`, the full test name and a counter, so a long `TMPDIR` (`nix
develop` sets one) plus a long test name can breach the limit. The symptom
is that a few tests fail on one machine and not another, with an error that
looks like a permissions problem. `internal/testsock.Path` exists for this:
use it instead of `t.TempDir()` for anything that will be bound.

### `logs` cannot read a rootful runtime's log files

nix-compose reads container logs straight off disk, from
`/tmp/nix-compose-logs/<project>/<service>/0.log`. A rootful containerd
creates those files `0640 root:root` and never chowns them, so an ordinary
user cannot read what the runtime wrote. The container itself is
unaffected — only `nix-compose logs` is.

Both `logs` and `doctor` now say so explicitly rather than printing
nothing, but the underlying restriction stands.

**Workaround:** `nix-compose ps -a` shows each container's state and exit
code without reading any log, which is usually enough to tell what
happened. To read the log text itself you need root — note that
`crictl logs` is *not* a way around this: it opens the same file as the
same user and fails identically.

For a service you control, having it tee its own output to a bind-mounted
file sidesteps the problem entirely and is worth doing in test fixtures.

## Kubernetes manifest emission

### `resources` are emitted but not enforced locally

`x-nix-compose.resources` (Kubernetes-style requests and limits) is
validated — a request larger than its limit is reported — and rendered into
the `Deployment` that `render --target k8s` emits. It is **not** applied to
the container nix-compose starts: nothing populates CRI's
`LinuxContainerResources`.

So a service can be over its declared limit locally and still look fine.
The declaration is a statement about the deployment target, not about the
local run.

**Workaround:** none currently. Treat the values as manifest input.

### No Ingress generation

`render --target k8s` does not generate `Ingress` or `IngressRoute`
manifests. Port mappings produce `Service` objects only.

**Workaround:** Add Ingress manifests manually or via a Kustomize overlay.

### No RBAC or ServiceAccount generation

Service accounts, roles, and role bindings are not generated.

**Workaround:** Define these in a Kustomize overlay alongside the
generated base manifests.

### `useHostStore` ignored in K8s target

The `/nix/store` bind-mount is a local development convenience and is
not emitted in Kubernetes manifests. Containers must use proper images
for K8s deployment.

### Readiness probes not surfaced in Compose

Compose has no readiness probe concept. Only liveness probes are mapped
to `healthcheck`. Readiness probes are emitted only in the K8s target.

## General

### Linux only

nix-compose only supports Linux. The goreleaser configuration produces
`linux/amd64` binaries only. Running on macOS requires a Linux VM or
remote builder.

### No interactive commands

Commands that require interactive input (e.g. `docker compose run -it`)
are not supported through nix-compose. The `exec` command connects
stdin/stdout but does not allocate a PTY.

**Workaround:** Use `{runtime} compose -f .nix-compose/compose.yaml exec -it <service> <cmd>`
directly for interactive sessions.

### No Helm chart generation

The K8s target produces raw manifests and Kustomize bases but not Helm
charts. This is a deliberate scope limitation.

**Workaround:** Use Kustomize overlays for parameterization.
