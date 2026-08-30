#!/usr/bin/env bash
# Fails if any DIRECT dependency in go.mod has a newer version available.
# Indirect/transitive dependencies are left alone — bumping those past
# what our direct dependencies themselves require is not our call and
# can introduce unintended version skew.
set -euo pipefail

cd "$(dirname "$0")/.."

direct_modules=$(go list -m -f '{{if not .Indirect}}{{.Path}}{{end}}' all | tail -n +2 | sort)

outdated=""
while IFS= read -r mod; do
  [ -z "$mod" ] && continue
  info=$(go list -m -u -f '{{if .Update}}{{.Path}} {{.Version}} -> {{.Update.Version}}{{end}}' "$mod" 2>/dev/null || true)
  if [ -n "$info" ]; then
    outdated="${outdated}${info}\n"
  fi
done <<< "$direct_modules"

if [ -n "$outdated" ]; then
  echo "The following direct dependencies are outdated:"
  echo -e "$outdated"
  echo "Run: go get -u <module> && go mod tidy"
  exit 1
fi

echo "All direct dependencies are up to date."
