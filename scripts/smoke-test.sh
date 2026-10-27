#!/usr/bin/env bash
# Smoke test for a running TaskFlow, through its public entry point only (the frontend).
# Meant for any environment: local compose now, post-deploy verification on Kubernetes later.
#
# Usage: scripts/smoke-test.sh [BASE_URL]        (default: $BASE_URL or http://127.0.0.1:8080)
# Side effects: creates one task and deletes it, even on failure. Other data is never touched.
# Exit code: 0 if every check passed, 1 otherwise.
set -uo pipefail

BASE="${1:-${BASE_URL:-http://127.0.0.1:8080}}"
API="$BASE/api/v1/tasks"
for tool in curl jq; do command -v "$tool" >/dev/null || { echo "smoke-test: $tool is required" >&2; exit 1; }; done

passed=0 failed=0 task_id=""
check() { # description actual expected
  if [[ "$2" == "$3" ]]; then
    printf '  PASS  %s\n' "$1"; passed=$((passed + 1))
  else
    printf '  FAIL  %s: got %q, want %q\n' "$1" "$2" "$3"; failed=$((failed + 1))
  fi
}
status() { curl -s -o /dev/null -w '%{http_code}' --max-time 10 "$@"; }
header() { curl -s -D - -o /dev/null --max-time 10 "$1" | tr -d '\r' | grep -i "^$2:" | head -1 | cut -d' ' -f2-; }
cleanup() { [[ -n "$task_id" ]] && curl -s -o /dev/null --max-time 10 -X DELETE "$API/$task_id"; }
trap cleanup EXIT

echo "Smoke test against $BASE"

echo "Frontend"
check "GET / returns 200"                     "$(status "$BASE/")" 200
check "page is the TaskFlow UI"               "$(curl -s --max-time 10 "$BASE/" | grep -c '<title>TaskFlow</title>')" 1
check "GET /app.js returns 200"               "$(status "$BASE/app.js")" 200
check "CSP allows only same-origin scripts"   "$(header "$BASE/" Content-Security-Policy | grep -c "script-src 'self';")" 1
check "X-Content-Type-Options is nosniff"     "$(header "$BASE/" X-Content-Type-Options)" nosniff

echo "API through the frontend proxy"
check "list returns 200"                      "$(status "$API")" 200
check "list body has an items array"          "$(curl -s --max-time 10 "$API" | jq -r '.items | type')" array

marker="smoke-$(date +%s)-$RANDOM"
resp="$(curl -s -D /tmp/smoke-headers.$$ --max-time 10 -X POST -H 'Content-Type: application/json' \
  -d "{\"title\":\"$marker\"}" "$API")"
task_id="$(jq -r '.id // empty' <<<"$resp")"
check "create returns 201"                    "$(head -1 /tmp/smoke-headers.$$ | awk '{print $2}')" 201
check "create returns a Location header"      "$(tr -d '\r' </tmp/smoke-headers.$$ | grep -i '^location:' | cut -d' ' -f2)" "/api/v1/tasks/$task_id"
rm -f /tmp/smoke-headers.$$
check "created task has default status todo"  "$(jq -r .status <<<"$resp")" todo
check "get returns the created task"          "$(curl -s --max-time 10 "$API/$task_id" | jq -r .title)" "$marker"
check "patch status to done"                  "$(curl -s --max-time 10 -X PATCH -H 'Content-Type: application/json' -d '{"status":"done"}' "$API/$task_id" | jq -r .status)" "done"
check "invalid input returns 400"             "$(status -X POST -H 'Content-Type: application/json' -d '{"title":""}' "$API")" 400
check "unknown task returns 404"              "$(status "$API/00000000-0000-7000-8000-000000000000")" 404
rid="smoke-rid-$RANDOM"
check "X-Request-ID is propagated end to end" "$(curl -s -D - -o /dev/null --max-time 10 -H "X-Request-ID: $rid" "$API" | tr -d '\r' | grep -i '^x-request-id:' | cut -d' ' -f2)" "$rid"
check "delete returns 204"                    "$(status -X DELETE "$API/$task_id")" 204
check "deleted task is gone"                  "$(status "$API/$task_id")" 404
task_id=""

echo "Internal endpoints are not exposed"
for path in /metrics /readyz /healthz; do
  check "GET $path through the frontend is 404" "$(status "$BASE$path")" 404
done

echo
echo "smoke-test: $passed passed, $failed failed"
(( failed == 0 ))
