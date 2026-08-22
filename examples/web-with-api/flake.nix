{
  description = "web-with-api — nix-compose migration example";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
  };

  outputs = { self, nixpkgs }:
    let
      system = "x86_64-linux";
      pkgs = import nixpkgs { inherit system; };
    in {
      composition = {
        config.out.dockerComposeYamlAttrs = {
          services.web = {
            image = "nginx:latest";
            ports = [ "8080:80" ];
            depends_on.api.condition = "service_healthy";
            volumes = [ "./nginx.conf:/etc/nginx/conf.d/default.conf:ro" ];
            x-nix-compose.serviceInfo.defaultExec = [ "bash" ];
          };

          services.api = {
            image = "node:20-slim";
            working_dir = "/app";
            command = [ "node" "server.js" ];
            ports = [ "3000:3000" ];
            environment = {
              NODE_ENV = "production";
              DATABASE_URL = "postgres://app:secret@db:5432/app";
            };
            depends_on.db.condition = "service_healthy";
            healthcheck = {
              test = [ "CMD-SHELL" "curl -sf http://localhost:3000/health || exit 1" ];
              interval = "10s";
              timeout = "5s";
              retries = 3;
              start_period = "30s";
            };
            volumes = [ "./app:/app" ];
            x-nix-compose.serviceInfo.defaultExec = [ "sh" "-c" "node" ];
          };

          services.db = {
            image = "postgres:16";
            environment = {
              POSTGRES_USER = "app";
              POSTGRES_PASSWORD = "secret";
              POSTGRES_DB = "app";
            };
            ports = [ "5432:5432" ];
            healthcheck = {
              test = [ "CMD-SHELL" "pg_isready -U app" ];
              interval = "5s";
              timeout = "3s";
              retries = 5;
            };
            volumes = [ "pgdata:/var/lib/postgresql/data" ];
            x-nix-compose.serviceInfo.defaultExec = [ "psql" "-U" "app" ];
          };

          volumes.pgdata = {};
        };
      };
    };
}
