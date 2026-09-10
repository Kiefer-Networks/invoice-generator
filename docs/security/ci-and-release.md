# Quality, security and release operations

Require `CI required`, `Security required` and `Container required` in the main
branch ruleset, alongside independent review, no bypasses and no force pushes. Each aggregator
runs even after failure and requires every declared dependency to succeed;
cancelled/skipped/missing jobs cannot satisfy it. Do not configure path-filtered
exceptions. Changes to `.github`, delivery scripts, Dockerfiles or policy require
security review. Enable GitHub dependency graph/dependency review and CodeQL for
this repository; private repositories need the appropriate GitHub license.
Missing platform capability is a failed gate, not a reason to skip a check.
Keep fork pull-request tokens read-only and disable sending write tokens or secrets
to fork workflows. Review these repository settings before enabling the gates.

The Linux and macOS suites run race plus atomic coverage, the migration matrix
also covers Windows, and dedicated jobs execute OIDC/security, actual Chrome,
PDF attachment/XSD, numbering and Paperless scenarios. Browser/PDF dependency
skips fail closed. Only the explicitly named Docker/Compose fixture tests may skip
in the ordinary unit suite because `Container required` runs the full real
`scripts/ci-local.sh` and PowerShell equivalent with their required flags.
Run `scripts/run-ci-tests.sh STAGE` locally to reproduce each hosted test job.

`scripts/check-actions-pinned.sh`, `scripts/check-container-security.sh`,
`go test ./internal/cipolicy`, `python3 scripts/test-scrub-artifact.py`, actionlint
1.7.12 and zizmor 1.30.0 are the workflow policy gates. The three narrow zizmor
self-repository suppressions retain GitHub's supported `./` reusable-workflow
syntax because actionlint 1.7.12 rejects the newer `$/` spelling. Both resolve at
the caller commit. No audit category is globally disabled.

Installer versions and SHA-256 values live in `scripts/install-ci-tool.sh`;
upstream release assets are downloaded using HTTPS, verified before extraction,
and never piped into a shell. Staticcheck v0.8.1 (2026.2.1) and govulncheck v1.7.0
use exact Go module versions with the public Go checksum database. Action pins
carry upstream version comments and were resolved from upstream release commits
on 2026-09-06. Dependabot proposes module/action/base changes. The weekly Security
schedule reruns the scanners and direct dependency freshness check even if source
has not changed. A newer release is a review signal; production is never updated
automatically. The Docker scanner databases intentionally refresh for new advisories.

Uploaded test artifacts contain only validated result fields and repository-relative
atomic coverage; arbitrary test output is discarded. SBOM/license JSON passes the
schema/field allowlist and credential/path scrubber before upload; arbitrary
configuration, environment and scanner metadata are discarded. Build record uploads are disabled. No raw
logs, screenshots of production, databases, backups or secret files are uploaded.
Retention is seven days. CodeQL analyzes with read-only credentials and checks its
private SARIF locally for findings; it does not grant PR code security-event write
permission. Review failed-run output locally with the same SHA and test command.

Before releasing, an administrator must create a protected `v*` tag ruleset that
restricts creation/update/deletion and a `release` environment requiring an
independent reviewer, preventing self-approval and allowing protected tags only.
Grant GHCR package access to this repository. Restrict allowed Actions to reviewed
pins. Those account settings cannot be enforced by a repository file alone.
Artifact attestations require a public repository or an eligible GitHub Enterprise
Cloud private repository. Provision native Linux ARM64 runners for candidate tests;
an unavailable runner or attestation capability blocks release.

Release runs only from a protected exact `vMAJOR.MINOR.PATCH` tag whose commit is
in main history; manual runs must select such a tag. It invokes all three reusable
workflows at that exact commit and waits for success. After environment approval,
one candidate multiarchitecture build uses that tested SHA without external cache.
Buildx v0.37.0 uses the separately pinned BuildKit v0.33.0 daemon. SBOMs are generated
by checksum-verified Syft and scrubbed before their attestation is published;
BuildKit's automatic raw SBOM export is disabled. Each platform is scanned by digest; SBOMs
and GitHub OIDC provenance are attested. Native AMD64 and ARM64 jobs then pull that
same immutable candidate and execute nonroot isolation, Chrome PDF, Java and
production-fixture-exclusion checks before promotion.
The promotion job verifies workflow identity/source digest, tags the same manifest,
checks digest equality and creates release notes. It never rebuilds source. Failed
candidates may remain under their SHA tag for investigation but cannot be promoted
by this workflow. Deploy by immutable digest from the release notes.

The web release publishes container images. The legacy CLI remains built/tested;
its optional local GoReleaser configuration has no mutating `go mod tidy` hook.
Registry publication, hosted CodeQL, dependency review and OIDC attestation cannot
be truthfully certified before the workflow runs on GitHub. Local reports list
their unexecuted hosted boundaries explicitly.

Sources: [GitHub workflow syntax](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax),
[artifact attestations](https://github.com/actions/attest),
[actionlint](https://github.com/rhysd/actionlint),
[zizmor](https://docs.zizmor.sh/), [Trivy](https://github.com/aquasecurity/trivy),
[ASVS register](asvs-5.0.0-level-2.md).
