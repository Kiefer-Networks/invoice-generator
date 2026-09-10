#!/usr/bin/env bash
# Release assets verified against upstream release SHA-256 manifests, 2026-09-06.
set -euo pipefail
tool=${1:?tool name required}
case "$tool" in
  # Official v0.37.0 release asset and downloaded bytes verified 2026-09-10.
  buildx) version=0.37.0; repo=docker/buildx; asset=buildx-v${version}.linux-amd64; sha=ae43fa08c796b44efc86d7a63c55f73f7c35f3101188dea7bf93bcd6f99577ba ;;
  golangci-lint) version=2.13.2; repo=golangci/golangci-lint; asset=golangci-lint-${version}-linux-amd64.tar.gz; sha=2277d43b98ec0054280f2ac26b53268bae97682444678a59a657dd565da021d6 ;;
  actionlint) version=1.7.12; repo=rhysd/actionlint; asset=actionlint_${version}_linux_amd64.tar.gz; sha=8aca8db96f1b94770f1b0d72b6dddcb1ebb8123cb3712530b08cc387b349a3d8 ;;
  zizmor) version=1.30.0; repo=zizmorcore/zizmor; asset=zizmor-x86_64-unknown-linux-gnu.tar.gz; sha=ec8c95cd800845abb9bbc5f377ec7c57d2eb8e2386a00a201d3a74ee4092e5ed ;;
  trivy) version=0.74.0; repo=aquasecurity/trivy; asset=trivy_${version}_Linux-64bit.tar.gz; sha=2ae6fe3ee734b7fdf11335663e18c75ea12dccc76062f09f164a3b0f8be4371a ;;
  syft) version=1.51.1; repo=anchore/syft; asset=syft_${version}_linux_amd64.tar.gz; sha=8fcb33017a0dc1058298c923c436d19dfa68ae93968e0b423248542e3afb9fc3 ;;
  gitleaks) version=8.30.1; repo=gitleaks/gitleaks; asset=gitleaks_${version}_linux_x64.tar.gz; sha=551f6fc83ea457d62a0d98237cbad105af8d557003051f41f3e7ca7b3f2470eb ;;
  *) echo "Unknown pinned tool: $tool" >&2; exit 1 ;;
esac
destination=${RUNNER_TEMP:-/tmp}/invoice-ci-tools
mkdir -p "$destination"
archive=$(mktemp)
trap 'rm -f "$archive"' EXIT
curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 "https://github.com/$repo/releases/download/v$version/$asset" -o "$archive"
printf '%s  %s\n' "$sha" "$archive" | sha256sum --check --status
if [[ $tool == buildx ]]; then
  [[ $(uname -s) == Linux && $(uname -m) == x86_64 ]]
  plugin_dir=${DOCKER_CONFIG:-$HOME/.docker}/cli-plugins
  mkdir -p "$plugin_dir"
  install -m 0755 "$archive" "$plugin_dir/docker-buildx"
  docker buildx version
  exit 0
fi
if [[ $tool == golangci-lint ]]; then
  tar -xzf "$archive" -C "$destination" --strip-components=1 "golangci-lint-${version}-linux-amd64/golangci-lint"
else
  tar -xzf "$archive" -C "$destination" "$tool"
fi
chmod 755 "$destination/$tool"
if [[ -n ${GITHUB_PATH:-} ]]; then printf '%s\n' "$destination" >> "$GITHUB_PATH"; fi
printf '%s\n' "$destination/$tool"
