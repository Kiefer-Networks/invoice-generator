# Automatic Paperless delivery

After an immutable invoice PDF is marked ready, SQLite creates its Paperless job in the same transaction. One background worker resolves these fixed tags and uploads the verified PDF:

- Kiefer Networks
- Rechnung
- Steuer
- Umsatzsteuer
- Gewerbesteuer
- INBOX

Set `INVOICE_PAPERLESS_URL` (or `--paperless-url`) to the administrator-controlled HTTPS origin, optionally with its installation path. Set `INVOICE_PAPERLESS_TOKEN_FILE` (or `--paperless-token-file`) to a separately mounted API token file. Do not put the token in a URL, command-line argument, environment variable, or repository. On POSIX the file must be owner-only (0600), regular, and at most 4096 bytes. On Windows provision an equivalent owner-only ACL. Symlinks are rejected. Token files are reread on every attempt, allowing mounted credential rotation.

An absent URL or token does not prevent invoice generation or server startup. The delivery job displays a configuration error and retries automatically within the attempt bound. After configuring delivery, use the authenticated **Retry Paperless delivery** form if the job is failed. An invalid configured URL or an existing insecure token file fails server validation. Development mode cannot use production Paperless URLs or token files.

The HTTP client uses the OS CA trust store, verified TLS 1.3, no environment proxy, no redirects, a five-second DNS/connect and TLS handshake bound, a ten-second response-header bound, and a one-minute total worker deadline. Response bodies are limited to 1 MiB; PDF input is limited to 20 MiB and verified against the stored SHA-256 before submission. Private RFC1918, IPv6 ULA, and NetBird CGNAT addresses are supported. Each connection resolves only the fixed configured hostname and validates every returned address before dialing the validated IP. Loopback, link-local/metadata, multicast, unspecified, reserved, and IPv6 translation/tunnel destinations are rejected in production. The CLI retains loopback HTTP fixture support and caller-selected tags.

Jobs use five-minute leases, fencing tokens, and at most five attempts, including process crashes. Retry delays start at 30 seconds, double, and add 0–25% jitter. Each attempt polls the remote task at most once; queued work is checked every 30 seconds. The invoice page shows queued, processing, delivered, or failed state, a safe error summary, and the delivered remote document ID. Delivery transitions and manual retries are audited without upstream response bodies or credentials.

The stable title contains the immutable invoice number and document ID. Before sending, the worker persists an upload-intent marker; after acceptance it persists the remote task ID, then the remote document ID after consumption. Restart first searches that exact title and resumes polling the existing task. A definite pre-consumption HTTP rejection (including authentication rejection or rate limiting) permits automatic resubmission. A timeout, server error, lost response, or crash during submission is ambiguous: subsequent attempts reconcile without submitting another copy. Manual retry resets attempts only on failed jobs and preserves the marker and remote IDs.

Paperless does not provide a transactional idempotency key shared with SQLite. Consequently, a crash immediately before sending can leave an intent with no remote document. Inspect Paperless and application records in this case; automatic retries intentionally never clear ambiguous intent. Keep the generated title intact in Paperless workflows so title reconciliation remains possible. A failed consumption task also remains linked for inspection. There is no UI action that silently discards remote history or forces another ambiguous upload.

API contract reference: https://docs.paperless-ngx.com/api/ (task UUID returned by `post_document`, status from `tasks/?task_id=`). Tag lookup, exact title filtering, and task `related_document` parsing are covered with local HTTP fixtures; no live credentials are required for tests.
