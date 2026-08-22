{
  description = "integration-test fixture — images built from Nix closures, no registry";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    # The nix-compose under test. A relative path keeps the fixture pinned to
    # this working tree rather than to a published revision.
    nix-compose = {
      url = "path:../../../..";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs = { self, nixpkgs, nix-compose }:
    let
      system = "x86_64-linux";
      pkgs = nixpkgs.legacyPackages.${system};
      nc = nix-compose.legacyPackages.${system};
    in {
      composition = nc.mkComposition {
        services = {
          # `package` with an explicit entrypoint. Stays running so `ps` can
          # see it.
          sleeper = {
            package = pkgs.coreutils;
            entrypoint = [ "${pkgs.coreutils}/bin/sleep" "infinity" ];
          };

          # `package` alone — the entrypoint comes from meta.mainProgram.
          # Exits immediately, so this one is asserted through `images`.
          hello.package = pkgs.hello;

          # `ociImage` — the image is built by hand for full control.
          custom = {
            ociImage = nc.buildOCIImage {
              name = "custom-sleeper";
              contents = [ pkgs.coreutils ];
              entrypoint = [ "${pkgs.coreutils}/bin/sleep" "infinity" ];
            };
          };
        };
      };
    };
}
