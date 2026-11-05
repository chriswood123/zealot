BINARY := zealot
PKG    := github.com/chriswood123/zealot

.PHONY: build test vet run clean

build:
	go build -o bin/$(BINARY) ./cmd/zealot

test:
	go test ./...

vet:
	go vet ./...

run: build
	./bin/$(BINARY) serve --config examples/node.yaml

clean:
	rm -rf bin
