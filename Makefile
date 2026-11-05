BINARY   := zealot
PKG      := github.com/chriswood123/zealot
BATS_DIR := e2e/.bats
BATS     := $(BATS_DIR)/bin/bats
BATS_VERSION := v1.11.0

.PHONY: build test e2e vet run clean

build:
	go build -o bin/$(BINARY) ./cmd/zealot

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

clean:
	rm -rf bin $(BATS_DIR)
