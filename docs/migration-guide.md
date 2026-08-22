# Migrating from Docker Compose

This guide walks through converting an existing `docker-compose.yaml` to
a nix-compose project, step by step.

## The short version

```sh
nix-compose import          # docker-compose.yaml → nix-compose.yaml
nix-compose suggest         # which images have a nixpkgs equivalent
nix-compose up -d           # instead of docker compose up -d
```

`import` does the typing for you and reports everything that did not survive
the conversion, both on the terminal and as a comment in the generated file. A
service that used `build:` comes out marked `FIXME`, because it has nothing to
run until you give it an `image:` or a `package:`.

The rest of this guide covers the conversion by hand, and writing the project
in Nix rather than YAML — worth reading if you want the detail of what maps to
what, or if your project needs computation the YAML format cannot express.

## Overview

nix-compose replaces your hand-written `docker-compose.yaml` with either a
`nix-compose.yaml` (compose's fields, plus `package:`) or a Nix-evaluated
config. Either way it generates the compose YAML automatically when the compose
backend is used; that generated file lives in `.nix-compose/compose.yaml` and
is not hand-edited.

## The migration that actually matters

Converting the file is typing. The change worth making afterwards is replacing
`image:` with `package:` service by service: a package is built from its Nix
closure and imported straight into the runtime, so nothing is pulled from a
registry at all. Both kinds of service coexist in one project, so this happens
incrementally and the project runs at every step.

`nix-compose suggest` shows which images have a nixpkgs equivalent, and warns
when taking the suggestion would change the major version:

```
cache  redis:7-alpine  → package: redis       (nixpkgs 8.8.1)  ! major version 7 → 8
db     postgres:16     → package: postgresql  (nixpkgs 18.6)   ! major version 16 → 18
web    myapp:2.1       → no nixpkgs match, keeping registry image
```

## Step-by-step process

### 1. Keep your existing file as reference

Don't delete `docker-compose.yaml` yet. You'll use it as a reference while
writing the Nix equivalent.

### 2. Create a `flake.nix`

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
          # Your services go here
          services = { };
        };
      };
    };
}
```

### 3. Convert services one by one

Translate each service from YAML to Nix. See the field mapping table below.

### 4. Test

```sh
nix-compose up -d
nix-compose ps
nix-compose logs
```

### 5. Clean up

Once everything works, the original `docker-compose.yaml` can be removed.
Add `.nix-compose/` to `.gitignore` — the generated files are build
artifacts.

## Field mapping table

| Docker Compose YAML | nix-compose Nix |
|---------------------|-----------------|
| `image: nginx:latest` | `image = "nginx:latest";` |
| `build: .` | `build = { context = "."; };` |
| `build.dockerfile: Dockerfile.prod` | `build = { context = "."; dockerfile = "Dockerfile.prod"; };` |
| `command: ["node", "app.js"]` | `command = [ "node" "app.js" ];` |
| `command: node app.js` | `command = "node app.js";` |
| `entrypoint: ["/entrypoint.sh"]` | `entrypoint = [ "/entrypoint.sh" ];` |
| `ports: ["8080:80"]` | `ports = [ "8080:80" ];` |
| `environment:` (map) | `environment = { KEY = "value"; };` |
| `volumes: ["data:/data"]` | `volumes = [ "data:/data" ];` |
| `tmpfs: ["/tmp"]` | `tmpfs = [ "/tmp" ];` |
| `depends_on: [db]` | `depends_on = [ "db" ];` |
| `depends_on:` (with condition) | `depends_on = { db = { condition = "service_healthy"; }; };` |
| `restart: unless-stopped` | `restart = "unless-stopped";` |
| `working_dir: /app` | `working_dir = "/app";` |
| `user: "1000:1000"` | `user = "1000:1000";` |
| `privileged: true` | `privileged = true;` |
| `hostname: myhost` | `hostname = "myhost";` |
| `network_mode: host` | `network_mode = "host";` |
| `extra_hosts: ["host:ip"]` | `extra_hosts = [ "host:ip" ];` |
| `profiles: [debug]` | `profiles = [ "debug" ];` |
| `labels:` (map) | `labels = { key = "value"; };` |
| `stop_signal: SIGQUIT` | `stop_signal = "SIGQUIT";` |
| `networks: [frontend]` | `networks = [ "frontend" ];` |
| `volumes:` (top-level) | `volumes = { name = {}; };` |
| `networks:` (top-level) | `networks = { name = {}; };` |

### Healthcheck mapping

**Docker Compose:**
```yaml
healthcheck:
  test: ["CMD", "curl", "-f", "http://localhost/health"]
  interval: 10s
  timeout: 5s
  retries: 3
  start_period: 30s
```

**nix-compose:**
```nix
healthcheck = {
  test = [ "CMD" "curl" "-f" "http://localhost/health" ];
  interval = "10s";
  timeout = "5s";
  retries = 3;
  start_period = "30s";
};
```

## Side-by-side example

### Docker Compose

```yaml
version: "3.8"

services:
  web:
    image: nginx:latest
    ports:
      - "8080:80"
    volumes:
      - ./html:/usr/share/nginx/html:ro
    depends_on:
      - api

  api:
    image: node:20
    command: ["node", "server.js"]
    ports:
      - "3000:3000"
    environment:
      DATABASE_URL: postgres://app:secret@db:5432/app
    depends_on:
      db:
        condition: service_healthy

  db:
    image: postgres:16
    environment:
      POSTGRES_DB: app
      POSTGRES_USER: app
      POSTGRES_PASSWORD: secret
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U app"]
      interval: 5s
      timeout: 3s
      retries: 5

volumes:
  pgdata:
```

### nix-compose

```nix
{
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

  outputs = { self, nixpkgs }:
    let
      pkgs = import nixpkgs { system = "x86_64-linux"; };
    in {
      composition.config.out.dockerComposeYamlAttrs = {
        services = {
          web = {
            image = "nginx:latest";
            ports = [ "8080:80" ];
            volumes = [ "./html:/usr/share/nginx/html:ro" ];
            depends_on = [ "api" ];
            x-nix-compose.serviceInfo.defaultExec = [ "bash" ];
          };

          api = {
            image = "node:20";
            command = [ "node" "server.js" ];
            ports = [ "3000:3000" ];
            environment.DATABASE_URL = "postgres://app:secret@db:5432/app";
            depends_on = {
              db = { condition = "service_healthy"; };
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
      };
    };
}
```

## What's different

### Syntax

- YAML uses `:` for key-value; Nix uses `=` and terminates with `;`
- YAML lists use `- item`; Nix lists use `[ item1 item2 ]`
- YAML maps use indentation; Nix maps use `{ key = value; }`
- Strings in Nix must always be quoted

### Extra features in nix-compose

Features available in nix-compose that Docker Compose doesn't have:

| Feature | Description |
|---------|-------------|
| `x-nix-compose.defaultExec` | Default command for `exec` without args |
| `x-nix-compose.useHostStore` | Bind-mount `/nix/store` into containers |
| `x-nix-compose.resources` | K8s-style resource limits/requests |
| `x-nix-compose.probes` | K8s-style liveness/readiness probes |
| `x-nix-compose.envFrom` | Load env vars from files (incl. sops) |
| `x-nix-compose.initContainers` | Run containers before main service starts |
| `x-nix-compose.namedPorts` | Named ports for K8s manifest generation |
| `render --target k8s` | Generate K8s manifests from same config |

### CLI mapping

| Docker Compose | nix-compose |
|---------------|-------------|
| `docker compose up -d` | `nix-compose up -d` |
| `docker compose down` | `nix-compose down` |
| `docker compose ps` | `nix-compose ps` |
| `docker compose logs -f` | `nix-compose logs -f` |
| `docker compose exec web bash` | `nix-compose exec web bash` |
| `docker compose build` | `nix-compose build` |
| `docker compose pull` | `nix-compose pull` |
| `docker compose restart` | `nix-compose restart` |
| `docker compose stop` | `nix-compose stop` |
| `docker compose start` | `nix-compose start` |
| `docker compose kill` | `nix-compose kill` |
| `docker compose rm` | `nix-compose rm` |
| `docker compose top` | `nix-compose top` |
| `docker compose images` | `nix-compose images` |
| `docker compose config` | `nix-compose config` |

## Common pitfalls

### Forgetting semicolons

Every Nix assignment must end with `;`. This is the most common syntax error:

```nix
# Wrong
environment = {
  KEY = "value"   # missing semicolon
}

# Right
environment = {
  KEY = "value";
};
```

### String quoting

All string values in Nix must be quoted, even single words:

```nix
# Wrong
image = nginx:latest;

# Right
image = "nginx:latest";
```

### `version` field

Docker Compose `version: "3.8"` is not needed and is not supported.
nix-compose generates valid Compose YAML without a version field.

### List syntax

Nix lists use spaces, not commas:

```nix
# Wrong
ports = [ "8080:80", "443:443" ];

# Right
ports = [ "8080:80" "443:443" ];
```

### Nested attributes

Nix allows shorthand for nested attributes:

```nix
# These are equivalent:
x-nix-compose.serviceInfo.defaultExec = [ "bash" ];

x-nix-compose = {
  serviceInfo = {
    defaultExec = [ "bash" ];
  };
};
```

### Environment variables

Docker Compose allows `KEY: value` without quoting the value. In Nix,
values are always strings:

```nix
# Wrong (in Nix)
environment.PORT = 3000;

# Right
environment.PORT = "3000";
```
