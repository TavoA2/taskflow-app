# taskflow-app

Application repository for **TaskFlow**: a deliberately simple task/request manager used to exercise
deployment, persistence, monitoring and recovery on Kubernetes.

Deployment, infrastructure and all architecture decisions live in
[`taskflow-platform`](https://github.com/TavoA2/taskflow-platform) (see its `docs/adr/`, starting with
ADR-0001 for why the code and platform are split).

## Components

| Component | Description | Status |
|-----------|-------------|--------|
| API | Go REST service: task CRUD, `/healthz`, `/readyz`, `/metrics` | CRUD, `/healthz`, graceful shutdown, image: Implemented + Tested (steps 1.1-1.3). `/readyz`, `/metrics`: Planned (1.4) |
| Frontend | Static site served by nginx | Planned (Phase 1) |
| Database | PostgreSQL 18 with goose migrations run as a one-shot `migrate` step | Implemented + Tested (step 1.2) |
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

## Local stack (docker compose)

```bash
make up              # creates .env from .env.example if missing; postgres -> migrate -> api
make ps              # `migrate` should show "Exited (0)"
make migrate-status  # applied / pending migrations
make psql            # SQL shell inside the postgres container
make down            # stop; the pgdata volume (your data) is kept
```

- The API listens on `127.0.0.1:8080` (`API_PORT` in `.env` to change). PostgreSQL is not published to the host.
- Migrations live in `api/internal/migrations/` and are embedded in the binary; `migrate` is the same
  image as `api` with a different command. A failed migration stops the stack before the API starts.
- `docker compose down -v` **deletes the database volume**; use it only to reset local data deliberately.

## API endpoints

| Method and path | Success | Errors |
|-----------------|---------|--------|
| `GET /api/v1/tasks?limit=20&offset=0` | 200 `{"items":[...],"limit":20,"offset":0}` newest first; `limit` 1-100 | 400 |
| `POST /api/v1/tasks` `{"title","description"?,"status"?}` | 201 + `Location` header | 400 |
| `GET /api/v1/tasks/{id}` | 200 | 400 (not a UUID), 404 |
| `PATCH /api/v1/tasks/{id}` (any subset of fields) | 200 | 400, 404 |
| `DELETE /api/v1/tasks/{id}` | 204 | 400, 404 |
| `GET /healthz` | 200 (liveness; never checks the database) | |

Rules: `title` 1-200 characters (trimmed), `description` up to 2000, `status` one of `todo`, `in_progress`, `done`
(default `todo`). Unknown JSON fields are rejected. A JSON `null` in a PATCH is treated as "not provided".

Every error has the same shape; internal errors never expose details (they are logged server-side):

```json
{"error": "validation failed", "code": "validation_failed", "fields": {"title": "is required"}}
```

Codes: `validation_failed`, `bad_request`, `not_found`, `internal`.

## API configuration

All configuration comes from environment variables.

| Variable | Default | Purpose |
|----------|---------|---------|
| `HTTP_ADDR` | `:8080` | Listen address |
| `DATABASE_URL` | (none) | PostgreSQL URL; required by `serve` and `migrate`. Contains credentials: never logged. Pool tuning via URL params (e.g. `pool_max_conns`) |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `DRAIN_DELAY` | `5s` | Time to keep serving after SIGTERM while traffic is moved away |
| `SHUTDOWN_TIMEOUT` | `15s` | Time allowed for in-flight requests after draining |

`DRAIN_DELAY + SHUTDOWN_TIMEOUT` must stay below the orchestrator's grace period
(Kubernetes `terminationGracePeriodSeconds`, default 30s; compose `stop_grace_period`, default 10s).
