.PHONY: build test lint vet vuln clean run dev version-sync

BINARY = web-researcher-mcp
VERSION ?= $(shell cat VERSION 2>/dev/null || git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS = -ldflags="-s -w -X main.version=$(VERSION)"

build:
	CGO_ENABLED=0 go build $(LDFLAGS) -o $(BINARY) ./cmd/web-researcher-mcp

build-fips:
	GOEXPERIMENT=boringcrypto CGO_ENABLED=0 go build $(LDFLAGS) -o $(BINARY) ./cmd/web-researcher-mcp

test:
	go test ./...

test-race:
	go test -race ./...

test-cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

test-e2e:
	go test -v ./tests/e2e/...

test-bench:
	go test -bench=. -benchmem ./tests/benchmark/

lint:
	golangci-lint run

vet:
	go vet ./...

vuln:
	govulncheck ./...

clean:
	rm -f $(BINARY) coverage.out coverage.html

run: build
	./$(BINARY)

dev:
	air

docker:
	docker build -t $(BINARY):$(VERSION) .

release:
	goreleaser release --snapshot --clean

version-sync:
	bash scripts/sync-version.sh

all: lint vet vuln test build
