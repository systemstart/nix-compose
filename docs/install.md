# Installing nix-compose

## Binary download (GitHub releases)

Download the latest archive from the
[releases page](https://github.com/systemstart/nix-compose/releases),
extract it, and place the binary on your `$PATH`:

```sh
tar xzf nix-compose_*_linux_amd64.tar.gz   # or _arm64
install -m 755 nix-compose ~/.local/bin/
```

## Nix flake

Add nix-compose as a flake input and use the package output:

```nix
{
  inputs.nix-compose.url = "git+https://github.com/systemstart/nix-compose.git";

  outputs = { self, nix-compose, ... }: {
    # Use directly
    packages.x86_64-linux.default = nix-compose.packages.x86_64-linux.default;

    # Or apply the overlay
    nixpkgs.overlays = [ nix-compose.overlays.default ];
  };
}
```

One-shot run without installing:

```sh
nix run git+https://github.com/systemstart/nix-compose.git -- up -d
```

Build locally from a checkout:

```sh
nix build   # result in ./result/bin/nix-compose
```

## go install

Requires Go 1.26+.

```sh
go install github.com/systemstart/nix-compose/cmd/nix-compose@latest
```

The binary is placed in `$GOPATH/bin` (or `$HOME/go/bin` by default).

## Build from source

```sh
git clone https://github.com/systemstart/nix-compose.git
cd nix-compose

# Option A: Nix dev shell
nix develop
make build

# Option B: Plain Go
go build -o nix-compose ./cmd/nix-compose
```

The compiled binary is self-contained — copy it anywhere on your `$PATH`.

## Requirements

Regardless of installation method, you need:

- [Nix](https://nixos.org/) with flakes enabled (`experimental-features = nix-command flakes`)
- A CRI runtime — [containerd](https://containerd.io/) or
  [CRI-O](https://cri-o.io/), with its socket readable by your user
