# ADR-025: Compose import and package suggestion

- **Status:** Accepted
- **Date:** 2026-08-19
- **Relates to:** ADR-024 (YAML project format), ADR-006 (image builds delegated to nix-oci)

## Context

ADR-024 makes a nix-compose project writable without Nix, but a user
with an existing `docker-compose.yml` still has to retype it, and then
has no way to tell which of their registry images could stop being
pulled at all.

The migration that matters is not "compose file → nix-compose file", it
is "registry image → Nix closure". The first is typing; the second is
the reason the project exists. Tooling should carry the typing and then
point at the second.

## Decision

Two commands.

**`nix-compose import [file]`** converts a compose file into a
`nix-compose.yaml`. It refuses to overwrite an existing one without
`--force`.

**`nix-compose suggest`** reads the project and, for every service
naming a registry image, looks for a nixpkgs package of the same name.
It changes nothing.

### The conversion is lossy, and says so

Most of a compose file passes straight through, because nix-compose's
service fields *are* compose's. The interesting part is what does not.
Every dropped key produces a note carrying the reason and, where one
exists, the thing to write instead — printed to the terminal *and*
written into the generated file as a header comment.

The importer asks `eval.ServiceKeys()` what survives rather than keeping
its own list, and runs `eval.ValidateDoc` over its own output before
writing: a generated file the loader would reject is a defect in the
importer, not the user's problem.

### A dropped `build:` leaves a hole, and the hole is marked

Removing `build:` leaves a service naming nothing to run. The document
is still structurally valid, so nothing would complain until `up`
failed. Such services are listed in `Result.NeedsImage` and get a
`FIXME` head comment in the generated file, above the service itself.

### Compose's alternative spellings are normalised

`environment` lists become mappings, all four `ports` spellings become
the short string form, and long-syntax `volumes` become short mounts.
An `- FOO` environment entry with no value means "inherit from the host
environment", which a declarative composition cannot express; it is
reported rather than guessed at.

Ports are written **quoted**. `gopkg.in/yaml.v3` reads the YAML 1.2 core
schema and parses a bare `8080:80` as a string, but compose files get
read by more than one parser, and under YAML 1.1 that is a sexagesimal
integer.

### `suggest` needs an alias table

Nothing named `postgres`, `node`, or `python` exists in nixpkgs. Without
`postgres → postgresql`, `node → nodejs`, `python → python3` and the
rest, the command would miss on exactly the stacks people run. One `nix
eval` answers for every service in the project.

### `suggest` warns rather than recommends

nixpkgs tracks its own versions: `redis:7` maps to a nixpkgs redis 8,
`postgres:16` to an 18. Taking a suggestion unread would be a major
upgrade nobody asked for, so the tag's major version is compared against
the package's and any difference is flagged, along with a missing
`meta.mainProgram` (which means an `entrypoint:` is also needed).

## Consequences

- Migration is incremental by construction. `image:` and `package:`
  coexist in one document, so a project moves one service at a time and
  is runnable at every step.
- `suggest` only reads YAML projects. For a flake it says to set
  `package = pkgs.<name>;` directly, because suggesting into Nix would
  mean parsing Nix. This asymmetry is accepted.
- `import` is one-way. There is no "re-import" that preserves hand
  edits; the refusal to overwrite without `--force` is what protects
  them.
