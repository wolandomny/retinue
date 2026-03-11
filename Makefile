.PHONY: build test lint fmt vet coverage clean install

BINARY := retinue
BINDIR := bin
GOFLAGS := -trimpath

build:
	go build $(GOFLAGS) -o $(BINDIR)/$(BINARY) ./cmd/retinue

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
	cp $(BINDIR)/$(BINARY) $(shell go env GOPATH)/bin/$(BINARY)
