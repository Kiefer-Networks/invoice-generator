#!/usr/bin/env bash
# The same commands are used by local development and required CI jobs.
set -euo pipefail
cd "$(dirname "$0")/.."
stage=${1:?test stage required}
mkdir -p .ci-private artifacts
case "$stage" in
  all) args=(./... -race -covermode=atomic -coverprofile=.ci-private/coverage.out) ;;
  # Linux/macOS full suites provide race coverage; migration portability must
  # not require a Windows C compiler for the pure-Go SQLite driver.
  migrations) args=(./internal/store -run 'Migration|Migrate|Schema|SQLite|Recovery|Restore') ;;
  oidc) args=(./internal/auth ./internal/web -race -run 'OIDC|Callback|Authorization|Session|CSRF|Host|Proxy|Security|Download|PKCE|Token|Readiness') ;;
  browser) args=(./internal/web -run '^TestBrowserWorkflow$') ;;
  documents) args=(./internal/documents ./internal/zugferd ./internal/pdfattach ./internal/render ./cmd/invoice -race) ;;
  numbering) args=(./internal/invoicing ./internal/store -race -run '^(Test(Draft|Final|ConcurrentFinal|Number|Sequence|Idempot|Correction|Transitions))') ;;
  paperless) args=(./internal/paperless ./internal/jobs ./internal/devmode -race -run 'Paperless|Runner') ;;
  container-runtime) args=(./cmd/server -run '^TestContainerRuntime$') ;;
  *) echo "Unknown test stage: $stage" >&2; exit 1 ;;
esac
result=0
go test "${args[@]}" -count=1 -json > ".ci-private/$stage.json" || result=$?
# No raw log is printed or uploaded. A failed/omitted prerequisite fails closed.
go run ./scripts/cipolicy summary ".ci-private/$stage.json" "artifacts/$stage.json"
cat "artifacts/$stage.json"
if [[ $stage == all ]]; then go run ./scripts/cipolicy coverage .ci-private/coverage.out artifacts/coverage.out; fi
exit "$result"
