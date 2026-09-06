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
