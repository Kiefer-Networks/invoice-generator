# Runtime package locks

The production image installs the complete Alpine package set from an
architecture-specific lock file:

- `docker/apk-lock.amd64` for Alpine `x86_64`
- `docker/apk-lock.arm64` for Alpine `aarch64`

Each manifest contains 210 sorted `name=version` entries. This includes the
packages inherited from the pinned `alpine:3.24.1` base image and the complete
transitive closure for Chromium, OpenJDK, fonts, TLS certificates, and the
health-check client. The manifests were resolved on 2026-09-10 with Alpine's
`apk` solver against the official HTTPS `v3.24/main` and `v3.24/community`
repositories for each target architecture. The reviewed manifest hashes are:

| Target | SHA-256 |
| --- | --- |
| `amd64` | `9321c96b5c1f5389978e56340c541f35d90c717ad14c8a19c15d744d09cc6acd` |
| `arm64` | `16b5178be9cd076d1ca6cd64ae00dec33c35d7c953d7f78c7b2417ce360c61a2` |

`docker/install-locked-apks` maps Docker's target architecture to Alpine's
architecture name, replaces the repository list with those two official
branches, and passes every locked package and exact version to `apk`. Alpine
verifies the signed indexes and packages with the keys in the digest-pinned
base image. The script then compares every installed package and version with
the selected manifest and fails on any missing, additional, or changed entry.

Repository signing proves artifact origin; it does not guarantee that Alpine
will retain an older package artifact indefinitely. If a locked artifact is no
longer available, the build fails closed. Refresh both manifests from the same
repository view, review the dependency changes and vulnerability results, and
update the recorded hashes in this document in the same commit.

Release jobs continue to build candidates once, scan the exact platform
images, attach sanitized SBOM and provenance attestations, and promote only the
tested immutable image digests. The package locks add version-level
reproducibility without changing that digest-bound release flow.
