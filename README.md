# nix-compose

Evaluate Nix service definitions and orchestrate containers.

Write your services in a `nix-compose.yaml` or in Nix, and manage the full
lifecycle with familiar commands.

> **Status: public beta.** It works, and it is used daily against real
> projects — but the `nix-compose.yaml` schema and the CLI surface may still
> change before 1.0, and releases are tagged as pre-releases until they
> stop. Read [docs/limitations.md](docs/limitations.md) before adopting it
> for anything load-bearing; it is a deliberately complete account of what
> does not work, not a marketing document.
>
> **Linux only, and a container runtime is required** — containerd or
> CRI-O. Run
> `nix-compose doctor` first; it checks every prerequisite and each finding
> carries its fix. If you are trying this on a CI runner, read
> [docs/running-in-ci.md](docs/running-in-ci.md) first — most hardened
> runners cannot run containers at all, and that document says what to do
> about it.

## Features

- **Talks CRI directly.** Every command drives containerd or CRI-O over gRPC
  — no daemon, no compose file, no compose CLI, nothing shelling out to
  another tool
- **Registry-free images.** A service can name a *package* instead of a tag
  (`package: hello`, or `services.web.package = pkgs.hello;` in Nix). The
  image is built from that package's Nix closure by
  [nix-oci](https://github.com/systemstart/nix-oci) and imported straight
  into the runtime: no Dockerfile, no `docker build`, nothing pushed or
  pulled
- **YAML or Nix.** Start with a `nix-compose.yaml` that looks like a compose
  file and needs no Nix knowledge, or write a flake when you need real
  computation. Both produce the same composition, so moving between them
  costs nothing. A YAML project naming no `package:` never invokes Nix at all
- **Familiar commands.** `up`, `down`, `ps`, `logs`, `exec`, `start`, `stop`,
  `restart`, `create`, `kill`, `rm`, `pull`, `top`, `images` behave the way
  you expect, including volume mounts (named, bind, tmpfs, Nix store),
  `down -v`, profiles, and `depends_on` with health conditions
- **Real dependency ordering.** Services start in topological order and each
  level waits on the health checks of the one below, rather than
  `depends_on` being a start-order hint
- **Restart supervision.** `always`, `on-failure` and `unless-stopped` are
  enforced by a built-in supervisor with exponential backoff while
  nix-compose runs in the foreground
- **Watch mode** (`up --watch`) — re-evaluates on file changes and restarts
  only the services actually affected
- **Declarative plan/apply.** Config is translated into typed manifests with
  a dependency DAG and diffed against recorded state: `plan` previews,
  `state` inspects rollouts, `drift` compares expected against actual,
  `rollback` reverts to a previous snapshot, `graph` answers what depends on
  what before anything starts or stops
- **Kubernetes manifest emission** (`render --target k8s`) — an escape
  hatch, not a deployment story: emits `Deployment`, `Service`, `Secret`,
  `PVC` and `kustomization.yaml` as a starting point you then own. If you
  already deploy with Kustomize, Helm or Argo, keep doing that
- **Migration path in** — `nix-compose import` converts an existing
  `docker-compose.yaml` (lossily, and it tells you what it dropped), and
  `nix-compose suggest` maps registry images to nixpkgs attributes
- **GC root management** — Nix store paths stay alive while services run
- **Remote and microVM modes** *(experimental)* — `--remote-socket`
  delegates container operations to a `nix-compose serve` instance while
  evaluating locally; `up --microvm` boots a NixOS microVM via
  cloud-hypervisor and orchestrates inside it over vsock

See [CHANGELOG.md](CHANGELOG.md) for the full release history, and
[docs/limitations.md](docs/limitations.md) for what does not work.

## Requirements

Run `nix-compose doctor` to check all of the below against the machine you are
on, and to catch the two problems that fail with messages naming the symptom
rather than the cause — an `iptables` missing from the *runtime's* PATH, and
container log files a non-root user cannot read:

```
$ nix-compose doctor
✓ CRI socket      /run/containerd/containerd.sock (containerd v2.3.3)
✓ cgroup driver   systemd — pods are placed under nix-compose.slice
✓ nix             nix (Nix) 2.34.8, flakes enabled
! CNI plugins     missing: bridge, host-local, portmap, firewall, dnsname
                  → containers fall back to host networking, so `ports:` is not
                    mapped and services reach each other only via localhost.
                    Install cni-plugins and dnsname-cni, or set CNI_PATH
```

It exits non-zero only when something would actually stop a composition
running, so it can gate a setup script.

On a CI runner, run `doctor` **inside the job**, not on the runner host — a
Kubernetes-hosted runner typically has a container runtime on the node and
none reachable from the job. See
[docs/running-in-ci.md](docs/running-in-ci.md) for what a runner has to
provide, and what to do when it provides none of it.

You need:

- [Nix](https://nixos.org/) with flakes enabled
- A CRI runtime — [containerd](https://containerd.io/) or
  [CRI-O](https://cri-o.io/). Every command talks to it over gRPC
- **CNI plugins (optional, recommended)** — `bridge`, `host-local`,
  `portmap`, `firewall`, and `dnsname` for per-project networking and
  DNS; searched in `/opt/cni/bin`, `/usr/lib/cni`, `/usr/libexec/cni`,
  and any directories in `CNI_PATH`. Without them, CRI containers use
  host networking. On NixOS / in `nix develop`, the plugins are provided
  by `cni-plugins` and `dnsname-cni` — the dev shell exports `CNI_PATH`
  automatically

## Install

**Binary download** — grab the latest archive from
[GitHub releases](https://github.com/systemstart/nix-compose/releases).

**Nix flake:**

```sh
nix run git+https://github.com/systemstart/nix-compose.git -- up -d
# or: nix build git+https://github.com/systemstart/nix-compose.git
```

**go install:**

```sh
go install github.com/systemstart/nix-compose/cmd/nix-compose@latest
```

**Build from source:**

```sh
nix develop  # enter dev shell
make build   # produces ./nix-compose
```

See [docs/install.md](docs/install.md) for full details on all methods.

## Quick start

### 1. Create a `nix-compose.yaml`

The shortest way in. No flake, no Nix knowledge, and — for a project that only
names registry images — no Nix evaluation at all:

```yaml
# nix-compose.yaml
services:
  web:
    image: nginx:latest
    ports: ["8080:80"]
    environment:
      NGINX_HOST: localhost
```

`package:` is what makes this more than another compose runner. Name a package
instead of an image and it is built from its Nix closure and imported straight
into the runtime — no registry, no Dockerfile, nothing pushed or pulled:

```yaml
# nix-compose.yaml
services:
  web:
    image: nginx:latest          # a registry image, as usual
  greeter:
    package: hello               # built from its closure
  ticker:
    package: coreutils
    entrypoint: ["sleep", "3600"] # bare names resolve against the package
```

The two kinds of service mix freely, so an existing project can move one
service at a time. Packages come from the nixpkgs nix-compose was built
against — package versions travel with the nix-compose version, the way a
distro release works. Override it per project with a top-level
`nixpkgs: github:NixOS/nixpkgs/nixos-unstable`.

Unknown keys are rejected rather than ignored, so a misspelled `enviroment:`
is an error and not a silent no-op.

A project is named after its directory unless the document says otherwise.
Set a top-level `name:` when the directory name is not distinctive — two
projects in like-named directories (`*/test/integration/`, say) otherwise
share a name, and `ps` in one lists the other's containers:

```yaml
name: paperless-itest
services:
  ...
```

`--project-name` overrides both.

See [examples/yaml](examples/yaml/) for a complete one. For anything that needs
real computation — overlays, custom packages, shared configuration — use a
flake instead; the YAML is a front-end onto the same evaluation, so nothing is
lost by starting here.

### 1a. …or create a flake

```nix
# flake.nix
{
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

  outputs = { self, nixpkgs }:
    let
      pkgs = import nixpkgs { system = "x86_64-linux"; };
    in {
      composition = {
        config.out.dockerComposeYamlAttrs = {
          services.web = {
            image = "nginx:latest";
            ports = [ "8080:80" ];
            environment.NGINX_HOST = "localhost";
            x-nix-compose.serviceInfo.defaultExec = [ "bash" ];
          };
        };
      };
    };
}
```

### 1b. …or skip the registry entirely

Instead of naming an image someone else pushed, name a package. nix-compose
builds the image from its Nix closure and imports it straight into containerd:

```nix
# flake.nix
{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    nix-compose = {
      url = "github:systemstart/nix-compose";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs = { self, nixpkgs, nix-compose }:
    let
      system = "x86_64-linux";
      pkgs = nixpkgs.legacyPackages.${system};
      nc = nix-compose.legacyPackages.${system};
    in {
      composition = nc.mkComposition {
        services.hello.package = pkgs.hello;
      };
    };
}
```

No Dockerfile, no `docker build`, no registry. `mkComposition` resolves
`package` into the image's store path; `up` builds it and imports it over
containerd's API. For control over the OCI config, build the image yourself and
pass it as `ociImage`:

```nix
services.web = {
  ociImage = nc.buildOCIImage {
    name = "caddy-static";
    contents = [ pkgs.caddy ];
    entrypoint = [ "${pkgs.caddy}/bin/caddy" "file-server" "--root" "/srv" ];
    exposedPorts = [ "8080/tcp" ];
  };
  ports = [ "8080:8080" ];
};
```

Both need a CRI runtime — this path has no compose-CLI equivalent. See
[ADR-006](docs/adrs/006-no-image-builds.md) and
[examples/nix-built-image](examples/nix-built-image).

### 2. Start services

```sh
nix-compose up -d
```

This evaluates the project, creates a GC root so the Nix store paths stay
alive, and starts each service in dependency order, waiting on health checks
between levels. Containers are created over CRI gRPC directly — there is no
compose file and nothing shelling out to another tool.

### 3. Interact

```sh
nix-compose ps            # list running services
nix-compose logs -f web   # follow logs
nix-compose exec web      # shell into container (uses defaultExec)
nix-compose exec web ls   # run a specific command
```

### 4. Stop

```sh
nix-compose down
```

Stops containers and removes the GC root.

## Container runtime backend

nix-compose supports two backend paths:

### CRI

Every command talks CRI gRPC directly to containerd or CRI-O.
nix-compose creates a pod sandbox per service and manages the full
pull → create → start lifecycle. Named volumes, bind mounts, tmpfs and
Nix store paths are translated to CRI mount specs; `down -v` removes
named volume directories.

If no CRI socket is found the command errors — there is no second
backend to fall back to. `nix-compose doctor` diagnoses why.

Sockets are probed in order:

```
/run/containerd/containerd.sock  →  /run/crio/crio.sock
```

To use a specific socket, pass `--cri-socket`:

```sh
nix-compose --cri-socket /run/containerd/containerd.sock up -d
```

### Remote mode

When `--remote-socket` is set, commands delegate container operations
to a remote `nix-compose serve` instance. Nix evaluation still runs
locally; only the orchestration pipeline (plan, apply, teardown, exec,
logs, state) is forwarded over the gRPC connection.

```sh
# Terminal 1: start the orchestrate server
nix-compose serve --socket /tmp/orch.sock

# Terminal 2: use the remote server
nix-compose --remote-socket /tmp/orch.sock plan
nix-compose --remote-socket /tmp/orch.sock up
nix-compose --remote-socket /tmp/orch.sock logs -f web
nix-compose --remote-socket /tmp/orch.sock exec web echo hello
nix-compose --remote-socket /tmp/orch.sock state list
nix-compose --remote-socket /tmp/orch.sock down
```

Supported commands: `up`, `down`, `exec` (non-interactive only),
`logs`, `plan`, `state`. Interactive exec is not supported via the
remote path (requires SPDY streaming). If the remote socket is
unreachable, a warning is printed and the command falls back to the
local CRI or compose path.

### MicroVM mode

When `--microvm` is passed to `up`, nix-compose boots a lightweight
NixOS VM via cloud-hypervisor and delegates all container operations
to the in-VM orchestrate engine over vsock. The host shares
`/nix/store` (read-only) and the project directory (read-write) via
virtiofs.

```sh
# Auto-build VM image and boot (first run builds via nix, cached afterwards)
nix-compose up --microvm

# Provide explicit kernel/rootfs
nix-compose up --microvm --vm-kernel ./result/kernel --vm-rootfs ./result/rootfs

# Tune VM resources
nix-compose up --microvm --vm-vcpus 4 --vm-memory 2048
```

Published TCP ports are automatically forwarded from the host to the
VM via userspace vsock proxying. For example, if a service declares
`ports: ["8080:80"]`, the host listens on `0.0.0.0:8080` and proxies
connections through vsock to port 80 inside the VM.

Foreground mode blocks on SIGINT/SIGTERM and shuts down the VM
gracefully. Detach mode (`-d`) prints the vsock CID for subsequent
management via `--remote-vsock-cid`.

## Commands

| Command | Description |
|---------|-------------|
| `up`    | Evaluate Nix config, create GC root, start services |
| `down`  | Stop services, remove GC root |
| `ps`    | List running services |
| `logs`  | View service logs |
| `exec`  | Run a command in a service container |
| `build` | Build service images |
| `start` | Start existing containers |
| `stop`  | Stop running containers |
| `restart` | Restart containers |
| `create` | Create containers without starting |
| `kill`  | Force stop services |
| `rm`    | Remove stopped containers |
| `pull`  | Pull service images |
| `top`   | Display running processes |
| `images` | List images used by services |
| `config` | Validate and view the generated compose config |
| `render` | Render K8s manifests from Nix config |
| `plan`  | Preview orchestration changes without applying |
| `state` | Inspect orchestration rollouts (`state list`, `state show <id>`) |
| `drift` | Compare expected orchestration state against actual CRI state |
| `rollback` | Revert to a previous deployment (`rollback list`, `rollback apply <id>`) |
| `graph` | Inspect the dependency graph (`graph show`, `graph deps <id>`, `graph impact <id>`) |
| `serve` | Start orchestrate gRPC server on a unix socket |
| `import` | Convert a docker-compose file into a `nix-compose.yaml` |
| `suggest` | Show which registry images could be built from nixpkgs instead |
| `doctor` | Check whether this machine can run a composition |
| `version` | Print version |

### Global flags

```
--file string             path to compose.nix or flake directory
--flake-attr string       flake attribute to evaluate (default: composition)
--impure                  allow impure nix evaluation (default true)
--project-dir string      project directory (default: current directory)
--project-name string     project name for compose
--cri-socket string       CRI runtime socket path; auto-detected if omitted
--remote-socket string    orchestrate gRPC server unix socket (delegates commands to remote engine)
--remote-vsock-cid uint32 vsock CID for remote orchestrate server (used with microVM mode)
--remote-vsock-port uint32 vsock port for remote orchestrate server (default 1024)
```

### `up` flags

```
-d, --detach                run containers in background
    --build                 build images before starting
    --watch                 watch for Nix file changes and restart affected services
    --wait                  wait for services to be running/healthy
    --microvm               boot a microVM and delegate orchestration
    --vm-kernel string      path to vmlinux kernel image (auto-built if omitted)
    --vm-rootfs string      path to rootfs image (auto-built if omitted)
    --vm-vcpus int          number of vCPUs for the microVM (default 1)
    --vm-memory int         memory in MB for the microVM (default 512)
    --vm-cid uint32         vsock CID for the microVM (default 3)
    --vm-image-flake string flake reference override for the VM image build
    --vm-portfwd-port uint32 vsock port for VM port forwarding (default 1025)
```

### `down` flags

```
-v, --volumes          remove named volumes
    --timeout int      shutdown timeout in seconds
    --remove-orphans   remove containers for services not defined in the config
    --rmi string       remove images (all or local)
```

### Profiles

Services can declare a `profiles` list. Only services matching an active
profile (or services with no profiles) are started:

```sh
nix-compose up --profile observability -d
# or: COMPOSE_PROFILES=observability nix-compose up -d
```

Multiple `--profile` flags can be combined. The generated YAML includes
`profiles:` so `docker compose --profile ...` also works on the generated file.

### `render` — Kubernetes manifests

Generate K8s manifests from the same Nix config used for local dev:

```sh
nix-compose render --target k8s --output ./k8s/
```

Emits `Deployment`, `Service`, `Secret`, `PersistentVolumeClaim`, and a base
`kustomization.yaml`. Optionally validates with
`kubectl apply --dry-run=client`.

### `exec` behaviour

When no command is given, `exec` uses the `defaultExec` from the service's
`x-nix-compose.serviceInfo` extension. This lets you configure a per-service
default shell:

```nix
services.db = {
  image = "postgres:16";
  x-nix-compose.serviceInfo.defaultExec = [ "psql" "-U" "app" ];
};
```

```sh
nix-compose exec db   # drops into psql
```

## Migrating from Docker Compose

nix-compose is a drop-in workflow replacement. Your existing
`docker-compose.yaml` keeps working with your runtime — nix-compose generates
its own YAML from your config in `.nix-compose/compose.yaml`.

### Step by step

```sh
nix-compose import          # docker-compose.yml → nix-compose.yaml
nix-compose suggest         # which images have a nixpkgs equivalent
nix-compose up -d           # instead of docker compose up -d
```

1. **`nix-compose import`** converts your compose file. The conversion is
   one-way and lossy, and says so: anything without an equivalent is reported
   and written into the generated file as a comment. A service that was built
   from a Dockerfile comes out marked `FIXME`, because it has nothing to run
   until you give it an `image:` or a `package:`.
2. **`nix-compose suggest`** looks up each registry image in nixpkgs:

   ```
   cache  redis:7-alpine  → package: redis       (nixpkgs 8.8.1)  ! major version 7 → 8
   db     postgres:16     → package: postgresql  (nixpkgs 18.6)   ! major version 16 → 18;
                                                                    needs an `entrypoint:`
   web    myapp:2.1       → no nixpkgs match, keeping registry image
   ```

   It changes nothing — the swap is yours to make. Note the warnings: nixpkgs
   tracks its own versions, so taking a suggestion unread can be a major
   upgrade nobody asked for.
3. **Swap `image:` for `package:` one service at a time.** Both kinds coexist
   in one file, so this never has to be a big-bang migration. Each service you
   convert stops touching a registry.
4. Run `nix-compose up` instead of `docker compose up`.
5. Once everything works, the original `docker-compose.yaml` can be removed.

### Examples

The [`examples/`](examples/) directory:

- **[yaml](examples/yaml/)** — the shortest way in: a `nix-compose.yaml`
  mixing registry images and `package:` builds, no Nix file anywhere
- **[basic-web](examples/basic-web/)** — single nginx service, as a flake
- **[web-with-api](examples/web-with-api/)** — web + API + postgres with
  healthchecks, `depends_on` conditions and named volumes
- **[nix-built-image](examples/nix-built-image/)** — a service whose image
  is built from a Nix closure and imported directly, with no registry
  involved

The first three flake examples also carry the `docker-compose.yaml` they
were converted from, so you can compare the two side by side and try
`nix-compose import` on them.

## Project structure

nix-compose evaluates your project, parses the result into typed Go
structs, and drives the container runtime over CRI gRPC.

```
nix-compose.yaml / flake.nix / compose.nix   <- you write this
    |
    v
nix eval --json                              <- skipped entirely for a YAML
    |                                           project naming no package:
    v
Composition (typed Go structs)
    |
    +--> .nix-compose/gc-root   (keeps referenced store paths alive)
    |
    v
CRI gRPC  ->  RunPodSandbox / CreateContainer / StartContainer
              (containerd or CRI-O; errors if no socket is found)
```

### Flake attribute

nix-compose looks for the `composition` flake output by default. Override with
`--flake-attr`:

```sh
nix-compose up --flake-attr myApp
```

### Legacy mode

If no `flake.nix` is found, nix-compose falls back to evaluating `compose.nix`
directly with `nix eval --expr`. A `pkgs.nix` file is used for the package set
if present.

### Extension fields

The `x-nix-compose` key in service definitions carries nix-compose-specific
metadata through the YAML round-trip:

```yaml
x-nix-compose:
  serviceInfo:
    defaultExec:
      - bash
```

## Agent guide

The full configuration reference and development guide for AI coding agents
is embedded in the binary and can also be read at
[`cmd/nix-compose/SKILL.md`](cmd/nix-compose/SKILL.md).

```sh
nix-compose docs   # print the embedded guide
```

## Development

```sh
nix develop      # enter dev shell with Go, golangci-lint, etc.
make test        # run tests (requires >= 80% coverage)
make lint        # run linters
make build       # build binary
make fmt         # auto-format
```

## License

[GPL-3.0](./LICENSE), matching [nix-oci](https://github.com/systemstart/nix-oci).

Dependencies are Apache-2.0, BSD and MIT, all of which combine into a GPLv3
work.
