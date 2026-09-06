# Task 4 report: Company profile and customer CRUD

## Scope delivered

- Added the company singleton repository with normalized profile data and stable validation errors.
- Added customer creation, retrieval, search, cursor pagination, optimistic updates, archive, and restore.
- Added authenticated company and customer HTTP routes, forms, HTMX detail swaps, and the Blue Split View master-detail UI.
- Extended the local asset manifest after updating the form and focus styles.

## TDD evidence

### Repository RED

Command:

```text
go test ./internal/store -run 'TestCompany|TestCustomer' -v
```

Result: failed as intended before implementation. The compiler reported missing `CustomerInput`, `Store.CustomerRepository`, `CompanyInput`, `Store.CompanyRepository`, and `IsValidationError` symbols.

### Repository GREEN

Command:

```text
go test ./internal/store -run 'TestCompany|TestCustomer' -v
```

Result: passed all company and customer repository tests. Coverage includes singleton upsert; opaque customer IDs; normalized fields; duplicate numbers; deterministic escaped search and cursor paging; optimistic conflicts; and archive/restore while retaining an invoice reference.

### HTTP RED

Command:

```text
go test ./internal/web -run 'TestCompany|TestCustomer' -v
```

Result: failed as intended before route implementation. The company profile, customer list, and customer mutation tests received `404 page not found` from the absent routes. The existing CSRF/auth denial test continued to pass.

### HTTP GREEN

Command:

```text
go test ./internal/web -run 'TestCompany|TestCustomer' -v
```

Result: passed company profile and customer HTTP tests. Coverage includes list/detail/create/edit, archive/restore labeled forms, HTMX retargeting, CSRF and authentication denial, invalid and oversized input preservation, duplicate numbers, and stale-version conflicts.

## Implementation notes

- Repositories select and scan explicit columns and use only parameterized SQL.
- Customer search escapes `%`, `_`, and the selected `!` escape character; page size is capped at 100 and ordering is by normalized display name, number, and ID.
- Updates, archive, and restore include `WHERE id = ? AND version = ?` and increment the version in the same statement.
- Domain-facing repository errors are stable: validation, duplicate, not-found, and optimistic-conflict errors are mapped without returning SQLite constraint text to handlers.
- State-changing web requests remain under the existing session, form content-type, and CSRF middleware. Full-page mutations use Post/Redirect/Get; HTMX mutations return a retargeted detail fragment.
- Customer archive and restore retain the database record and invoice foreign-key reference.

## Changed files

- `internal/store/company.go`, `internal/store/company_test.go`
- `internal/store/customer.go`, `internal/store/customer_test.go`
- `internal/web/company.go`, `internal/web/company_test.go`
- `internal/web/customers.go`, `internal/web/customers_test.go`
- `internal/web/server.go`
- `internal/web/templates/layout.html`, `company.html`, `customers.html`, `customer_detail.html`, `customer_form.html`
- `internal/web/static/app.css`
- `internal/web/testdata/asset-manifest.json`

## Verification

Passed:

```text
go test ./internal/store ./internal/web -run 'Company|Customer' -v
go test ./...
go vet ./...
git diff --check
```

The requested race command was attempted:

```text
go test ./internal/store ./internal/web -race -run 'Company|Customer' -v
```

It could not run because this Windows Go installation reports `go: -race requires cgo; enable cgo by setting CGO_ENABLED=1`. The non-race equivalent above passed.

## Remaining concern

Race-detector coverage requires a Windows environment with CGO and a compatible C compiler enabled. No application test failures remain in the available environment.

## Fix round 1: review findings

### Root causes and RED evidence

Focused regressions were added before the fixes and run with:

```text
go test ./internal/store ./internal/web -run 'TestCustomerRepository(UsesUnicode|CapsPage|RejectsUnsupported)|TestHTMXConfiguration|TestCustomerValidationFullPage|TestCompanyValidationPreserves|TestCustomerConflictUses|TestCustomersExpose' -v
```

The initial run failed as intended:

- Unicode `é` search returned no rows because SQLite `LOWER()` only handled ASCII while cursor values used Go Unicode lowercasing.
- `ZZ` and `ZZZ` passed the former length-only country and currency validation.
- HTMX used its default 4xx response policy, which declines swaps; validation responses also lacked a retarget header.
- Customer validation and conflict handlers unconditionally rendered a form fragment even for ordinary full-page POSTs.
- Integer parsing returned a generic conversion error, so validation was not associated with `payment_terms_days` and the raw input was lost.
- No explicit archived-state filter was parsed or rendered, and pagination links discarded both the query and state.

### Fixes

- Added migration 003 with persisted `search_key` and `sort_key` columns. The repository writes them on create/update and safely backfills existing rows using the same Go Unicode normalization before querying. Search and cursor ordering no longer use SQLite `LOWER()`.
- Enforced explicit ISO 3166-1 alpha-2 and ISO 4217 allow lists for company and customer data.
- Added a safe HTMX `responseHandling` configuration that swaps 400 and 409 responses while keeping other 4xx/5xx responses unswapped errors. HTMX validation and conflict responses set the correct retarget header.
- Rendered a complete Blue Split View page for non-HTMX validation and conflict responses; only actual HTMX requests receive fragments.
- Kept raw submitted form values independently of typed conversion. Both forms now include a focusable validation summary, field-specific message IDs, invalid-state attributes, described-by links, and preserved unsupported language options.
- Added an accessible active/archived/all customer filter. Archived records visibly identify their state and continue to expose their deliberate restore form. Pagination URLs preserve escaped `q`, `state`, and cursor values.

### Fix-round GREEN and verification

Passed:

```text
go test ./internal/store ./internal/web -run 'Company|Customer' -v
go test ./...
go vet ./...
git diff --check
```

The race command was attempted again and remains unavailable only because Go reports:

```text
go: -race requires cgo; enable cgo by setting CGO_ENABLED=1
```

New focused coverage includes Unicode accented search and cursor paging, literal `%`, `_`, and `!` search, the 100-record cap, unsupported ISO placeholders, 400/409 HTMX swap configuration and outcomes, full-page versus fragment errors, raw invalid number and unsupported language preservation, archived filtering and restore UI, and escaped query/filter pagination.
