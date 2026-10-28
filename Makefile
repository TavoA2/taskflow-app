SHELL := /bin/bash
.DEFAULT_GOAL := help

# Image tag = git commit SHA (never `latest`); "-dirty" marks builds with uncommitted changes.
VERSION   := $(shell git describe --always --dirty --abbrev=12 2>/dev/null || echo dev)
API_IMAGE      := taskflow-api:$(VERSION)
FRONTEND_IMAGE := taskflow-frontend:$(VERSION)
COMPOSE   := API_TAG=$(VERSION) docker compose
BUILD_DIR := build
# Standard OCI labels: "source" links the GHCR package to this repository; "revision" records the exact
# commit. "created" is the commit time (not the build time) so the label is reproducible per commit.
# url/version/description are set explicitly: otherwise the frontend inherits the nginx base image's.
OCI_LABELS := --label org.opencontainers.image.source=https://github.com/TavoA2/taskflow-app \
              --label org.opencontainers.image.url=https://github.com/TavoA2/taskflow-app \
              --label org.opencontainers.image.revision=$(shell git rev-parse HEAD 2>/dev/null) \
              --label org.opencontainers.image.version=$(VERSION) \
              --label org.opencontainers.image.created=$(shell git log -1 --format=%cI 2>/dev/null) \
              --label org.opencontainers.image.licenses=MIT
BASE_URL  ?= http://127.0.0.1:8080
GITHUB_REPO := TavoA2/taskflow-app
# Linters run from pinned container images, so local runs and CI use exactly the same versions.
GITLEAKS_IMAGE   := ghcr.io/gitleaks/gitleaks:v8.30.1
GOLANGCI_IMAGE   := golangci/golangci-lint:v2.15.0
HADOLINT_IMAGE   := hadolint/hadolint:v2.15.1
SHELLCHECK_IMAGE := koalaman/shellcheck:v0.11.0
ACTIONLINT_IMAGE := rhysd/actionlint:1.7.12
# The scanner sees every layer of our images: pinned by digest too (a tag can be re-pointed, a digest cannot).
TRIVY_IMAGE      := aquasec/trivy:0.76.1@sha256:790fd66d1697395d8693d1acaa1ea702406a20f109b6fc7bac8db623eca6c06c
GOVULNCHECK_VERSION := v1.9.0
# Trivy vulnerability DB cache, kept between runs (CI restores it with actions/cache).
TRIVY_CACHE ?= $(HOME)/.cache/trivy
# Images are scanned from `docker save` archives, so the scanner never gets the Docker socket
# (access to the socket is equivalent to root on the host).
TRIVY := docker run --rm -u $$(id -u):$$(id -g) -e HOME=/tmp -v "$(TRIVY_CACHE)":/cache \
         -v "$(CURDIR)":/src:ro -v "$(CURDIR)/$(BUILD_DIR)":/build $(TRIVY_IMAGE) --cache-dir /cache
# Reuse the host Go module and build caches inside the linter container (no re-download every run).
# GOPATH must be writable by the non-root user so Go can fetch the exact toolchain in go.mod (and its
# checksum database entries) when the image's Go is older. The module cache is mounted outside GOPATH:
# a mount inside it would make Docker create GOPATH's parent directories as root.
GO_CACHES := -e GOPATH=/tmp/gopath -e GOMODCACHE=/tmp/gomodcache -v "$$(go env GOMODCACHE)":/tmp/gomodcache \
             -e GOCACHE=/tmp/gocache -v "$$(go env GOCACHE)":/tmp/gocache \
             -e GOLANGCI_LINT_CACHE=/tmp/gocache/golangci-lint -e HOME=/tmp

.PHONY: help bootstrap ruleset ruleset-check fmt vet lint test test-race test-integration images image-archives vulncheck scan sbom env up down ps logs migrate-status psql smoke persistence-test secrets-scan

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

lint: ## golangci-lint, hadolint, shellcheck, actionlint, and consistency of pinned images
	docker run --rm -u $$(id -u):$$(id -g) $(GO_CACHES) -v "$(CURDIR)":/src -w /src/api $(GOLANGCI_IMAGE) golangci-lint run ./...
	docker run --rm -v "$(CURDIR)":/src:ro -w /src $(HADOLINT_IMAGE) hadolint api/Dockerfile frontend/Dockerfile
	docker run --rm -v "$(CURDIR)":/src:ro -w /src $(SHELLCHECK_IMAGE) scripts/*.sh
	docker run --rm -v "$(CURDIR)":/src:ro -w /src $(ACTIONLINT_IMAGE) -no-color
	./scripts/check-pins.sh
	@# Scripts must be committed as executable (mode 100755); editors on other filesystems can drop the bit.
	@bad=$$(git ls-files -s 'scripts/*.sh' | awk '$$1 != "100755" {print $$4}'); \
	  if [ -n "$$bad" ]; then echo "not executable in git (git update-index --chmod=+x): $$bad"; exit 1; fi; \
	  echo "scripts executable in git: OK"

ruleset: ## Create or update the main-branch ruleset on GitHub from .github/rulesets/main.json (admin)
	./scripts/apply-ruleset.sh $(GITHUB_REPO) .github/rulesets/main.json

ruleset-check: ## Fail if the live ruleset on GitHub differs from .github/rulesets/main.json
	./scripts/apply-ruleset.sh $(GITHUB_REPO) .github/rulesets/main.json --check

test: ## Run unit tests (fast, no Docker)
	cd api && go test -count=1 ./...

test-integration: ## Run unit + integration tests against a real PostgreSQL container (needs Docker)
	cd api && go test -tags integration -count=1 -timeout 5m ./...

test-race: ## Unit + integration tests with the race detector (needs Docker and a C compiler, e.g. gcc)
	cd api && CGO_ENABLED=1 go test -race -tags integration -count=1 -timeout 10m ./...

images: ## Build the API and frontend images tagged with the git SHA
	docker build --build-arg VERSION=$(VERSION) $(OCI_LABELS) --label org.opencontainers.image.title=taskflow-api \
	  --label "org.opencontainers.image.description=TaskFlow REST API (Go)" -t $(API_IMAGE) api
	docker build $(OCI_LABELS) --label org.opencontainers.image.title=taskflow-frontend \
	  --label "org.opencontainers.image.description=TaskFlow web UI served by nginx" -t $(FRONTEND_IMAGE) frontend

vulncheck: ## govulncheck: known Go vulnerabilities in code paths the API actually calls
	cd api && go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

image-archives: # (internal) export the built images for the scanner
	@mkdir -p $(BUILD_DIR) "$(TRIVY_CACHE)"
	docker save -o $(BUILD_DIR)/api.tar $(API_IMAGE)
	docker save -o $(BUILD_DIR)/frontend.tar $(FRONTEND_IMAGE)

scan: image-archives ## Trivy: report HIGH+CRITICAL, then the CVE gate (fail on CRITICAL with a fix available)
	@for img in api frontend; do \
	  echo "=== $$img: HIGH and CRITICAL (report only)"; \
	  $(TRIVY) image --input /build/$$img.tar --scanners vuln --severity HIGH,CRITICAL --exit-code 0 --no-progress || exit 1; \
	done
	@status=0; for img in api frontend; do \
	  echo "=== $$img: CVE gate (CRITICAL with a fix available, minus .trivyignore.yaml exceptions)"; \
	  $(TRIVY) image --input /build/$$img.tar --scanners vuln --severity CRITICAL --ignore-unfixed \
	    --ignorefile /src/.trivyignore.yaml --skip-db-update --exit-code 1 --no-progress || status=1; \
	done; exit $$status

sbom: image-archives ## CycloneDX SBOM of each image, written to build/<image>.cdx.json
	@for img in api frontend; do \
	  $(TRIVY) image --input /build/$$img.tar --format cyclonedx --output /build/$$img.cdx.json --no-progress || exit 1; \
	  echo "build/$$img.cdx.json: $$(jq '.components | length' $(BUILD_DIR)/$$img.cdx.json) components"; \
	done

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
