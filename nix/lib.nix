# nix-compose's Nix-side surface: build a service's image from a closure.
#
# ADR-006 used to say image builds were out of scope, so `image` had to name a
# registry tag someone else had pushed. It is now delegated to nix-oci: a
# service can name a *package* instead, and the image is built from that
# package's closure and handed to containerd as a store path. `pkg/cri` imports
# it directly (ADR-015), so nothing is pushed and nothing is pulled.
{
  lib,
  nix-oci,
}:

let
  inherit (nix-oci) buildOCIImage buildOCIImageCached buildOCIMultiArch;

  # Keys mkComposition consumes and rewrites into `image`. Everything else is
  # passed through to the compose service untouched.
  imageKeys = [ "package" "ociImage" ];

  # entrypointFor picks the command an image built from `package` runs.
  # An explicit service-level entrypoint wins; otherwise the package must say
  # what its main program is.
  entrypointFor = name: svc:
    if svc ? entrypoint then
      lib.toList svc.entrypoint
    else if (svc.package.meta.mainProgram or null) != null then
      [ (lib.getExe svc.package) ]
    else
      throw ''
        nix-compose: service '${name}' sets `package = ${lib.getName svc.package}`,
        but that package has no `meta.mainProgram`, so nix-compose cannot tell
        which binary to run.

        Either set the service's `entrypoint`:

            services.${name} = {
              package = <pkg>;
              entrypoint = [ "''${<pkg>}/bin/<binary>" ];
            };

        or build the image yourself and pass it as `ociImage`.
      '';

  # imageFor builds the OCI image for a service that declared a package. The
  # image is named after the package rather than the service so that two
  # services sharing a package share one image — and therefore one import.
  imageFor = name: svc:
    buildOCIImage {
      # Suffixed so the image is distinguishable from the package it wraps,
      # both in /nix/store and in `crictl images`.
      name = "${lib.getName svc.package}-oci";
      contents = [ svc.package ];
      entrypoint = entrypointFor name svc;
    };

  # withImage records both the image's store path and the derivation that
  # produces it. Evaluation yields the path without building anything, so
  # nix-compose needs the derivation to realise the bytes before it can import
  # them (see pkg/eval/realise.go).
  withImage = svc: drv:
    svc // {
      image = "${drv}";
      x-nix-compose = (svc.x-nix-compose or { }) // { imageDrv = drv.drvPath; };
    };

  # resolveService rewrites one service's image-producing keys into `image`.
  resolveService = name: svc:
    let
      declared = lib.filter (k: svc ? ${k}) imageKeys;
      rest = removeAttrs svc imageKeys;
    in
    if declared == [ ] then
      svc
    else if lib.length declared > 1 then
      throw "nix-compose: service '${name}' sets both `package` and `ociImage` — pick one."
    else if svc ? image then
      throw "nix-compose: service '${name}' sets both `image` and `${lib.head declared}` — pick one."
    else if svc ? ociImage then
      withImage rest svc.ociImage
    else
      withImage rest (imageFor name svc);

in
{
  # Re-exported from nix-oci so a composition needs only one flake input.
  inherit buildOCIImage buildOCIImageCached buildOCIMultiArch;

  # mkComposition wraps a set of compose attributes as the `composition` output
  # nix-compose evaluates, resolving `package` / `ociImage` into `image` on the
  # way through.
  #
  #   composition = nix-compose.legacyPackages.${system}.mkComposition {
  #     services.hello.package = pkgs.hello;
  #   };
  mkComposition = attrs: {
    config.out.dockerComposeYamlAttrs =
      attrs // lib.optionalAttrs (attrs ? services) {
        services = lib.mapAttrs resolveService attrs.services;
      };
  };

  # resolveServices is mkComposition's rewrite on its own, for compositions that
  # build `config.out.dockerComposeYamlAttrs` by hand.
  resolveServices = services: lib.mapAttrs resolveService services;
}
