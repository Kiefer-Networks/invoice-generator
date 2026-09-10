# Invoice application threat model

Reviewed scope: the production server, embedded browser assets, SQLite and document
roots, Chrome/Java rendering, OIDC client, Paperless worker, encrypted backup CLI,
container delivery, GitHub Actions and the operator workstation. The legacy local
invoice CLI remains trusted-user software. Development fixtures are excluded from
the production dependency graph and image.

## Assets and trust boundaries

| Asset / boundary | Protection and entry points | Evidence / owner |
| --- | --- | --- |
| Invoice/customer data, bank details, finalized numbers, PDF/XML | Authenticated HTTP forms/downloads; exact integer arithmetic; CSRF; immutable final snapshots; SQLite transactions and checksums | `internal/web`, `internal/invoicing`, `internal/store`, `internal/documents`; application maintainer |
| Browser to NetBird to server | Browser and incoming headers are untrusted; explicit host and trusted proxy allowlists; TLS at documented hops; no public application port | `docs/container-deployment.md`; live header/TLS/firewall probes; operator |
| Pocket ID to application | Fixed HTTPS issuer/client/callback; bounded discovery and JWKS; signatures, audience, issuer, nonce, PKCE, state, time and group validation; one-time callback transaction | `internal/auth/*test.go`; identity operator owns passkey enrollment and invoice-admins membership |
| SQLite/document storage to host | Numeric nonroot service identity; separate restricted data roots; path containment; immutable output; encrypted host disks; bounded atomic writes | storage and migration/recovery tests; host encryption and mount inspection; operator |
| Invoice content to Chrome/Java | Templates are trusted embedded code; dynamic text escaped; fixed process arguments; network denied for document subresources; namespace sandbox; read-only root; no capabilities; bounded memory, PIDs and temporary files | `internal/render`, `internal/documents`, `Dockerfile`, runtime/browser/visual tests; application maintainer |
| Application to Paperless | Fixed validated HTTPS destination; token read from file; no redirects; bounded responses/timeouts; task/content matching and reconciliation before retry | `internal/paperless`, `internal/jobs`; operator controls upstream token scope and data retention |
| Backup archives/key to recovery root | Authenticated encryption and bounded strict manifest/schema validation; verify before confirmed restore; no source overwrite; session exclusion option | `internal/store/backup*`, recovery drill; operator owns offline key escrow |
| GitHub contribution to release registry | PR jobs have read-only tokens, no production secrets or persistent checkout credentials; pins and checksums; no shared publication cache; protected-tag ancestry and release environment; source checks precede candidate; scan and attest before digest promotion | workflow policy, CI/Security/Container required checks; repository administrator |
| Administrator workstation to production | Workstation, SSH/NetBird identity, browser extensions and backup-key handling can bypass app controls | disk encryption, updates, passkey/SSH access review and session revocation; operator |

## Abuse cases and mitigations

| Threat | Mitigation / evidence | Residual risk and owner |
| --- | --- | --- |
| Forged host/proxy headers bypass secure cookie or origin checks | Trusted CIDRs, exact hosts, CSRF/method enforcement; adversarial HTTP tests | Misconfigured proxy can invalidate assumptions; operator verifies each deployed hop |
| OIDC replay, issuer mix-up, stolen session or removed group member | Nonce/state/PKCE, issuer-subject identity, session HMAC, short absolute session expiry, rotation/revocation, group enforcement | Existing session lifetime bounds group revocation latency; identity operator can revoke sessions |
| Concurrent finalization duplicates or skips invoice numbers | Transactional number allocation, unique constraints, idempotency and immutable snapshots; race/numbering suite | Host/storage loss still needs tested recovery; data owner |
| Logo/PDF/template injection and renderer SSRF | Raster decoding/re-encoding, no SVG upload, context-aware output encoding, fixed schema/Java config, renderer URL blocking and sandbox | Chrome/Java vulnerabilities remain a patching obligation; security maintainer |
| File traversal, symlink substitution or corrupted backup | OS-rooted storage, generated identifiers, atomic writes, exact schema/manifest, digest verification and recovery drills | Root on host can replace plaintext live data; host encryption protects powered-off media only; operator |
| Paperless timeout produces duplicate uploads | Persisted leases/tasks, explicit safe retry distinctions, content adoption/reconciliation and bounded retries | Ambiguous external processing needs manual reconciliation; operator |
| Resource exhaustion through expensive documents or login floods | Timeouts, bounded bodies/queues/concurrency, container CPU/memory/PID/tmpfs limits, short transactions | NetBird/proxy rate limits and host capacity remain operational controls; operator |
| Malicious PR gains registry/token access | No privileged PR event, no PR secrets, read-only default, isolated hosted runners, caches disabled, no untrusted value interpolation in shell | Repository administrators can change workflows/rules; independent reviews and protected environment required |
| Artifact leaks logs, credentials or host paths | Test-result field allowlist; strict coverage path grammar; JSON inventory credential scan/path redaction; no raw log/database/archive uploads; seven-day retention | SBOM intentionally reveals dependency and filesystem inventory; security maintainer reviews public metadata |
| Supply-chain compromise or stale vulnerable base | Full action commits, checksum-pinned installer assets, Go checksum database, digest-pinned bases/QEMU/BuildKit/SBOM generator, dependency review, CodeQL, gitleaks, Trivy, SBOM and identity attestations | Pinning preserves known content but does not prove it benign; maintainers review updates and advisories |
| Lost backup key or compromised admin device | Separate offline key escrow, restore rehearsal, encrypted workstation, least-privilege access and revocation procedure | Lost key makes authenticated backups unrecoverable; compromised admin can authorize valid harmful actions; operator |

## Operational verification and review triggers

Before production, the operator records the exact NetBird routing/TLS topology,
Pocket ID client/passkey/recovery configuration, encrypted storage and secret file
ownership, log destination/retention, clock synchronization, firewall exposure,
backup-key escrow and a successful restore drill. Application tests do not certify
those external systems. The ASVS register records these as pending operator checks.

Treat active exploitation and critical reachable vulnerabilities as immediate
release blockers. Triage high severity within one business day; remediate or approve
a documented, time-bounded compensating control within seven days. Triage remaining
findings within seven days and record a risk-based deadline. `.trivyignore` starts
empty: exceptions require an owner, rationale, issue, expiry and reviewer.

Review after changes to authentication/authorization, proxy routes/TLS, schema or
recovery format, document rendering/network behavior, Paperless API, filesystem
roots, secret lifecycle, CI publication permissions, dependency major versions,
cryptographic algorithms, an incident, or a new public exposure. Review operator
access and recovery evidence at least quarterly. Release reviewers accept residual
risks explicitly; this document does not silently accept an unverified control.
