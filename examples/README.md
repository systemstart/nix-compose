# Examples

Each directory contains a self-contained nix-compose project you can start
with a single command. Most include a `flake.nix` (the nix-compose source of
truth); [`yaml/`](yaml/) instead uses a `nix-compose.yaml` and no Nix file at
all, which is the lowest-ceremony way in.

> **Note:** Generated files (`.nix-compose/` and `flake.lock`) are excluded
> via the shared `examples/.gitignore`. They are created at runtime and should
> not be committed.

## Prerequisites

- NixOS (or Nix with flakes enabled)
- A CRI runtime — containerd or CRI-O — with its socket readable by your
  user. Run `nix-compose doctor` to check
- nix-compose binary on `$PATH` (`make build` from the repo root)

## Running any example

```bash
cd examples/<name>
nix-compose up -d          # evaluate Nix config, generate YAML, start containers
nix-compose ps             # check running services
nix-compose logs -f        # follow logs
nix-compose down           # stop and clean up
```

## Rendering K8s manifests

Any example can also produce Kubernetes manifests:

```bash
cd examples/<name>

# Multi-document YAML to stdout
nix-compose render --target k8s

# Individual files + kustomization.yaml to a directory
nix-compose render --target k8s --output ./k8s/

# With a custom namespace
nix-compose render --target k8s --namespace production --output ./k8s/

# Validate with kubectl (requires kubectl on PATH)
nix-compose render --target k8s --dry-run
```

---

## yaml

**A project with no Nix file — `nix-compose.yaml` only.**

```
examples/yaml/
  nix-compose.yaml    # the whole project
```

```bash
cd examples/yaml
nix-compose up -d
nix-compose ps        # ticker and cache running
nix-compose images    # note which references are nix-compose.local/
nix-compose down
```

The point of the example is the mixture. `greeter` and `ticker` name a
`package:`, so their images are built from a Nix closure and imported straight
into the runtime — no registry, no Dockerfile. `cache` names a registry
`image:` like any compose file would. Both kinds of service coexist, which is
what lets an existing project move one service at a time rather than all at
once.

A document that names no `package:` at all never runs Nix, and evaluates in
milliseconds.

---

## basic-web

**Single Nginx container serving a static page.**

```
examples/basic-web/
  flake.nix           # nix-compose config
  html/index.html     # static content
```

### What it demonstrates

- Minimal nix-compose setup: one service, one port, one volume
- `x-nix-compose.serviceInfo.defaultExec` for `nix-compose exec web bash`

### What to expect

```bash
cd examples/basic-web
nix-compose up -d
curl http://localhost:8080    # -> "Hello from nix-compose!"
nix-compose exec web bash     # interactive shell in the container
nix-compose down
```

---

## nix-built-image

**Two services whose images are built from Nix closures — no registry.**

```
examples/nix-built-image/
  flake.nix           # nix-compose config, images built by nix-oci
  site/index.html     # static content
```

### What it demonstrates

- `services.<name>.package` — name a package, get an image built from its
  closure and imported straight into containerd
- `services.<name>.ociImage` — build the image yourself when you need control
  over the OCI config (ports, entrypoint, user)
- `mkComposition` — resolves both into the service's `image`

### What to expect

```bash
cd examples/nix-built-image
nix-compose up -d
nix-compose logs hello           # -> "Hello, world!"
curl http://localhost:8080       # -> "Served from a Nix closure"
crictl images | grep nix-compose.local
nix-compose down
```

**Requires containerd** — local image import has no CRI-O equivalent
(see [docs/limitations.md](../docs/limitations.md)).

---

## web-with-api

**Three-service stack: Nginx reverse proxy, Node.js API, PostgreSQL database.**

```
examples/web-with-api/
  flake.nix           # nix-compose config
  app/server.js       # Node.js API with /health endpoint
  nginx.conf          # reverse proxy config
```

### What it demonstrates

- Multi-service composition with `depends_on` and health conditions
- Healthcheck configuration (`CMD-SHELL` with curl)
- Named volumes (`pgdata`)
- Different `defaultExec` per service (`bash`, `sh -c node`, `psql -U app`)

### What to expect

```bash
cd examples/web-with-api
nix-compose up -d
curl http://localhost:8080          # -> proxied to API
curl http://localhost:3000/health   # -> {"status":"ok"}
nix-compose exec db psql -U app    # interactive psql session
nix-compose down -v                # stop and remove volumes
```

Services start in order: `db` (waits for healthy) -> `api` (waits for healthy)
-> `web`.

---
