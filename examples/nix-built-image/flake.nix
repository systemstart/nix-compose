{
  description = "nix-built-image — a service whose image is built from a Nix closure";

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
      # No registry, no Dockerfile, no `docker build`. The image is built from
      # the package's closure and imported straight into containerd.
      composition = nc.mkComposition {
        services = {
          # The short form: name a package. nix-compose builds an image from
          # its closure and runs its meta.mainProgram.
          hello.package = pkgs.hello;

          # The long form: build the image yourself when you need control over
          # the OCI config — ports, user, env, extra layers.
          web = {
            ociImage = nc.buildOCIImage {
              name = "caddy-static";
              contents = [ pkgs.caddy ];
              entrypoint = [ "${pkgs.caddy}/bin/caddy" "file-server" "--root" "/srv" "--listen" ":8080" ];
              exposedPorts = [ "8080/tcp" ];
            };
            ports = [ "8080:8080" ];
            volumes = [ "./site:/srv:ro" ];
          };
        };
      };
    };
}
