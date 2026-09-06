# Secure Invoice Web Application Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver a secure Go and HTMX web application for Pocket ID authorized administrators to manage customers, catalog items, and invoices with SQLite, immutable finalization, PDF/ZUGFeRD output, Paperless delivery, local development, Docker, and complete CI gates.

**Architecture:** Build a modular monolith beside the existing CLI. SQLite repositories and explicit transactions own persistence, focused services own authentication and invoicing rules, and `net/http` handlers render full pages or HTMX fragments. Durable jobs call the existing renderer, ZUGFeRD, and Paperless packages without adding database knowledge to them.

**Tech Stack:** Go 1.27.1, `net/http`, `html/template`, HTMX 2.x vendored locally, modernc SQLite driver v1.58.0, `github.com/coreos/go-oidc/v3` v3.21.0, `golang.org/x/oauth2` v0.36.0, `golang.org/x/crypto` v0.56.0, Chrome, Docker Compose, GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-09-06-web-application-design.md`

## Global Constraints

- Preserve the existing CLI and all current rendering, locale, Paperless, and ZUGFeRD behavior.
- Use OWASP ASVS 5.0.0 Level 2 as the minimum security baseline.
- Use integer minor currency units and scaled integer quantities and tax rates in persistent business data.
- Keep finalized invoice snapshots immutable and allocate numbers in the same SQLite transaction that finalizes the invoice.
- Keep all browser assets local and enforce a nonce-based Content Security Policy without `unsafe-inline` or `unsafe-eval`.
- Never store OIDC authorization codes, OIDC tokens, client secrets, session tokens, CSRF tokens, Paperless tokens, or backup keys in logs or ordinary database columns.
- Production must run as a non-root container with a read-only root filesystem and no Linux capabilities.
- Development mode must bind only to loopback and must never contact real Paperless.
- Every production behavior follows red-green-refactor: add one focused failing test, verify the intended failure, add the minimum implementation, and rerun the focused and full suites.
- Commit messages are in English and contain no references to automated authorship.

---

### Task 1: Toolchain, SQLite connection, and schema migration foundation

**Files:**
- Modify: `go.mod`
- Modify: `.github/workflows/ci.yml`
- Modify: `.github/workflows/security.yml`
- Create: `internal/store/store.go`
- Create: `internal/store/migrate.go`
- Create: `internal/store/migrations/001_initial.sql`
- Create: `internal/store/store_test.go`
- Create: `internal/store/migrate_test.go`

**Interfaces:**
- Produces: `store.Open(ctx context.Context, path string) (*store.Store, error)`
- Produces: `(*store.Store).DB() *sql.DB`, `(*store.Store).Close() error`, and `(*store.Store).Migrate(ctx context.Context) error`
- Produces: all tables, constraints, and indexes consumed by later repositories

- [ ] **Step 1: Upgrade and pin the toolchain and direct dependencies**

Set `go 1.27.1`, add `modernc.org/sqlite v1.58.0`, and update `golang.org/x/crypto` to `v0.56.0`. Change both workflows from `1.26.x` to `1.27.1`. Run `go mod tidy` only after the first importing implementation exists.

- [ ] **Step 2: Write failing connection-policy tests**

Create tests that open a temporary database and assert the actual values of `PRAGMA foreign_keys`, `PRAGMA journal_mode`, `PRAGMA busy_timeout`, and `PRAGMA trusted_schema`:

```go
func TestOpenAppliesSecurityPragmas(t *testing.T) {
	t.Parallel()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "app.db"))
	if err != nil { t.Fatal(err) }
	t.Cleanup(func() { _ = s.Close() })

	checks := map[string]string{
		"foreign_keys": "1",
		"journal_mode": "wal",
		"busy_timeout": "5000",
		"trusted_schema": "0",
	}
	for pragma, want := range checks {
		var got string
		if err := s.db.QueryRowContext(context.Background(), "PRAGMA "+pragma).Scan(&got); err != nil { t.Fatal(err) }
		if strings.ToLower(got) != want { t.Fatalf("%s=%q, want %q", pragma, got, want) }
	}
}
```

- [ ] **Step 3: Run the focused test and verify the missing API failure**

Run: `go test ./internal/store -run TestOpenAppliesSecurityPragmas -v`

Expected: compile failure because `Open` and `Store` do not exist.

- [ ] **Step 4: Implement the bounded SQLite connection**

Implement one connection pool owner with `_pragma=foreign_keys(1)`, `_pragma=journal_mode(WAL)`, `_pragma=busy_timeout(5000)`, `_pragma=trusted_schema(0)`, `_pragma=synchronous(FULL)`, a maximum of four open connections, two idle connections, and a five-minute connection lifetime. Ping during `Open` and close on setup failure.

- [ ] **Step 5: Write the failing migration and constraint tests**

Assert a clean migration creates `schema_migrations`, `companies`, `customers`, `catalog_items`, `invoices`, `invoice_items`, `documents`, `paperless_jobs`, `oidc_users`, `sessions`, and `audit_events`. Assert a second migration is a no-op. Execute invalid inserts to prove foreign keys, state checks, unique issuer-subject pairs, unique invoice numbers, nonnegative money, and immutable snapshot preconditions are enforced.

- [ ] **Step 6: Run migration tests and verify they fail because no migration exists**

Run: `go test ./internal/store -run 'TestMigrate|TestSchemaConstraints' -v`

Expected: failure reporting missing tables.

- [ ] **Step 7: Add the embedded migration runner and initial schema**

Use `//go:embed migrations/*.sql`, calculate SHA-256 for each migration, and store version, name, checksum, and applied time. Apply each file with `BEGIN IMMEDIATE`; reject checksum drift. Define opaque text IDs, UTC timestamp text, integer minor units, scaled quantity integers, state checks, foreign keys, partial indexes for active records, unique customer/item numbers, unique non-null invoice numbers, and one-row company enforcement.

- [ ] **Step 8: Verify migration behavior and the existing project**

Run:

```bash
go mod tidy
go test ./internal/store -v
go test ./...
go vet ./...
```

Expected: all commands exit 0.

- [ ] **Step 9: Commit the foundation**

```bash
git add go.mod go.sum .github/workflows/ci.yml .github/workflows/security.yml internal/store
git commit -m "Add SQLite migration foundation"
```

### Task 2: Pocket ID OIDC, group authorization, sessions, and CSRF

**Files:**
- Create: `internal/auth/oidc.go`
- Create: `internal/auth/oidc_test.go`
- Create: `internal/auth/transaction.go`
- Create: `internal/auth/transaction_test.go`
- Create: `internal/auth/session.go`
- Create: `internal/auth/session_test.go`
- Create: `internal/store/auth.go`
- Create: `internal/store/auth_test.go`

**Interfaces:**
- Consumes: `*store.Store`, Pocket ID issuer URL, client ID, client secret, redirect URL, and required group `invoice-admins`
- Produces: `auth.Manager.Begin(returnTo string) (redirectURL string, transactionCookie *http.Cookie, error)`
- Produces: `auth.Manager.Callback(ctx context.Context, callbackURL *url.URL, transactionCookie *http.Cookie) (SessionResult, error)`
- Produces: `auth.Manager.Authenticate`, `Logout`, `RevokeAll`, and `CSRFToken`
- Produces: `store.AuthRepository` methods for issuer-subject projections and hashed local sessions

- [ ] **Step 1: Add pinned OIDC dependencies and write failing discovery tests**

Add `github.com/coreos/go-oidc/v3 v3.21.0` and `golang.org/x/oauth2 v0.36.0`. Use a TLS `httptest.Server` that serves discovery and JWKS. Test rejection of HTTP issuers in production, issuer mismatch, missing authorization/token/JWKS endpoints, cross-origin endpoints, unsupported signing algorithms, stale discovery, oversized documents, and redirect URI mismatch.

- [ ] **Step 2: Verify discovery tests fail**

Run: `go test ./internal/auth -run TestOIDCDiscovery -v`

Expected: compile failure because `auth.Manager` does not exist.

- [ ] **Step 3: Implement strict Pocket ID discovery configuration**

Wrap `go-oidc` and `oauth2` behind a small provider interface. Require HTTPS outside loopback development, an exact configured issuer, same-origin discovered endpoints, a fixed callback URL, `openid profile email groups`, Authorization Code flow, and a client secret read from a protected file. Bound discovery/JWKS response size and HTTP timeouts.

- [ ] **Step 4: Write failing authorization transaction tests**

Assert `Begin` generates independent 32-byte state, nonce, and PKCE verifier values; sends `code_challenge_method=S256`; retains only a safe relative return path; creates a five-minute encrypted/authenticated one-use cookie; and rejects tampering, replay, expiry, duplicate query parameters, OAuth errors, and missing or mismatched state before token exchange.

- [ ] **Step 5: Verify transaction tests fail**

Run: `go test ./internal/auth -run TestAuthorizationTransaction -v`

Expected: compile failure because `Begin` and `Callback` are incomplete.

- [ ] **Step 6: Implement Authorization Code with PKCE and strict callback validation**

Exchange the code once with the original verifier. Verify ID-token signature, exact issuer, client audience, authorized-party claim when present, nonce, expiry, issued-at skew, and authentication time. Require nonempty `sub` and the exact `invoice-admins` entry in the verified `groups` array. Never use email as identity and never write authorization codes or tokens to errors or logs.

- [ ] **Step 7: Write failing identity and session lifecycle tests**

Assert authorized group members upsert the `(issuer, subject)` projection and receive a session; users without the group are denied; changed email cannot merge identities; only `HMAC-SHA-256(serverKey, token)` exists in SQLite; session expiry is 15 minutes from verified membership; login rotates the token; logout revokes one session; global revocation removes all sessions; transaction cookies are deleted after success and failure; and CSRF tokens are session-bound and constant-time validated.

- [ ] **Step 8: Implement the auth repository and local session manager**

Generate 32-byte session and CSRF secrets with `crypto/rand`, store only keyed hashes, and link each session to issuer, subject, and authorization expiry. Delete expired rows during bounded maintenance. Require a separate 32-byte session key and transaction-cookie key from protected files. Keep Pocket ID as the source of truth and retain only subject, issuer, display name, email, last login, and last authorization check.

- [ ] **Step 9: Run focused and full verification**

Run:

```bash
go mod tidy
go test ./internal/auth ./internal/store -race -v
go test ./...
go vet ./...
```

Expected: all commands exit 0.

- [ ] **Step 10: Commit Pocket ID authentication**

```bash
git add go.mod go.sum internal/auth internal/store/auth.go internal/store/auth_test.go
git commit -m "Add Pocket ID authentication"
```

### Task 3: Server configuration, middleware, and authenticated application shell

**Files:**
- Create: `cmd/server/main.go`
- Create: `cmd/server/main_test.go`
- Create: `internal/web/server.go`
- Create: `internal/web/server_test.go`
- Create: `internal/web/middleware.go`
- Create: `internal/web/middleware_test.go`
- Create: `internal/web/templates/layout.html`
- Create: `internal/web/templates/login.html`
- Create: `internal/web/static/app.css`
- Create: `internal/web/static/htmx.min.js`
- Create: `internal/web/static/icons.svg`
- Create: `internal/web/assets.go`
- Create: `internal/web/testdata/asset-manifest.json`

**Interfaces:**
- Consumes: `auth.Manager` and `*store.Store`
- Produces: `web.New(web.Dependencies) (http.Handler, error)`
- Produces: `web.Config` with listener, trusted proxies, allowed hosts, TLS, development, and body-limit settings
- Produces: `server serve` and `server sessions revoke-all`; Pocket ID retains all user and credential administration

- [ ] **Step 1: Write failing configuration tests**

Test rejection of missing secret files, short keys, default keys, world-readable Unix secret modes, non-HTTPS Pocket ID issuer, invalid callback URL, missing `invoice-admins` group configuration, non-loopback development listeners, wildcard production hosts, malformed proxy networks, development with real Pocket ID or Paperless enabled, and production without TLS or an explicitly trusted TLS-terminating proxy.

- [ ] **Step 2: Verify configuration tests fail**

Run: `go test ./cmd/server -run TestConfig -v`

Expected: compile failure because the server command does not exist.

- [ ] **Step 3: Implement strict configuration and session administration**

Read non-secret settings from flags and environment. Read the Pocket ID client secret and application keys from protected files. Exit nonzero on unsafe issuer, callback, group, secret, proxy, listener, or TLS configuration. The session-revocation command opens the same database through `store.Open`, migrates, revokes all local sessions, and closes it. No local user or password command exists.

- [ ] **Step 4: Write failing HTTP security tests**

Create table-driven `httptest` cases for request correlation IDs, allowed hosts, trusted forwarded headers, 1 MiB form limits, method enforcement, content-type enforcement, Pocket ID redirects, callback failure handling, CSRF rejection, transaction-cookie `SameSite=Lax`, application-cookie `SameSite=Strict`, `Secure`/`HttpOnly` cookies, no-cache private pages, CSP nonces, frame denial, content-type sniff prevention, strict referrer policy, and minimal permissions policy.

- [ ] **Step 5: Verify middleware tests fail**

Run: `go test ./internal/web -run 'TestMiddleware|TestSecurityHeaders' -v`

Expected: compile failure because `New` and middleware do not exist.

- [ ] **Step 6: Implement the middleware chain and login flow**

Order middleware as recovery, correlation ID, trusted proxy normalization, host allow-list, size/time limits, security headers, access logging, local session authentication, and CSRF. Add fixed `/auth/login`, `/auth/callback`, and `/auth/logout` routes around the Pocket ID manager. Generate a fresh CSP nonce per response and pass it through request context. Return HTML for normal requests and fragments for `HX-Request: true`; never trust `HX-*` headers for authorization.

- [ ] **Step 7: Build the Blue Split View application shell**

Embed fingerprinted production assets. Vendor the reviewed HTMX release and an SVG symbol sprite with its license. Implement the compact icon rail, skip link, focus states, responsive split panes, reduced-motion support, tooltips, accessible names, and 44-pixel touch targets. Keep finalize, cancel, logout, and credential actions visibly labeled.

- [ ] **Step 8: Verify asset integrity and HTTP behavior**

Add a manifest test that hashes embedded assets and rejects missing license metadata or unexpected remote URLs. Run:

```bash
go test ./internal/web ./cmd/server -race -v
go test ./...
go vet ./...
```

Expected: all commands exit 0.

- [ ] **Step 9: Commit the secure shell**

```bash
git add cmd/server internal/web
git commit -m "Add secure web application shell"
```

### Task 4: Company profile and customer CRUD

**Files:**
- Create: `internal/store/company.go`
- Create: `internal/store/company_test.go`
- Create: `internal/store/customer.go`
- Create: `internal/store/customer_test.go`
- Create: `internal/web/company.go`
- Create: `internal/web/company_test.go`
- Create: `internal/web/customers.go`
- Create: `internal/web/customers_test.go`
- Create: `internal/web/templates/company.html`
- Create: `internal/web/templates/customers.html`
- Create: `internal/web/templates/customer_detail.html`
- Create: `internal/web/templates/customer_form.html`

**Interfaces:**
- Produces: `store.CompanyRepository.Get/Save`
- Produces: `store.CustomerRepository.Create/Get/List/Update/Archive/Restore`
- Produces: normalized `store.CustomerInput`, `store.Customer`, and cursor-based `store.CustomerPage`

- [ ] **Step 1: Write failing repository behavior tests**

Test company singleton upsert, customer creation, generated opaque ID, unique customer number, trimmed normalized fields, deterministic search order, cursor pagination, optimistic version conflict, archive instead of delete, restore, and inability to lose a customer referenced by an invoice.

- [ ] **Step 2: Verify repository tests fail**

Run: `go test ./internal/store -run 'TestCompany|TestCustomer' -v`

Expected: compile failure for missing repository types.

- [ ] **Step 3: Implement repositories with explicit column lists**

Use parameterized statements, scan explicit columns, update with `WHERE id = ? AND version = ?`, increment versions, and convert unique/check failures into stable domain errors. Search uses escaped `LIKE` patterns and a maximum page size of 100.

- [ ] **Step 4: Write failing customer HTTP tests**

Exercise GET list/detail/create/edit, POST create/update/archive/restore, HTMX fragment responses, CSRF, invalid email/country/currency/color, oversized fields, duplicate number, stale version, preserved user input, and authorization. Assert archive and restore require deliberate labeled forms.

- [ ] **Step 5: Verify handler tests fail**

Run: `go test ./internal/web -run 'TestCompany|TestCustomer' -v`

Expected: 404 responses for the new routes.

- [ ] **Step 6: Implement forms and master-detail views**

Register fixed method-aware routes under `/settings/company` and `/customers`. Render customer results in the left split pane and the selected record or form in the right pane. Use Post/Redirect/Get for full pages and retargeted fragments for HTMX success. Return 400 for validation, 403 for CSRF/auth, 404 for missing IDs, and 409 for optimistic conflicts.

- [ ] **Step 7: Verify focused and full behavior**

Run:

```bash
go test ./internal/store ./internal/web -race -run 'Company|Customer' -v
go test ./...
```

Expected: all commands exit 0.

- [ ] **Step 8: Commit company and customer management**

```bash
git add internal/store/company* internal/store/customer* internal/web/company* internal/web/customer* internal/web/templates/company.html internal/web/templates/customer*.html
git commit -m "Add company and customer management"
```

### Task 5: Goods and services catalog CRUD

**Files:**
- Create: `internal/store/catalog.go`
- Create: `internal/store/catalog_test.go`
- Create: `internal/web/catalog.go`
- Create: `internal/web/catalog_test.go`
- Create: `internal/web/templates/catalog.html`
- Create: `internal/web/templates/catalog_detail.html`
- Create: `internal/web/templates/catalog_form.html`

**Interfaces:**
- Produces: `store.CatalogRepository.Create/Get/List/Update/Archive/Restore`
- Produces: `store.CatalogItem` with kind `good|service`, integer `UnitPriceMinor`, `TaxRateBasisPoints`, unit, and version

- [ ] **Step 1: Write failing catalog repository tests**

Test goods and services, unique item numbers, zero and positive prices, allowed tax rates from 0 through 10000 basis points, normalized units, search, pagination, optimistic conflicts, archive, restore, and historical references.

- [ ] **Step 2: Verify repository tests fail**

Run: `go test ./internal/store -run TestCatalog -v`

Expected: compile failure for `CatalogRepository`.

- [ ] **Step 3: Implement the catalog repository**

Use the same error mapping, cursor, version, and archive conventions as customers. Keep price and tax values as integers at every repository boundary.

- [ ] **Step 4: Write failing catalog handler tests**

Test full-page and HTMX list/detail/create/edit flows, locale-aware decimal parsing at the HTTP boundary, invalid kinds, negative prices, invalid tax rates, duplicate item numbers, stale writes, CSRF, authentication, and accessible icon controls.

- [ ] **Step 5: Verify handlers fail with missing routes**

Run: `go test ./internal/web -run TestCatalog -v`

Expected: 404 responses.

- [ ] **Step 6: Implement catalog Blue Split View routes and templates**

Use `/catalog`, `/catalog/new`, `/catalog/{id}`, `/catalog/{id}/edit`, `/catalog/{id}/archive`, and `/catalog/{id}/restore`. Keep the item kind visually recognizable by icon and accessible text. Exclude archived items from the default picker and permit an explicit archived filter.

- [ ] **Step 7: Verify and commit catalog CRUD**

Run `go test ./internal/store ./internal/web -race -run TestCatalog -v` and `go test ./...`, then commit:

```bash
git add internal/store/catalog* internal/web/catalog* internal/web/templates/catalog*.html
git commit -m "Add goods and services catalog"
```

### Task 6: Draft invoice service and editor

**Files:**
- Create: `internal/invoicing/money.go`
- Create: `internal/invoicing/money_test.go`
- Create: `internal/invoicing/drafts.go`
- Create: `internal/invoicing/drafts_test.go`
- Create: `internal/store/invoice.go`
- Create: `internal/store/invoice_test.go`
- Create: `internal/web/invoices.go`
- Create: `internal/web/invoices_test.go`
- Create: `internal/web/templates/invoices.html`
- Create: `internal/web/templates/invoice_editor.html`
- Create: `internal/web/templates/invoice_items.html`
- Create: `internal/web/static/invoice.js`

**Interfaces:**
- Produces: `invoicing.Calculate([]Line) Totals`
- Produces: `invoicing.DraftService.Create/Get/Update/AddCatalogItem/RemoveLine/ReorderLines`
- Produces: `store.InvoiceRepository` draft persistence methods

- [ ] **Step 1: Write failing integer calculation tests**

Cover quantity scaling, per-line half-up rounding, multiple VAT rates, zero VAT, discounts, totals, overflow rejection, and conversion into existing renderer floats only at the rendering boundary. Include the case `3 × 0.3333 at 19%` with exact expected minor-unit totals.

- [ ] **Step 2: Verify calculation tests fail**

Run: `go test ./internal/invoicing -run TestCalculate -v`

Expected: compile failure because `Calculate` does not exist.

- [ ] **Step 3: Implement checked integer calculations**

Use signed 64-bit integers with checked multiplication and division helpers. Represent quantity in ten-thousandths, discount in basis points, and tax in basis points. Return structured totals grouped by tax rate.

- [ ] **Step 4: Write failing draft service tests**

Test draft creation without an invoice number, copying customer and catalog values into editable draft snapshots, position add/edit/remove/reorder, stale version conflict, archived reference visibility, currency consistency, due-date derivation, and validation without database writes on failure.

- [ ] **Step 5: Verify service tests fail**

Run: `go test ./internal/invoicing ./internal/store -run TestDraft -v`

Expected: compile failure for draft APIs.

- [ ] **Step 6: Implement draft repository and service**

Persist draft snapshots and positions in transactions. Do not allocate a number. Require active customers for new drafts while retaining archived customer snapshots in existing drafts. Copy catalog content once and allow invoice-specific edits thereafter.

- [ ] **Step 7: Write failing invoice editor HTTP tests**

Test invoice list filters, new draft, customer selection, catalog picker, manual line entry, edit, remove, reorder, totals fragment, stale version handling, input preservation, request limits, CSRF, and authentication. Assert browser-visible errors use an ARIA live region and focusable summary.

- [ ] **Step 8: Implement the split-view invoice editor**

Use fixed routes under `/invoices` and nested position actions. Return recalculated position and totals fragments after each valid mutation. Keep the right-side preview endpoint debounced through the small local script; the script only dispatches events and contains no business calculations.

- [ ] **Step 9: Verify and commit draft invoicing**

Run:

```bash
go test ./internal/invoicing ./internal/store ./internal/web -race -run 'Calculate|Draft|InvoiceEditor' -v
go test ./...
```

Then commit:

```bash
git add internal/invoicing internal/store/invoice* internal/web/invoice* internal/web/templates/invoice* internal/web/static/invoice.js
git commit -m "Add draft invoice workflow"
```

### Task 7: Atomic finalization, immutable snapshots, cancellation, and correction

**Files:**
- Create: `internal/invoicing/finalize.go`
- Create: `internal/invoicing/finalize_test.go`
- Create: `internal/invoicing/transitions.go`
- Create: `internal/invoicing/transitions_test.go`
- Modify: `internal/store/invoice.go`
- Modify: `internal/store/invoice_test.go`
- Modify: `internal/web/invoices.go`
- Modify: `internal/web/invoices_test.go`
- Create: `internal/web/templates/invoice_review.html`
- Create: `internal/web/templates/invoice_detail.html`

**Interfaces:**
- Produces: `Finalize(ctx context.Context, draftID, idempotencyKey string) (FinalizedInvoice, error)`
- Produces: `MarkPaid`, `MarkOverdue`, `Cancel`, and `CreateCorrection`
- Produces: immutable `invoicing.Snapshot` convertible to `config.Config`

- [ ] **Step 1: Write failing finalization tests**

Test complete validation, one transaction for snapshot and number allocation, formatted `PREFIX-YEAR-SEQUENCE`, rollback without consuming a number, idempotent repeated submission, and immutable finalized rows. Run 32 concurrent finalizations and assert 32 unique contiguous numbers ordered by sequence.

- [ ] **Step 2: Verify finalization tests fail**

Run: `go test ./internal/invoicing -run 'TestFinalize|TestConcurrentFinalization' -v`

Expected: compile failure for `Finalize`.

- [ ] **Step 3: Implement finalization with `BEGIN IMMEDIATE` semantics**

Acquire the company sequence row within the transaction, verify the draft version and idempotency key, calculate totals, insert frozen JSON plus normalized query fields, copy immutable positions, increment the next sequence, append the audit event, and commit. Reject all content mutations when state is not `draft`.

- [ ] **Step 4: Write failing transition tests**

Test allowed and forbidden transitions, required cancellation reason, paid timestamp, scheduled overdue selection, correction linkage, copied correction lines, new draft ID, no number before correction finalization, and audit entries.

- [ ] **Step 5: Implement transitions and review/detail handlers**

Use explicit methods and transactions per transition. Add review, finalize, paid, cancel, correction, and detail routes. Finalization requires a one-use idempotency key stored in the draft form and displays recipient, totals, tax groups, due date, and validation readiness.

- [ ] **Step 6: Verify immutability and commit**

Run:

```bash
go test ./internal/invoicing ./internal/store ./internal/web -race -run 'Finalize|Concurrent|Transition|Correction' -v
go test ./...
```

Then commit:

```bash
git add internal/invoicing internal/store/invoice* internal/web/invoice* internal/web/templates/invoice_review.html internal/web/templates/invoice_detail.html
git commit -m "Add immutable invoice finalization"
```

### Task 8: Document preview, PDF, and ZUGFeRD jobs

**Files:**
- Create: `internal/documents/service.go`
- Create: `internal/documents/service_test.go`
- Create: `internal/documents/storage.go`
- Create: `internal/documents/storage_test.go`
- Create: `internal/jobs/runner.go`
- Create: `internal/jobs/runner_test.go`
- Create: `internal/store/documents.go`
- Create: `internal/store/documents_test.go`
- Create: `internal/web/documents.go`
- Create: `internal/web/documents_test.go`
- Modify: `internal/web/invoices.go`
- Modify: `internal/web/templates/invoice_detail.html`

**Interfaces:**
- Consumes: immutable invoice snapshots and existing render/ZUGFeRD packages
- Produces: `documents.Service.Preview`, `Generate`, `OpenAuthorized`
- Produces: `jobs.Runner.Start(ctx)`, `Stop(ctx)`, and `Wake()`

- [ ] **Step 1: Write failing storage containment tests**

Test generated opaque keys, temporary-file writes, owner-only modes, atomic rename, traversal rejection, symlink rejection, checksum verification, cleanup on error, maximum document size, and unchanged existing files after failed replacement.

- [ ] **Step 2: Verify storage tests fail**

Run: `go test ./internal/documents -run TestStorage -v`

Expected: compile failure because storage does not exist.

- [ ] **Step 3: Implement contained atomic storage**

Resolve the configured root once, use server-generated base32 identifiers, open with exclusive creation, write and sync a sibling temporary file, validate, rename atomically, and store only relative keys. Reopen downloads through containment checks and return a read-only handle plus metadata.

- [ ] **Step 4: Write failing generation tests**

Assert preview uses the draft snapshot without persistence, finalized generation uses only the immutable snapshot, Chrome HTML layout remains selected with ZUGFeRD, repeated generation has stable business content, XML validates, `factur-x.xml` has the required relationship and MIME type, and failure remains retryable without exposing a partial document.

- [ ] **Step 5: Verify generation tests fail**

Run: `go test ./internal/documents -run 'TestPreview|TestGenerate' -v`

Expected: compile failure for `documents.Service`.

- [ ] **Step 6: Implement conversion and generation orchestration**

Convert integer snapshot values to the existing `config.Config` once. Render HTML through Chrome, generate CII XML, attach it with `pdfattach.EmbedFacturX`, validate the resulting PDF and attachment, store atomically, and record SHA-256, size, kind, generator version, and status.

- [ ] **Step 7: Write and implement durable runner tests**

Test bounded concurrency of two, database claiming, lease expiry, restart recovery, cancellation during shutdown, retry schedule, maximum attempts, idempotent completed jobs, and wake-up without busy polling. Use fake clock and work functions.

- [ ] **Step 8: Add authorized preview and download handlers**

Test authentication, unpredictable IDs, missing ownership, traversal-shaped IDs, exact content type, `nosniff`, private cache control, safe `Content-Disposition`, checksum mismatch, and range behavior. Implement `/invoices/{id}/preview` and `/documents/{id}/download` with repository authorization before storage access.

- [ ] **Step 9: Verify and commit documents**

Run:

```bash
go test ./internal/documents ./internal/jobs ./internal/store ./internal/web -race -run 'Storage|Preview|Generate|Runner|Document' -v
go test ./...
```

Then commit:

```bash
git add internal/documents internal/jobs internal/store/documents* internal/web/documents* internal/web/invoices.go internal/web/templates/invoice_detail.html
git commit -m "Add durable invoice document generation"
```

### Task 9: Durable automatic Paperless delivery

**Files:**
- Create: `internal/jobs/paperless.go`
- Create: `internal/jobs/paperless_test.go`
- Create: `internal/store/paperless_jobs.go`
- Create: `internal/store/paperless_jobs_test.go`
- Modify: `internal/paperless/paperless.go`
- Modify: `internal/paperless/paperless_test.go`
- Modify: `internal/web/documents.go`
- Modify: `internal/web/documents_test.go`
- Modify: `internal/web/templates/invoice_detail.html`

**Interfaces:**
- Consumes: completed PDF document and mounted Paperless token
- Produces: `jobs.PaperlessWorker.Process(ctx, job) error`
- Produces: queue, claim, complete, retry, and manual-retry repository operations

- [ ] **Step 1: Write failing Paperless idempotency and retry tests**

Use `httptest.Server` to assert default tags `Kiefer Networks`, `Rechnung`, `Steuer`, `Umsatzsteuer`, `Gewerbesteuer`, and `INBOX`; reuse existing tags; bounded bodies; connect, response-header, and total timeouts; TLS verification; no token in errors; one upload after restart; exponential backoff with jitter; terminal failure after the configured bound; and manual retry resetting only a failed job.

- [ ] **Step 2: Verify the tests fail**

Run: `go test ./internal/jobs ./internal/paperless -run TestPaperless -v`

Expected: compile failure for the worker and idempotency behavior.

- [ ] **Step 3: Harden the Paperless client and implement the worker**

Add context-aware upload, an injected bounded `http.Client`, safe response parsing, and a deterministic idempotency title containing the immutable invoice number. Keep API compatibility for the CLI wrapper. Store only a safe error code and short summary.

- [ ] **Step 4: Add automatic enqueue and manual retry UI**

Create the Paperless job in the same transaction that records a completed PDF document. Display queued, delivered, or failed state. The retry form is authenticated, CSRF-protected, POST-only, and valid only for a failed job.

- [ ] **Step 5: Verify and commit Paperless delivery**

Run:

```bash
go test ./internal/jobs ./internal/paperless ./internal/store ./internal/web -race -run TestPaperless -v
go test ./...
```

Then commit:

```bash
git add internal/jobs/paperless* internal/store/paperless_jobs* internal/paperless internal/web/documents* internal/web/templates/invoice_detail.html
git commit -m "Add durable Paperless delivery"
```

### Task 10: Encrypted backup, verification, and restore

**Files:**
- Create: `internal/store/backup.go`
- Create: `internal/store/backup_test.go`
- Create: `internal/store/restore.go`
- Create: `internal/store/restore_test.go`
- Modify: `cmd/server/main.go`
- Modify: `cmd/server/main_test.go`

**Interfaces:**
- Produces: `store.Backup(ctx, BackupOptions) (BackupResult, error)`
- Produces: `store.VerifyBackup(ctx, VerifyOptions) error`
- Produces: `store.Restore(ctx, RestoreOptions) error`
- Produces: `server backup`, `server backup verify`, `server restore`, and `server integrity-check`

- [ ] **Step 1: Write failing encrypted backup tests**

Create a populated database and documents, back it up, and assert plaintext customer values and the SQLite header are absent. Verify AES-256-GCM authentication, random salt and nonce, Argon2id wrapping, versioned header, included schema version, database integrity, document checksums, wrong-key failure, bit-flip failure, truncated input failure, and owner-only output permissions.

- [ ] **Step 2: Verify backup tests fail**

Run: `go test ./internal/store -run TestBackup -v`

Expected: compile failure because `Backup` does not exist.

- [ ] **Step 3: Implement a versioned authenticated backup envelope**

Use SQLite's online backup mechanism or `VACUUM INTO` under controlled access, archive the database and checksum manifest, generate a random 32-byte data key, encrypt chunks with AES-256-GCM using sequence-bound associated data, and wrap the data key with either a protected key file or an Argon2id-derived key. Sync and atomically rename the final archive.

- [ ] **Step 4: Write failing restore safety tests**

Assert restore refuses a running writable service lock, never overwrites live data before full verification, rejects newer incompatible schema, verifies `PRAGMA integrity_check`, checks every document hash, restores into a sibling temporary root, and atomically switches only after success.

- [ ] **Step 5: Implement restore and administrative commands**

Read passphrases interactively or keys from protected files, never flags or environment. Record only safe audit outcomes. Preserve the failed temporary restore for no longer than the current command and securely remove key buffers.

- [ ] **Step 6: Verify and commit recovery tooling**

Run:

```bash
go test ./internal/store ./cmd/server -race -run 'Backup|Restore|Integrity' -v
go test ./...
```

Then commit:

```bash
git add internal/store/backup* internal/store/restore* cmd/server
git commit -m "Add encrypted backup and recovery"
```

### Task 11: Local development mode and browser workflow tests

**Files:**
- Create: `internal/devmode/fixtures.go`
- Create: `internal/devmode/fixtures_test.go`
- Create: `internal/devmode/oidc.go`
- Create: `internal/devmode/oidc_test.go`
- Create: `internal/devmode/paperless.go`
- Create: `internal/devmode/paperless_test.go`
- Create: `internal/web/browser_test.go`
- Create: `testdata/dev/company.json`
- Create: `testdata/dev/customers.json`
- Create: `testdata/dev/catalog.json`
- Create: `scripts/test.ps1`
- Create: `scripts/test.sh`
- Create: `scripts/ci-local.ps1`
- Create: `scripts/ci-local.sh`
- Modify: `cmd/server/main.go`
- Modify: `cmd/server/main_test.go`

**Interfaces:**
- Produces: deterministic `devmode.Seed(ctx, *store.Store) error`
- Produces: loopback-only fake OIDC discovery, authorization, token, userinfo, and JWKS endpoints
- Produces: local fake Paperless states selected only by test configuration
- Produces: `server serve -dev` and reproducible local test commands

- [ ] **Step 1: Write failing isolation and fixture tests**

Assert development mode rejects non-loopback listeners, production database paths, production secret directories, and real Pocket ID or Paperless URLs. Assert repeated fixture seeding is idempotent and produces fixed company, customer, catalog, draft, finalized, document-job, and Paperless-job records.

- [ ] **Step 2: Verify development tests fail**

Run: `go test ./internal/devmode ./cmd/server -run 'TestDev|TestSeed' -v`

Expected: compile failure because `devmode` does not exist.

- [ ] **Step 3: Implement isolated development startup and fake Paperless**

Create the development database only beneath the configured development root. Display a persistent development banner. Keep OIDC login, the `invoice-admins` group check, local sessions, and CSRF enabled. Start a loopback-only fake provider with one authorized identity and one identity outside the required group; issue short-lived signed tokens from an ephemeral test key and implement discovery, JWKS, authorization, token, and userinfo endpoints. Permit non-`Secure` cookies only for loopback HTTP. Implement deterministic fake Paperless responses for accepted, delayed, rejected, and timed-out requests.

- [ ] **Step 4: Write browser end-to-end tests with the existing Chrome dependency**

Start an `httptest` server backed by a temporary SQLite database and the fake OIDC provider. Use `chromedp` to prove an out-of-group identity is denied, then exercise Pocket ID login as an `invoice-admins` member, customer creation/edit/archive, service creation, draft creation, catalog insertion, totals refresh, preview, review, finalization, download, paid transition, cancellation confirmation, correction creation, logout, and Paperless retry. Locate controls by accessible name or stable `data-testid`, not CSS position.

- [ ] **Step 5: Verify browser tests fail before route wiring is complete**

Run: `go test ./internal/web -run TestBrowserWorkflow -v`

Expected: failure at the first unavailable workflow control.

- [ ] **Step 6: Complete browser-visible wiring and test scripts**

Make `scripts/test` run format verification, vet, unit, integration, and browser tests. Make `scripts/ci-local` additionally build the server, build the Docker image once it exists, and run container smoke/security checks. Both PowerShell and shell variants execute equivalent named stages and stop on first failure.

- [ ] **Step 7: Verify and commit local development**

Run:

```bash
go test ./internal/devmode ./internal/web ./cmd/server -race -v
go test ./...
```

Then run `powershell -File scripts/test.ps1` on Windows and commit:

```bash
git add internal/devmode internal/web/browser_test.go cmd/server testdata/dev scripts/test.* scripts/ci-local.*
git commit -m "Add isolated local development mode"
```

### Task 12: Production Docker image and local Compose environment

**Files:**
- Create: `Dockerfile`
- Create: `compose.yaml`
- Create: `compose.dev.yaml`
- Create: `.dockerignore`
- Create: `docker/entrypoint`
- Create: `docker/healthcheck`
- Create: `docker/seccomp.json`
- Create: `internal/web/health.go`
- Create: `internal/web/health_test.go`
- Modify: `README.md`
- Modify: `SECURITY.md`
- Modify: `scripts/ci-local.ps1`
- Modify: `scripts/ci-local.sh`

**Interfaces:**
- Produces: `/health/live` and authenticated-detail-free `/health/ready`
- Produces: non-root OCI image for Linux `amd64` and `arm64`
- Produces: production and development Compose profiles

- [ ] **Step 1: Write failing health and shutdown tests**

Test liveness without dependency checks, readiness failure for unavailable database, unapplied migrations, failed Pocket ID discovery, invalid callback configuration, missing Chrome, or unwritable document storage; no version/path leakage; readiness success when complete; and graceful shutdown waiting for in-flight writes and bounded jobs.

- [ ] **Step 2: Verify health tests fail**

Run: `go test ./internal/web ./cmd/server -run 'TestHealth|TestGracefulShutdown' -v`

Expected: 404 or missing lifecycle API.

- [ ] **Step 3: Implement health and graceful shutdown**

Keep health payloads fixed and minimal. Cache expensive Chrome readiness briefly. On SIGTERM, stop accepting requests, drain with a fixed deadline, stop job claims, finish or release leases, checkpoint SQLite, and close resources.

- [ ] **Step 4: Create the least-privilege image and Compose files**

Use a digest-pinned Go 1.27.1 builder and reviewed digest-pinned Chrome runtime. Copy only the server binary, Chrome runtime, CA certificates, fonts, and licenses. Run as a numeric UID/GID. Compose sets read-only root, `cap_drop: [ALL]`, `security_opt: [no-new-privileges:true]`, tmpfs with size/noexec/nosuid, memory/CPU/PID limits, internal network exposure, healthcheck, and separate secret mounts. The development override binds only `127.0.0.1` and supplies a development volume.

- [ ] **Step 5: Write container assertions before accepting the image**

Add local script checks that inspect numeric user, read-only root, capabilities, port binding, secret mounts, health transition, writable-volume containment, SIGTERM exit, and absence of development fixtures. Execute a login and finalized PDF smoke flow against Compose.

- [ ] **Step 6: Document exact NetBird and recovery topology**

Document browser-to-NetBird, NetBird-to-application, and application-to-Pocket-ID TLS hops; the exact Pocket ID issuer, callback URL, OIDC client creation, PKCE setting, `groups` claim, and `invoice-admins` membership; trusted proxy CIDRs; allowed hosts; no direct public port; secret file modes; encrypted server volume; session revocation; upgrade; backup; restore drill; rollback; log handling; and post-quantum TLS verification using `openssl s_client` or a Go probe that reports the negotiated hybrid group without printing secrets.

- [ ] **Step 7: Verify and commit container delivery**

Run:

```bash
docker build -t invoice-generator:test .
docker compose -f compose.yaml -f compose.dev.yaml up -d --build
./scripts/ci-local.sh
docker compose -f compose.yaml -f compose.dev.yaml down
go test ./...
```

Expected: all checks pass and containers stop cleanly. Commit:

```bash
git add Dockerfile compose*.yaml .dockerignore docker internal/web/health* README.md SECURITY.md scripts/ci-local.*
git commit -m "Add hardened container deployment"
```

### Task 13: Complete GitHub Actions quality, security, and release gates

**Files:**
- Modify: `.github/workflows/ci.yml`
- Modify: `.github/workflows/security.yml`
- Modify: `.github/workflows/release.yml`
- Create: `.github/workflows/container.yml`
- Create: `.github/dependabot.yml`
- Create: `.github/zizmor.yml`
- Create: `.trivyignore`
- Create: `scripts/check-actions-pinned.sh`
- Create: `scripts/check-container-security.sh`
- Create: `docs/security/asvs-5.0.0-level-2.md`
- Create: `docs/security/threat-model.md`

**Interfaces:**
- Consumes: all test commands and the production Dockerfile
- Produces: required PR checks and release artifacts tied to one tested commit
- Produces: ASVS traceability and repository threat model

- [ ] **Step 1: Add failing policy scripts**

Write shell fixtures proving the action pin checker rejects mutable action tags and accepts full reviewed commit SHAs. Write container-policy fixtures proving rejection of root user, writable root, added capabilities, public binding, missing healthcheck, and unbounded temporary storage.

- [ ] **Step 2: Verify policy tests fail against the current workflows**

Run:

```bash
bash scripts/check-actions-pinned.sh
bash scripts/check-container-security.sh
```

Expected: failure until workflows and Compose use the required pins and controls.

- [ ] **Step 3: Expand CI into independent required jobs**

Add jobs for format/vet/staticcheck/lint, race/coverage on supported hosts, migration matrix, Pocket ID discovery and OIDC callback/security tests, Chrome browser workflow, PDF/ZUGFeRD verification, concurrency numbering, Paperless scenarios, Linux `amd64`/`arm64`, Compose smoke, and local-script parity. Upload coverage and diagnostic artifacts only after running a secret/path scrubber.

- [ ] **Step 4: Expand security workflows**

Retain CodeQL, gitleaks, dependency review, and `govulncheck`. Add workflow linting, pinned-action policy, Trivy filesystem and image scans, SBOM creation, provenance attestation, license inventory, and scheduled freshness checks. Pin installer artifacts by version and SHA-256; do not use `@latest` in CI commands.

- [ ] **Step 5: Map ASVS and threat controls to evidence**

For each applicable ASVS 5.0.0 Level 2 control, record implementation location, automated test or manual verification, and result owner. Document assets, trust boundaries, entry points, threats, mitigations, accepted residual risks, and review triggers for NetBird, SQLite, document storage, Chrome, Paperless, backup keys, and the admin workstation.

- [ ] **Step 6: Make releases consume the tested commit**

Build multi-architecture images once from the exact tested SHA, attach SBOM and provenance, sign or attest through GitHub's supported identity flow, and promote the same digest. Block release when any required workflow is absent or unsuccessful.

- [ ] **Step 7: Run the complete verification suite**

Run:

```bash
./scripts/ci-local.sh
go test ./... -race -covermode=atomic
go vet ./...
git diff --check
```

Expected: all commands exit 0 with no skipped security or browser workflow tests in the Linux environment.

- [ ] **Step 8: Commit CI and security gates**

```bash
git add .github .trivyignore scripts/check-actions-pinned.sh scripts/check-container-security.sh docs/security
git commit -m "Expand web application security gates"
```

### Task 14: Final verification and review preparation

**Files:**
- Modify only files required to fix a failing acceptance check

**Interfaces:**
- Consumes: the complete implementation
- Produces: a reviewable branch with clean status and reproducible evidence

- [ ] **Step 1: Run functional acceptance locally**

Start development mode, sign in, create and edit a customer, create a service, create a draft, add and edit positions, preview, finalize, download, inspect the ZUGFeRD attachment, simulate Paperless failure, retry successfully, mark paid, cancel a separate invoice, and create a correction. Confirm final records remain immutable.

- [ ] **Step 2: Run recovery acceptance**

Create an encrypted backup, verify it, restore it into a clean root, run integrity checks, compare document checksums, authenticate through fake Pocket ID with the restored issuer-subject projection, and confirm active source sessions are not copied when the selected backup policy excludes them.

- [ ] **Step 3: Run security and container acceptance**

Run the full local CI script, inspect the image user and capabilities, verify read-only-root operation, confirm only the internal port is exposed, check TLS 1.3 and the negotiated hybrid group on every application-terminated TLS hop, and verify no secrets occur in image history, environment output, logs, database dumps, or uploaded artifacts.

- [ ] **Step 4: Run regression and race suites**

Run:

```bash
go test ./... -race -count=1
go test ./... -count=1
go vet ./...
./scripts/ci-local.sh
git diff --check
git status --short
```

Expected: all commands exit 0 and `git status --short` is empty after any necessary fix commit.

- [ ] **Step 5: Review the branch diff**

Review every change from `main`, confirm no generated invoice, local database, backup, customer data, API token, secret, or development credential is tracked, and verify commit messages are concise English descriptions.

- [ ] **Step 6: Prepare the final review summary**

Report the behavior delivered, architecture, security evidence, local development commands, Docker startup commands, database migration and backup instructions, test results, CI run links after push, and any explicit residual operational requirements for NetBird and encrypted host storage.
