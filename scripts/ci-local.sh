#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
bash scripts/test.sh
echo 'Production build and isolation'
go build -tags=production -trimpath -buildvcs=false -ldflags='-s -w -buildid=' -o /dev/null ./cmd/server
go test ./cmd/server -run '^TestProductionBinaryExcludesDevelopment$' -count=1
# Git Bash must pass Linux container paths through unchanged.
export MSYS_NO_PATHCONV=1
echo 'Compose configuration'
docker compose -f compose.yaml config --quiet
docker compose -f compose.yaml -f compose.dev.yaml config --quiet
echo 'Production container'
docker build --target production -t invoice-generator:test .
INVOICE_CONTAINER_TEST=1 go test ./cmd/server -run '^TestContainerRuntime$' -count=1 -v
echo 'Compose browser and PDF smoke'
docker compose -p invoice-ci -f compose.yaml -f compose.dev.yaml build
trap 'docker compose -p invoice-ci -f compose.yaml -f compose.dev.yaml down' EXIT
docker compose -p invoice-ci -f compose.yaml -f compose.dev.yaml up -d --wait --wait-timeout 180
docker compose -p invoice-ci -f compose.yaml -f compose.dev.yaml exec -T invoice /usr/local/bin/healthcheck
INVOICE_COMPOSE_TEST=running go test ./cmd/server -run '^TestComposeRuntimeState$' -count=1
docker compose -p invoice-ci -f compose.yaml -f compose.dev.yaml run --rm --no-deps --entrypoint /usr/local/bin/browser.test invoice -test.run '^TestBrowserWorkflow$' -test.v -test.timeout 5m
docker compose -p invoice-ci -f compose.yaml -f compose.dev.yaml run --rm --no-deps -e INVOICE_BROWSER_BASE_URL=http://127.0.0.1:8080 --entrypoint /usr/local/bin/browser.test invoice '-test.run=^TestComposeBrowserWorkflow$' -test.v -test.timeout=5m
docker compose -p invoice-ci -f compose.yaml -f compose.dev.yaml restart invoice
docker compose -p invoice-ci -f compose.yaml -f compose.dev.yaml up -d --wait --wait-timeout 180
docker compose -p invoice-ci -f compose.yaml -f compose.dev.yaml run --rm --no-deps -e INVOICE_BROWSER_BASE_URL=http://127.0.0.1:8080 --entrypoint /usr/local/bin/browser.test invoice '-test.run=^TestComposeBrowserPersistence$' -test.v -test.timeout=5m
docker compose -p invoice-ci -f compose.yaml -f compose.dev.yaml stop -t 35
INVOICE_COMPOSE_TEST=stopped go test ./cmd/server -run '^TestComposeRuntimeState$' -count=1

INVOICE_CONTAINER_TEST=1 go test ./cmd/server -run '^TestContainerRecoveryDrill$' -count=1 -v
