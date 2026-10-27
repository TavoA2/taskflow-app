#!/usr/bin/env bash
# Verifies the local workstation against .tool-versions. Read-only: installs nothing.
#   Pinned tools (.tool-versions): same major.minor required; patch drift is a warning.
#   Required tools (REQUIRED_TOOLS): must be present; versions are not pinned.
# Exit code: 0 = ready, 1 = at least one FAIL.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TOOL_VERSIONS="$ROOT/.tool-versions"
REQUIRED_TOOLS=(git make curl jq docker)

fail=0
ok()   { printf '  \033[32mOK\033[0m    %s\n' "$*"; }
warn() { printf '  \033[33mWARN\033[0m  %s\n' "$*"; }
err()  { printf '  \033[31mFAIL\033[0m  %s\n' "$*"; fail=1; }

# .tool-versions uses asdf/mise names; map them to the binary on PATH.
binary_for() { case "$1" in golang) echo go ;; *) echo "$1" ;; esac; }

installed_version() {
  case "$1" in
    golang)  go env GOVERSION | sed 's/^go//' ;;
    kubectl) kubectl version --client | sed -n 's/^Client Version: v//p' ;;
    k3d)     k3d version | sed -n 's/^k3d version v//p' ;;
    *)       return 1 ;;
  esac
} 2>/dev/null </dev/null

echo "Required tools:"
for t in "${REQUIRED_TOOLS[@]}"; do
  if command -v "$t" >/dev/null 2>&1; then ok "$t"; else err "$t: not installed"; fi
done

echo "Pinned tools ($(basename "$TOOL_VERSIONS")):"
while read -r tool want _; do
  [[ -z "$tool" || "$tool" == \#* ]] && continue
  if ! command -v "$(binary_for "$tool")" >/dev/null 2>&1; then
    err "$tool: not installed (pinned $want)"
    continue
  fi
  have="$(installed_version "$tool")"
  if [[ -z "$have" ]]; then
    err "$tool: could not read installed version"
  elif [[ "$have" == "$want" ]]; then
    ok "$tool $have"
  elif [[ "${have%.*}" == "${want%.*}" ]]; then
    warn "$tool $have (pinned $want: patch drift)"
  else
    err "$tool $have (pinned $want: major/minor mismatch)"
  fi
done < "$TOOL_VERSIONS"

echo "Docker engine:"
if command -v docker >/dev/null 2>&1 && timeout 15 docker info >/dev/null 2>&1; then
  ok "daemon reachable (server $(docker version --format '{{.Server.Version}}' 2>/dev/null))"
else
  err "daemon not reachable within 15s: is Docker Desktop running, not paused, with WSL integration on?"
fi

echo
if (( fail )); then
  echo "bootstrap: NOT READY (fix the FAIL lines above; see README > Prerequisites)"
else
  echo "bootstrap: READY"
fi
exit "$fail"
