.PHONY: build test vet fmt fmt-check lint lint-new revive verify tidy clean install hooks e2e lint-tools

CMD := ./cmd/skill-up
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
VERSION := $(shell echo $(VERSION) | sed 's/^v//')
LDFLAGS := -ldflags "-X main.version=$(VERSION)"
# GOPROXY and GOPRIVATE defaults; override locally via environment if needed
# (e.g. `GOPROXY=https://goproxy.cn,direct make build` for mainland China users)
GOPROXY   ?= https://proxy.golang.org,direct
GOPRIVATE ?=
TOOLS_BIN := $(CURDIR)/.tools/bin
GOLANGCI_LINT := $(TOOLS_BIN)/golangci-lint
REVIVE := $(TOOLS_BIN)/revive
GO_VERSION := 1.25
GOLANGCI_LINT_VERSION := v2.11.4
REVIVE_VERSION := v1.10.0
GOBIN := $(TOOLS_BIN)
GOCACHE := $(CURDIR)/.cache/go-build
GOLANGCI_LINT_CACHE := $(CURDIR)/.cache/golangci-lint
GOFLAGS := -buildvcs=false
PATH := $(TOOLS_BIN):$(PATH)

export GOPRIVATE GOPROXY GOBIN GOCACHE GOLANGCI_LINT_CACHE GOFLAGS PATH

build: hooks
	go build $(LDFLAGS) -o bin/skill-up ./cmd/skill-up

test:
	go test -race ./...

vet:
	go vet ./...

fmt:
	go fmt ./...

# Fails if any .go file is not gofmt-formatted.
fmt-check:
	@files=$$(gofmt -l .); \
	if [ -n "$$files" ]; then \
		echo "These files need gofmt (run: make fmt):"; \
		echo "$$files"; \
		exit 1; \
	fi

lint-tools: $(GOLANGCI_LINT) $(REVIVE)

$(GOLANGCI_LINT):
	@mkdir -p "$(TOOLS_BIN)"
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

$(REVIVE):
	@mkdir -p "$(TOOLS_BIN)"
	go install github.com/mgechev/revive@$(REVIVE_VERSION)

lint: lint-tools
	@mkdir -p "$(GOCACHE)" "$(GOLANGCI_LINT_CACHE)"
	$(GOLANGCI_LINT) run ./...

lint-new: lint-tools
	@mkdir -p "$(GOCACHE)" "$(GOLANGCI_LINT_CACHE)"
	$(GOLANGCI_LINT) run --new-from-rev "$${QUALITY_BASE_REF:-origin/master}" ./...

# Run revive linter with project config (used for incremental scan).
revive: lint-tools
	$(REVIVE) -config revive.toml ./...

# Format check, vet, revive, and full golangci-lint (matches pre-commit and documented behavior).
# Use `make lint-new` for incremental checks against origin/master in CI.
verify: fmt-check vet revive lint

tidy:
	go mod tidy

clean:
	rm -rf bin/

install: hooks
	go install $(LDFLAGS) $(CMD)

# Point Git at this repo's hooks (run once per clone).
# Called automatically by `make build`; safe to run multiple times.
hooks:
	@if [ "$$(git config core.hooksPath)" != ".githooks" ]; then \
		git config core.hooksPath .githooks; \
		echo "git hooks installed (.githooks)"; \
	fi

# Run e2e tests
e2e:
	go test -tags e2e -v ./e2e

# Note: multi-platform builds, checksums and archives for releases are produced
# by GoReleaser (see .goreleaser.yaml and .github/workflows/release.yml).
