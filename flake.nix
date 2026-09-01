{
  description = "nix-compose — Nix-powered container orchestration";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
    # Builds a service's image from a Nix closure (ADR-006, ADR-015). Its
    # nixpkgs is followed so an image and the closure it packages agree.
    nix-oci = {
      url = "github:systemstart/nix-oci";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs = { self, nixpkgs, flake-utils, nix-oci }:
    let
      # Overlay is system-independent.
      overlayFn = final: prev: {
        nix-compose = final.callPackage ./nix-package.nix { };
      };
    in
    {
      overlays.default = overlayFn;
    }
    //
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs {
          inherit system;
          overlays = [ overlayFn ];
        };
      in {
        # buildOCIImage and mkComposition are functions, so they cannot live
        # under `packages` (which must hold derivations).
        legacyPackages = import ./nix/lib.nix {
          inherit (pkgs) lib;
          nix-oci = nix-oci.legacyPackages.${system};
        };

        packages = {
          default = pkgs.nix-compose;
        } // pkgs.lib.optionalAttrs pkgs.stdenv.hostPlatform.isLinux {
          microvm-image = (pkgs.callPackage ./nix/microvm-image.nix {
            nixpkgs = nixpkgs;
            nix-compose-bin = pkgs.nix-compose;
            inherit system;
          }).combined;

          ci-vm-image = (pkgs.callPackage ./nix/ci-vm-image.nix {
            nixpkgs = nixpkgs;
            inherit system;
          }).combined;
        };

        devShells.default = import ./shell.nix { inherit pkgs; };
      }
    );
}
