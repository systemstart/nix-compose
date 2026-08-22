# Configuration reference

nix-compose evaluates a Nix expression that produces a composition — a set
of services, networks, and volumes following the Docker Compose specification
with nix-compose extensions.

## Top-level structure

The Nix expression must produce an attribute set under
`composition.config.out.dockerComposeYamlAttrs`:

```nix
composition = {
  config.out.dockerComposeYamlAttrs = {
    services = { ... };
    networks = { ... };  # optional
    volumes  = { ... };  # optional
  };
};
```

The flake attribute defaults to `composition`. Override with `--flake-attr`.

### YAML project keys

A `nix-compose.yaml` document accepts `services`, `networks`, `volumes` and
`x-nix-compose-microvm` as above, plus two of nix-compose's own and one
compose carries over:

| Key | Type | Description |
|-----|------|-------------|
| `name` | string | Project name. Defaults to the directory basename; `--project-name` overrides it. Must match `[a-z0-9][a-z0-9_-]*` — it becomes part of pod and container names |
| `nixpkgs` | string | Flake reference pinning the nixpkgs that `package:` resolves against |
| `version` | string | Accepted and ignored, as compose does |

`name` is addressing metadata rather than part of the composition: it selects
which containers a command acts on, and every command resolves it the same
way, so `up` and `down` cannot disagree about which project they mean.

---

## Services

Each key in `services` is a service name. Supported fields:

### Core

| Field | Type | Description |
|-------|------|-------------|
| `image` | string | Container image (e.g. `"nginx:latest"`) |
| `build` | object | Build configuration (see below) |
| `command` | string or list | Override the default command |
| `entrypoint` | string or list | Override the default entrypoint |
| `ports` | list of strings | Port mappings (`"host:container"` or `"host:container/proto"`) |
| `environment` | attrset | Environment variables (`{ KEY = "value"; }`) |
| `volumes` | list of strings | Volume mounts (`"name:/path"` or `"./host:/path:ro"`) |
| `tmpfs` | list of strings | tmpfs mounts |
| `working_dir` | string | Working directory inside the container |
| `user` | string | User to run as (`"uid:gid"`) |
| `hostname` | string | Container hostname |
| `privileged` | bool | Run in privileged mode |
| `restart` | string | Restart policy (`"no"`, `"always"`, `"on-failure"`, `"unless-stopped"`) |
| `stop_signal` | string | Signal to send on stop (default: `SIGTERM`) |

### Networking

| Field | Type | Description |
|-------|------|-------------|
| `network_mode` | string | Network mode (`"host"`, `"bridge"`, `"none"`, `"service:name"`) |
| `networks` | list or map | Networks to attach (see Networks below) |
| `extra_hosts` | list of strings | Extra `/etc/hosts` entries (`"hostname:ip"`) |

### Dependencies

| Field | Type | Description |
|-------|------|-------------|
| `depends_on` | list or map | Service dependencies |
| `healthcheck` | object | Health check configuration |
| `profiles` | list of strings | Profiles this service belongs to |
| `labels` | attrset | Container labels |

#### `depends_on` (map form)

```nix
depends_on = {
  db = { condition = "service_healthy"; };
  cache = { condition = "service_started"; };
};
```

#### `depends_on` (list form)

```nix
depends_on = [ "db" "cache" ];  # implies condition = "service_started"
```

#### `healthcheck`

```nix
healthcheck = {
  test = [ "CMD" "curl" "-f" "http://localhost:8080/health" ];
  interval = "10s";
  timeout = "5s";
  retries = 3;
  start_period = "30s";
};
```

### Build configuration

```nix
build = {
  context = ".";
  dockerfile = "Dockerfile";
  args = { BUILD_ENV = "production"; };
  target = "runtime";
};
```

---

## `x-nix-compose` extensions

The `x-nix-compose` key holds nix-compose-specific fields that are carried
through the YAML round-trip.

### `serviceInfo`

```nix
x-nix-compose.serviceInfo.defaultExec = [ "bash" ];
```

Specifies the default command for `nix-compose exec <service>` when no
command is given.

### `useHostStore`

```nix
x-nix-compose.useHostStore = true;
```

Bind-mounts the host `/nix/store` into the container. Enables running
Nix-built binaries inside containers without baking them into images.

### `nixStorePaths`

```nix
x-nix-compose.nixStorePaths = [
  "/nix/store/abc...-my-app"
];
```

Explicit list of Nix store paths to bind-mount (subset of full store).

### `resources`

Kubernetes-style resource requests and limits. Validated, and emitted as
`resources` by `render --target k8s`. Note they are **not** applied to the
container nix-compose starts — see [limitations.md](limitations.md).

```nix
x-nix-compose.resources = {
  limits = {
    cpu = "500m";
    memory = "256Mi";
  };
  requests = {
    cpu = "100m";
    memory = "128Mi";
  };
};
```

### `probes`

Kubernetes-style liveness and readiness probes. Drive host-side health
gating during `up` (liveness takes precedence), and are emitted as probes
by `render --target k8s`.

```nix
x-nix-compose.probes = {
  liveness = {
    httpGet = { path = "/healthz"; port = 8080; };
    initialDelaySeconds = 10;
    periodSeconds = 15;
    timeoutSeconds = 5;
    failureThreshold = 3;
  };
  readiness = {
    exec = { command = [ "cat" "/tmp/ready" ]; };
    periodSeconds = 5;
  };
};
```

#### HTTP probe

| Field | Type | Description |
|-------|------|-------------|
| `httpGet.path` | string | HTTP path to probe |
| `httpGet.port` | int | Port to connect to |
| `httpGet.scheme` | string | `"HTTP"` (default) or `"HTTPS"` |

#### Exec probe

| Field | Type | Description |
|-------|------|-------------|
| `exec.command` | list of strings | Command to run inside the container |

#### Timing fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `initialDelaySeconds` | int | 0 | Delay before first probe |
| `periodSeconds` | int | 10 | Probe interval |
| `timeoutSeconds` | int | 1 | Probe timeout |
| `failureThreshold` | int | 3 | Failures before unhealthy |

### `envFrom`

Load environment variables from files (plain text or sops-encrypted).

```nix
x-nix-compose.envFrom = [
  { secretFile = "./secrets/api.env"; }
  { sopsFile = "./secrets/encrypted.env"; prefix = "SECURE_"; }
];
```

| Field | Type | Description |
|-------|------|-------------|
| `secretFile` | string | Path to a plain `KEY=VALUE` file |
| `sopsFile` | string | Path to a sops-encrypted env file |
| `prefix` | string | Prefix to prepend to all variable names |

### `initContainers`

Containers that run to completion before the main service starts.
Synthesized as short-lived services that the main one `depends_on`.

```nix
x-nix-compose.initContainers = [
  {
    name = "migrate";
    image = "my-app:latest";
    command = [ "node" "migrate.js" ];
    environment = { DATABASE_URL = "postgres://..."; };
    volumes = [ "data:/data" ];
  }
];
```

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Init container name (must be unique) |
| `image` | string | Container image |
| `command` | string or list | Command to execute |
| `environment` | attrset | Environment variables |
| `volumes` | list of strings | Volume mounts |

### `namedPorts`

Named port mappings preserved for K8s manifest generation.

```nix
x-nix-compose.namedPorts = [
  { name = "http"; containerPort = 8080; hostPort = 8080; protocol = "TCP"; }
  { name = "metrics"; containerPort = 9090; }
  { name = "admin"; containerPort = 8081; hostPort = 8081; hostIP = "127.0.0.1"; }
];
```

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Port name (used in K8s `Service`) |
| `containerPort` | int | Port inside the container |
| `hostPort` | int | Port on the host (optional, for local dev) |
| `hostIP` | string | Host IP to bind to (optional, e.g. `"127.0.0.1"` for localhost-only) |
| `protocol` | string | `"TCP"` (default) or `"UDP"` |

---

## Networks

```nix
networks = {
  frontend = {};
  backend = {
    driver = "bridge";
  };
  external-net = {
    name = "my-external-network";
    external = true;
  };
};
```

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Network name (defaults to key name) |
| `driver` | string | Network driver |
| `external` | bool | Use an existing external network |

### Per-service network config

```nix
services.web.networks = {
  frontend = { aliases = [ "web-alias" ]; };
  backend = {};
};
```

---

## Volumes

```nix
volumes = {
  pgdata = {};
  shared = {
    driver = "local";
    driver_opts = { type = "tmpfs"; device = "tmpfs"; };
  };
  existing = {
    external = true;
  };
};
```

| Field | Type | Description |
|-------|------|-------------|
| `driver` | string | Volume driver |
| `driver_opts` | attrset | Driver-specific options |
| `external` | bool | Use an existing external volume |

---

## CLI flags

### Global flags

| Flag | Default | Description |
|------|---------|-------------|
| `--file` | — | Path to `compose.nix` or flake directory |
| `--flake-attr` | `composition` | Flake attribute to evaluate |
| `--impure` | `true` | Allow impure Nix evaluation |
| `--project-dir` | `.` | Project directory |
| `--project-name` | directory name | Project name; overrides a `name:` in the document |
| `--profile` | — | Activate profile (repeatable) |

### `up` flags

| Flag | Description |
|------|-------------|
| `-d, --detach` | Run in background |
| `--build` | Build images before starting |
| `--watch` | Watch Nix files and restart on change |
| `--wait` | Wait for services to be running/healthy |

### `down` flags

| Flag | Description |
|------|-------------|
| `-v, --volumes` | Remove named volumes |
| `--timeout INT` | Shutdown timeout in seconds |
| `--remove-orphans` | Remove containers not defined in config |
| `--rmi STRING` | Remove images (`all` or `local`) |

### `ps` flags

| Flag | Description |
|------|-------------|
| `-a, --all` | Show all containers (including stopped) |
| `-q, --quiet` | Only display container IDs |
| `--format STRING` | Output format (`table`, `json`) |
| `--services` | Display services only |

### `logs` flags

| Flag | Description |
|------|-------------|
| `-f, --follow` | Follow log output |
| `--tail STRING` | Lines from end (`100`, `all`) |
| `-t, --timestamps` | Show timestamps |
| `--since STRING` | Since timestamp (`2021-01-01T00:00:00Z`, `42m`) |
| `--no-log-prefix` | Don't print service name prefix |

### `kill` flags

| Flag | Description |
|------|-------------|
| `-s, --signal STRING` | Signal to send (default: `SIGKILL`) |

### `rm` flags

| Flag | Description |
|------|-------------|
| `-f, --force` | Don't ask to confirm |
| `-s, --stop` | Stop before removing |
| `-v, --volumes` | Remove anonymous volumes |

### `render` flags

| Flag | Description |
|------|-------------|
| `--target STRING` | Output format (required: `k8s`) |
| `--output STRING` | Write files to directory (default: stdout) |
| `--namespace STRING` | K8s namespace (default: `default`) |
| `--dry-run` | Validate via `kubectl apply --dry-run=client` |
