SHELL := /bin/bash
.DEFAULT_GOAL := help

.PHONY: help bootstrap

help: ## List available targets
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

bootstrap: ## Verify workstation tools against .tool-versions (read-only)
	@./scripts/bootstrap.sh
