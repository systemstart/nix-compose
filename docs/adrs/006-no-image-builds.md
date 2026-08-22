# ADR-006: Image builds delegated to nix-oci

**Status:** Accepted (supersedes the original "No image build support",
2026-04-01)
**Date:** 2026-08-18
**Context:** CRI migration (M7); revised after nix-oci (ADR-015)

## Context

CRI has no build API. Compose supports `build:` directives to build images
before starting containers.

Alternatives considered originally:
1. Shell out to `buildctl` / `buildah` / `nerdctl build` — adds a
   non-CRI dependency for the build step only
2. Use buildkit's Go client library (`github.com/moby/buildkit/client`)
   — adds significant dependency surface
3. Declare builds out of scope — users build images with Nix
   (`dockerTools.buildImage`) or external CI and reference by tag

Option 3 was taken. It had a hole nobody noticed for four months: there was no
way to get a Nix-built image *in*. `PullImage` is the only image path CRI
offers and it speaks only to registries, so "build it with Nix" still meant
"then push it to a registry and pull it back". The Nix-native story dead-ended
one step short of working.

## Decision

Builds stay out of the **orchestrator**, but they are no longer out of
**scope**. They are delegated to [nix-oci](https://github.com/systemstart/nix-oci),
which turns a Nix closure into a bit-reproducible OCI layout, and the result is
imported directly into containerd ([ADR-015](015-virtiofs-image-delivery.md)).

A service names a package instead of a tag:

```nix
composition = nix-compose.legacyPackages.${system}.mkComposition {
  services.hello.package = pkgs.hello;
};
```

`mkComposition` builds the image from that package's closure and rewrites the
service's `image` to the resulting store path, which `pkg/cri` imports. Nothing
is pushed and nothing is pulled. `services.<name>.ociImage` takes a
`buildOCIImage` derivation directly for full control of the OCI config.

`build:` is still refused, but the error now points at these options rather
than telling users to go build the image somewhere else
(`cri.ValidateImages`).

Evaluation yields the image's store path without building it — `nix eval` never
builds — so the CRI commands realise the derivation first
(`eval.RealiseImages`). The derivation path travels alongside the image path as
`x-nix-compose.imageDrv`, because an output path on its own cannot be built.

## Consequences

- No build-related dependencies in the runtime; the build is Nix's job, and it
  happens through the same evaluation that produces the service graph
- The registry stops being a required participant in the loop — this is the
  property compose and KinD cannot structurally offer
- The image is content-addressed by its store path, so re-`up` on an unchanged
  image is a no-op and a rebuilt image lands under a fresh reference
- Users who rely on `docker compose build` need to adapt: build the image
  externally and reference it by tag, or express it as a Nix package
- A runtime without containerd's native API (CRI-O) can pull images but cannot
  import locally built ones
