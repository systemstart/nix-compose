{
  description = "integration-test fixture — a build: directive the CRI backend cannot honour";

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
            build.context = ".";
            ports = [ "18081:80" ];
          };
        };
      };
    };
}
