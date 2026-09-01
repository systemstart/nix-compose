# 75, lowered from 80 when the compose-CLI backend was removed. That package
# was small, pure and near-fully covered, so deleting it removed more covered
# statements than uncovered ones and the average fell. What remains in pkg/cli
# is dominated by command entry points and CRI polling loops that need a live
# container runtime — those are covered by `make test-integration`, which needs
# a CRI socket and so cannot run on a stock CI runner (docs/running-in-ci.md).
#
# This is a floor to raise, not a target to sink to. Do not lower it again
# without removing tested code to match.
COVERAGE_THRESHOLD := 75
COVERPROFILE := coverage.out
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: sync-nixpins diff-closures build test test-integration lint tag release release-notes release-tag release-tag-preview sign clean fmt proto containerd containerd-setup containerd-clean containerd-exec

# Rewrite pkg/nixpins/pins.go from flake.lock. Run after `nix flake update`;
# Renovate runs it automatically via postUpgradeTasks in renovate.json.
sync-nixpins:
	@scripts/sync-nixpins.sh

# What did the last `nix flake update` actually change? Rebuilds an attribute
# against the nixpkgs revision committed at HEAD and diffs the two closures --
# the lock diff alone only says a revision moved, not whether the move reaches
# anything we build. Evaluation-only unless the derivations differ.
#
# Both Go attributes are reported because the two halves of the repo choose
# independently: nix-package.nix builds with the default `go`, while shell.nix
# derives `go_1_26` from go.mod's directive.
#
# Pass through with ARGS, e.g. ARGS='-i nix-oci -a packages.x86_64-linux.default'.
diff-closures:
	@scripts/diff-closures.sh -p go -p go_1_26 $(ARGS)

proto:
	protoc --go_out=paths=source_relative:. --go-grpc_out=paths=source_relative:. \
		api/orchestrate/v1/orchestrate.proto

build:
	CGO_ENABLED=1 go build -ldflags '$(LDFLAGS)' -o nix-compose ./cmd/nix-compose

test:
	@echo "Running tests (coverage threshold: $(COVERAGE_THRESHOLD)%)..."
	CGO_ENABLED=1 go test -v -race -coverprofile=$(COVERPROFILE) -covermode=atomic ./pkg/...
	@total=$$(go tool cover -func=$(COVERPROFILE) | awk '/^total:/ {gsub(/%/,"",$$NF); print $$NF}'); \
	printf "Total coverage: %s%%\n" "$$total"; \
	if awk "BEGIN{exit !($$total < $(COVERAGE_THRESHOLD))}"; then \
		printf "FAIL: coverage %s%% is below %d%%\n" "$$total" "$(COVERAGE_THRESHOLD)"; \
		exit 1; \
	fi

test-integration: build
	@echo "Running integration tests..."
	go test -tags integration -v -timeout 300s ./test/integration/...

lint:
	golangci-lint run


fmt:
	golangci-lint fmt

# Release notes come from git-cliff (see cliff.toml), not from goreleaser:
# goreleaser's changelog only ever sees commit subjects, so a BREAKING CHANGE
# footer could not reach the release page. --release-notes replaces the body
# entirely, which is why cliff.toml carries the header and footer too.
release: lint test
	@notes="$$(mktemp)"; \
	git-cliff --latest > "$$notes" || { rm -f "$$notes"; exit 1; }; \
	goreleaser release --clean --config .goreleaser.yaml --release-notes "$$notes"; \
	rc=$$?; rm -f "$$notes"; exit $$rc

# Preview the release notes for the current tag without releasing anything.
release-notes:
	@git-cliff --latest

containerd-setup:
	sudo mkdir -p /run/containerd/s /run/containerd/runc
	sudo chown $$USER:$$USER /run/containerd/s /run/containerd/runc
	sudo mkdir -p /etc/systemd/system/user@.service.d
	printf '[Service]\nDelegate=cpu cpuset io memory pids\n' | sudo tee /etc/systemd/system/user@.service.d/delegate.conf >/dev/null
	sudo systemctl daemon-reload
	@echo "Cgroup delegation enabled for rootless containers."
	@echo "You may need to re-login or run: systemctl --user restart dbus"
	@echo "To persist /run dirs across reboots:"
	@echo "  echo 'd /run/containerd/s 0755 $$USER $$USER -' | sudo tee /etc/tmpfiles.d/containerd-rootless.conf"

containerd:
	mkdir -p /tmp/ctrd
	sed "s|@CNI_BIN_DIR@|$$CNI_BIN_DIR|" $(CURDIR)/containerd-rootless.toml > /tmp/ctrd/config.toml
	rootlesskit --net=slirp4netns --copy-up=/etc --copy-up=/run --disable-host-loopback \
		--state-dir=$$XDG_RUNTIME_DIR/rootlesskit-containerd \
		sh -c "mkdir -p /run/containerd/s /run/containerd/runc && exec containerd --config=/tmp/ctrd/config.toml"

containerd-exec:
	@PID=$$(cat $$XDG_RUNTIME_DIR/rootlesskit-containerd/child_pid 2>/dev/null) || \
		{ echo "rootlesskit not running, start with: make containerd"; exit 1; }; \
	nsenter -U --preserve-credentials -m -n -t $$PID env PATH="$$PATH" $(ARGS)

containerd-clean:
	-pkill -f "rootlesskit.*containerd"
	@sleep 1
	-rootlesskit rm -rf /tmp/ctrd-root
	rm -rf /tmp/ctrd /tmp/ctrd-cni

clean:
	rm -f nix-compose $(COVERPROFILE)

# Preview the version `release-tag` would cut, without touching anything.
# gsemver derives it from the conventional-commit history since the last tag,
# and it fetches, so this needs network access to the remote.
release-tag-preview:
	@v="$$(gsemver bump)"; \
	test -n "$$v" || { echo "gsemver produced no version — is the remote reachable?" >&2; exit 1; }; \
	printf "next tag: v%s\n" "$$v"

# Cuts and pushes in one step — run release-tag-preview first if you want to
# see the version before it is public. The tag is signed with `git tag -s`.
# Note that tag.forcesignannotated does NOT achieve this: git gives an explicit
# --annotate/-a precedence over that config, so `git tag -a` produces an
# unsigned tag however the config is set.
#
# The computed version is also written to ./VERSION and committed as part of
# the release, because that file is what nix-package.nix reads. Writing it here
# is what keeps the Nix package, the goreleaser artifact and the tag in
# agreement.
#
# The release notes are goreleaser's job, not this target's: it groups the
# commits since the last tag into the GitHub release body. CHANGELOG.md is
# hand-written and only covers releases worth a paragraph.
release-tag:
	$(eval VERSION := $(shell gsemver bump))
	@test -n "$(VERSION)" || { \
		echo "gsemver produced no version — refusing to tag." >&2; \
		echo "make hides a failing command inside its shell function, and an empty" >&2; \
		echo "version would tag and push a ref literally named v. Check the remote" >&2; \
		echo "is reachable and gsemver is on PATH." >&2; \
		exit 1; \
	}
	@git diff --quiet && git diff --cached --quiet || { \
		echo "working tree is dirty — commit or stash before releasing." >&2; \
		exit 1; \
	}
	@git config --get user.signingkey >/dev/null || { \
		echo "user.signingkey is unset — the signed tag would fail after the" >&2; \
		echo "release commit was already made, leaving a half-cut release." >&2; \
		exit 1; \
	}
	@printf "tagging v%s\n" "$(VERSION)"
	@printf '%s\n' "$(VERSION)" > VERSION
	git add VERSION
	git commit -m "chore: release v$(VERSION)" VERSION
	git tag -s "v$(VERSION)" -m "Release v$(VERSION)"
	git push origin HEAD "v$(VERSION)"

sign:
	@if [ -z "$(IMAGE)" ]; then echo "IMAGE is required"; exit 1; fi
	@if [ -z "$$COSIGN_KEY" ]; then echo "COSIGN_KEY is required"; exit 1; fi
	@REGISTRY_FLAGS=""; \
	if [ -n "$$REGISTRY_USER" ] && [ -n "$$REGISTRY_PASS" ]; then \
		REGISTRY_FLAGS="--registry-username=$$REGISTRY_USER --registry-password=$$REGISTRY_PASS"; \
	fi; \
	echo "$$COSIGN_KEY" > cosign.key; \
	trap 'rm -f cosign.key' EXIT; \
	cosign sign --key cosign.key $$REGISTRY_FLAGS $(IMAGE)

attest:
	@if [ -z "$(IMAGE)" ]; then echo "IMAGE is required"; exit 1; fi
	@if [ -z "$$COSIGN_KEY" ]; then echo "COSIGN_KEY is required"; exit 1; fi
	@if [ ! -f sbom.cdx.json ]; then echo "sbom.cdx.json not found, run 'make sbom' first"; exit 1; fi
	@REGISTRY_FLAGS=""; \
	if [ -n "$$REGISTRY_USER" ] && [ -n "$$REGISTRY_PASS" ]; then \
		REGISTRY_FLAGS="--registry-username=$$REGISTRY_USER --registry-password=$$REGISTRY_PASS"; \
	fi; \
	echo "$$COSIGN_KEY" > cosign.key; \
	trap 'rm -f cosign.key' EXIT; \
	cosign attest --key cosign.key $$REGISTRY_FLAGS --type cyclonedx --predicate sbom.cdx.json $(IMAGE)

