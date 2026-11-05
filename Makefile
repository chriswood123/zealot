BINARY   := zealot
PKG      := github.com/chriswood123/zealot
BATS_DIR := e2e/.bats
BATS     := $(BATS_DIR)/bin/bats
BATS_VERSION := v1.11.0
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
IMAGE    ?= zealot

.PHONY: build test e2e vet run clean docker docker-smoke

build:
	go build -ldflags "-X $(PKG)/internal/metrics.Version=$(VERSION)" -o bin/$(BINARY) ./cmd/zealot

test:
	go test ./...

# End-to-end tests exercising examples/ via bats. bats-core is vendored into
# e2e/.bats (gitignored) on first run.
e2e: build $(BATS)
	$(BATS) e2e/*.bats

$(BATS):
	git clone --depth 1 --branch $(BATS_VERSION) \
		https://github.com/bats-core/bats-core.git $(BATS_DIR)

vet:
	go vet ./...

run: build
	./bin/$(BINARY) serve --config examples/node.yaml

# Container image (multi-stage → distroless static binary).
docker:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE):latest .

# Smoke-test the image: `version` prints, then a container serves the example
# node config (bind-mounted, as ConfigMaps are in k8s) and answers requests.
docker-smoke: docker
	docker run --rm $(IMAGE):latest version
	@cid=$$(docker run -d --rm -p 18099:8080 \
		-v "$(CURDIR)/examples/node.yaml:/node.yaml:ro" \
		$(IMAGE):latest serve --config /node.yaml); \
	trap "docker stop $$cid >/dev/null 2>&1 || true" EXIT; \
	ready=0; \
	for _ in $$(seq 1 50); do \
		curl -sf localhost:18099/healthz >/dev/null 2>&1 && { ready=1; break; }; \
		sleep 0.2; \
	done; \
	[ "$$ready" = 1 ] || { echo "container did not become ready" >&2; exit 1; }; \
	curl -sf localhost:18099/ | grep -q '"service":"frontend"' \
		&& echo "docker smoke OK"

clean:
	rm -rf bin $(BATS_DIR)
