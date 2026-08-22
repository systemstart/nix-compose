{ lib, buildGo126Module, rev ? "dev" }:

buildGo126Module {
  pname = "nix-compose";
  version = rev;
  src = lib.cleanSource ./.;

  vendorHash = "sha256-4/qQSPDhk5mOvwH2eduO9LdQWypEkR5l2Rb5piV8yNk=";

  # buildGoModule now sets CGO_ENABLED inside `env`, and nixpkgs rejects the
  # same name appearing in both `env` and the derivation arguments.
  env.CGO_ENABLED = 0;

  ldflags = [
    "-X main.version=${rev}"
  ];

  subPackages = [ "cmd/nix-compose" ];

  meta = {
    description = "Evaluate Nix service definitions and orchestrate containers";
    mainProgram = "nix-compose";
    license = lib.licenses.gpl3Only;
  };
}
