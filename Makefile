SHELL := /bin/bash
.DEFAULT_GOAL := help

# Image tag = git commit SHA (never `latest`); "-dirty" marks builds with uncommitted changes.
VERSION   := $(shell git describe --always --dirty --abbrev=12 2>/dev/null || echo dev)
API_IMAGE := taskflow-api:$(VERSION)
COMPOSE   := API_TAG=$(VERSION) docker compose
BASE_URL  ?= http://127.0.0.1:8080
# Linters run from pinned container images, so local runs and CI use exactly the same versions.
GITLEAKS_IMAGE   := ghcr.io/gitleaks/gitleaks:v8.30.1
GOLANGCI_IMAGE   := golangci/golangci-lint:v2.15.0
HADOLINT_IMAGE   := hadolint/hadolint:v2.15.1
SHELLCHECK_IMAGE := koalaman/shellcheck:v0.11.0
ACTIONLINT_IMAGE := rhysd/actionlint:1.7.12
# Reuse the host Go module and build caches inside the linter container (no re-download every run).
# GOPATH must be writable by the non-root user so Go can fetch the exact toolchain in go.mod (and its
# checksum database entries) when the image's Go is older. The module cache is mounted outside GOPATH:
# a mount inside it would make Docker create GOPATH's parent directories as root.
GO_CACHES := -e GOPATH=/tmp/gopath -e GOMODCACHE=/tmp/gomodcache -v "$$(go env GOMODCACHE)":/tmp/gomodcache \
             -e GOCACHE=/tmp/gocache -v "$$(go env GOCACHE)":/tmp/gocache \
             -e GOLANGCI_LINT_CACHE=/tmp/gocache/golangci-lint -e HOME=/tmp

.PHONY: help bootstrap fmt vet lint test test-race test-integration api-image env up down ps logs migrate-status psql smoke persistence-test secrets-scan

help: ## List available targets
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

bootstrap: ## Verify workstation tools against .tool-versions (read-only)
	@./scripts/bootstrap.sh

secrets-scan: ## Scan the full git history for secrets with gitleaks (read-only mount)
	docker run --rm -u $$(id -u):$$(id -g) -v "$(CURDIR)":/repo:ro $(GITLEAKS_IMAGE) git /repo --redact --no-banner

fmt: ## Fail if any Go file is not gofmt-formatted
	@out="$$(gofmt -l api)"; if [[ -n "$$out" ]]; then echo "not gofmt-formatted:"; echo "$$out"; exit 1; fi

vet: ## Run go vet static analysis (including integration-tagged files)
	cd api && go vet ./... && go vet -tags integration ./...

lint: ## golangci-lint (Go), hadolint (Dockerfiles), shellcheck (scripts), actionlint (workflows)
	docker run --rm -u $$(id -u):$$(id -g) $(GO_CACHES) -v "$(CURDIR)":/src -w /src/api $(GOLANGCI_IMAGE) golangci-lint run ./...
	docker run --rm -v "$(CURDIR)":/src:ro -w /src $(HADOLINT_IMAGE) hadolint api/Dockerfile frontend/Dockerfile
	docker run --rm -v "$(CURDIR)":/src:ro -w /src $(SHELLCHECK_IMAGE) scripts/*.sh
	docker run --rm -v "$(CURDIR)":/src:ro -w /src $(ACTIONLINT_IMAGE) -no-color

test: ## Run unit tests (fast, no Docker)
	cd api && go test -count=1 ./...

test-integration: ## Run unit + integration tests against a real PostgreSQL container (needs Docker)
	cd api && go test -tags integration -count=1 -timeout 5m ./...

test-race: ## Unit + integration tests with the race detector (needs Docker and a C compiler, e.g. gcc)
	cd api && CGO_ENABLED=1 go test -race -tags integration -count=1 -timeout 10m ./...

api-image: ## Build the API image tagged with the git SHA
	docker build --build-arg VERSION=$(VERSION) -t $(API_IMAGE) api
	@docker image ls $(API_IMAGE) --format 'built {{.Repository}}:{{.Tag}} ({{.Size}})'

env: ## Create .env from .env.example if it does not exist
	@if [[ -f .env ]]; then echo ".env exists, leaving it untouched"; else cp .env.example .env && echo "created .env from .env.example"; fi

up: env ## Build and start the stack (postgres -> migrate -> api -> frontend); app on http://127.0.0.1:8080
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

smoke: ## Smoke-test a running stack through the frontend (BASE_URL=http://127.0.0.1:8080)
	./scripts/smoke-test.sh $(BASE_URL)

persistence-test: ## Prove data survives a postgres restart and compose down/up (restarts the stack)
	API_TAG=$(VERSION) ./scripts/persistence-test.sh $(BASE_URL)
