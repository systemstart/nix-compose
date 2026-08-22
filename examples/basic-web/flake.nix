{
  description = "basic-web — nix-compose migration example";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
  };

  outputs = { self, nixpkgs }:
    let
      system = "x86_64-linux";
      pkgs = import nixpkgs { inherit system; };
    in {
      # nix-compose evaluates this attribute by default.
      composition = {
        config.out.dockerComposeYamlAttrs = {
          services.web = {
            image = "nginx:latest";
            ports = [ "8080:80" ];
            volumes = [ "./html:/usr/share/nginx/html:ro" ];
            environment = {
              NGINX_HOST = "localhost";
            };
            restart = "unless-stopped";

            # nix-compose extension: default shell for `nix-compose exec web`
            x-nix-compose.serviceInfo.defaultExec = [ "bash" ];
          };
        };
      };
    };
}
