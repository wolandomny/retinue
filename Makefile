.PHONY: build test lint fmt vet coverage cover cover-check clean install verify

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

# Minimum total statement coverage enforced by `cover-check` (and CI).
# Ratchet this up over time as coverage improves; never lower it.
COVER_THRESHOLD ?= 45.0

# cover: run tests with a coverage profile and print the total coverage line.
cover:
	go test ./... -coverprofile=cover.out -covermode=atomic
	go tool cover -func=cover.out | tail -1

# cover-check: like `cover`, but fail if total coverage drops below COVER_THRESHOLD.
cover-check: cover
	@total=$$(go tool cover -func=cover.out | tail -1 | awk '{print $$3}' | tr -d '%'); \
	echo "Total coverage: $$total% (threshold: $(COVER_THRESHOLD)%)"; \
	awk -v t="$$total" -v min="$(COVER_THRESHOLD)" 'BEGIN { if (t+0 < min+0) { exit 1 } }' || { \
		echo "FAIL: coverage $$total% is below threshold $(COVER_THRESHOLD)%"; exit 1; }

clean:
	rm -rf $(BINDIR) coverage.out coverage.html cover.out

install: build
	mkdir -p $(shell go env GOPATH)/bin
	rm -f $(shell go env GOPATH)/bin/$(BINARY)
	cp $(BINDIR)/$(BINARY) $(shell go env GOPATH)/bin/$(BINARY)

verify: build vet test
