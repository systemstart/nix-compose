# The package definition used by ./default.nix and by the flake's overlay.
# Kept deliberately close to pkgs/by-name/ni/nix-compose/package.nix in
# nixpkgs; the only difference there is that `src` is a fetchFromGitHub of the
# release tag rather than the working tree.
{ lib, buildGoModule }:

let
  # Version is the single source of truth in ./VERSION, bumped and committed by
  # `make release-tag` as part of tagging -- so the Nix build, the goreleaser
  # artifact, and the git tag can never drift. It names the Nix package and
  # stamps the binary (via ldflags).
  version = lib.fileContents ./VERSION;
in
buildGoModule {
  pname = "nix-compose";
  inherit version;
  src = lib.cleanSource ./.;

  vendorHash = "sha256-cd2vMnPe7+WbhWOLS9ZYVseBQG6wPlp/yrVswxBkMMI=";

  # buildGoModule sets CGO_ENABLED inside `env`, and nixpkgs rejects the same
  # name appearing in both `env` and the derivation arguments.
  env.CGO_ENABLED = 0;

  ldflags = [
    "-s"
    "-w"
    "-X"
    "main.version=${version}"
  ];

  subPackages = [ "cmd/nix-compose" ];

  # `description` deliberately has no leading article and no trailing period:
  # nixpkgs' meta lint rejects both.
  meta = {
    description = "Nix-powered container orchestration from service definitions";
    longDescription = ''
      nix-compose evaluates Nix service definitions and orchestrates the
      resulting containers through a CRI runtime.

      It drives several tools that must come from the host rather than from
      this package's closure, and so are deliberately not wrapped in:

        * nix, which has to match the user's daemon and store;
        * containerd or CRI-O, reached over its CRI socket;
        * cni-plugins, plus iptables on the *runtime's* PATH -- containerd
          execs the CNI bridge plugin with its own environment, not with
          nix-compose's;
        * virtiofsd and a hypervisor, for the microvm backend only.

      Run `nix-compose doctor` to check which of them are present.
    '';
    homepage = "https://github.com/systemstart/nix-compose";
    changelog = "https://github.com/systemstart/nix-compose/blob/v${version}/CHANGELOG.md";
    license = lib.licenses.gpl3Only;
    mainProgram = "nix-compose";
    platforms = lib.platforms.linux;
  };
}
