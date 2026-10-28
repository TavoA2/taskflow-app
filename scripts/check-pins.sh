#!/usr/bin/env bash
# Fails if pinned references that must stay identical have drifted apart.
# Dependabot updates the PostgreSQL image in compose.yaml but cannot update the Go constant used by
# the integration tests, so this check makes such a Dependabot PR fail until both are aligned.
set -euo pipefail
cd "$(dirname "$0")/.."

compose_pg=$(sed -n 's/^ *image: \(postgres:.*\)$/\1/p' compose.yaml)
testdb_pg=$(sed -n 's/^const defaultImage = "\(.*\)"$/\1/p' api/internal/testdb/testdb.go)

if [[ -z "$compose_pg" || -z "$testdb_pg" ]]; then
  echo "check-pins: could not read the PostgreSQL image (compose='$compose_pg' testdb='$testdb_pg')" >&2
  exit 1
fi
if [[ "$compose_pg" != "$testdb_pg" ]]; then
  echo "check-pins: PostgreSQL image differs between compose.yaml and api/internal/testdb/testdb.go" >&2
  echo "  compose.yaml: $compose_pg" >&2
  echo "  testdb.go:    $testdb_pg" >&2
  exit 1
fi
echo "check-pins: PostgreSQL image consistent ($compose_pg)"
