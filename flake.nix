{
  description = "nix-compose — Nix-powered container orchestration";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
    # Builds a service's image from a Nix closure (ADR-006, ADR-015). Its
    # nixpkgs is followed so an image and the closure it packages agree.
    #
    # Pinned to a release tag, not the default branch. nix-oci's compressor is
    # digest-affecting, so every move of this input re-digests every image
    # nix-compose builds; on a branch ref that would happen silently on any
    # lock-file-maintenance run. A tag makes it a deliberate, reviewable bump
    # that lines up with a nix-oci release -- the same way the consumer repos
    # pin it.
    nix-oci = {
      url = "github:systemstart/nix-oci/v0.5.0";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    # The Go toolchain. go-overlay tracks go.dev directly -- every patch and
    # RC, within hours of release -- so the pin below is never gated on when
    # nixpkgs gets round to packaging a release: its default `go` still lagged
    # 1.27.0 by weeks. Same mechanism nix-oci uses, where it matters more
    # (there the compressor version is digest-affecting).
    go-overlay = {
      url = "github:purpleclay/go-overlay";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs = { self, nixpkgs, flake-utils, nix-oci, go-overlay }:
    let
      # The Go release this repo builds and develops against. Note this is not
      # go.mod's directive: that stays at the dependency graph's true minimum
      # (containerd v2 needs 1.26.3), which is a floor for consumers, not a
      # statement about which toolchain we build with.
      # Renovate proposes bumps from the golang-version datasource (see
      # renovate.json). go-overlay publishes a new Go release within hours, but
      # this flake sees only what flake.lock has: bumping the literal on its own
      # fails to evaluate -- `attribute '"1.27.1"' missing` -- for as long as
      # the locked go-overlay predates the release. Merge the lock-file
      # maintenance PR first (scheduled earlier for that reason), or run
      # `nix flake update go-overlay` in the same branch.
      # renovate: datasource=golang-version depName=go
      goVersion = "1.27.0";

      # Overlay is system-independent.
      #
      # The toolchain pin lives here rather than in nix-package.nix, because
      # that file is deliberately kept close to
      # pkgs/by-name/ni/nix-compose/package.nix in nixpkgs, which cannot take a
      # third-party overlay. So: when go-overlay is in scope (our flake, below)
      # the pinned release is used; when someone applies this overlay to a plain
      # nixpkgs, it falls back to that nixpkgs' default Go -- which is exactly
      # what the nixpkgs build does anyway.
      overlayFn = final: prev: {
        nix-compose = final.callPackage ./nix-package.nix (
          nixpkgs.lib.optionalAttrs (final ? go-bin) {
            buildGoModule = final.buildGoModule.override {
              go = final.go-bin.versions.${goVersion};
            };
          }
        );
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
          overlays = [ go-overlay.overlays.default overlayFn ];
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

        devShells.default = import ./shell.nix {
          inherit pkgs;
          go = pkgs.go-bin.versions.${goVersion};
        };
      }
    );
}
