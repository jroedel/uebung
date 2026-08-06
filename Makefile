# Übung — der/die/das trainer.
#
# Targets mirror the sibling project's workflow (build / vet / fmt / lint /
# test) so the same habits apply here. This module is stdlib-only, so there is
# no dependency download step and `go test ./...` runs fully offline.

GO        ?= go
BINARY    ?= uebung
CMD       ?= ./cmd/uebung
ADDR      ?= :8080

.DEFAULT_GOAL := help

.PHONY: help
help: ## List targets
	@grep -E '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN{FS=":.*?## "}{printf "  %-18s %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the server binary
	$(GO) build -o $(BINARY) $(CMD)

.PHONY: run
run: ## Run the server (make run ADDR=:9090)
	$(GO) run $(CMD) -addr $(ADDR)

.PHONY: vet
vet: ## go vet the whole module
	$(GO) vet ./...

.PHONY: fmt
fmt: ## Format all Go sources in place
	$(GO) fmt ./...

.PHONY: fmt-check
fmt-check: ## Fail if any Go source is not gofmt-clean
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt needed on:"; echo "$$unformatted"; exit 1; \
	fi

.PHONY: lint
lint: vet fmt-check ## vet + gofmt check

.PHONY: test-unit
test-unit: ## Run unit tests
	$(GO) test ./...

.PHONY: test-integration
test-integration: ## Run tests tagged as integration
	$(GO) test -tags=integration ./...

.PHONY: test
test: test-unit lint ## Full check: unit tests + lint

.PHONY: cover
cover: ## Unit tests with a coverage summary
	$(GO) test -cover ./...

.PHONY: tidy
tidy: ## Tidy go.mod
	$(GO) mod tidy

.PHONY: clean
clean: ## Remove build artifacts
	rm -f $(BINARY)
