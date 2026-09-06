# Invoice Web Application Design

## Purpose

Extend the existing invoice CLI into a secure, self-hosted web application while preserving the CLI and its established HTML, PDF, locale, Paperless, and Factur-X/ZUGFeRD behavior. The application is intended for one administrator and runs on a private server behind a NetBird proxy.

The first release provides:

- one company profile;
- customer CRUD;
- a reusable goods and services catalog without inventory management;
- draft, finalization, payment, cancellation, and correction workflows for invoices;
- PDF and Factur-X/ZUGFeRD generation through the existing renderer;
- automatic Paperless-ngx upload with visible status and manual retry;
- encrypted backup and restore commands;
- a local development and test mode;
- a production Docker image and comprehensive GitHub Actions checks.

Quotes, multiple administrators, roles, inventory, online payment, email delivery, and public registration are outside the first release.

## Architecture

The application is a modular Go monolith. A new web entry point uses `net/http`, server-side Go templates, and vendored HTMX. Business rules, persistence, authentication, rendering, and background jobs are isolated behind explicit interfaces. The existing CLI remains a separate entry point and continues to use the shared rendering and configuration packages.

SQLite is the system of record. It runs in WAL mode with foreign keys enabled, a bounded busy timeout, and explicit transactions for business operations. One process owns normal application writes. PDF generation and Paperless delivery run through a bounded in-process job queue; durable job state remains in SQLite so interrupted work resumes after restart.

The production deployment consists of one non-root container. A persistent data volume holds the SQLite database, generated documents, and audit records. Secrets and configuration are mounted separately. NetBird is the external access boundary, but the application retains its own authentication, authorization, session, request-validation, and rate-limit controls.

## Package Boundaries

The implementation introduces these focused units:

- `cmd/server`: production and development server startup, configuration validation, graceful shutdown, and administrative subcommands.
- `internal/store`: SQLite connection policy, migrations, transactions, repositories, backup, and restore.
- `internal/auth`: password hashing, login throttling, opaque sessions, CSRF tokens, and administrator bootstrap.
- `internal/web`: routes, middleware, view models, response negotiation, and error mapping.
- `internal/web/templates`: full-page and HTMX fragment templates.
- `internal/web/static`: vendored HTMX, icon assets, CSS, and minimal local JavaScript.
- `internal/invoicing`: draft editing, monetary calculation, finalization, number allocation, cancellation, and correction rules.
- `internal/documents`: immutable invoice snapshots, PDF/ZUGFeRD generation orchestration, checksums, and authorized delivery.
- `internal/jobs`: bounded durable background processing and retry policy.

Existing packages under `internal/config`, `internal/locale`, `internal/render`, `internal/pdfattach`, `internal/pdfgen`, `internal/zugferd`, and `internal/paperless` remain the rendering foundation. Web-domain values are converted into the existing `config.Config` at the document boundary. Rendering packages do not acquire a database dependency.

## Data Model

All primary keys are opaque, randomly generated identifiers. Timestamps are UTC in the database and formatted in the configured locale at the UI boundary. Money is stored as integer minor units with an explicit ISO 4217 currency code. Quantities and tax rates use scaled integers to avoid floating-point persistence errors.

### Company

There is exactly one active company profile. It includes legal name, contact details, postal address, tax number, VAT identifier, bank details, logo reference, brand color, default language, currency, payment terms, invoice prefix, next invoice sequence, and standard notes. Secrets are never stored in this record.

### Customers

Customers contain display and legal names, contact person, email, billing address, country, VAT identifier, preferred language, currency, payment term, notes, and active state. A customer referenced by an invoice cannot be physically deleted; deletion archives it. Search covers name, contact, email, VAT identifier, and customer number.

### Catalog items

A catalog item is either a good or a service. It contains a unique item number, title, description, unit, net unit price, tax rate, and active state. The catalog does not track stock. Archiving preserves references from draft invoices while excluding the item from the default picker.

### Invoices and positions

Invoice states are `draft`, `finalized`, `paid`, `overdue`, and `cancelled`. Drafts use an opaque internal identifier and do not consume an invoice number. A draft references a customer and contains editable positions.

Finalization runs in one database transaction. It validates the complete document, allocates the next sequence, stores the formatted number, freezes company, customer, payment, locale, tax, note, and position snapshots, writes an audit event, and commits. The sequence is unique and monotonically increasing. Concurrent requests cannot receive the same number.

Finalized content is immutable. A finalized invoice may be marked paid, marked overdue by a scheduled rule, or cancelled with a reason. A correction is a new draft that references the original invoice; it receives its own number when finalized. Historical output is always regenerated from its immutable snapshot, never from current customer or catalog data.

### Documents and jobs

Each generated document records invoice ID, kind, relative storage key, media type, size, SHA-256 checksum, generator version, creation time, and status. Storage keys are generated internally and never accepted from request paths.

Paperless jobs record document ID, state, attempt count, next attempt time, last safe error summary, remote task identifier, and timestamps. Jobs are idempotent. Successful jobs are not submitted twice. Failed jobs use bounded exponential backoff and can be retried manually.

### Audit events

Audit events cover authentication outcomes, administrator credential changes, invoice finalization, status transitions, correction links, configuration changes, backup/restore operations, and Paperless delivery state. They contain actor, action, target type and ID, result, request correlation ID, timestamp, and a minimal structured change summary. Passwords, session tokens, CSRF tokens, API keys, full customer records, and rendered document contents are prohibited from logs and audit payloads.

## User Experience

The selected design is **Blue Split View**. It uses the existing brand blue `#5B9BD5`, neutral blue-gray surfaces, a compact left icon rail, and side-by-side list and detail panels. Responsive layouts stack these panels on narrow screens.

Visible text is limited to content, state, validation, and consequential actions. Routine navigation and secondary actions use consistent icons. Every icon control has an accessible name, tooltip, keyboard focus treatment, and at least a 44-by-44 CSS-pixel target on touch layouts. Icon meaning does not depend on color alone.

The primary sections are dashboard, invoices, customers, catalog, and settings:

- The dashboard shows outstanding value, current-month value, draft count, recent invoices, Paperless state, backup freshness, and rendering readiness.
- Customer and catalog pages use a searchable master-detail view with create, edit, archive, and restore actions.
- The invoice editor places editable data and positions beside a continuously refreshed document preview. Catalog selection copies an editable snapshot into the draft.
- The final review displays totals, tax breakdown, recipient, due date, ZUGFeRD validation, and Paperless behavior before finalization.
- Finalize and cancel controls include text labels and require a deliberate confirmation. Repeating a submitted finalization request is idempotent.

All workflows meet WCAG 2.2 AA, support keyboard-only use, retain focus across HTMX swaps, announce validation results, and remain usable without animation. Server-rendered pages remain functional when optional JavaScript enhancements fail; dynamic position editing requires HTMX but returns actionable error states.

## Authentication and Sessions

There is one administrator account and no registration endpoint. The first administrator is created with an interactive local command. Startup refuses normal service when no administrator exists. Password reset is also an interactive local command and invalidates all sessions.

Passwords use Argon2id with a random per-password salt and an application pepper supplied through a Docker secret. Parameters are calibrated on the deployment host to an approved memory and time floor above the current OWASP recommendation and encoded with the hash for future upgrades. Passwords are never passed through command-line arguments or environment variables.

Sessions use opaque random 256-bit tokens from the operating-system CSPRNG. Only a keyed hash is stored. Authentication rotates the token. Sessions expire after inactivity and at an absolute limit, and logout or password reset revokes them server-side. Cookies use `Secure`, `HttpOnly`, `SameSite=Strict`, a narrow path, and no domain attribute.

Login failures use per-account and per-source progressive delay with bounded state and generic responses. Successful authentication clears only the appropriate failure window. Authentication, session, and recovery paths are constant in observable response structure where practical.

## Application Security

OWASP ASVS 5.0.0 Level 2 is the minimum verification baseline. A repository threat model identifies assets, trust boundaries, entry points, abuse cases, and mitigations. Security requirements are mapped to automated checks or documented manual verification.

The web layer provides:

- synchronizer-token CSRF protection for every state-changing request, including HTMX requests;
- strict method and content-type enforcement;
- server-side allow-list validation and normalized length limits;
- parameterized SQL exclusively;
- contextual HTML escaping through `html/template`;
- a nonce-based Content Security Policy with no remote scripts, inline event handlers, or `unsafe-eval`;
- HSTS when TLS terminates at the application, `frame-ancestors 'none'`, `X-Content-Type-Options: nosniff`, strict referrer policy, and a minimal permissions policy;
- request body, multipart, header, concurrency, and execution-time limits;
- explicit HTTP server read-header, read, write, and idle timeouts;
- generic external errors and correlated structured internal errors;
- no public metrics, debug, profiling, schema, or version endpoints.

Forwarded headers are accepted only from configured NetBird proxy addresses. Host headers are allow-listed. The production application must reject insecure configuration, default secrets, world-readable secrets, development mode on a non-loopback listener, and writable executable/template directories.

PDF downloads require an authenticated session and authorization check, use server-generated identifiers, set a fixed media type and safe filename, and prevent MIME sniffing. Uploaded logos are decoded, size- and dimension-limited, re-encoded to an allowed raster format, and stored outside executable paths. SVG upload is not accepted in the first release.

## Cryptography and Post-Quantum Readiness

No custom cryptographic primitives or experimental third-party post-quantum protocol are introduced. Go's current stable `crypto/tls` defaults are used for direct TLS, including its standardized hybrid classical/post-quantum key exchanges. TLS is restricted to 1.3 for application-terminated production HTTPS.

NetBird may terminate the client-facing connection. The deployment documentation therefore distinguishes browser-to-proxy and proxy-to-application transport and provides a verification command for each hop. Internal TLS remains available so the proxy-to-application hop need not use cleartext. The application is crypto-agile: algorithms and encrypted payload formats are versioned, and keys can rotate without changing business data.

The server volume uses host-level full-disk encryption. Backup archives are additionally encrypted with AES-256-GCM using a random data key and authenticated metadata. A passphrase-derived wrapping key uses Argon2id with a unique salt, or a randomly generated wrapping key is supplied through a protected file. Backup keys never share storage with backup archives. Restore verifies the authenticated envelope, SQLite integrity, migration compatibility, and document checksums before replacing live data.

## Local Development and Test Mode

`go run ./cmd/server -dev` starts a clearly marked development instance on `127.0.0.1`. It uses a separate development database and deterministic sample company, customer, catalog, invoice, and job data. It never reads production database paths or production secret locations.

Development mode retains authentication, CSRF, validation, authorization, and security headers. It may use a loopback-compatible non-`Secure` session cookie only when the request is plain HTTP on loopback. It refuses non-loopback binding and cannot enable real Paperless delivery. A local fake Paperless server exercises success, delayed processing, rejection, and retry behavior.

Templates and static assets reload from a dedicated development directory. Production embeds immutable, fingerprinted assets. Go source changes use a fast process restart. No development endpoint, sample credential, verbose stack trace, test data, or file watcher is included in the production image.

`docker compose up --build` reproduces the production runtime locally with a development-only override for ports, volumes, and fixture data. Documented commands provide fast tests and a complete local CI run before pushing.

## Document Generation

The existing HTML/Chrome renderer remains the default and authoritative layout. Factur-X/ZUGFeRD XML is attached after Chrome renders the selected HTML template so enabling electronic invoicing cannot change page layout. The built-in renderer remains an explicit fallback path and is not silently selected after a Chrome runtime failure.

Finalization queues generation from the committed immutable snapshot. The UI reports `pending`, `ready`, or `failed`. A generation failure leaves the finalized invoice valid and retryable; it does not reuse or advance another number. A successful result is written to a temporary file, validated, hashed, atomically renamed into document storage, and then recorded in SQLite.

The Factur-X XML is schema-validated before the document becomes downloadable. PDF checks confirm a readable PDF, expected page count range, embedded `factur-x.xml`, correct attachment relationship and MIME metadata, and an exact checksum on repeated retrieval.

## Paperless Integration

Finalized invoices automatically create a Paperless job after document generation. Tags are configured with the initial values `Kiefer Networks`, `Rechnung`, `Steuer`, `Umsatzsteuer`, `Gewerbesteuer`, and `INBOX`. Existing tags are reused and missing tags are created through the current Paperless client behavior.

The API token is mounted as a secret. It is never displayed after configuration. Delivery uses explicit network timeouts, TLS verification, bounded response bodies, safe error summaries, idempotency safeguards, and exponential backoff with jitter. The UI shows queued, uploaded, or failed state and exposes a manual retry control. Paperless unavailability never changes invoice finalization or document integrity.

## Container and Operations

The image uses a multi-stage build and pins the Go toolchain, runtime base image, Chrome, fonts, HTMX, and icon assets to reviewed versions and immutable digests where supported. Version updates are automated through reviewable pull requests; production never downloads executable assets at startup.

The runtime container:

- uses a numeric non-root user;
- drops all Linux capabilities;
- has a read-only root filesystem and writable mounts only for data and bounded temporary storage;
- sets `no-new-privileges`;
- defines CPU, memory, process, and temporary-storage limits in the deployment example;
- exposes one unprivileged port only to the internal Docker network;
- includes no shell or package manager when the chosen Chrome runtime permits it;
- handles termination signals and drains writes and jobs before exit;
- reports separate liveness and readiness without confidential details.

Startup validates directory ownership, file permissions, database integrity, migration state, secret quality, trusted proxy configuration, Chrome availability, and document storage writability. Failure is closed and explicit.

## Migrations, Backup, and Recovery

Migrations are embedded, checksummed, monotonic, and applied under an exclusive migration lock. The process creates and verifies an encrypted backup before a production schema migration. Migration failure leaves the prior database recoverable and prevents service readiness.

Administrative commands provide backup, verify, restore, integrity-check, administrator creation, password reset, and session revocation. Restore always writes to a new temporary database, verifies it, then atomically switches after explicitly stopping normal service access. Recovery documentation includes regular restore drills and expected recovery-time and recovery-point behavior.

## Error Handling and Observability

Expected validation errors appear next to fields and in a focusable summary. Conflicts such as a stale draft return a clear refresh/review action. Unavailable rendering or Paperless services show retryable state without losing user input.

Logs are structured JSON in production and human-readable locally. A request correlation ID connects HTTP, audit, document, and job events. Sensitive fields are removed by construction rather than redacted after formatting. Health and logs support operations without revealing customer data, SQL, filesystem paths, tokens, or stack traces.

## CI and Quality Gates

GitHub Actions is a required gate for every pull request and release commit. Fast checks start first; independent expensive checks run in parallel. Required checks include:

- formatting, `go vet`, `staticcheck`, and the configured Go linter;
- unit and integration tests with the race detector and atomic coverage;
- repository tests against temporary SQLite databases;
- forward migration, clean install, populated upgrade, backup, integrity, and restore tests;
- authentication, session rotation/revocation, CSRF, rate-limit, host-header, proxy-header, validation, security-header, and unauthorized-download tests;
- browser end-to-end tests for login, customer CRUD, catalog CRUD, draft editing, preview, finalization, PDF download, cancellation, and Paperless retry;
- concurrent invoice finalization tests proving unique monotonic numbers;
- HTML/PDF visual regression checks at fixed fonts and viewport, plus Factur-X/ZUGFeRD validation and attachment checks;
- Paperless success, timeout, rejection, restart recovery, and idempotency tests;
- Linux `amd64` and `arm64` builds;
- Docker build, health, read-only filesystem, non-root execution, dropped-capability, graceful-shutdown, and local Compose smoke tests;
- CodeQL, `govulncheck`, secret scanning, dependency review, action workflow linting, and container vulnerability scanning;
- generation and retention of an SBOM and provenance for release images;
- verification that GitHub Actions and container base images are pinned according to repository policy.

Release workflows consume artifacts built from the tested commit and cannot rebuild from a different source state. A release is blocked unless all required checks pass.

## Version Policy

Implementation starts on the newest stable compatible Go patch release, currently Go 1.27.1. The SQLite driver must expose SQLite 3.53.4 or a newer stable compatible patch at implementation time. Direct dependencies, HTMX, browser assets, GitHub Actions, Chrome, and container images are pinned and recorded in the SBOM.

Automated freshness checks report newer stable releases. Security fixes are prioritized. Major upgrades and cryptographic changes require tests and review rather than automatic production adoption.

## Acceptance Criteria

The first release is accepted when:

1. An administrator can start a local development instance with fixtures and exercise every workflow without deploying a server release.
2. The administrator can securely log in and manage customers and catalog items through the Blue Split View UI.
3. Draft invoices can be edited without consuming a number.
4. Concurrent finalization produces unique monotonic numbers and immutable snapshots.
5. Each finalized invoice produces the selected HTML layout as a PDF with a valid Factur-X/ZUGFeRD attachment.
6. Paperless delivery runs automatically, exposes status, survives restart, and can be retried safely.
7. Finalized invoices cannot be edited; payment, cancellation, and correction workflows preserve history.
8. Backup, verification, and restore complete successfully with an encrypted archive.
9. The production container passes the documented least-privilege checks behind the NetBird proxy topology.
10. All required GitHub Actions checks pass, and the existing CLI behavior remains covered and functional.
