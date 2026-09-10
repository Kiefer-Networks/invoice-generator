# Local development

Start from the repository root with Chrome/Chromium installed:

```sh
go run ./cmd/server serve -dev
```

Open `http://127.0.0.1:8080`. The local Pocket ID page offers an `invoice-admins`
identity and an identity outside that group. Login still uses signed OIDC tokens,
PKCE, nonce/state verification, database-backed sessions, and CSRF protection.
The application and sign-out page display a development banner.

`-dev-root PATH` selects a dedicated development directory; its default is
`.invoice-development`. The directory must be new, empty, or already marked by
this application for synthetic development data. Database, documents and random
session keys stay below it. Existing data is retained on restart. Development
rejects production connection/secret flags, inherited deployment configuration,
non-loopback listeners, linked directories and linked secret files. Development
cannot be activated through `INVOICE_DEVELOPMENT`.

Use `-listen 127.0.0.1:8181` for another fixed local port. Both fake services use
separate ephemeral loopback listeners. The OIDC signing key and fake Paperless
remote state are process-local; application records and document bytes persist.
Use a fresh development root for a completely reproducible remote-delivery run.

The fixtures include a company, two customers, a good and a service, a draft,
two finalized invoices, document jobs and a failed Paperless job backed by a real
PDF with ZUGFeRD. Open finalized invoice `DEV-2026-0001` and choose **Retry
Paperless delivery** to exercise recovery. Reload its detail page to see updated
background-job status. The source fixtures use only synthetic example data.

`-dev-paperless accepted` is the default. The other deterministic scenarios are
`delayed` (remote task remains pending), `rejected`, `timeout`, and `reject-once`.
These scenarios contact only the local fake; no real Paperless token is used.

Run the same named Format, Vet, Unit, Integration and Browser stages on either
platform:

```powershell
powershell -NoProfile -File scripts/test.ps1
```

```sh
bash scripts/test.sh
```

Browser tests require Chrome and fail if it is unavailable. `INVOICE_CHROME`
can select an installed Chrome executable. The browser test uses a temporary
SQLite database and real application authentication; it never reads browser
cookies or browser storage. `go test ./...` also includes the browser workflow.
On hosts with CGO and a supported C toolchain, additionally run
`go test -race ./internal/devmode ./internal/web ./cmd/server`.

The complete container/CI entry points are `scripts/ci-local.ps1` and
`scripts/ci-local.sh`; they also run the local stages above.

Templates and static assets are embedded by default. For edits to reload per
request, run `go run ./cmd/server serve -dev -dev-assets ./internal/web`.
The dedicated directory is confined through `os.OpenRoot`; production builds
exclude the reload implementation and development fixtures. Go source edits
still require a process restart. For Linux/opt-in Docker Desktop host networking,
use the [development Compose profile](container-deployment.md).
