COVERAGE_THRESHOLD := 80
COVERPROFILE := coverage.out
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: build test test-integration lint tag release clean proto containerd containerd-setup containerd-clean containerd-exec

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

release: lint test
	goreleaser release --clean --config .goreleaser.yaml

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

release-tag:
	$(eval VERSION := $(shell gsemver bump))
	git tag -a "v$(VERSION)" -m "Release v$(VERSION)"
	git push origin "v$(VERSION)"

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

