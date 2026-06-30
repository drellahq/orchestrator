.DEFAULT_GOAL := help

.PHONY: help build check test

BINARY := orchestrator

help: ## Show this help
	@grep -E '^[a-zA-Z0-9_-]+:.*## ' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

build: ## Build the orchestrator binary
	go build -o $(BINARY) ./cmd/orchestrator

check: ## Check the local setup is complete
	@test -x ./$(BINARY) || $(MAKE) build
	./$(BINARY) check-setup

test: ## Run unit tests
	go test ./...
