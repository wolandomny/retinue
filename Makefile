.PHONY: build test lint fmt vet coverage clean install

BINARY := retinue
BINDIR := bin
GOFLAGS := -trimpath

# Version information
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X github.com/wolandomny/retinue/internal/cli.version=$(VERSION) \
           -X github.com/wolandomny/retinue/internal/cli.commit=$(COMMIT) \
           -X github.com/wolandomny/retinue/internal/cli.date=$(DATE)

build:
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINDIR)/$(BINARY) ./cmd/retinue

test:
	go test -race -count=1 ./...

lint:
	golangci-lint run ./...

fmt:
	gofumpt -w .
	goimports -w .

vet:
	go vet ./...

coverage:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

clean:
	rm -rf $(BINDIR) coverage.out coverage.html

install: build
	mkdir -p $(shell go env GOPATH)/bin
	rm -f $(shell go env GOPATH)/bin/$(BINARY)
	cp $(BINDIR)/$(BINARY) $(shell go env GOPATH)/bin/$(BINARY)
