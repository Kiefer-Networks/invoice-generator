# invoice-generator

[![CI](https://github.com/kiefer-networks/invoice-generator/actions/workflows/ci.yml/badge.svg)](https://github.com/kiefer-networks/invoice-generator/actions/workflows/ci.yml)
[![Security](https://github.com/kiefer-networks/invoice-generator/actions/workflows/security.yml/badge.svg)](https://github.com/kiefer-networks/invoice-generator/actions/workflows/security.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/kiefer-networks/invoice-generator.svg)](https://pkg.go.dev/github.com/kiefer-networks/invoice-generator)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

A fast, single-binary CLI tool that generates professional invoice and
quote PDFs from simple YAML or TOML config files. Supports 7 languages,
customizable HTML templates, and ZUGFeRD/Factur-X e-invoicing. Runs
natively on Linux, macOS, and Windows.

## Features

- **PDF generation** for both **invoices** and **quotes (Angebote)** from YAML/TOML configuration files
- **Dual renderer**: HTML template + Chrome/Chromium/Edge (best quality) with built-in fpdf fallback
- **ZUGFeRD / Factur-X** e-invoice XML embedding (BASIC profile, EN 16931) — invoices only
- **7 languages**: German, English, French, Spanish, Italian, Dutch, Portuguese
- **Customizable HTML template** — extract, edit CSS/layout, use your own design
- **Locale-aware formatting** — currency symbols, decimal/thousand separators, date formats
- **20+ currencies** supported out of the box
- **Logo support** — SVG, PNG, JPG
- **Local config overrides** — keep real bank/tax data out of version control
- **Paperless-ngx upload** — optionally send the generated PDF straight into your document archive
- **Cross-platform** — Linux, macOS, and Windows, with automatic font/browser discovery on each
- **Single binary**, no runtime dependencies (Chrome/Chromium/Edge optional for the HTML renderer)
- **English config schema** — all YAML/TOML field names are English, regardless of invoice language

## Quick Start

```bash
# Build
go build -o invoice ./cmd/invoice

# Generate starter configs
invoice init company
invoice init invoice

# Edit company.yaml and invoice.yaml with your data, then:
invoice -company company.yaml invoice.yaml
```

## Installation

```bash
go install github.com/kiefer-networks/invoice-generator/cmd/invoice@latest
```

Or build from source:

```bash
git clone https://github.com/kiefer-networks/invoice-generator.git
cd invoice-generator
go build -o invoice ./cmd/invoice
```

Prebuilt binaries for Linux, macOS, and Windows (amd64 + arm64) are
published on the [Releases](../../releases) page for every tagged
version, along with checksums and build provenance attestations.

### Requirements

- **Go 1.26+** to build
- **Chrome, Chromium, or Edge** (optional) — for HTML template rendering. Falls back to the built-in renderer automatically if none is found.

## Usage

```
invoice <invoice.yaml|.toml> [flags]     Generate invoice PDF
invoice quote <quote.yaml|.toml> [flags] Generate quote (Angebot) PDF
invoice init company [--lang <code>]     Create company config template
invoice init invoice [--lang <code>]     Create invoice template
invoice init quote [--lang <code>]       Create quote template
invoice init paperless                   Create paperless.yaml upload config template
invoice init template                    Extract HTML template for customization
invoice version                          Show version
invoice help                             Show this help
```

### Flags

| Flag | Description |
|------|-------------|
| `-company <path>` | Load separate company config file |
| `-o <path>` | Output PDF path (default: `<YYYYMMDD>; <company>; Rechnung <nr>.pdf` / `... Angebot <nr>.pdf`, generation date) |
| `-zugferd` | Embed ZUGFeRD/Factur-X XML (BASIC profile) — invoices only |
| `-lang <code>` | Override language (`de`, `en`, `fr`, `es`, `it`, `nl`, `pt`) |
| `-html` | Also save the rendered HTML file |
| `-t <path>` | Use custom HTML template |
| `-fpdf` | Force built-in renderer (no Chrome needed) |
| `-paperless` | Upload the generated PDF to Paperless-ngx |
| `-paperless-config <path>` | Paperless config file (default: `paperless.yaml` next to `-company`, or the document) |

### Environment variables

| Variable | Purpose |
|----------|---------|
| `INVOICE_CHROME` | Path to a specific Chrome/Chromium/Edge binary |
| `INVOICE_FONT_REGULAR` | Path to a TTF font for the built-in renderer |
| `INVOICE_FONT_BOLD` | Path to the matching bold TTF font |

### Examples

```bash
# Basic invoice
invoice invoice.yaml

# A quote / Angebot instead of an invoice
invoice quote quote.yaml

# With separate company config
invoice -company company.yaml invoice.yaml

# With e-invoice (ZUGFeRD/Factur-X)
invoice -zugferd -company company.yaml invoice.yaml

# Export HTML alongside PDF
invoice -html -company company.yaml invoice.yaml

# Use a custom template
invoice -t custom.html -company company.yaml invoice.yaml

# Upload to Paperless-ngx after generating
invoice -paperless -company company.yaml invoice.yaml

# Generate English templates
invoice init company --lang en
invoice init invoice --lang en
invoice init quote --lang en
```

## Configuration

Split your data into two files: a **company config** (reused across all
invoices/quotes) and a **document file** (per invoice or quote). All
field names are English, independent of the `language` used to render
the document (`de`, `en`, `fr`, `es`, `it`, `nl`, `pt`).

### Company config (`company.yaml`)

```yaml
logo: "./logo.png"
language: "en"
color: "#5B9BD5"
currency: "EUR"

company:
  name: "My Company GmbH"
  address: "Sample Street 1"
  zip: "12345"
  city: "Berlin"
  country: "DE"
  email: "info@mycompany.de"
  phone: "+49 30 12345678"
  vat_id: "DE123456789"
  bank:
    name: "Sample Bank"
    iban: "DE89 3704 0044 0532 0130 00"
    bic: "COBADEFFXXX"

vat:
  liable: true
  rate: 19.0

payment_terms: "Payable within 14 days of invoice date."
payment_method: "Bank transfer"
```

### Invoice file (`invoice.yaml`)

```yaml
customer:
  name: "Client GmbH"
  contact: "Jane Doe"
  email: "jane@client.de"
  address: "Client Road 42"
  zip: "54321"
  city: "Munich"
  country: "DE"
  vat_id: "DE987654321"

invoice:
  number: 2026-001
  date: "01.03.2026"
  due_date: "15.03.2026"
  status: "SENT"

items:
  - description: "Web Development"
    details: "Landing page development"
    quantity: 10
    unit: "hours"
    price: 85.00

  - description: "Server Maintenance"
    quantity: 1
    unit: "flat"
    price: 150.00

notes: "Thank you for your business."
```

### Quote file (`quote.yaml`)

A quote uses the same schema, but `valid_until` replaces `due_date`,
and `-zugferd` is not available (e-invoicing applies to actual
invoices only):

```yaml
invoice:
  number: A-2026-001
  date: "01.03.2026"
  valid_until: "31.03.2026"
```

```bash
invoice quote -company company.yaml quote.yaml
```

## Local Config Overrides

Commit a safe, sample `company.yaml` to version control, and keep your
**real** bank details, tax IDs, and address in a sibling
`company.local.yaml` (or `.toml`) with the same keys — it is picked up
automatically and its values take precedence over the tracked file:

```
company.yaml         # tracked, safe sample data
company.local.yaml    # gitignored, your real data — created by you
```

`*.local.yaml`, `*.local.yml`, and `*.local.toml` are excluded via
`.gitignore` by default (and `invoice init company` adds the pattern to
an existing `.gitignore` automatically if it isn't there yet). This
applies to any config file, not just `company.yaml` — `invoice.local.yaml`
next to `invoice.yaml`, or `paperless.local.yaml` next to `paperless.yaml`,
work the same way.

## Custom Templates

Extract the built-in HTML template and customize it:

```bash
invoice init template
# Edit template.html — change colors, fonts, layout
invoice -t template.html -company company.yaml invoice.yaml
```

The template uses Go's `text/template` syntax with CSS custom properties for easy theming. All data is pre-formatted — the template only needs to place values, no logic required.

Note: the repeating per-page footer (company/bank details, page numbers) is rendered by Chrome's native print header/footer mechanism, not baked into this template's HTML — it only appears in the generated PDF, not in a `-html` export or when previewing `template.html` directly in a browser.

To customize that footer, add `{{define "footer"}}...{{end}}` to your
HTML template. It receives the same formatted data as the body (for example,
`{{.CompanyName}}` and `{{.BankIBAN}}`). Use inline styles because Chrome
prints the footer in a separate context. Templates without this definition
keep the default footer. This applies to the HTML renderer; `-fpdf` uses
the built-in PDF layout. `-zugferd` preserves whichever renderer was selected
and embeds the electronic invoice after HTML/Chrome rendering when needed.

## Paperless-ngx Upload

Automatically archive every generated PDF in [Paperless-ngx](https://docs.paperless-ngx.com/):

```bash
invoice init paperless
# edit paperless.yaml — url, api_key, tags
invoice -paperless -company company.yaml invoice.yaml
```

`paperless.yaml`:

```yaml
url: "https://paperless.example.com"
api_key: "your-paperless-api-key"
tags:
  - "Invoices"
```

- Tags are matched by name and created automatically in Paperless if
  they don't exist yet.
- The document title is the generated PDF's filename (without
  extension).
- Like `company.yaml`, the API key can be kept out of version control
  in a gitignored `paperless.local.yaml` sitting next to it — see
  [Local Config Overrides](#local-config-overrides).
- By default `paperless.yaml` is looked up next to the `-company` file
  (or next to the document, if `-company` isn't used); override the
  location with `-paperless-config <path>`.
- A failed upload only prints a warning — the PDF is generated and
  saved locally regardless of whether the upload succeeds.

## E-Invoicing (ZUGFeRD / Factur-X)

Generate EN 16931 compliant electronic invoices:

```bash
invoice -zugferd -company company.yaml invoice.yaml
```

This creates:
- A PDF with the Factur-X XML embedded as an attachment
- A standalone `_factur-x.xml` file (CII XML, BASIC profile)

ZUGFeRD does not change the visual renderer: custom HTML templates continue
to be used. If Chrome is unavailable, or `-fpdf` is specified, the built-in
renderer creates the PDF and embeds the same XML directly.

Mandatory in Germany: reception since 2025, sending B2B from 2027–2028.

Quotes are not e-invoices and cannot be generated with `-zugferd`.

## Supported Languages

| Code | Language |
|------|----------|
| `de` | Deutsch |
| `en` | English |
| `fr` | Fran&ccedil;ais |
| `es` | Espa&ntilde;ol |
| `it` | Italiano |
| `nl` | Nederlands |
| `pt` | Portugu&ecirc;s |

## Formatting Overrides

Override locale defaults per config file:

```yaml
format:
  decimal_separator: ","
  thousand_separator: "."
  currency_before: false
  currency_space: true
  date: "02.01.2006"
```

## Project Structure

```
cmd/invoice/          CLI entry point, flag parsing, help/init templates
internal/config/      Config schema, YAML/TOML loading, merging, local overrides
internal/locale/      Language labels, number/currency/date formatting
internal/render/      HTML template rendering + headless Chrome → PDF
internal/pdfgen/      Built-in fpdf renderer (Chrome-free fallback)
internal/zugferd/     Factur-X / ZUGFeRD CII XML generation
internal/paperless/   Paperless-ngx REST API upload
```

Each package has its own test suite (`go test ./...`); `cmd/invoice`
carries end-to-end tests that build and exercise the real binary.

## Security & Privacy

This is a local-first, offline-by-default tool — it never phones home,
and the only network request it ever makes is the explicit, opt-in
`-paperless` upload. See [SECURITY.md](SECURITY.md) for the full
policy and hardening details. In short:

- Generated PDFs/HTML/XML (which contain customer and bank data) are
  written with owner-only file permissions where the OS supports it.
- Config files are size-limited to guard against malicious/huge input.
- Filenames derived from config data are sanitized against path traversal.
- Headless Chrome runs with a timeout, an isolated temp profile, and a
  sandbox that is only relaxed when unavoidable (running as root in a
  container).
- `-paperless` only talks to the Paperless-ngx URL you configure, warns
  if that URL is plain HTTP, and never runs unless the flag is passed.
- Dependencies are continuously scanned via Dependabot, `govulncheck`,
  and CodeQL.

## Contributing / CI

Every push and pull request runs: `gofmt`/`go vet`, `golangci-lint`,
the full test suite (with the race detector) on Linux/macOS/Windows,
a 6-way cross-compilation check (linux/darwin/windows × amd64/arm64),
a dependency-freshness gate, CodeQL, and `govulncheck`. Tagged releases
(`vX.Y.Z`) are built and published automatically via GoReleaser, with
checksums and build provenance attestations.

## License

MIT
