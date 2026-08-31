# Security Policy

## Supported Versions

Only the latest tagged release is supported with security fixes. Please
update to the newest version before reporting an issue.

## Reporting a Vulnerability

Please **do not** open a public GitHub issue for security vulnerabilities.

Instead, use GitHub's private vulnerability reporting:
[Security → Report a vulnerability](../../security/advisories/new) on this
repository. If that is unavailable, open a normal issue asking a
maintainer to provide a private contact channel — do not include
exploit details in that issue.

Please include:

- A description of the vulnerability and its potential impact
- Steps to reproduce (a minimal config file / command line is ideal)
- The affected version / commit

We aim to acknowledge reports within 5 business days.

## Scope

`invoice-generator` is a local-first, offline-by-default CLI tool: it
reads a config file you supply and writes a PDF/XML you asked for. It
does not run a server, does not phone home, and does not process
untrusted network input. The one exception is the explicit, opt-in
`-paperless` flag, which uploads the generated PDF to a Paperless-ngx
instance you configure yourself. The security-relevant surface is
therefore:

- Parsing of YAML/TOML config files (potential resource exhaustion,
  injection into generated filenames)
- Invocation of a local Chrome/Chromium/Edge binary as a subprocess
- Handling of customer/company PII and financial data (bank details, tax
  IDs) written to disk (PDF, HTML, ZUGFeRD XML)
- The optional `-paperless` upload: an outbound HTTPS/HTTP request to a
  user-configured URL, carrying a user-configured API key and the
  generated PDF

Reports about any of the above are welcome. Reports that require an
attacker to already control the config file you run the tool against, or
your local machine, are generally out of scope (the config file is
trusted input by design — it is your own invoice data).

## Hardening Already in Place

- Config files are size-limited (5 MiB) to mitigate resource-exhaustion
  (e.g. YAML "billion laughs" style expansion).
- Filenames derived from config data (the invoice number) are sanitized
  against path traversal.
- Generated files that contain personal or financial data (PDF, HTML,
  ZUGFeRD XML) are written with owner-only permissions (`0600`) where the
  OS honors POSIX permission bits.
- Headless Chrome is invoked with a bounded timeout, an isolated
  temporary profile directory, and its sandbox is only relaxed when
  already running as root (a container-only, already-reduced trust
  boundary) — never by default.
- Dependencies are scanned continuously via Dependabot, `govulncheck`,
  and CodeQL (see `.github/workflows/security.yml`).
- `-paperless` only ever contacts the URL in your own `paperless.yaml`/
  `paperless.local.yaml` — never a default or third-party endpoint — and
  warns to stderr if that URL is plain HTTP (other than localhost),
  since the API key is sent in a header on every request. The upload is
  bounded by a request timeout, and a failed upload is a non-fatal
  warning: the generated PDF is kept locally either way. Like company
  bank/tax data, the API key can be kept out of version control in a
  gitignored `paperless.local.yaml` (see README: "Local Config
  Overrides").
