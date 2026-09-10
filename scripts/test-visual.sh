#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
export MSYS_NO_PATHCONV=1
case "${1:-}" in
  '') update=0 ;;
  --update-goldens)
    if [[ -n "${CI:-}" ]]; then
      echo 'Golden updates are forbidden in CI' >&2
      exit 1
    fi
    if [[ "$(id -u)" == 0 ]]; then
      echo 'Update goldens as a nonroot user' >&2
      exit 1
    fi
    update=1 ;;
  *) echo 'Usage: scripts/test-visual.sh [--update-goldens]' >&2; exit 2 ;;
esac
docker build --platform linux/amd64 --target visual -t invoice-generator:visual .
args=(--rm --init --platform linux/amd64 --network none --read-only --cap-drop ALL
  --security-opt no-new-privileges:true --security-opt seccomp=./docker/seccomp.json
  --pids-limit 256 --memory 1g --cpus 2
  --tmpfs /tmp:rw,noexec,nosuid,nodev,size=256m -e "CI=${CI:-}")
if [[ "$update" == 1 ]]; then
  args+=(--user "$(id -u):$(id -g)" -e INVOICE_UPDATE_VISUAL=1 --mount "type=bind,source=$(pwd)/internal/render/testdata/visual,target=/golden")
fi
docker run "${args[@]}" invoice-generator:visual
