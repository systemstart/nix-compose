{
  description = "integration-test fixture — minimal service";

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
            ports = [ "18080:80" ];
          };
        };
      };
    };
}
