# nix-compose Agent Guide

This file tells AI coding agents how to use and develop nix-compose.

## What is nix-compose?

nix-compose evaluates Nix or YAML service definitions and orchestrates
containers over CRI (containerd or CRI-O), speaking gRPC directly rather
than shelling out to another tool. It bridges Nix's reproducible
configuration with container orchestration, using familiar compose-style
commands.

Use nix-compose when a project has a `flake.nix` that outputs a
`composition.config.out.dockerComposeYamlAttrs` attribute.

## Common Commands

```bash
# Start services (detached)
nix-compose up -d

# Start with file watching for live reload
nix-compose up -d --watch

# Start only services matching a profile
nix-compose up -d --profile backend

# Stop services and remove GC root
nix-compose down

# View running services
nix-compose ps

# Follow logs
nix-compose logs -f

# Execute a command in a running service
nix-compose exec web bash

# Build images
nix-compose build

# Render K8s manifests to stdout
nix-compose render --target k8s

# Render K8s manifests to a directory with kustomization.yaml
nix-compose render --target k8s --output ./k8s/

# Render with a custom namespace
nix-compose render --target k8s --namespace production

# Render and validate with kubectl
nix-compose render --target k8s --dry-run
```

## Configuration Structure

Services are defined in `flake.nix`. The Nix evaluation produces a JSON
structure that maps directly to Compose YAML:

```nix
{
  # flake.nix
  outputs = { self, nixpkgs, ... }: {
    composition.config.out.dockerComposeYamlAttrs = {
      services = {
        web = {
          image = "nginx:latest";
          ports = [ "8080:80" ];
          x-nix-compose = {
            serviceInfo.defaultExec = [ "bash" ];
            profiles = [ "frontend" ];
          };
        };
      };
    };
  };
}
```

## Key Concepts

### x-nix-compose Extensions

The `x-nix-compose` field on each service carries nix-compose metadata:

- **serviceInfo.defaultExec**: Default command for `nix-compose exec`
- **useHostStore**: When `true`, bind-mounts Nix store paths into the container
  (skips image builds entirely)
- **nixStorePaths**: List of `/nix/store/...` paths to mount when useHostStore
  is enabled
- **profiles**: List of profile tags for `--profile` filtering

### useHostStore

When a service sets `useHostStore: true`, nix-compose generates read-only
bind-mounts for each path in `nixStorePaths`. This lets you run Nix-built
binaries directly from the host store without building a container image.

### Profiles

Services can be tagged with profiles using the standard Compose `profiles`
field. The `--profile` flag is a global persistent flag available on all
subcommands. Use `--profile backend` to start only services matching that
profile. Services with no profiles are always included.

When a profiled service is activated, its `depends_on` targets are
transitively included even if they belong to a different profile.

The `COMPOSE_PROFILES` environment variable (comma-separated) is also
supported and merged with `--profile` flags.

The legacy `x-nix-compose.profiles` path is deprecated; use top-level
`profiles` instead.

```nix
services.loki = {
  image = "grafana/loki:latest";
  profiles = [ "observability" ];
};
```

### Resource Limits

K8s-style resource requests and limits map to Compose
`deploy.resources`. Requests become `reservations`.  nix-compose warns
when requests exceed limits.

CPU values accept plain numbers (`"1.0"`, `"0.25"`) and millicores
(`"100m"`, `"500m"`).  Memory values accept plain bytes and suffixed
strings (`K`, `M`, `G`, `T` for powers of 1000; `Ki`, `Mi`, `Gi`, `Ti`
for powers of 1024).  Comparisons are always numeric, so `"25m"` is
correctly recognised as less than `"100m"`.

```nix
x-nix-compose.resources = {
  limits = { cpu = "1.0"; memory = "512M"; };
  requests = { cpu = "250m"; memory = "128Mi"; };
};
```

### Probes

Liveness and readiness probes map to Compose `healthcheck`. Liveness takes
priority; readiness is used as fallback. An explicit `healthcheck` on the
service always overrides probes.

- HTTP GET probes generate `wget -qO- <url>` commands
- Exec probes use `CMD` test format
- `initialDelaySeconds` → `start_period`, `periodSeconds` → `interval`,
  `timeoutSeconds` → `timeout`, `failureThreshold` → `retries`

### EnvFrom Secrets

`envFrom` resolves environment variables from secret files at compose
generation time. Supports plaintext `.env` files and sops-encrypted files.
Explicit environment variables on the service take precedence.

```nix
x-nix-compose.envFrom = [
  { secretFile = "secrets/api.env"; }
  { sopsFile = "secrets/encrypted.env"; prefix = "APP_"; }
];
```

### Init Containers

Init containers are synthesized into short-lived Compose services with
`restart: "no"`. Multiple init containers are chained sequentially via
`depends_on` with `service_completed_successfully`. The main service depends
on the last init container.

```nix
x-nix-compose.initContainers = [
  {
    name = "migrate";
    image = "flyway:latest";
    command = ["flyway" "migrate"];
    environment = { DB_URL = "postgres://db:5432/app"; };
    volumes = [ "data:/mnt/data" ];
  }
];
```

### Named Ports

Named ports emit standard Compose port strings and preserve names in
`x-nix-compose.namedPorts` metadata. When rendering K8s manifests, named ports
become proper `containerPort` entries with names and protocols. Supports TCP
(default) and UDP protocols.

```nix
x-nix-compose.namedPorts = [
  { name = "http"; containerPort = 3000; hostPort = 8080; }
  { name = "dns"; containerPort = 53; hostPort = 5353; protocol = "udp"; }
];
```

### Watch Mode

`nix-compose up --watch` polls for `.nix` file changes and selectively
restarts only the services whose configuration changed. Target: sub-5-second
restarts.

### Container Runtime Detection

nix-compose probes CRI sockets in order: `/run/containerd/containerd.sock`,
then `/run/crio/crio.sock`. `--cri-socket` overrides. If none respond the
command errors — there is no fallback backend. `nix-compose doctor`
diagnoses why.

## Development Workflow

```bash
# Lint (golangci-lint with cyclop, wrapcheck, gosec, etc.)
make lint

# Run tests (requires >= 80% coverage)
make test

# Build binary
make build

# Format code
make fmt
```

## Project Structure

```
cmd/nix-compose/         # CLI entry point
pkg/
  cli/                   # Cobra commands, flags, CLI wiring
  eval/                  # Nix evaluation bridge, composition parsing
  compose/               # Compose YAML generation, profile filtering,
                         # init container synthesis, resource validation
  depgraph/              # Dependency graph validation (cycles, missing refs)
  envfrom/               # EnvFrom secret resolution (dotenv, sops)
  gcroot/                # Nix GC root management
  nixerror/              # Structured Nix error parsing
  proc/                  # Signal-forwarding subprocess wrapper
  k8s/                   # Kubernetes manifest generation (Deployment, Service,
                         # Secret, PVC, kustomization.yaml)
  watch/                 # File-change detection and selective restart
testdata/                # JSON/YAML fixtures for tests
examples/                # Example projects (yaml, basic-web, web-with-api, nix-built-image)
```

### Package Responsibilities

| Package | Purpose |
|---------|---------|
| `cli` | Cobra command tree, flag handling, dependency injection |
| `eval` | Calls `nix eval --json`, parses output into Go structs |
| `composition` | Profile filtering, init container synthesis, resource validation, defaultExec lookup |
| `envfrom` | Resolves envFrom secrets (dotenv files, sops-encrypted files) into environment variables |
| `depgraph` | Validates service dependency graph before starting |
| `gcroot` | Creates/removes Nix GC roots to prevent store garbage collection |
| `nixerror` | Extracts file locations and messages from Nix eval errors |
| `proc` | Wraps subprocesses with SIGINT/SIGTERM forwarding and graceful shutdown |
| `k8s` | Converts eval structs to K8s manifests (Deployment, Service, Secret, PVC), multi-doc YAML or directory output with kustomization.yaml |
| `watch` | Polls for .nix file changes, diffs compositions, restarts changed services |

## Troubleshooting

### No CRI runtime found

```
no CRI runtime found (checked /run/containerd/containerd.sock, /run/crio/crio.sock)
```

Run `nix-compose doctor`. It checks the socket, the cgroup driver, Nix,
the CNI plugins and log readability, and every finding carries its fix.

### Nix evaluation errors

nix-compose parses Nix stderr and reports file locations. Check that your
`flake.nix` evaluates correctly with `nix eval --json .#composition...`.

### Dependency cycle errors

```
dependency cycle detected involving "a" -> "b"
```

Fix the `depends_on` graph in your Nix configuration to remove circular
dependencies.
