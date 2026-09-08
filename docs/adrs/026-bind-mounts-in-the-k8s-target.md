# ADR-026: Bind mounts in the K8s render target

**Status:** Accepted
**Date:** 2026-09-07
**Context:** `render --target k8s` (pkg/k8s)

## Context

`render --target k8s` translated every volume string that was not a
declared named volume into `emptyDir: {}`, under a name derived from the
host path by replacing `/` with `-`.

Both halves of that are wrong for a bind mount:

- The name is not an RFC 1123 label. `./container/files/auth/hydra/hydra.yml`
  became `.-container-files-auth-hydra-hydra.yml`, which the API server
  rejects — it starts with `.` and contains `.`. Neither `kubectl kustomize`
  nor `kubectl apply --dry-run=client` validates names, so a consumer that
  renders and diffs in CI saw nothing wrong.
- `emptyDir` carries no content. A service bind-mounting its configuration
  file got a Deployment that applies cleanly once the name is fixed, and
  then starts with an empty directory where its config should be.

The second failure is the dangerous one: it is silent at render time, and
surfaces as a container that cannot find its configuration. Sanitizing the
name alone would convert a loud build-time failure into a quiet runtime one.

A downstream consumer had 35 such mounts across 24 Deployments, every one of
them an `emptyDir`.

## Decision

### A bind-mounted file becomes a generated ConfigMap

`pkg/k8s` reads the file the bind mount points at and emits a ConfigMap
holding its content, mounted with `subPath` — the idiomatic Kubernetes
equivalent of mounting one file:

```yaml
volumeMounts:
  - name: hydra-hydra-yml
    mountPath: /etc/hydra/hydra.yml
    subPath: hydra.yml
    readOnly: true
volumes:
  - name: hydra-hydra-yml
    configMap:
      name: hydra-hydra-yml
```

The mount is `readOnly: true` whether or not the compose file said `:ro`,
because a ConfigMap volume is read-only regardless.

This makes `pkg/k8s` read the filesystem, which it did not before. The
alternative — a pure function over the composition — cannot represent a bind
mount at all, and that is the defect being fixed. `pkg/orchestrate/convert`
stays pure (ADR-016); it is the CRI path, where a bind mount is a bind mount.

### Names are sanitized against the label regex

Every generated name is lower-cased, has each run of disallowed characters
collapsed to a single `-`, is stripped of leading and trailing `-`, and is
truncated to 63 characters with a hash suffix. The suffix also disambiguates
two sources that sanitize alike, so a service mounting `a/conf.yml` and
`b/conf.yml` gets two distinct volumes rather than one silently shared.

A ConfigMap is named `<service>-<filename>`, so it is greppable from the
Deployment that mounts it, and generated per service: one file mounted into
two services produces two ConfigMaps rather than a shared resource whose
lifetime belongs to neither.

### What cannot be represented is refused, not guessed

Rendering fails, naming the service and the volume string, when the source
is a directory, is outside the project directory, is missing or unreadable,
is not valid UTF-8, or is larger than the 1 MiB ConfigMap limit.

A directory is as often runtime data as configuration, and a ConfigMap is
the wrong answer for the former. Rather than pick, the error says so and
points at the alternatives (mount the files individually, or declare a named
volume).

The project directory is the boundary because content inside it is part of
the composition; an arbitrary host path is a property of the machine that
happened to run `render`. The Nix store is the exception — it is readable,
content-addressed and deterministic, so `"${configFile}:/etc/app.yml"`, and
a project file that is a symlink into the store, are both representable.

### Secret material is refused, not warned about

The content reaches the manifest verbatim, so a bind-mounted private key
would become plain text in a ConfigMap — and in version control wherever the
rendered output is committed. v0.4.0 reported that as a warning. That was too
weak: a warning does not reach the exit status, so a CI job that renders and
commits does the wrong thing silently, and the remedy afterwards is a history
rewrite rather than deleting a file.

A file carrying a PEM private key header now fails the render, under the same
policy shape as an unrepresentable mount (`--secret-material=configmap`
restores the old behaviour). Only PEM keys are detected: a token in a YAML
config is indistinguishable from configuration, so this is a backstop for the
recognisable case, not a guarantee.

The refusals of both kinds are collected together and reported in one run,
and the error names only the override flags that actually apply to what was
refused.

### Secret material is referenced, not generated

`x-nix-compose.secretMounts` names a mount whose content the cluster
supplies. It renders as `volumes[].secret.secretName` plus the usual
`subPath`, and emits no `Secret` — the file is never read.

Generating a `Secret` from the file was the shape originally requested, and
it does not solve the problem that prompted it. A Kubernetes Secret is not
encrypted: `stringData` is plaintext and `data` is base64. Rendering one
writes the key into the same committed file under a different `kind:`, while
suppressing the refusal that currently keeps it out. The requester would have
ended up worse off than before.

Referencing keeps secret material out of rendered output entirely, which is
what makes the output safe to commit, and it is less machinery: no read, no
encoding, no manifest. What the cluster does about the Secret — sops,
sealed-secrets, external-secrets — stays that cluster's business, which is
where it belongs.

Because the file is not read, the source need not exist where the render
runs. An entry matching no volume is refused rather than ignored: silently
ignoring it fails open, rendering as content the mount it was meant to cover.

Generating a Secret remains possible as a later opt-in, and would have to be
documented as writing the material into the manifest.

### The refusal is a policy, not a law

`--unrepresentable-mounts=empty-dir` (`RenderOptions.UnrepresentableMounts`)
restores the old behaviour, with one warning per mount naming the service,
the volume and the reason. A consumer with dozens of affected mounts can
render while it migrates, and still sees the list of what needs work.

The default is `error`, because the failure this policy guards against is
otherwise invisible until a container starts.

## Consequences

- `k8s.Convert` returns `(*Result, error)`; `Result` carries the manifests
  and the warnings. Callers that ignored the possibility of failure have to
  handle it.
- The K8s target now emits ConfigMaps, so `kustomization.yaml` lists them.
- A project that renders today and has an unrepresentable bind mount will
  fail to render until it is fixed or the policy is relaxed. That is the
  intended behaviour: those manifests never worked.
- `render` is no longer reproducible from the composition alone — it reads
  project files. Two checkouts with different file content render different
  ConfigMaps, which is the point.
- File content lands in the rendered manifest verbatim, so a bind-mounted
  private key becomes plain text in a ConfigMap — and in version control
  where the output is committed. A PEM private key header is warned about;
  nothing else is detectable without guessing. Under the previous `emptyDir`
  behaviour no content was emitted at all, so this is a new exposure and the
  warning is the mitigation.

## Alternatives Considered

1. **Sanitize the name and keep `emptyDir`.** Rejected outright: it turns a
   failed `kubectl apply` into a container that starts and then misbehaves.

2. **Emit `hostPath` volumes.** Faithful to the compose semantics and wrong
   for a cluster: it ties a Deployment to a path on whichever node the pod
   lands on, and is exactly the pattern cluster policy tends to forbid.

3. **A ConfigMap per bind-mounted directory.** Deferred rather than
   rejected. It is well-defined for a directory of configuration and wrong
   for a data directory, and there is nothing in a compose volume string
   that distinguishes them. `:ro` was considered as the signal and rejected:
   too many compose files omit it for it to mean anything.

4. **Generate a kustomize `configMapGenerator` instead of the ConfigMap.**
   It would keep the file content out of the rendered YAML, but only works
   for the directory output mode, not for the stdout stream, and makes the
   manifests depend on kustomize rather than being applyable as they stand.
