#!/usr/bin/env bash
set -euo pipefail
export MSYS_NO_PATHCONV=1
cd "$(dirname "$0")/.."
if [[ $# != 1 || ! "$1" =~ ^[a-zA-Z0-9_-]+$ ]]; then
  echo 'Usage: scripts/recovery-drill.sh UNIQUE_DRILL_NAME (production Compose must be stopped)' >&2
  exit 2
fi
running=$(docker compose ps --status running -q invoice)
if [[ -n "$running" ]]; then
  echo 'Stop the production service before running the isolated recovery drill.' >&2
  exit 1
fi
key="${INVOICE_DRILL_KEY_FILE:-/run/secrets/backup_key}"
archive="/backup/$1.enc"
target="/backup/$1-restored"
docker compose run --rm --no-deps invoice backup -database /data/database/invoice.sqlite -document-root /data/documents -output "$archive" -key-file "$key"
docker compose run --rm --no-deps invoice backup verify -archive "$archive" -key-file "$key"
docker compose run --rm --no-deps invoice restore -archive "$archive" -target-root "$target" -key-file "$key" -confirm
docker compose run --rm --no-deps invoice integrity-check -database "$target/database.sqlite" -document-root "$target/documents"
echo 'Drill passed. Original data is unchanged; retain the archive and inspect the new recovery root.'
