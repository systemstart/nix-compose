# ADR-024: YAML project format

- **Status:** Accepted
- **Date:** 2026-08-19
- **Relates to:** ADR-006 (image builds delegated to nix-oci), ADR-015 (virtiofs image delivery)

## Context

nix-compose took Nix as its only input language. That is the right
language for the problem — the composition and the images come from one
evaluation — but it is also the single largest barrier to trying the
tool. A user evaluating the *runtime* (CRI orchestration, plan/drift,
registry-free images) had to first learn the *language*.

Two facts made a second front-end cheap:

- `eval.Service` was already a compose-shaped struct whose JSON tags are
  compose's own field names (`depends_on`, `working_dir`,
  `network_mode`, …). Everything downstream — `depgraph`, `health`,
  `envfrom`, `volumes`, `cni`, `orchestrate`, the CRI backend — consumes
  only that struct.
- `DetectMode` had exactly one caller.

So a YAML front-end is a parser, not an architectural change.

The risk is the obvious one: a YAML surface that only accepts `image:`
would be a worse podman-compose. What distinguishes this project is not
Nix-the-language, it is `package:` — naming a package instead of a
registry tag, so the image is built from its closure and imported
straight into the runtime (ADR-006, ADR-015). The question was whether
that field could survive the move out of Nix.

## Decision

Add **`nix-compose.yaml`** as a third project mode, and keep `package:`
in it.

YAML cannot carry a derivation, so `package:` carries a nixpkgs
attribute path instead, and `nix/yaml.nix` turns that string back into
the derivation `nix/lib.nix` expects. YAML mode is a *front-end onto*
`nix/lib.nix`, not a second implementation of it.

```yaml
services:
  greeter:
    package: hello                # built from its closure, no registry
  cache:
    image: redis:7-alpine         # registry images mix freely
```

### Mode precedence

`flake.nix`, then `compose.nix`, then `nix-compose.yaml`. Adding a YAML
file to a project that already has a Nix one cannot silently change how
it is evaluated.

### Two evaluation paths

A document that names no `package:` is pure data: it is parsed straight
into a `Composition` and **nix is never executed** (~11 ms, and it works
on a machine with no Nix installed). Only `package:` triggers an
evaluation.

### Naming

The input is `nix-compose.yaml`, deliberately not `compose.yaml`: docker
compose claims that name, and a file it would pick up and choke on is a
worse first experience than a name that is obviously ours. The
*generated* compose file was renamed `.nix-compose/compose.yaml` at the
same time, so the input and the output no longer share a basename.

### Strictness

Unknown keys are **rejected**, not ignored. Compose silently ignores
them, which is how a misspelled `enviroment:` becomes an hour of
debugging. The set of valid service keys is derived by reflection over
`eval.Service`, so a field cannot be supported by the backend and
rejected by the parser at the same time.

### Entrypoints

An image built from a closure has no `PATH`, and YAML has no string
interpolation, so neither `/bin/sleep` nor a store path is writable by
hand. A bare command name in `entrypoint:` is resolved against the
package's own `bin` directory. Without this, any package lacking
`meta.mainProgram` is unusable from YAML.

## Consequences

**The nixpkgs version is not in the file.** `image: nginx:1.27` states
the version; `package: nginx` does not. It resolves against the pin the
binary carries (`pkg/nixpins`, checked against `flake.lock` by a test),
so the nix-compose version determines package versions the way a distro
release does. Two machines with different nix-compose versions get
different results from a byte-identical document, and upgrading
nix-compose re-imports every YAML-built image (new closure → new store
hash → new content-addressed reference). A top-level `nixpkgs:` key
overrides the pin per project.

This is not worse than compose's untagged `image: nginx`, but it *looks*
more precise than it is. **A lock file is the intended resolution** and
is not implemented yet — see the "YAML lock file" row in
[ROADMAP.md](../../ROADMAP.md).

**Evaluation is impure.** The generated expression needs `--impure` for
one reason: it reads the embedded Nix sources by absolute path. Nothing
else about it is impure — the system double is passed in from Go and
both flake references are full revisions. Note that `nix store add` does
**not** avoid this: pure evaluation refuses a `/nix/store` path inside
an `--expr` just as it refuses any other absolute path. The only route
to pure evaluation is to stop using `--expr` and generate a *flake*,
which would also produce the lock file above. The two are one change.

**No `eject`.** A project that outgrows YAML has to be rewritten as a
flake by hand. The YAML is a strict subset of what `mkComposition`
accepts, so nothing is lost, but the conversion is manual.

## Alternatives considered

- **A YAML surface without `package:`** — rejected. It would compete
  with docker-compose and KinD on maturity, which is exactly the
  judgement that parked this project once already.
- **Requiring `nixpkgs:` in every document** — rejected. Boilerplate
  before anything runs, which defeats the purpose of the format.
- **Reading `docker-compose.yml` directly as a project format** —
  rejected. It would make compose's whole surface a compatibility
  obligation. Conversion is a one-way import instead (ADR-025).
