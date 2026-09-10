#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
echo Format
source_list=$(mktemp)
trap 'rm -f "$source_list"' EXIT
if ! git ls-files -z --cached --others --exclude-standard -- '*.go' > "$source_list"; then
  echo 'Cannot enumerate Go sources' >&2
  exit 1
fi
mapfile -d '' files < "$source_list"
unformatted=$(gofmt -l "${files[@]}")
if [[ -n "$unformatted" ]]; then
  echo "Run gofmt on: $unformatted" >&2
  exit 1
fi
echo Vet
go vet ./...
echo Unit
go test ./cmd/invoice ./internal/config ./internal/invoicing ./internal/locale ./internal/paperless ./internal/pdfattach ./internal/pdfgen ./internal/render ./internal/units ./internal/zugferd
echo Integration
go test ./cmd/server ./internal/auth ./internal/devmode ./internal/documents ./internal/jobs ./internal/store ./internal/web -skip '^TestBrowserWorkflow$'
echo Browser
go test ./internal/web -run '^TestBrowserWorkflow$' -count=1 -v
