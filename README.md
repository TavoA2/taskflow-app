# taskflow-app

Application repository for **TaskFlow**: a deliberately simple task/request manager used to exercise
deployment, persistence, monitoring and recovery on Kubernetes.

Deployment, infrastructure and all architecture decisions live in
[`taskflow-platform`](https://github.com/TavoA2/taskflow-platform) (see its `docs/adr/`, starting with
ADR-0001 for why the code and platform are split).

## Components

| Component | Description | Status |
|-----------|-------------|--------|
| API | Go REST service: task CRUD, `/healthz`, `/readyz`, `/metrics` | `/healthz`, graceful shutdown and image Implemented + Tested (step 1.1); rest Planned |
| Frontend | Static site served by nginx | Planned (Phase 1) |
| Database | PostgreSQL with versioned migrations | Planned (Phase 1) |
| App CI | Lint, test, build, Trivy scan + SBOM, push to GHCR tagged by commit SHA | Planned (Phase 2) |

## Status

| Phase | Scope | Status |
|-------|-------|--------|
| 0 | Repo skeleton, pinned tools, `make bootstrap` | Implemented, Tested (evidence: `taskflow-platform/docs/test-results/phase-0-workstation.md`) |
| 1 | API + frontend + Postgres via docker compose, migrations, tests | Planned |
| 2 | App CI pipeline | Planned |

## Prerequisites

Windows 11 + WSL2 (Ubuntu) + Docker Desktop with WSL integration. Work inside `~/code/...`.

| Tool | Version | Install (Ubuntu) |
|------|---------|------------------|
| docker | managed by Docker Desktop | Docker Desktop > Settings > Resources > WSL integration |
| git, make, curl, jq | distro packages | `sudo apt-get install -y git make curl jq` |
| Go | see `.tool-versions` | `curl -fLo /tmp/go.tgz https://go.dev/dl/go1.27.3.linux-amd64.tar.gz && sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf /tmp/go.tgz`, then add `/usr/local/go/bin` to `PATH` |

## Usage

```bash
make help        # list targets
make bootstrap   # verify the workstation; exit code 0 = READY
make fmt vet     # formatting and static analysis
make test        # unit tests
make api-image   # build taskflow-api:<git sha>[-dirty]
```

## API configuration

All configuration comes from environment variables.

| Variable | Default | Purpose |
|----------|---------|---------|
| `HTTP_ADDR` | `:8080` | Listen address |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `DRAIN_DELAY` | `5s` | Time to keep serving after SIGTERM while traffic is moved away |
| `SHUTDOWN_TIMEOUT` | `15s` | Time allowed for in-flight requests after draining |

`DRAIN_DELAY + SHUTDOWN_TIMEOUT` must stay below the orchestrator's grace period
(Kubernetes `terminationGracePeriodSeconds`, default 30s; compose `stop_grace_period`, default 10s).
