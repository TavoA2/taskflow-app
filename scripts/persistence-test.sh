#!/usr/bin/env bash
# Persistence test for the docker compose stack: a task must survive
#   1. a restart of the PostgreSQL container, and
#   2. a full `docker compose down` + `up` (containers and network removed; the named volume kept).
#
# Usage: make persistence-test   (or: API_TAG=<tag> scripts/persistence-test.sh [BASE_URL])
# Run from the repository root with the stack already up. Honours COMPOSE_PROJECT_NAME.
# It restarts the stack's containers but never removes volumes. Exit code 0 = data survived both.
set -uo pipefail

BASE="${1:-${BASE_URL:-http://127.0.0.1:8080}}"
API="$BASE/api/v1/tasks"
: "${API_TAG:?API_TAG must be set to the image tag of the running stack (make persistence-test sets it)}"
export API_TAG

failed=0 task_id=""
log() { printf '%s  %s\n' "$(date +%T)" "$*"; }
cleanup() { [[ -n "$task_id" ]] && curl -s -o /dev/null --max-time 10 -X DELETE "$API/$task_id"; }
trap cleanup EXIT

# wait_for_task waits until the task is served through the frontend again and prints it.
wait_for_task() {
  for _ in $(seq 1 60); do
    if body="$(curl -sf --max-time 5 "$API/$task_id")"; then echo "$body"; return 0; fi
    sleep 1
  done
  return 1
}

verify() { # label before-json
  local after
  if ! after="$(wait_for_task)"; then
    log "FAIL  $1: task not reachable within 60s"; failed=1; return
  fi
  if [[ "$(jq -cS . <<<"$after")" == "$(jq -cS . <<<"$2")" ]]; then
    log "PASS  $1: task identical (id, title, description, status, timestamps)"
  else
    log "FAIL  $1: task changed"; echo "  before: $2"; echo "  after:  $after"; failed=1
  fi
}

volume_id() { docker volume inspect "$(docker compose config --format json | jq -r '.volumes.pgdata.name')" --format '{{.Name}} created {{.CreatedAt}}' 2>/dev/null; }

created="$(curl -sf --max-time 10 -X POST -H 'Content-Type: application/json' \
  -d "{\"title\":\"persistence-$(date +%s)\",\"description\":\"must survive restarts\"}" "$API")" \
  || { log "FAIL  could not create the test task (is the stack up?)"; exit 1; }
task_id="$(jq -r .id <<<"$created")"
# Read it back once so "before" has the exact representation the API returns.
before="$(curl -sf --max-time 10 "$API/$task_id")"
log "created task $task_id"
log "volume: $(volume_id)"

log "1/2 restarting the postgres container"
docker compose restart postgres >/dev/null 2>&1
verify "after postgres restart" "$before"

log "2/2 docker compose down (volume kept) and up"
docker compose down >/dev/null 2>&1
log "containers left in project: $(docker compose ps -a -q | wc -l)"
docker compose up -d --wait >/dev/null 2>&1 || log "WARN  compose up --wait reported a problem"
verify "after compose down + up" "$before"
log "volume: $(volume_id)"

echo
if (( failed )); then echo "persistence-test: FAILED"; else echo "persistence-test: PASSED"; fi
exit "$failed"
