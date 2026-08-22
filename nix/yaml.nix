# Resolve a nix-compose.yaml document into a composition.
#
# YAML mode exists so that the field which makes the registry-free loop
# possible — `package` — survives the move out of Nix. YAML cannot carry a
# derivation, so it carries a nixpkgs attribute path instead, and this file
# turns that string back into the derivation nix/lib.nix expects. Everything
# else in the document is already compose-shaped and passes straight through.
#
# `data` is the YAML document as JSON, handed over by pkg/eval.
{
  pkgs,
  nclib,
  data,
}:

let
  inherit (pkgs) lib;

  # resolvePackage looks up a dotted attribute path in nixpkgs. It walks the
  # path by hand so a failure can name the service and the string the user
  # actually wrote — nixpkgs' own error reports neither, and in YAML mode the
  # user has no Nix file to look at for context.
  resolvePackage =
    svc: path:
    let
      parts = lib.splitString "." path;
      step =
        acc: part:
        if !acc.ok then
          acc
        else if lib.isAttrs acc.value && acc.value ? ${part} then
          {
            ok = true;
            value = acc.value.${part};
            seen = acc.seen ++ [ part ];
          }
        else
          {
            ok = false;
            value = null;
            seen = acc.seen ++ [ part ];
          };
      found = lib.foldl' step {
        ok = true;
        value = pkgs;
        seen = [ ];
      } parts;
    in
    if found.ok then
      found.value
    else
      throw ''
        nix-compose: service '${svc}' sets `package: ${path}`, but there is no
        `${lib.concatStringsSep "." found.seen}` in the pinned nixpkgs.

        The attribute is often not the command name — `package: ripgrep`
        provides `rg`. Search for the right one at:

            https://search.nixos.org/packages

        If the package only exists in a different nixpkgs, pin one for this
        project:

            nixpkgs: github:NixOS/nixpkgs/nixos-unstable
      '';

  # resolveEntrypoint makes `entrypoint: ["sleep", "3600"]` mean what a compose
  # reader expects — run the `sleep` this package ships. An image built from a
  # closure has no PATH to search and a YAML file cannot interpolate a store
  # path, so a bare command name is resolved against the package's own bin
  # directory. Absolute paths are left alone.
  resolveEntrypoint =
    pkg: entrypoint:
    let
      parts = lib.toList entrypoint;
      head = builtins.head parts;
    in
    if parts == [ ] || lib.hasPrefix "/" head then
      parts
    else
      [ "${pkg}/bin/${head}" ] ++ builtins.tail parts;

  resolveService =
    name: svc:
    if !(svc ? package) then
      svc
    else if !(builtins.isString svc.package) then
      throw ''
        nix-compose: service '${name}' sets `package` to a ${builtins.typeOf svc.package},
        but in a YAML project it must be a nixpkgs attribute name:

            services:
              ${name}:
                package: hello
      ''
    else
      let
        pkg = resolvePackage name svc.package;
      in
      svc
      // { package = pkg; }
      // lib.optionalAttrs (svc ? entrypoint) { entrypoint = resolveEntrypoint pkg svc.entrypoint; };

in
# `nixpkgs` is nix-compose's own configuration, not a compose key, so it is
# dropped before the document becomes a composition.
nclib.mkComposition (
  removeAttrs data [
    "name"
    "nixpkgs"
    "services"
    "version"
  ]
  // lib.optionalAttrs (data ? services) { services = lib.mapAttrs resolveService data.services; }
)
