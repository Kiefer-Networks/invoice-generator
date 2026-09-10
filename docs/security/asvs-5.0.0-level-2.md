# ASVS 5.0.0 Level 2 traceability

Baseline: [OWASP ASVS 5.0.0](https://github.com/OWASP/ASVS/tree/v5.0.0/5.0/en), including every Level 1 and Level 2 requirement. This is a verification register, not an ASVS certification. Each requirement has an implementation boundary, repeatable evidence, and accountable owner. Automated suites provide supporting evidence; they do not replace a requirement-by-requirement manual assessment.

**Result vocabulary:** `Pass` records a dated source assessment with concrete repository evidence. `Fail` records a verified gap that blocks ASVS Level 2 acceptance until remediation and reassessment. `Review pending` means repository evidence is not yet sufficient for a source decision. `Operator pending` requires evidence from the actual deployment or identity provider. `N/A` records an absent feature and must be revisited when scope changes. None of the pending or failed entries is represented as passed.

Application maintainers own source behavior, identity operators own Pocket ID, operators own NetBird/TLS/host controls, data owners own retention, and security maintainers approve residual risks. The release environment reviewers must verify the operator register before first production promotion; GitHub cannot inspect private production state.

## Assessment record

Source assessment performed 2026-09-10 against commit `2c6bb3975833983a00cfcc92bd40d6aa2c22d437` and the complete `main..HEAD` implementation. Every one of the 253 L1/L2 controls was compared with the normative OWASP ASVS 5.0.0 text from the pinned `v5.0.0` tag, then checked against the implementation locations and evidence named in its row. The assessment included fresh `go test ./...`, the real Chrome workflow, vet, staticcheck, golangci-lint, action/container policy, actionlint, module verification, govulncheck, focused snapshot/ZUGFeRD/backup/Paperless tests, and source/diff review.

A source `Pass` covers behavior controlled by this repository. It does not imply that Pocket ID, NetBird, Paperless permissions, host encryption, proxy/TLS settings, production logs, retention, or release-protection settings were inspected. Those remain `Operator pending`. The current Alpine runtime image has not completed its image scan because Docker is unavailable on the assessment host, so its component-remediation control remains `Review pending` until CI supplies that evidence.

## V1 Encoding and Sanitization

Normative source: [0x10-V1-Encoding-and-Sanitization.md](https://github.com/OWASP/ASVS/blob/v5.0.0/5.0/en/0x10-V1-Encoding-and-Sanitization.md); downloaded source SHA-256 `8c0e36a42c3cbf711e2579eb68e1fea8090f52301cac7c400d7c392207b24ee7`.

| Control | Level | Applicability / result | Implementation location | Automated evidence or manual verification | Result owner |
| --- | --- | --- | --- | --- | --- |
| 1.1.1 | 2 | Pass: source assessment 2026-09-10 | `internal/web; internal/render; internal/zugferd` | go test ./internal/web ./internal/render ./internal/zugferd; review context-specific escaping and parser options | Application maintainer |
| 1.1.2 | 2 | Pass: source assessment 2026-09-10 | `internal/web; internal/render; internal/zugferd` | go test ./internal/web ./internal/render ./internal/zugferd; review context-specific escaping and parser options | Application maintainer |
| 1.2.1 | 1 | Pass: source assessment 2026-09-10 | `internal/web; internal/render; internal/zugferd` | go test ./internal/web ./internal/render ./internal/zugferd; review context-specific escaping and parser options | Application maintainer |
| 1.2.2 | 1 | Pass: source assessment 2026-09-10 | `internal/web; internal/render; internal/zugferd` | go test ./internal/web ./internal/render ./internal/zugferd; review context-specific escaping and parser options | Application maintainer |
| 1.2.3 | 1 | Pass: source assessment 2026-09-10 | `internal/web; internal/render; internal/zugferd` | go test ./internal/web ./internal/render ./internal/zugferd; review context-specific escaping and parser options | Application maintainer |
| 1.2.4 | 1 | Pass: source assessment 2026-09-10 | `internal/web; internal/render; internal/zugferd` | go test ./internal/web ./internal/render ./internal/zugferd; review context-specific escaping and parser options | Application maintainer |
| 1.2.5 | 1 | Pass: source assessment 2026-09-10 | `internal/web; internal/render; internal/zugferd` | go test ./internal/web ./internal/render ./internal/zugferd; review context-specific escaping and parser options | Application maintainer |
| 1.2.6 | 2 | N/A: No LDAP interpreter | `internal/web; internal/render; internal/zugferd` | Review feature scope when adding a new interpreter or protocol | Application maintainer |
| 1.2.7 | 2 | N/A: No XPath query interpreter | `internal/web; internal/render; internal/zugferd` | Review feature scope when adding a new interpreter or protocol | Application maintainer |
| 1.2.8 | 2 | N/A: No LaTeX processor | `internal/web; internal/render; internal/zugferd` | Review feature scope when adding a new interpreter or protocol | Application maintainer |
| 1.2.9 | 2 | N/A: No untrusted input is compiled as a regular expression | `internal/web; internal/render; internal/zugferd` | go test ./internal/web ./internal/render ./internal/zugferd; review context-specific escaping and parser options | Application maintainer |
| 1.3.1 | 1 | N/A: No rich HTML editor; text is escaped | `internal/web; internal/render; internal/zugferd` | Review feature scope when adding a new interpreter or protocol | Application maintainer |
| 1.3.2 | 1 | Pass: source assessment 2026-09-10 | `internal/web; internal/render; internal/zugferd` | go test ./internal/web ./internal/render ./internal/zugferd; review context-specific escaping and parser options | Application maintainer |
| 1.3.3 | 2 | Pass: source assessment 2026-09-10 | `internal/web; internal/render; internal/zugferd` | go test ./internal/web ./internal/render ./internal/zugferd; review context-specific escaping and parser options | Application maintainer |
| 1.3.4 | 2 | N/A: SVG upload is rejected; raster-only logos | `internal/web; internal/render; internal/zugferd` | Review feature scope when adding a new interpreter or protocol | Application maintainer |
| 1.3.5 | 2 | N/A: No user-supplied scriptable markup, style, or expression language is processed | `internal/web; internal/render; internal/zugferd` | go test ./internal/web ./internal/render ./internal/zugferd; review context-specific escaping and parser options | Application maintainer |
| 1.3.6 | 2 | Pass: source assessment 2026-09-10 | `internal/web; internal/render; internal/zugferd` | go test ./internal/web ./internal/render ./internal/zugferd; review context-specific escaping and parser options | Application maintainer |
| 1.3.7 | 2 | Pass: source assessment 2026-09-10 | `internal/web; internal/render; internal/zugferd` | go test ./internal/web ./internal/render ./internal/zugferd; review context-specific escaping and parser options | Application maintainer |
| 1.3.8 | 2 | N/A: No JNDI | `internal/web; internal/render; internal/zugferd` | Review feature scope when adding a new interpreter or protocol | Application maintainer |
| 1.3.9 | 2 | N/A: No memcache | `internal/web; internal/render; internal/zugferd` | Review feature scope when adding a new interpreter or protocol | Application maintainer |
| 1.3.10 | 2 | N/A: No untrusted format string is interpreted | `internal/web; internal/render; internal/zugferd` | go test ./internal/web ./internal/render ./internal/zugferd; review context-specific escaping and parser options | Application maintainer |
| 1.3.11 | 2 | N/A: No email transport | `internal/web; internal/render; internal/zugferd` | Review feature scope when adding a new interpreter or protocol | Application maintainer |
| 1.4.1 | 2 | Pass: source assessment 2026-09-10 | `internal/web; internal/render; internal/zugferd` | go test ./internal/web ./internal/render ./internal/zugferd; review context-specific escaping and parser options | Application maintainer |
| 1.4.2 | 2 | Pass: source assessment 2026-09-10 | `internal/web; internal/render; internal/zugferd` | go test ./internal/web ./internal/render ./internal/zugferd; review context-specific escaping and parser options | Application maintainer |
| 1.4.3 | 2 | Pass: source assessment 2026-09-10 | `internal/web; internal/render; internal/zugferd` | go test ./internal/web ./internal/render ./internal/zugferd; review context-specific escaping and parser options | Application maintainer |
| 1.5.1 | 1 | Pass: source assessment 2026-09-10 | `internal/web; internal/render; internal/zugferd` | go test ./internal/web ./internal/render ./internal/zugferd; review context-specific escaping and parser options | Application maintainer |
| 1.5.2 | 2 | Pass: source assessment 2026-09-10 | `internal/web; internal/render; internal/zugferd` | go test ./internal/web ./internal/render ./internal/zugferd; review context-specific escaping and parser options | Application maintainer |

## V2 Validation and Business Logic

Normative source: [0x11-V2-Validation-and-Business-Logic.md](https://github.com/OWASP/ASVS/blob/v5.0.0/5.0/en/0x11-V2-Validation-and-Business-Logic.md); downloaded source SHA-256 `70ad0f68df22ddd825c2837e74999fa026851e75a3128372307685e2bb526969`.

| Control | Level | Applicability / result | Implementation location | Automated evidence or manual verification | Result owner |
| --- | --- | --- | --- | --- | --- |
| 2.1.1 | 1 | Pass: source assessment 2026-09-10 | `internal/invoicing; internal/store` | go test ./internal/invoicing ./internal/store -race; review server input bounds and transaction limits | Application maintainer |
| 2.1.2 | 2 | Pass: source assessment 2026-09-10 | `internal/invoicing; internal/store` | go test ./internal/invoicing ./internal/store -race; review server input bounds and transaction limits | Application maintainer |
| 2.1.3 | 2 | Pass: source assessment 2026-09-10 | `internal/invoicing; internal/store` | go test ./internal/invoicing ./internal/store -race; review server input bounds and transaction limits | Application maintainer |
| 2.2.1 | 1 | Pass: source assessment 2026-09-10 | `internal/invoicing; internal/store` | go test ./internal/invoicing ./internal/store -race; review server input bounds and transaction limits | Application maintainer |
| 2.2.2 | 1 | Pass: source assessment 2026-09-10 | `internal/invoicing; internal/store` | go test ./internal/invoicing ./internal/store -race; review server input bounds and transaction limits | Application maintainer |
| 2.2.3 | 2 | Pass: source assessment 2026-09-10 | `internal/invoicing; internal/store` | go test ./internal/invoicing ./internal/store -race; review server input bounds and transaction limits | Application maintainer |
| 2.3.1 | 1 | Pass: source assessment 2026-09-10 | `internal/invoicing; internal/store` | go test ./internal/invoicing ./internal/store -race; review server input bounds and transaction limits | Application maintainer |
| 2.3.2 | 2 | Pass: source assessment 2026-09-10 | `internal/invoicing; internal/store` | go test ./internal/invoicing ./internal/store -race; review server input bounds and transaction limits | Application maintainer |
| 2.3.3 | 2 | Pass: source assessment 2026-09-10 | `internal/invoicing; internal/store` | go test ./internal/invoicing ./internal/store -race; review server input bounds and transaction limits | Application maintainer |
| 2.3.4 | 2 | Pass: source assessment 2026-09-10 | `internal/invoicing; internal/store` | go test ./internal/invoicing ./internal/store -race; review server input bounds and transaction limits | Application maintainer |
| 2.4.1 | 2 | Pass: source assessment 2026-09-10 | `internal/invoicing; internal/store` | go test ./internal/invoicing ./internal/store -race; review server input bounds and transaction limits | Application maintainer |

## V3 Web Frontend Security

Normative source: [0x12-V3-Web-Frontend-Security.md](https://github.com/OWASP/ASVS/blob/v5.0.0/5.0/en/0x12-V3-Web-Frontend-Security.md); downloaded source SHA-256 `02de197d5aa55592cfa524e66d4aa15bd3e8955e23e76438ac413c94172488fc`.

| Control | Level | Applicability / result | Implementation location | Automated evidence or manual verification | Result owner |
| --- | --- | --- | --- | --- | --- |
| 3.2.1 | 1 | Pass: source assessment 2026-09-10 | `internal/web/server.go; internal/auth/session.go` | go test ./internal/web ./internal/auth; browser test; inspect deployed response headers | Application maintainer + operator |
| 3.2.2 | 1 | Pass: source assessment 2026-09-10 | `internal/web/server.go; internal/auth/session.go` | go test ./internal/web ./internal/auth; browser test; inspect deployed response headers | Application maintainer + operator |
| 3.3.1 | 1 | Fail: production cookie names lack the required __Secure- or __Host- prefix | `internal/web/server.go; internal/auth/session.go` | go test ./internal/web ./internal/auth; browser test; inspect deployed response headers | Application maintainer + operator |
| 3.3.2 | 2 | Pass: source assessment 2026-09-10 | `internal/web/server.go; internal/auth/session.go` | go test ./internal/web ./internal/auth; browser test; inspect deployed response headers | Application maintainer + operator |
| 3.3.3 | 2 | Fail: production cookie names lack the required __Host- prefix | `internal/web/server.go; internal/auth/session.go` | go test ./internal/web ./internal/auth; browser test; inspect deployed response headers | Application maintainer + operator |
| 3.3.4 | 2 | Pass: source assessment 2026-09-10 | `internal/web/server.go; internal/auth/session.go` | go test ./internal/web ./internal/auth; browser test; inspect deployed response headers | Application maintainer + operator |
| 3.4.1 | 1 | Pass: source assessment 2026-09-10 | `internal/web/server.go; internal/auth/session.go` | go test ./internal/web ./internal/auth; browser test; inspect deployed response headers | Application maintainer + operator |
| 3.4.2 | 1 | Pass: source assessment 2026-09-10 | `internal/web/server.go; internal/auth/session.go` | go test ./internal/web ./internal/auth; browser test; inspect deployed response headers | Application maintainer + operator |
| 3.4.3 | 2 | Pass: source assessment 2026-09-10 | `internal/web/server.go; internal/auth/session.go` | go test ./internal/web ./internal/auth; browser test; inspect deployed response headers | Application maintainer + operator |
| 3.4.4 | 2 | Pass: source assessment 2026-09-10 | `internal/web/server.go; internal/auth/session.go` | go test ./internal/web ./internal/auth; browser test; inspect deployed response headers | Application maintainer + operator |
| 3.4.5 | 2 | Pass: source assessment 2026-09-10 | `internal/web/server.go; internal/auth/session.go` | go test ./internal/web ./internal/auth; browser test; inspect deployed response headers | Application maintainer + operator |
| 3.4.6 | 2 | Pass: source assessment 2026-09-10 | `internal/web/server.go; internal/auth/session.go` | go test ./internal/web ./internal/auth; browser test; inspect deployed response headers | Application maintainer + operator |
| 3.5.1 | 1 | Pass: source assessment 2026-09-10 | `internal/web/server.go; internal/auth/session.go` | go test ./internal/web ./internal/auth; browser test; inspect deployed response headers | Application maintainer + operator |
| 3.5.2 | 1 | N/A: No CORS preflight authorization dependency | `internal/web/server.go; internal/auth/session.go` | Review feature scope when adding a new interpreter or protocol | Application maintainer + operator |
| 3.5.3 | 1 | Pass: source assessment 2026-09-10 | `internal/web/server.go; internal/auth/session.go` | go test ./internal/web ./internal/auth; browser test; inspect deployed response headers | Application maintainer + operator |
| 3.5.4 | 2 | Operator pending: verify production host separation for the application, Pocket ID, and Paperless | `internal/web/server.go; internal/auth/session.go` | go test ./internal/web ./internal/auth; browser test; inspect deployed response headers | Application maintainer + operator |
| 3.5.5 | 2 | N/A: No postMessage interface | `internal/web/server.go; internal/auth/session.go` | Review feature scope when adding a new interpreter or protocol | Application maintainer + operator |
| 3.7.1 | 2 | Pass: source assessment 2026-09-10 | `internal/web/server.go; internal/auth/session.go` | go test ./internal/web ./internal/auth; browser test; inspect deployed response headers | Application maintainer + operator |
| 3.7.2 | 2 | Pass: source assessment 2026-09-10 | `internal/web/server.go; internal/auth/session.go` | go test ./internal/web ./internal/auth; browser test; inspect deployed response headers | Application maintainer + operator |

## V4 API and Web Service

Normative source: [0x13-V4-API-and-Web-Service.md](https://github.com/OWASP/ASVS/blob/v5.0.0/5.0/en/0x13-V4-API-and-Web-Service.md); downloaded source SHA-256 `efcadbf5ad78fb977c96d3d30e9a1d02aaafd4d787112a529ac82788a990406b`.

| Control | Level | Applicability / result | Implementation location | Automated evidence or manual verification | Result owner |
| --- | --- | --- | --- | --- | --- |
| 4.1.1 | 1 | Pass: source assessment 2026-09-10 | `internal/web; cmd/server; docs/container-deployment.md` | go test ./internal/web ./cmd/server; inspect deployed proxy request framing and TLS | Application maintainer + operator |
| 4.1.2 | 2 | Operator pending: verify NetBird proxy HTTP-to-HTTPS behavior | `internal/web; cmd/server; docs/container-deployment.md` | go test ./internal/web ./cmd/server; inspect deployed proxy request framing and TLS | Application maintainer + operator |
| 4.1.3 | 2 | Pass: source assessment 2026-09-10 | `internal/web; cmd/server; docs/container-deployment.md` | go test ./internal/web ./cmd/server; inspect deployed proxy request framing and TLS | Application maintainer + operator |
| 4.2.1 | 2 | Operator pending: verify NetBird proxy request-boundary normalization with the Go server | `internal/web; cmd/server; docs/container-deployment.md` | go test ./internal/web ./cmd/server; inspect deployed proxy request framing and TLS | Application maintainer + operator |
| 4.3.1 | 2 | N/A: no GraphQL or application WebSocket API | `internal/web; cmd/server; docs/container-deployment.md` | Review routing when API protocols change | Application maintainer + operator |
| 4.3.2 | 2 | N/A: no GraphQL or application WebSocket API | `internal/web; cmd/server; docs/container-deployment.md` | Review routing when API protocols change | Application maintainer + operator |
| 4.4.1 | 1 | N/A: no GraphQL or application WebSocket API | `internal/web; cmd/server; docs/container-deployment.md` | Review routing when API protocols change | Application maintainer + operator |
| 4.4.2 | 2 | N/A: no GraphQL or application WebSocket API | `internal/web; cmd/server; docs/container-deployment.md` | Review routing when API protocols change | Application maintainer + operator |
| 4.4.3 | 2 | N/A: no GraphQL or application WebSocket API | `internal/web; cmd/server; docs/container-deployment.md` | Review routing when API protocols change | Application maintainer + operator |
| 4.4.4 | 2 | N/A: no GraphQL or application WebSocket API | `internal/web; cmd/server; docs/container-deployment.md` | Review routing when API protocols change | Application maintainer + operator |

## V5 File Handling

Normative source: [0x14-V5-File-Handling.md](https://github.com/OWASP/ASVS/blob/v5.0.0/5.0/en/0x14-V5-File-Handling.md); downloaded source SHA-256 `079a51123e5156a5ffc6329f266d49752cb60f6b780899484c3f1cfde3734ebb`.

| Control | Level | Applicability / result | Implementation location | Automated evidence or manual verification | Result owner |
| --- | --- | --- | --- | --- | --- |
| 5.1.1 | 2 | Pass: source assessment 2026-09-10 | `internal/documents; internal/web/documents.go; internal/web/company.go` | go test ./internal/documents ./internal/web; review upload decoder limits and document-root permissions | Application maintainer |
| 5.2.1 | 1 | Pass: source assessment 2026-09-10 | `internal/documents; internal/web/documents.go; internal/web/company.go` | go test ./internal/documents ./internal/web; review upload decoder limits and document-root permissions | Application maintainer |
| 5.2.2 | 1 | Pass: source assessment 2026-09-10 | `internal/documents; internal/web/documents.go; internal/web/company.go` | go test ./internal/documents ./internal/web; review upload decoder limits and document-root permissions | Application maintainer |
| 5.2.3 | 2 | N/A: No user archive upload | `internal/documents; internal/web/documents.go; internal/web/company.go` | Review feature scope when adding a new interpreter or protocol | Application maintainer |
| 5.3.1 | 1 | Pass: source assessment 2026-09-10 | `internal/documents; internal/web/documents.go; internal/web/company.go` | go test ./internal/documents ./internal/web; review upload decoder limits and document-root permissions | Application maintainer |
| 5.3.2 | 1 | Pass: source assessment 2026-09-10 | `internal/documents; internal/web/documents.go; internal/web/company.go` | go test ./internal/documents ./internal/web; review upload decoder limits and document-root permissions | Application maintainer |
| 5.4.1 | 2 | Pass: source assessment 2026-09-10 | `internal/documents; internal/web/documents.go; internal/web/company.go` | go test ./internal/documents ./internal/web; review upload decoder limits and document-root permissions | Application maintainer |
| 5.4.2 | 2 | Pass: source assessment 2026-09-10 | `internal/documents; internal/web/documents.go; internal/web/company.go` | go test ./internal/documents ./internal/web; review upload decoder limits and document-root permissions | Application maintainer |
| 5.4.3 | 2 | N/A: No files from untrusted end users are accepted or redistributed | `internal/documents; internal/web/documents.go; internal/web/company.go` | go test ./internal/documents ./internal/web; review upload decoder limits and document-root permissions | Application maintainer |

## V6 Authentication

Normative source: [0x15-V6-Authentication.md](https://github.com/OWASP/ASVS/blob/v5.0.0/5.0/en/0x15-V6-Authentication.md); downloaded source SHA-256 `e1e0aaa15e48f7941560176d28eeb11ad3b9b4f9d323437c0e950b3a16cd2b23`.

| Control | Level | Applicability / result | Implementation location | Automated evidence or manual verification | Result owner |
| --- | --- | --- | --- | --- | --- |
| 6.1.1 | 1 | Pass: source assessment 2026-09-10 | `internal/auth; docs/container-deployment.md; Pocket ID` | go test ./internal/auth; inspect Pocket ID passkey/authentication strength and recovery policy | Identity operator |
| 6.1.2 | 2 | Operator pending: identity provider boundary | `Pocket ID; docs/container-deployment.md` | Confirm applicability to passkey-only Pocket ID, enrollment/recovery and provider policy; document justified N/A where no password/OTP exists | Identity operator |
| 6.1.3 | 2 | N/A: Pocket ID OIDC is the only authentication pathway | `internal/auth; docs/container-deployment.md; Pocket ID` | go test ./internal/auth; inspect Pocket ID passkey/authentication strength and recovery policy | Identity operator |
| 6.2.1 | 1 | Operator pending: identity provider boundary | `Pocket ID; docs/container-deployment.md` | Confirm applicability to passkey-only Pocket ID, enrollment/recovery and provider policy; document justified N/A where no password/OTP exists | Identity operator |
| 6.2.2 | 1 | Operator pending: identity provider boundary | `Pocket ID; docs/container-deployment.md` | Confirm applicability to passkey-only Pocket ID, enrollment/recovery and provider policy; document justified N/A where no password/OTP exists | Identity operator |
| 6.2.3 | 1 | Operator pending: identity provider boundary | `Pocket ID; docs/container-deployment.md` | Confirm applicability to passkey-only Pocket ID, enrollment/recovery and provider policy; document justified N/A where no password/OTP exists | Identity operator |
| 6.2.4 | 1 | Operator pending: identity provider boundary | `Pocket ID; docs/container-deployment.md` | Confirm applicability to passkey-only Pocket ID, enrollment/recovery and provider policy; document justified N/A where no password/OTP exists | Identity operator |
| 6.2.5 | 1 | Operator pending: identity provider boundary | `Pocket ID; docs/container-deployment.md` | Confirm applicability to passkey-only Pocket ID, enrollment/recovery and provider policy; document justified N/A where no password/OTP exists | Identity operator |
| 6.2.6 | 1 | Operator pending: identity provider boundary | `Pocket ID; docs/container-deployment.md` | Confirm applicability to passkey-only Pocket ID, enrollment/recovery and provider policy; document justified N/A where no password/OTP exists | Identity operator |
| 6.2.7 | 1 | Operator pending: identity provider boundary | `Pocket ID; docs/container-deployment.md` | Confirm applicability to passkey-only Pocket ID, enrollment/recovery and provider policy; document justified N/A where no password/OTP exists | Identity operator |
| 6.2.8 | 1 | Operator pending: identity provider boundary | `Pocket ID; docs/container-deployment.md` | Confirm applicability to passkey-only Pocket ID, enrollment/recovery and provider policy; document justified N/A where no password/OTP exists | Identity operator |
| 6.2.9 | 2 | Operator pending: identity provider boundary | `Pocket ID; docs/container-deployment.md` | Confirm applicability to passkey-only Pocket ID, enrollment/recovery and provider policy; document justified N/A where no password/OTP exists | Identity operator |
| 6.2.10 | 2 | Operator pending: identity provider boundary | `Pocket ID; docs/container-deployment.md` | Confirm applicability to passkey-only Pocket ID, enrollment/recovery and provider policy; document justified N/A where no password/OTP exists | Identity operator |
| 6.2.11 | 2 | Operator pending: identity provider boundary | `Pocket ID; docs/container-deployment.md` | Confirm applicability to passkey-only Pocket ID, enrollment/recovery and provider policy; document justified N/A where no password/OTP exists | Identity operator |
| 6.2.12 | 2 | Operator pending: identity provider boundary | `Pocket ID; docs/container-deployment.md` | Confirm applicability to passkey-only Pocket ID, enrollment/recovery and provider policy; document justified N/A where no password/OTP exists | Identity operator |
| 6.3.1 | 1 | Operator pending: verify Pocket ID credential-stuffing and brute-force controls | `internal/auth; docs/container-deployment.md; Pocket ID` | go test ./internal/auth; inspect Pocket ID passkey/authentication strength and recovery policy | Identity operator |
| 6.3.2 | 1 | Pass: source assessment 2026-09-10 | `internal/auth; docs/container-deployment.md; Pocket ID` | go test ./internal/auth; inspect Pocket ID passkey/authentication strength and recovery policy | Identity operator |
| 6.3.3 | 2 | Operator pending: verify Pocket ID multi-factor/passkey policy | `internal/auth; docs/container-deployment.md; Pocket ID` | go test ./internal/auth; inspect Pocket ID passkey/authentication strength and recovery policy | Identity operator |
| 6.3.4 | 2 | N/A: Pocket ID OIDC is the only authentication pathway | `internal/auth; docs/container-deployment.md; Pocket ID` | go test ./internal/auth; inspect Pocket ID passkey/authentication strength and recovery policy | Identity operator |
| 6.4.1 | 1 | Operator pending: identity provider boundary | `Pocket ID; docs/container-deployment.md` | Confirm applicability to passkey-only Pocket ID, enrollment/recovery and provider policy; document justified N/A where no password/OTP exists | Identity operator |
| 6.4.2 | 1 | Operator pending: identity provider boundary | `Pocket ID; docs/container-deployment.md` | Confirm applicability to passkey-only Pocket ID, enrollment/recovery and provider policy; document justified N/A where no password/OTP exists | Identity operator |
| 6.4.3 | 2 | Operator pending: identity provider boundary | `Pocket ID; docs/container-deployment.md` | Confirm applicability to passkey-only Pocket ID, enrollment/recovery and provider policy; document justified N/A where no password/OTP exists | Identity operator |
| 6.4.4 | 2 | Operator pending: identity provider boundary | `Pocket ID; docs/container-deployment.md` | Confirm applicability to passkey-only Pocket ID, enrollment/recovery and provider policy; document justified N/A where no password/OTP exists | Identity operator |
| 6.5.1 | 2 | Operator pending: identity provider boundary | `Pocket ID; docs/container-deployment.md` | Confirm applicability to passkey-only Pocket ID, enrollment/recovery and provider policy; document justified N/A where no password/OTP exists | Identity operator |
| 6.5.2 | 2 | Operator pending: identity provider boundary | `Pocket ID; docs/container-deployment.md` | Confirm applicability to passkey-only Pocket ID, enrollment/recovery and provider policy; document justified N/A where no password/OTP exists | Identity operator |
| 6.5.3 | 2 | Operator pending: identity provider boundary | `Pocket ID; docs/container-deployment.md` | Confirm applicability to passkey-only Pocket ID, enrollment/recovery and provider policy; document justified N/A where no password/OTP exists | Identity operator |
| 6.5.4 | 2 | Operator pending: identity provider boundary | `Pocket ID; docs/container-deployment.md` | Confirm applicability to passkey-only Pocket ID, enrollment/recovery and provider policy; document justified N/A where no password/OTP exists | Identity operator |
| 6.5.5 | 2 | Operator pending: identity provider boundary | `Pocket ID; docs/container-deployment.md` | Confirm applicability to passkey-only Pocket ID, enrollment/recovery and provider policy; document justified N/A where no password/OTP exists | Identity operator |
| 6.6.1 | 2 | Operator pending: identity provider boundary | `Pocket ID; docs/container-deployment.md` | Confirm applicability to passkey-only Pocket ID, enrollment/recovery and provider policy; document justified N/A where no password/OTP exists | Identity operator |
| 6.6.2 | 2 | Operator pending: identity provider boundary | `Pocket ID; docs/container-deployment.md` | Confirm applicability to passkey-only Pocket ID, enrollment/recovery and provider policy; document justified N/A where no password/OTP exists | Identity operator |
| 6.6.3 | 2 | Operator pending: identity provider boundary | `Pocket ID; docs/container-deployment.md` | Confirm applicability to passkey-only Pocket ID, enrollment/recovery and provider policy; document justified N/A where no password/OTP exists | Identity operator |
| 6.8.1 | 2 | N/A: Exactly one configured Pocket ID issuer is supported | `internal/auth; docs/container-deployment.md; Pocket ID` | go test ./internal/auth; inspect Pocket ID passkey/authentication strength and recovery policy | Identity operator |
| 6.8.2 | 2 | Pass: source assessment 2026-09-10 | `internal/auth; docs/container-deployment.md; Pocket ID` | go test ./internal/auth; inspect Pocket ID passkey/authentication strength and recovery policy | Identity operator |
| 6.8.3 | 2 | N/A: OIDC only; no SAML | `internal/auth; docs/container-deployment.md; Pocket ID` | Review feature scope when adding a new interpreter or protocol | Identity operator |
| 6.8.4 | 2 | Operator pending: verify Pocket ID authentication strength and recentness policy | `internal/auth; docs/container-deployment.md; Pocket ID` | go test ./internal/auth; inspect Pocket ID passkey/authentication strength and recovery policy | Identity operator |

## V7 Session Management

Normative source: [0x16-V7-Session-Management.md](https://github.com/OWASP/ASVS/blob/v5.0.0/5.0/en/0x16-V7-Session-Management.md); downloaded source SHA-256 `4aec329f2642ae750fbe1abac5da7e8a7bcfec6fc331bb0a46916970f9c88236`.

| Control | Level | Applicability / result | Implementation location | Automated evidence or manual verification | Result owner |
| --- | --- | --- | --- | --- | --- |
| 7.1.1 | 2 | Pass: source assessment 2026-09-10 | `internal/auth/session.go; internal/auth/transaction.go` | go test ./internal/auth; inspect timeout, cookie and revocation behavior against deployment | Application maintainer |
| 7.1.2 | 2 | Fail: concurrent-session limit and behavior are not documented | `internal/auth/session.go; internal/auth/transaction.go` | go test ./internal/auth; inspect timeout, cookie and revocation behavior against deployment | Application maintainer |
| 7.1.3 | 2 | Operator pending: verify coordinated Pocket ID and application session lifecycle | `internal/auth/session.go; internal/auth/transaction.go` | go test ./internal/auth; inspect timeout, cookie and revocation behavior against deployment | Application maintainer |
| 7.2.1 | 1 | Pass: source assessment 2026-09-10 | `internal/auth/session.go; internal/auth/transaction.go` | go test ./internal/auth; inspect timeout, cookie and revocation behavior against deployment | Application maintainer |
| 7.2.2 | 1 | Pass: source assessment 2026-09-10 | `internal/auth/session.go; internal/auth/transaction.go` | go test ./internal/auth; inspect timeout, cookie and revocation behavior against deployment | Application maintainer |
| 7.2.3 | 1 | Pass: source assessment 2026-09-10 | `internal/auth/session.go; internal/auth/transaction.go` | go test ./internal/auth; inspect timeout, cookie and revocation behavior against deployment | Application maintainer |
| 7.2.4 | 1 | Pass: source assessment 2026-09-10 | `internal/auth/session.go; internal/auth/transaction.go` | go test ./internal/auth; inspect timeout, cookie and revocation behavior against deployment | Application maintainer |
| 7.3.1 | 2 | Pass: source assessment 2026-09-10 | `internal/auth/session.go; internal/auth/transaction.go` | go test ./internal/auth; inspect timeout, cookie and revocation behavior against deployment | Application maintainer |
| 7.3.2 | 2 | Pass: source assessment 2026-09-10 | `internal/auth/session.go; internal/auth/transaction.go` | go test ./internal/auth; inspect timeout, cookie and revocation behavior against deployment | Application maintainer |
| 7.4.1 | 1 | Pass: source assessment 2026-09-10 | `internal/auth/session.go; internal/auth/transaction.go` | go test ./internal/auth; inspect timeout, cookie and revocation behavior against deployment | Application maintainer |
| 7.4.2 | 1 | Fail: Pocket ID account disablement is enforced only when the 15-minute local authorization lifetime expires | `internal/auth/session.go; internal/auth/transaction.go` | go test ./internal/auth; inspect timeout, cookie and revocation behavior against deployment | Application maintainer |
| 7.4.3 | 2 | N/A: Authentication-factor management exists only in Pocket ID, not this application | `internal/auth/session.go; internal/auth/transaction.go` | go test ./internal/auth; inspect timeout, cookie and revocation behavior against deployment | Application maintainer |
| 7.4.4 | 2 | Pass: source assessment 2026-09-10 | `internal/auth/session.go; internal/auth/transaction.go` | go test ./internal/auth; inspect timeout, cookie and revocation behavior against deployment | Application maintainer |
| 7.4.5 | 2 | Fail: no supported administrator function terminates one user or all application sessions | `internal/auth/session.go; internal/auth/transaction.go` | go test ./internal/auth; inspect timeout, cookie and revocation behavior against deployment | Application maintainer |
| 7.5.1 | 2 | N/A: The application has no local authentication or recovery attributes | `internal/auth/session.go; internal/auth/transaction.go` | go test ./internal/auth; inspect timeout, cookie and revocation behavior against deployment | Application maintainer |
| 7.5.2 | 2 | Fail: users cannot view and terminate their active application sessions | `internal/auth/session.go; internal/auth/transaction.go` | go test ./internal/auth; inspect timeout, cookie and revocation behavior against deployment | Application maintainer |
| 7.6.1 | 2 | Operator pending: verify Pocket ID and relying-party lifetime/termination behavior | `internal/auth/session.go; internal/auth/transaction.go` | go test ./internal/auth; inspect timeout, cookie and revocation behavior against deployment | Application maintainer |
| 7.6.2 | 2 | Pass: source assessment 2026-09-10 | `internal/auth/session.go; internal/auth/transaction.go` | go test ./internal/auth; inspect timeout, cookie and revocation behavior against deployment | Application maintainer |

## V8 Authorization

Normative source: [0x17-V8-Authorization.md](https://github.com/OWASP/ASVS/blob/v5.0.0/5.0/en/0x17-V8-Authorization.md); downloaded source SHA-256 `60c188b1703cab2086841d8f1b4adc0ededf66ed38c676a11bb92347646b5330`.

| Control | Level | Applicability / result | Implementation location | Automated evidence or manual verification | Result owner |
| --- | --- | --- | --- | --- | --- |
| 8.1.1 | 1 | Pass: source assessment 2026-09-10 | `internal/auth; internal/web` | go test ./internal/auth ./internal/web; inspect invoice-admins membership | Application maintainer + identity operator |
| 8.1.2 | 2 | Pass: source assessment 2026-09-10 | `internal/auth; internal/web` | go test ./internal/auth ./internal/web; inspect invoice-admins membership | Application maintainer + identity operator |
| 8.2.1 | 1 | Pass: source assessment 2026-09-10 | `internal/auth; internal/web` | go test ./internal/auth ./internal/web; inspect invoice-admins membership | Application maintainer + identity operator |
| 8.2.2 | 1 | Pass: source assessment 2026-09-10 | `internal/auth; internal/web` | go test ./internal/auth ./internal/web; inspect invoice-admins membership | Application maintainer + identity operator |
| 8.2.3 | 2 | Pass: source assessment 2026-09-10 | `internal/auth; internal/web` | go test ./internal/auth ./internal/web; inspect invoice-admins membership | Application maintainer + identity operator |
| 8.3.1 | 1 | Pass: source assessment 2026-09-10 | `internal/auth; internal/web` | go test ./internal/auth ./internal/web; inspect invoice-admins membership | Application maintainer + identity operator |
| 8.4.1 | 2 | N/A: The application is single-tenant with one administrator role | `internal/auth; internal/web` | go test ./internal/auth ./internal/web; inspect invoice-admins membership | Application maintainer + identity operator |

## V9 Self-contained Tokens

Normative source: [0x18-V9-Self-contained-Tokens.md](https://github.com/OWASP/ASVS/blob/v5.0.0/5.0/en/0x18-V9-Self-contained-Tokens.md); downloaded source SHA-256 `5f14e09fb67892e805ad386959eb3a8bafca03271dbb502ccf7323e82b3f812c`.

| Control | Level | Applicability / result | Implementation location | Automated evidence or manual verification | Result owner |
| --- | --- | --- | --- | --- | --- |
| 9.1.1 | 1 | Pass: source assessment 2026-09-10 | `internal/auth/oidc.go; internal/auth/hardening_test.go` | go test ./internal/auth; adversarial JWT signature/issuer/audience/nonce/time assertions | Application maintainer |
| 9.1.2 | 1 | Pass: source assessment 2026-09-10 | `internal/auth/oidc.go; internal/auth/hardening_test.go` | go test ./internal/auth; adversarial JWT signature/issuer/audience/nonce/time assertions | Application maintainer |
| 9.1.3 | 1 | Pass: source assessment 2026-09-10 | `internal/auth/oidc.go; internal/auth/hardening_test.go` | go test ./internal/auth; adversarial JWT signature/issuer/audience/nonce/time assertions | Application maintainer |
| 9.2.1 | 1 | Pass: source assessment 2026-09-10 | `internal/auth/oidc.go; internal/auth/hardening_test.go` | go test ./internal/auth; adversarial JWT signature/issuer/audience/nonce/time assertions | Application maintainer |
| 9.2.2 | 2 | Pass: source assessment 2026-09-10 | `internal/auth/oidc.go; internal/auth/hardening_test.go` | go test ./internal/auth; adversarial JWT signature/issuer/audience/nonce/time assertions | Application maintainer |
| 9.2.3 | 2 | Pass: source assessment 2026-09-10 | `internal/auth/oidc.go; internal/auth/hardening_test.go` | go test ./internal/auth; adversarial JWT signature/issuer/audience/nonce/time assertions | Application maintainer |
| 9.2.4 | 2 | Operator pending: verify Pocket ID signing-key and audience isolation policy | `internal/auth/oidc.go; internal/auth/hardening_test.go` | go test ./internal/auth; adversarial JWT signature/issuer/audience/nonce/time assertions | Application maintainer |

## V10 OAuth and OIDC

Normative source: [0x19-V10-OAuth-and-OIDC.md](https://github.com/OWASP/ASVS/blob/v5.0.0/5.0/en/0x19-V10-OAuth-and-OIDC.md); downloaded source SHA-256 `2da443986ba40987bd0908bbd7da2372ab0d777701cc590f4eb0fdad6ae804ed`.

| Control | Level | Applicability / result | Implementation location | Automated evidence or manual verification | Result owner |
| --- | --- | --- | --- | --- | --- |
| 10.1.1 | 2 | Pass: source assessment 2026-09-10 | `internal/auth/oidc.go; internal/auth/transaction.go` | go test ./internal/auth; inspect actual Pocket ID client settings | Application maintainer + identity operator |
| 10.1.2 | 2 | Pass: source assessment 2026-09-10 | `internal/auth/oidc.go; internal/auth/transaction.go` | go test ./internal/auth; inspect actual Pocket ID client settings | Application maintainer + identity operator |
| 10.2.1 | 2 | Pass: source assessment 2026-09-10 | `internal/auth/oidc.go; internal/auth/transaction.go` | go test ./internal/auth; inspect actual Pocket ID client settings | Application maintainer + identity operator |
| 10.2.2 | 2 | N/A: One fixed production issuer | `internal/auth/oidc.go; internal/auth/transaction.go` | Review feature scope when adding a new interpreter or protocol | Application maintainer + identity operator |
| 10.3.1 | 2 | N/A: application is not an OAuth resource server | `Pocket ID; docs/container-deployment.md` | Inspect provider grant, client, scope, consent and token lifecycle settings | Application maintainer + identity operator |
| 10.3.2 | 2 | N/A: application is not an OAuth resource server | `Pocket ID; docs/container-deployment.md` | Inspect provider grant, client, scope, consent and token lifecycle settings | Application maintainer + identity operator |
| 10.3.3 | 2 | N/A: application is not an OAuth resource server | `Pocket ID; docs/container-deployment.md` | Inspect provider grant, client, scope, consent and token lifecycle settings | Application maintainer + identity operator |
| 10.3.4 | 2 | N/A: application is not an OAuth resource server | `Pocket ID; docs/container-deployment.md` | Inspect provider grant, client, scope, consent and token lifecycle settings | Application maintainer + identity operator |
| 10.4.1 | 1 | Operator pending: provider boundary | `Pocket ID; docs/container-deployment.md` | Inspect provider grant, client, scope, consent and token lifecycle settings | Application maintainer + identity operator |
| 10.4.2 | 1 | Operator pending: provider boundary | `Pocket ID; docs/container-deployment.md` | Inspect provider grant, client, scope, consent and token lifecycle settings | Application maintainer + identity operator |
| 10.4.3 | 1 | Operator pending: provider boundary | `Pocket ID; docs/container-deployment.md` | Inspect provider grant, client, scope, consent and token lifecycle settings | Application maintainer + identity operator |
| 10.4.4 | 1 | Operator pending: provider boundary | `Pocket ID; docs/container-deployment.md` | Inspect provider grant, client, scope, consent and token lifecycle settings | Application maintainer + identity operator |
| 10.4.5 | 1 | Operator pending: provider boundary | `Pocket ID; docs/container-deployment.md` | Inspect provider grant, client, scope, consent and token lifecycle settings | Application maintainer + identity operator |
| 10.4.6 | 2 | Operator pending: provider boundary | `Pocket ID; docs/container-deployment.md` | Inspect provider grant, client, scope, consent and token lifecycle settings | Application maintainer + identity operator |
| 10.4.7 | 2 | Operator pending: provider boundary | `Pocket ID; docs/container-deployment.md` | Inspect provider grant, client, scope, consent and token lifecycle settings | Application maintainer + identity operator |
| 10.4.8 | 2 | Operator pending: provider boundary | `Pocket ID; docs/container-deployment.md` | Inspect provider grant, client, scope, consent and token lifecycle settings | Application maintainer + identity operator |
| 10.4.9 | 2 | Operator pending: provider boundary | `Pocket ID; docs/container-deployment.md` | Inspect provider grant, client, scope, consent and token lifecycle settings | Application maintainer + identity operator |
| 10.4.10 | 2 | Operator pending: provider boundary | `Pocket ID; docs/container-deployment.md` | Inspect provider grant, client, scope, consent and token lifecycle settings | Application maintainer + identity operator |
| 10.4.11 | 2 | Operator pending: provider boundary | `Pocket ID; docs/container-deployment.md` | Inspect provider grant, client, scope, consent and token lifecycle settings | Application maintainer + identity operator |
| 10.5.1 | 2 | Pass: source assessment 2026-09-10 | `internal/auth/oidc.go; internal/auth/transaction.go` | go test ./internal/auth; inspect actual Pocket ID client settings | Application maintainer + identity operator |
| 10.5.2 | 2 | Pass: source assessment 2026-09-10 | `internal/auth/oidc.go; internal/auth/transaction.go` | go test ./internal/auth; inspect actual Pocket ID client settings | Application maintainer + identity operator |
| 10.5.3 | 2 | Pass: source assessment 2026-09-10 | `internal/auth/oidc.go; internal/auth/transaction.go` | go test ./internal/auth; inspect actual Pocket ID client settings | Application maintainer + identity operator |
| 10.5.4 | 2 | Pass: source assessment 2026-09-10 | `internal/auth/oidc.go; internal/auth/transaction.go` | go test ./internal/auth; inspect actual Pocket ID client settings | Application maintainer + identity operator |
| 10.5.5 | 2 | N/A: No OIDC back-channel logout | `internal/auth/oidc.go; internal/auth/transaction.go` | Review feature scope when adding a new interpreter or protocol | Application maintainer + identity operator |
| 10.6.1 | 2 | Operator pending: provider boundary | `Pocket ID; docs/container-deployment.md` | Inspect provider grant, client, scope, consent and token lifecycle settings | Application maintainer + identity operator |
| 10.6.2 | 2 | Operator pending: provider boundary | `Pocket ID; docs/container-deployment.md` | Inspect provider grant, client, scope, consent and token lifecycle settings | Application maintainer + identity operator |
| 10.7.1 | 2 | Operator pending: provider boundary | `Pocket ID; docs/container-deployment.md` | Inspect provider grant, client, scope, consent and token lifecycle settings | Application maintainer + identity operator |
| 10.7.2 | 2 | Operator pending: provider boundary | `Pocket ID; docs/container-deployment.md` | Inspect provider grant, client, scope, consent and token lifecycle settings | Application maintainer + identity operator |
| 10.7.3 | 2 | Operator pending: provider boundary | `Pocket ID; docs/container-deployment.md` | Inspect provider grant, client, scope, consent and token lifecycle settings | Application maintainer + identity operator |

## V11 Cryptography

Normative source: [0x20-V11-Cryptography.md](https://github.com/OWASP/ASVS/blob/v5.0.0/5.0/en/0x20-V11-Cryptography.md); downloaded source SHA-256 `a64f3f2dc6f53565dfba66e40fd336b50eab6cdb530c2791f62db8b58202bacf`.

| Control | Level | Applicability / result | Implementation location | Automated evidence or manual verification | Result owner |
| --- | --- | --- | --- | --- | --- |
| 11.1.1 | 2 | Operator pending | `internal/auth; internal/store/backup.go; docs/container-deployment.md` | go test ./internal/auth ./internal/store; review crypto inventory and operator key lifecycle | Application maintainer + operator |
| 11.1.2 | 2 | Operator pending | `internal/auth; internal/store/backup.go; docs/container-deployment.md` | go test ./internal/auth ./internal/store; review crypto inventory and operator key lifecycle | Application maintainer + operator |
| 11.2.1 | 2 | Pass: source assessment 2026-09-10 | `internal/auth; internal/store/backup.go; docs/container-deployment.md` | go test ./internal/auth ./internal/store; review crypto inventory and operator key lifecycle | Application maintainer + operator |
| 11.2.2 | 2 | Pass: source assessment 2026-09-10 | `internal/auth; internal/store/backup.go; docs/container-deployment.md` | go test ./internal/auth ./internal/store; review crypto inventory and operator key lifecycle | Application maintainer + operator |
| 11.2.3 | 2 | Pass: source assessment 2026-09-10 | `internal/auth; internal/store/backup.go; docs/container-deployment.md` | go test ./internal/auth ./internal/store; review crypto inventory and operator key lifecycle | Application maintainer + operator |
| 11.3.1 | 1 | Pass: source assessment 2026-09-10 | `internal/auth; internal/store/backup.go; docs/container-deployment.md` | go test ./internal/auth ./internal/store; review crypto inventory and operator key lifecycle | Application maintainer + operator |
| 11.3.2 | 1 | Pass: source assessment 2026-09-10 | `internal/auth; internal/store/backup.go; docs/container-deployment.md` | go test ./internal/auth ./internal/store; review crypto inventory and operator key lifecycle | Application maintainer + operator |
| 11.3.3 | 2 | Pass: source assessment 2026-09-10 | `internal/auth; internal/store/backup.go; docs/container-deployment.md` | go test ./internal/auth ./internal/store; review crypto inventory and operator key lifecycle | Application maintainer + operator |
| 11.4.1 | 1 | Pass: source assessment 2026-09-10 | `internal/auth; internal/store/backup.go; docs/container-deployment.md` | go test ./internal/auth ./internal/store; review crypto inventory and operator key lifecycle | Application maintainer + operator |
| 11.4.2 | 2 | N/A: No locally stored user passwords | `internal/auth; internal/store/backup.go; docs/container-deployment.md` | Review feature scope when adding a new interpreter or protocol | Application maintainer + operator |
| 11.4.3 | 2 | Pass: source assessment 2026-09-10 | `internal/auth; internal/store/backup.go; docs/container-deployment.md` | go test ./internal/auth ./internal/store; review crypto inventory and operator key lifecycle | Application maintainer + operator |
| 11.4.4 | 2 | N/A: Backup key is random, not password-derived | `internal/auth; internal/store/backup.go; docs/container-deployment.md` | Review feature scope when adding a new interpreter or protocol | Application maintainer + operator |
| 11.5.1 | 2 | Pass: source assessment 2026-09-10 | `internal/auth; internal/store/backup.go; docs/container-deployment.md` | go test ./internal/auth ./internal/store; review crypto inventory and operator key lifecycle | Application maintainer + operator |
| 11.6.1 | 2 | Pass: source assessment 2026-09-10 | `internal/auth; internal/store/backup.go; docs/container-deployment.md` | go test ./internal/auth ./internal/store; review crypto inventory and operator key lifecycle | Application maintainer + operator |

## V12 Secure Communication

Normative source: [0x21-V12-Secure-Communication.md](https://github.com/OWASP/ASVS/blob/v5.0.0/5.0/en/0x21-V12-Secure-Communication.md); downloaded source SHA-256 `62f7737e74172d3d2177bd838c1bc44461d0a70c37cfd09d9e4669252b480775`.

| Control | Level | Applicability / result | Implementation location | Automated evidence or manual verification | Result owner |
| --- | --- | --- | --- | --- | --- |
| 12.1.1 | 1 | Operator pending | `internal/auth/oidc.go; internal/paperless/client.go; docs/container-deployment.md` | go test ./internal/auth ./internal/paperless; run documented TLS chain/hostname/hybrid probes on every live hop | Operator |
| 12.1.2 | 2 | Operator pending | `internal/auth/oidc.go; internal/paperless/client.go; docs/container-deployment.md` | go test ./internal/auth ./internal/paperless; run documented TLS chain/hostname/hybrid probes on every live hop | Operator |
| 12.1.3 | 2 | Operator pending | `internal/auth/oidc.go; internal/paperless/client.go; docs/container-deployment.md` | Review feature scope when adding a new interpreter or protocol | Operator |
| 12.2.1 | 1 | Operator pending | `internal/auth/oidc.go; internal/paperless/client.go; docs/container-deployment.md` | go test ./internal/auth ./internal/paperless; run documented TLS chain/hostname/hybrid probes on every live hop | Operator |
| 12.2.2 | 1 | Operator pending | `internal/auth/oidc.go; internal/paperless/client.go; docs/container-deployment.md` | go test ./internal/auth ./internal/paperless; run documented TLS chain/hostname/hybrid probes on every live hop | Operator |
| 12.3.1 | 2 | Operator pending | `internal/auth/oidc.go; internal/paperless/client.go; docs/container-deployment.md` | go test ./internal/auth ./internal/paperless; run documented TLS chain/hostname/hybrid probes on every live hop | Operator |
| 12.3.2 | 2 | Operator pending | `internal/auth/oidc.go; internal/paperless/client.go; docs/container-deployment.md` | go test ./internal/auth ./internal/paperless; run documented TLS chain/hostname/hybrid probes on every live hop | Operator |
| 12.3.3 | 2 | Operator pending | `internal/auth/oidc.go; internal/paperless/client.go; docs/container-deployment.md` | go test ./internal/auth ./internal/paperless; run documented TLS chain/hostname/hybrid probes on every live hop | Operator |
| 12.3.4 | 2 | Operator pending | `internal/auth/oidc.go; internal/paperless/client.go; docs/container-deployment.md` | go test ./internal/auth ./internal/paperless; run documented TLS chain/hostname/hybrid probes on every live hop | Operator |

## V13 Configuration

Normative source: [0x22-V13-Configuration.md](https://github.com/OWASP/ASVS/blob/v5.0.0/5.0/en/0x22-V13-Configuration.md); downloaded source SHA-256 `f66ef1306eba07e04fcd0f23b52d082747f1b4fba0a19934167fb35507afba6d`.

| Control | Level | Applicability / result | Implementation location | Automated evidence or manual verification | Result owner |
| --- | --- | --- | --- | --- | --- |
| 13.1.1 | 2 | Pass: source assessment 2026-09-10 | `Dockerfile; compose.yaml; cmd/server; docs/container-deployment.md` | scripts/check-container-security.sh; scripts/ci-local.sh; inspect real secret mounts and firewall | Application maintainer + operator |
| 13.2.1 | 2 | Fail: Paperless service authentication uses a long-lived API token | `Dockerfile; compose.yaml; cmd/server; docs/container-deployment.md` | scripts/check-container-security.sh; scripts/ci-local.sh; inspect real secret mounts and firewall | Application maintainer + operator |
| 13.2.2 | 2 | Operator pending: verify least-privilege Paperless service identity and host accounts | `Dockerfile; compose.yaml; cmd/server; docs/container-deployment.md` | scripts/check-container-security.sh; scripts/ci-local.sh; inspect real secret mounts and firewall | Application maintainer + operator |
| 13.2.3 | 2 | Pass: source assessment 2026-09-10 | `Dockerfile; compose.yaml; cmd/server; docs/container-deployment.md` | scripts/check-container-security.sh; scripts/ci-local.sh; inspect real secret mounts and firewall | Application maintainer + operator |
| 13.2.4 | 2 | Pass: source assessment 2026-09-10 | `Dockerfile; compose.yaml; cmd/server; docs/container-deployment.md` | scripts/check-container-security.sh; scripts/ci-local.sh; inspect real secret mounts and firewall | Application maintainer + operator |
| 13.2.5 | 2 | Pass: source assessment 2026-09-10 | `Dockerfile; compose.yaml; cmd/server; docs/container-deployment.md` | scripts/check-container-security.sh; scripts/ci-local.sh; inspect real secret mounts and firewall | Application maintainer + operator |
| 13.3.1 | 2 | Operator pending | `Dockerfile; compose.yaml; cmd/server; docs/container-deployment.md` | scripts/check-container-security.sh; scripts/ci-local.sh; inspect real secret mounts and firewall | Application maintainer + operator |
| 13.3.2 | 2 | Operator pending | `Dockerfile; compose.yaml; cmd/server; docs/container-deployment.md` | scripts/check-container-security.sh; scripts/ci-local.sh; inspect real secret mounts and firewall | Application maintainer + operator |
| 13.4.1 | 1 | Pass: source assessment 2026-09-10 | `Dockerfile; compose.yaml; cmd/server; docs/container-deployment.md` | scripts/check-container-security.sh; scripts/ci-local.sh; inspect real secret mounts and firewall | Application maintainer + operator |
| 13.4.2 | 2 | Pass: source assessment 2026-09-10 | `Dockerfile; compose.yaml; cmd/server; docs/container-deployment.md` | scripts/check-container-security.sh; scripts/ci-local.sh; inspect real secret mounts and firewall | Application maintainer + operator |
| 13.4.3 | 2 | Pass: source assessment 2026-09-10 | `Dockerfile; compose.yaml; cmd/server; docs/container-deployment.md` | scripts/check-container-security.sh; scripts/ci-local.sh; inspect real secret mounts and firewall | Application maintainer + operator |
| 13.4.4 | 2 | Pass: source assessment 2026-09-10 | `Dockerfile; compose.yaml; cmd/server; docs/container-deployment.md` | scripts/check-container-security.sh; scripts/ci-local.sh; inspect real secret mounts and firewall | Application maintainer + operator |
| 13.4.5 | 2 | Pass: source assessment 2026-09-10 | `Dockerfile; compose.yaml; cmd/server; docs/container-deployment.md` | scripts/check-container-security.sh; scripts/ci-local.sh; inspect real secret mounts and firewall | Application maintainer + operator |

## V14 Data Protection

Normative source: [0x23-V14-Data-Protection.md](https://github.com/OWASP/ASVS/blob/v5.0.0/5.0/en/0x23-V14-Data-Protection.md); downloaded source SHA-256 `a9ba33b7c77379fed0f27fd019945e10fa143c63b8c4e4783b7cf587c9f56039`.

| Control | Level | Applicability / result | Implementation location | Automated evidence or manual verification | Result owner |
| --- | --- | --- | --- | --- | --- |
| 14.1.1 | 2 | Operator pending | `internal/web; internal/store; docs/server-storage.md` | go test ./internal/web ./internal/store; inspect encrypted host storage, retention and browser logout/cache behavior | Data owner + operator |
| 14.1.2 | 2 | Operator pending | `internal/web; internal/store; docs/server-storage.md` | go test ./internal/web ./internal/store; inspect encrypted host storage, retention and browser logout/cache behavior | Data owner + operator |
| 14.2.1 | 1 | Operator pending | `internal/web; internal/store; docs/server-storage.md` | go test ./internal/web ./internal/store; inspect encrypted host storage, retention and browser logout/cache behavior | Data owner + operator |
| 14.2.2 | 2 | Operator pending | `internal/web; internal/store; docs/server-storage.md` | go test ./internal/web ./internal/store; inspect encrypted host storage, retention and browser logout/cache behavior | Data owner + operator |
| 14.2.3 | 2 | Operator pending | `internal/web; internal/store; docs/server-storage.md` | go test ./internal/web ./internal/store; inspect encrypted host storage, retention and browser logout/cache behavior | Data owner + operator |
| 14.2.4 | 2 | Operator pending | `internal/web; internal/store; docs/server-storage.md` | go test ./internal/web ./internal/store; inspect encrypted host storage, retention and browser logout/cache behavior | Data owner + operator |
| 14.3.1 | 1 | Operator pending | `internal/web; internal/store; docs/server-storage.md` | go test ./internal/web ./internal/store; inspect encrypted host storage, retention and browser logout/cache behavior | Data owner + operator |
| 14.3.2 | 2 | Operator pending | `internal/web; internal/store; docs/server-storage.md` | go test ./internal/web ./internal/store; inspect encrypted host storage, retention and browser logout/cache behavior | Data owner + operator |
| 14.3.3 | 2 | Operator pending | `internal/web; internal/store; docs/server-storage.md` | go test ./internal/web ./internal/store; inspect encrypted host storage, retention and browser logout/cache behavior | Data owner + operator |

## V15 Secure Coding and Architecture

Normative source: [0x24-V15-Secure-Coding-and-Architecture.md](https://github.com/OWASP/ASVS/blob/v5.0.0/5.0/en/0x24-V15-Secure-Coding-and-Architecture.md); downloaded source SHA-256 `4c068e82b157159e9ab7099daecb40af4c2642ad791751f791a26785dead6b22`.

| Control | Level | Applicability / result | Implementation location | Automated evidence or manual verification | Result owner |
| --- | --- | --- | --- | --- | --- |
| 15.1.1 | 1 | Pass: source assessment 2026-09-10 | `Dockerfile; .github/workflows; internal/jobs; internal/web` | scripts/ci-local.sh; Security required; inspect SBOM and remediation records | Security maintainer |
| 15.1.2 | 2 | Pass: source assessment 2026-09-10 | `Dockerfile; .github/workflows; internal/jobs; internal/web` | scripts/ci-local.sh; Security required; inspect SBOM and remediation records | Security maintainer |
| 15.1.3 | 2 | Pass: source assessment 2026-09-10 | `Dockerfile; .github/workflows; internal/jobs; internal/web` | scripts/ci-local.sh; Security required; inspect SBOM and remediation records | Security maintainer |
| 15.2.1 | 1 | Review pending: current Alpine runtime image requires a completed vulnerability scan | `Dockerfile; .github/workflows; internal/jobs; internal/web` | scripts/ci-local.sh; Security required; inspect SBOM and remediation records | Security maintainer |
| 15.2.2 | 2 | Pass: source assessment 2026-09-10 | `Dockerfile; .github/workflows; internal/jobs; internal/web` | scripts/ci-local.sh; Security required; inspect SBOM and remediation records | Security maintainer |
| 15.2.3 | 2 | Pass: source assessment 2026-09-10 | `Dockerfile; .github/workflows; internal/jobs; internal/web` | scripts/ci-local.sh; Security required; inspect SBOM and remediation records | Security maintainer |
| 15.3.1 | 1 | Pass: source assessment 2026-09-10 | `Dockerfile; .github/workflows; internal/jobs; internal/web` | scripts/ci-local.sh; Security required; inspect SBOM and remediation records | Security maintainer |
| 15.3.2 | 2 | Pass: source assessment 2026-09-10 | `Dockerfile; .github/workflows; internal/jobs; internal/web` | scripts/ci-local.sh; Security required; inspect SBOM and remediation records | Security maintainer |
| 15.3.3 | 2 | Pass: source assessment 2026-09-10 | `Dockerfile; .github/workflows; internal/jobs; internal/web` | scripts/ci-local.sh; Security required; inspect SBOM and remediation records | Security maintainer |
| 15.3.4 | 2 | Pass: source assessment 2026-09-10 | `Dockerfile; .github/workflows; internal/jobs; internal/web` | scripts/ci-local.sh; Security required; inspect SBOM and remediation records | Security maintainer |
| 15.3.5 | 2 | Pass: source assessment 2026-09-10 | `Dockerfile; .github/workflows; internal/jobs; internal/web` | scripts/ci-local.sh; Security required; inspect SBOM and remediation records | Security maintainer |
| 15.3.6 | 2 | Pass: source assessment 2026-09-10 | `Dockerfile; .github/workflows; internal/jobs; internal/web` | scripts/ci-local.sh; Security required; inspect SBOM and remediation records | Security maintainer |
| 15.3.7 | 2 | Pass: source assessment 2026-09-10 | `Dockerfile; .github/workflows; internal/jobs; internal/web` | scripts/ci-local.sh; Security required; inspect SBOM and remediation records | Security maintainer |

## V16 Security Logging and Error Handling

Normative source: [0x25-V16-Security-Logging-and-Error-Handling.md](https://github.com/OWASP/ASVS/blob/v5.0.0/5.0/en/0x25-V16-Security-Logging-and-Error-Handling.md); downloaded source SHA-256 `23b0c0d4a54cc62a53e8669cbd15bb97f323249e9d837d3055f95873b2ef1652`.

| Control | Level | Applicability / result | Implementation location | Automated evidence or manual verification | Result owner |
| --- | --- | --- | --- | --- | --- |
| 16.1.1 | 2 | Operator pending | `internal/web; internal/store; cmd/server; docs/container-deployment.md` | go test ./internal/web ./internal/store ./cmd/server; inspect actual log inventory, redaction and remote alert delivery | Security maintainer + operator |
| 16.2.1 | 2 | Operator pending | `internal/web; internal/store; cmd/server; docs/container-deployment.md` | go test ./internal/web ./internal/store ./cmd/server; inspect actual log inventory, redaction and remote alert delivery | Security maintainer + operator |
| 16.2.2 | 2 | Operator pending | `internal/web; internal/store; cmd/server; docs/container-deployment.md` | go test ./internal/web ./internal/store ./cmd/server; inspect actual log inventory, redaction and remote alert delivery | Security maintainer + operator |
| 16.2.3 | 2 | Operator pending | `internal/web; internal/store; cmd/server; docs/container-deployment.md` | go test ./internal/web ./internal/store ./cmd/server; inspect actual log inventory, redaction and remote alert delivery | Security maintainer + operator |
| 16.2.4 | 2 | Operator pending | `internal/web; internal/store; cmd/server; docs/container-deployment.md` | go test ./internal/web ./internal/store ./cmd/server; inspect actual log inventory, redaction and remote alert delivery | Security maintainer + operator |
| 16.2.5 | 2 | Operator pending | `internal/web; internal/store; cmd/server; docs/container-deployment.md` | go test ./internal/web ./internal/store ./cmd/server; inspect actual log inventory, redaction and remote alert delivery | Security maintainer + operator |
| 16.3.1 | 2 | Operator pending | `internal/web; internal/store; cmd/server; docs/container-deployment.md` | go test ./internal/web ./internal/store ./cmd/server; inspect actual log inventory, redaction and remote alert delivery | Security maintainer + operator |
| 16.3.2 | 2 | Operator pending | `internal/web; internal/store; cmd/server; docs/container-deployment.md` | go test ./internal/web ./internal/store ./cmd/server; inspect actual log inventory, redaction and remote alert delivery | Security maintainer + operator |
| 16.3.3 | 2 | Operator pending | `internal/web; internal/store; cmd/server; docs/container-deployment.md` | go test ./internal/web ./internal/store ./cmd/server; inspect actual log inventory, redaction and remote alert delivery | Security maintainer + operator |
| 16.3.4 | 2 | Operator pending | `internal/web; internal/store; cmd/server; docs/container-deployment.md` | go test ./internal/web ./internal/store ./cmd/server; inspect actual log inventory, redaction and remote alert delivery | Security maintainer + operator |
| 16.4.1 | 2 | Operator pending | `internal/web; internal/store; cmd/server; docs/container-deployment.md` | go test ./internal/web ./internal/store ./cmd/server; inspect actual log inventory, redaction and remote alert delivery | Security maintainer + operator |
| 16.4.2 | 2 | Operator pending | `internal/web; internal/store; cmd/server; docs/container-deployment.md` | go test ./internal/web ./internal/store ./cmd/server; inspect actual log inventory, redaction and remote alert delivery | Security maintainer + operator |
| 16.4.3 | 2 | Operator pending | `internal/web; internal/store; cmd/server; docs/container-deployment.md` | go test ./internal/web ./internal/store ./cmd/server; inspect actual log inventory, redaction and remote alert delivery | Security maintainer + operator |
| 16.5.1 | 2 | Operator pending | `internal/web; internal/store; cmd/server; docs/container-deployment.md` | go test ./internal/web ./internal/store ./cmd/server; inspect actual log inventory, redaction and remote alert delivery | Security maintainer + operator |
| 16.5.2 | 2 | Operator pending | `internal/web; internal/store; cmd/server; docs/container-deployment.md` | go test ./internal/web ./internal/store ./cmd/server; inspect actual log inventory, redaction and remote alert delivery | Security maintainer + operator |
| 16.5.3 | 2 | Operator pending | `internal/web; internal/store; cmd/server; docs/container-deployment.md` | go test ./internal/web ./internal/store ./cmd/server; inspect actual log inventory, redaction and remote alert delivery | Security maintainer + operator |

## V17 WebRTC

Normative source: [0x26-V17-WebRTC.md](https://github.com/OWASP/ASVS/blob/v5.0.0/5.0/en/0x26-V17-WebRTC.md); downloaded source SHA-256 `29a55aed304efc92f6bf827aeee3323671d0521ddab823f8e6265018ce0ff2c0`.

| Control | Level | Applicability / result | Implementation location | Automated evidence or manual verification | Result owner |
| --- | --- | --- | --- | --- | --- |
| 17.1.1 | 2 | N/A: no real-time communication | `docs/superpowers/specs/2026-09-06-web-application-design.md` | Review scope: no WebRTC, TURN, media or signaling services | Application maintainer |
| 17.2.1 | 2 | N/A: no real-time communication | `docs/superpowers/specs/2026-09-06-web-application-design.md` | Review scope: no WebRTC, TURN, media or signaling services | Application maintainer |
| 17.2.2 | 2 | N/A: no real-time communication | `docs/superpowers/specs/2026-09-06-web-application-design.md` | Review scope: no WebRTC, TURN, media or signaling services | Application maintainer |
| 17.2.3 | 2 | N/A: no real-time communication | `docs/superpowers/specs/2026-09-06-web-application-design.md` | Review scope: no WebRTC, TURN, media or signaling services | Application maintainer |
| 17.2.4 | 2 | N/A: no real-time communication | `docs/superpowers/specs/2026-09-06-web-application-design.md` | Review scope: no WebRTC, TURN, media or signaling services | Application maintainer |
| 17.3.1 | 2 | N/A: no real-time communication | `docs/superpowers/specs/2026-09-06-web-application-design.md` | Review scope: no WebRTC, TURN, media or signaling services | Application maintainer |
| 17.3.2 | 2 | N/A: no real-time communication | `docs/superpowers/specs/2026-09-06-web-application-design.md` | Review scope: no WebRTC, TURN, media or signaling services | Application maintainer |

## Assessment record

This register enumerates 253 L1/L2 controls. For each pending row the owner records date, tested commit or deployment identifier, command/report reference, result and any time-bounded exception in the release review. High/critical unmitigated findings block production; N/A decisions need an explicit feature-boundary reason. Reassess after any identity, proxy, renderer, backup, dependency or authorization change.

OWASP ASVS is licensed CC BY-SA 4.0; this register references control identifiers and paraphrases applicability without reproducing normative requirement text.
