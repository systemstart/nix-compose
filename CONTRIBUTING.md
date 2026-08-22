# Contributing

nix-compose is in public beta. Bug reports are more useful than feature
requests right now, and a report that includes `nix-compose doctor` output
is worth several that do not.

## Before you file a bug

Run `nix-compose doctor`. It checks every environment prerequisite and each
finding carries the fix. A large share of "nix-compose is broken" turns out
to be a missing CNI plugin, an `iptables` the *runtime* cannot see, or a
container log the current user cannot read — all of which doctor names.

Include:

- `nix-compose doctor` output
- `nix-compose ps -a` (it shows exit codes, which the default view hides)
- the `nix-compose.yaml` or flake, reduced to the smallest thing that fails

Note that container logs are written by the runtime, typically
`root:root 0640`, so `nix-compose logs` cannot read them as an ordinary
user. This is expected — see
[docs/limitations.md](docs/limitations.md). `ps -a` still reports state and
exit code.

## Development

```sh
nix develop      # dev shell with the full toolchain
make test        # unit tests
make lint        # golangci-lint + nix fmt check
make build       # produces ./nix-compose
```

Integration tests need a working CRI socket and are behind a build tag:

```sh
go test -tags integration ./test/integration/...
```

`make proto` regenerates the gRPC bindings. Do **not** hand-edit
`api/orchestrate/v1/*.pb.go` — the file descriptor embeds length-prefixed
strings, so a search-and-replace that changes any embedded path silently
corrupts it and fails at init with a slice-bounds panic.

## Pull requests

- Keep the change and its test in the same commit.
- New behaviour needs a test; a bug fix needs a test that fails without it.
- If you change what a user sees, update the docs in the same PR —
  `docs/limitations.md` in particular is meant to stay honest.
- `make lint` and `make test` must pass.

## License

By contributing you agree that your contribution is licensed under
[GPL-3.0](./LICENSE).
