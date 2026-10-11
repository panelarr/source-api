# Self-documenting Makefile: `make` or `make help` lists every target with a `## description`.
.DEFAULT_GOAL := help

# Developer tools are pinned here and installed into ./bin by `make setup`,
# so every contributor and CI run uses the same versions.
BUF_VERSION                   := v1.73.0
PROTOC_GEN_GO_VERSION         := v1.36.12
PROTOC_GEN_GO_GRPC_VERSION    := v1.6.2
PROTOC_GEN_CONNECT_GO_VERSION := v1.21.0
PROTOC_GEN_OPENAPI_VERSION    := v0.28.0
GOLANGCI_LINT_VERSION         := v2.14.0
GOVULNCHECK_VERSION           := v1.9.0

BIN      := $(CURDIR)/bin
export PATH := $(BIN):$(PATH)
GO       ?= go
# Build tools with the same Go version as the module, so linters understand its code.
TOOLCHAIN := $(shell $(GO) env GOVERSION)
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -s -w

.PHONY: help setup build conformance run-example test test-short lint fmt fmt-check vet vuln buf-lint breaking gen gen-check clean check

help: ## List targets
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z0-9_.-]+:.*## / {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

setup: ## Install pinned developer tools into ./bin and download modules
	@mkdir -p $(BIN)
	GOTOOLCHAIN=$(TOOLCHAIN) GOBIN=$(BIN) $(GO) install github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION)
	GOTOOLCHAIN=$(TOOLCHAIN) GOBIN=$(BIN) $(GO) install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)
	GOTOOLCHAIN=$(TOOLCHAIN) GOBIN=$(BIN) $(GO) install google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GO_GRPC_VERSION)
	GOTOOLCHAIN=$(TOOLCHAIN) GOBIN=$(BIN) $(GO) install connectrpc.com/connect/cmd/protoc-gen-connect-go@$(PROTOC_GEN_CONNECT_GO_VERSION)
	GOTOOLCHAIN=$(TOOLCHAIN) GOBIN=$(BIN) $(GO) install github.com/sudorandom/protoc-gen-connect-openapi@$(PROTOC_GEN_OPENAPI_VERSION)
	GOTOOLCHAIN=$(TOOLCHAIN) GOBIN=$(BIN) $(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	GOTOOLCHAIN=$(TOOLCHAIN) GOBIN=$(BIN) $(GO) install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	$(GO) mod download

build: ## Build the conformance runner and the example plugin into ./bin
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN)/source-conformance ./cmd/source-conformance
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN)/static-source ./sdk/examples/static-source

conformance: build ## Check a running plugin: make conformance TARGET=host:port [KEY=api-key] [ARGS="--max-sources 5"]
	@test -n "$(TARGET)" || (echo "usage: make conformance TARGET=host:port [KEY=api-key] [ARGS=...]" && exit 2)
	$(BIN)/source-conformance --target "$(TARGET)" --api-key "$(KEY)" $(ARGS)

run-example: build ## Run the example plugin standalone on :8904
	@echo "static-source listening on http://localhost:8904"
	$(BIN)/static-source --listen :8904

test: ## Run all tests with the race detector
	$(GO) test -race -cover ./...

test-short: ## Run the fast tests only
	$(GO) test -short -race ./...

fmt: ## Format Go and proto files
	$(GO) fmt ./...
	$(BIN)/buf format -w

fmt-check: ## Fail if Go or proto files are not formatted
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "not gofmt-ed:"; echo "$$out"; exit 1; fi
	$(BIN)/buf format --diff --exit-code

vet: ## go vet
	$(GO) vet ./...

lint: ## golangci-lint and buf lint
	$(BIN)/golangci-lint run ./...
	$(BIN)/buf lint

buf-lint: ## Lint protos only
	$(BIN)/buf lint

BREAKING_AGAINST ?= .git\#branch=main

breaking: ## Fail on breaking proto changes against main (BREAKING_AGAINST to compare with something else)
	@if git rev-parse --verify -q main >/dev/null && git ls-tree -d --name-only main proto | grep -q proto; then \
		$(BIN)/buf breaking --against '$(BREAKING_AGAINST)'; \
	else echo "main has no protos yet; nothing to compare"; fi

vuln: ## Check dependencies and the toolchain for known vulnerabilities
	$(BIN)/govulncheck ./...

gen: ## Generate code from the protos (Go, gRPC, Connect, OpenAPI)
	$(BIN)/buf generate

gen-check: gen ## Fail if generated code is out of date
	@if [ -n "$$(git status --porcelain -- gen)" ]; then git status --short -- gen; echo "generated code is stale: run make gen and commit"; exit 1; fi

clean: ## Remove build artifacts (keeps installed tools)
	rm -f $(BIN)/source-conformance $(BIN)/static-source
	rm -rf dist coverage.out

check: fmt-check vet lint breaking vuln test ## Format check, lint, breaking-change check, vulnerabilities and tests (run before every report/PR)
