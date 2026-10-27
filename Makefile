SHELL := /bin/bash
.DEFAULT_GOAL := help

# Image tag = git commit SHA (never `latest`); "-dirty" marks builds with uncommitted changes.
VERSION   := $(shell git describe --always --dirty --abbrev=12 2>/dev/null || echo dev)
API_IMAGE := taskflow-api:$(VERSION)
COMPOSE   := API_TAG=$(VERSION) docker compose

.PHONY: help bootstrap fmt vet test api-image env up down ps logs migrate-status psql

help: ## List available targets
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

bootstrap: ## Verify workstation tools against .tool-versions (read-only)
	@./scripts/bootstrap.sh

fmt: ## Fail if any Go file is not gofmt-formatted
	@out="$$(gofmt -l api)"; if [[ -n "$$out" ]]; then echo "not gofmt-formatted:"; echo "$$out"; exit 1; fi

vet: ## Run go vet static analysis
	cd api && go vet ./...

test: ## Run unit tests
	cd api && go test -count=1 ./...

api-image: ## Build the API image tagged with the git SHA
	docker build --build-arg VERSION=$(VERSION) -t $(API_IMAGE) api
	@docker image ls $(API_IMAGE) --format 'built {{.Repository}}:{{.Tag}} ({{.Size}})'

env: ## Create .env from .env.example if it does not exist
	@if [[ -f .env ]]; then echo ".env exists, leaving it untouched"; else cp .env.example .env && echo "created .env from .env.example"; fi

up: env ## Build and start the stack (postgres -> migrate -> api)
	$(COMPOSE) up -d --build --wait

down: ## Stop the stack; keeps the database volume
	$(COMPOSE) down

ps: ## Show stack containers (including exited one-shot jobs)
	$(COMPOSE) ps -a

logs: ## Follow logs of all services
	$(COMPOSE) logs -f

migrate-status: ## Show applied/pending migrations
	$(COMPOSE) run --rm migrate migrate status

psql: ## Open psql inside the postgres container
	$(COMPOSE) exec postgres sh -c 'psql -U "$$POSTGRES_USER" -d "$$POSTGRES_DB"'
