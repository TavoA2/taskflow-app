#!/usr/bin/env bash
# Keeps a GitHub repository ruleset in sync with a JSON file in this repository (settings as code).
#
# Usage: scripts/apply-ruleset.sh OWNER/REPO FILE           create or update the ruleset named in FILE
#        scripts/apply-ruleset.sh OWNER/REPO FILE --check   exit 1 if the live ruleset differs from FILE
# Needs gh authenticated as a repository admin. GitHub does not read FILE by itself: this script is
# the only way the file reaches GitHub, and --check detects changes made by hand in the web UI.
set -euo pipefail

repo=${1:?usage: apply-ruleset.sh OWNER/REPO FILE [--check]}
file=${2:?usage: apply-ruleset.sh OWNER/REPO FILE [--check]}
mode=${3:-apply}

name=$(jq -r .name "$file")
id=$(gh api "repos/$repo/rulesets" --jq ".[] | select(.name == \"$name\") | .id")

# Compare only the fields this file defines; the API adds read-only metadata (ids, links, dates).
normalize() {
  jq -S '{name, target, enforcement, conditions, bypass_actors,
          rules: (.rules | map(if .parameters then .parameters |= with_entries(select(.value != null)) else . end)
                         | sort_by(.type))}'
}

if [[ "$mode" == --check ]]; then
  [[ -n "$id" ]] || { echo "ruleset '$name' not found on $repo" >&2; exit 1; }
  if diff -u <(normalize <"$file") <(gh api "repos/$repo/rulesets/$id" | normalize); then
    echo "ruleset '$name' on $repo matches $file"
  else
    echo "ruleset '$name' on $repo differs from $file (diff above: - file, + live)" >&2
    exit 1
  fi
elif [[ -n "$id" ]]; then
  gh api -X PUT "repos/$repo/rulesets/$id" --input "$file" >/dev/null
  echo "updated ruleset '$name' (id $id) on $repo"
else
  id=$(gh api -X POST "repos/$repo/rulesets" --input "$file" --jq .id)
  echo "created ruleset '$name' (id $id) on $repo"
fi
