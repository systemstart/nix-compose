# Getting started

This guide walks you through creating your first nix-compose project.

## Prerequisites

- nix-compose installed (see [install.md](install.md))
- Nix with flakes enabled — needed for `package:` and for flake projects,
  but not for a YAML project that only names registry images
- A CRI runtime — containerd or CRI-O — reachable by your user
  (run `nix-compose doctor` to check)

Run `nix-compose doctor` to check all of this against your machine.

## 1. Create a project

```sh
mkdir my-project && cd my-project
git init
```

## 2. Write a `nix-compose.yaml`…

The quickest way in. The field names are docker-compose's:

```yaml
# nix-compose.yaml
services:
  web:
    image: nginx:latest
    ports: ["8080:80"]
```

Name a `package:` instead of an `image:` and the image is built from that
package's Nix closure and imported straight into the runtime — no registry, no
Dockerfile. The two kinds mix in one file:

```yaml
services:
  web:
    image: nginx:latest
  greeter:
    package: hello
```

Already have a `docker-compose.yaml`? `nix-compose import` converts it, and
`nix-compose suggest` shows which of its images have a nixpkgs equivalent. See
[migration-guide.md](migration-guide.md).

Then skip to step 3. For anything needing real computation — overlays, custom
packages, shared configuration — write a flake instead:

## 2b. …or write a `flake.nix`

```nix
{
  description = "my-project services";

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
            volumes = [ "./html:/usr/share/nginx/html:ro" ];
            environment.NGINX_HOST = "localhost";

            x-nix-compose.serviceInfo.defaultExec = [ "bash" ];
          };
        };
      };
    };
}
```

## 3. Start services

```sh
nix-compose up -d
```

nix-compose will:
1. Evaluate the project — `nix eval --json` for a flake or `compose.nix`; a
   `nix-compose.yaml` naming no `package:` skips Nix entirely
2. Create a GC root to keep the Nix store paths alive
3. Pull or import each service's image
4. Start services in dependency order over CRI gRPC, waiting on health
   checks between levels

No compose file is written and nothing shells out to another tool.

## 4. Inspect

```sh
nix-compose ps              # list running services
nix-compose logs -f web     # follow logs
nix-compose exec web        # shell into container (uses defaultExec)
nix-compose exec web ls /   # run a specific command
```

## 5. Stop

```sh
nix-compose down
```

This stops containers and removes the GC root.

## Adding more services

Add services to the `services` attribute set:

```nix
services = {
  web = {
    image = "nginx:latest";
    ports = [ "8080:80" ];
    depends_on.api.condition = "service_healthy";
  };

  api = {
    image = "node:20";
    command = [ "node" "server.js" ];
    ports = [ "3000:3000" ];
    environment.DATABASE_URL = "postgres://app:secret@db:5432/app";
    depends_on.db.condition = "service_healthy";
    healthcheck = {
      test = [ "CMD" "curl" "-f" "http://localhost:3000/health" ];
      interval = "10s";
      timeout = "5s";
      retries = 3;
    };
  };

  db = {
    image = "postgres:16";
    environment = {
      POSTGRES_DB = "app";
      POSTGRES_USER = "app";
      POSTGRES_PASSWORD = "secret";
    };
    volumes = [ "pgdata:/var/lib/postgresql/data" ];
    healthcheck = {
      test = [ "CMD-SHELL" "pg_isready -U app" ];
      interval = "5s";
      timeout = "3s";
      retries = 5;
    };
    x-nix-compose.serviceInfo.defaultExec = [ "psql" "-U" "app" ];
  };
};

volumes.pgdata = {};
```

## Using profiles

Tag services with `profiles` so they only start when explicitly activated:

```nix
services.grafana = {
  image = "grafana/grafana:latest";
  ports = [ "3001:3000" ];
  profiles = [ "observability" ];
};
```

```sh
nix-compose up -d --profile observability
```

Services without profiles always start. See the
[config reference](config-reference.md) for details.

## Watch mode

Re-evaluate and restart services automatically when Nix files change:

```sh
nix-compose up --watch
```

Only services whose configuration changed are restarted.

## Kubernetes manifests

`render --target k8s` emits `Deployment`, `Service`, `Secret` and `PVC`
manifests plus a `kustomization.yaml` from the same config:

```sh
nix-compose render --target k8s --output ./k8s/
```

Treat the output as a starting point you then own, not as a deployment
pipeline — it is a one-way escape hatch. If you already deploy with
Kustomize, Helm or Argo, keep doing that. Note that `x-nix-compose.resources`
is emitted into the manifests but is not applied to locally running
containers; see [limitations.md](limitations.md).

## Next steps

- [Configuration reference](config-reference.md) — every supported field
- [Migration guide](migration-guide.md) — converting from Docker Compose
- [Limitations](limitations.md) — known gaps and workarounds
