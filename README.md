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
- **Cross-platform** — Linux, macOS, and Windows, with automatic font/browser discovery on each
- **Single binary**, no runtime dependencies (Chrome/Chromium/Edge optional for the HTML renderer)

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

- **Go 1.25+** to build
- **Chrome, Chromium, or Edge** (optional) — for HTML template rendering. Falls back to the built-in renderer automatically if none is found.

## Usage

```
invoice <invoice.yaml|.toml> [flags]     Generate invoice PDF
invoice quote <quote.yaml|.toml> [flags] Generate quote (Angebot) PDF
invoice init company [--lang <code>]     Create company config template
invoice init invoice [--lang <code>]     Create invoice template
invoice init quote [--lang <code>]       Create quote template
invoice init template                    Extract HTML template for customization
invoice version                          Show version
invoice help                             Show this help
```

### Flags

| Flag | Description |
|------|-------------|
| `-company <path>` | Load separate company config file |
| `-o <path>` | Output PDF path (default: `Rechnung_<nr>.pdf` / `Angebot_<nr>.pdf`) |
| `-zugferd` | Embed ZUGFeRD/Factur-X XML (BASIC profile) — invoices only |
| `-lang <code>` | Override language (`de`, `en`, `fr`, `es`, `it`, `nl`, `pt`) |
| `-html` | Also save the rendered HTML file |
| `-t <path>` | Use custom HTML template |
| `-fpdf` | Force built-in renderer (no Chrome needed) |

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

# Generate English templates
invoice init company --lang en
invoice init invoice --lang en
invoice init quote --lang en
```

## Configuration

Split your data into two files: a **company config** (reused across all
invoices/quotes) and a **document file** (per invoice or quote).

### Company config (`company.yaml`)

```yaml
logo: "./logo.png"
sprache: "en"
farbe: "#5B9BD5"
waehrung: "EUR"

firma:
  name: "My Company GmbH"
  adresse: "Sample Street 1"
  plz: "12345"
  ort: "Berlin"
  land: "DE"
  email: "info@mycompany.de"
  telefon: "+49 30 12345678"
  ust_id: "DE123456789"
  bank:
    name: "Sample Bank"
    iban: "DE89 3704 0044 0532 0130 00"
    bic: "COBADEFFXXX"

mwst:
  pflichtig: true
  satz: 19.0

zahlungsbedingungen: "Payable within 14 days of invoice date."
zahlungsmethode: "Bank transfer"
```

### Invoice file (`invoice.yaml`)

```yaml
kunde:
  name: "Client GmbH"
  ansprechpartner: "Jane Doe"
  email: "jane@client.de"
  adresse: "Client Road 42"
  plz: "54321"
  ort: "Munich"
  land: "DE"
  ust_id: "DE987654321"

rechnung:
  nummer: 2026-001
  datum: "01.03.2026"
  faelligkeit: "15.03.2026"
  status: "SENT"

positionen:
  - beschreibung: "Web Development"
    details: "Landing page development"
    menge: 10
    einheit: "hours"
    preis: 85.00

  - beschreibung: "Server Maintenance"
    menge: 1
    einheit: "flat"
    preis: 150.00

notizen: "Thank you for your business."
```

### Quote file (`quote.yaml`)

A quote uses the same schema, but `gueltig_bis` (valid until) replaces
`faelligkeit` (due date), and `-zugferd` is not available (e-invoicing
applies to actual invoices only):

```yaml
rechnung:
  nummer: A-2026-001
  datum: "01.03.2026"
  gueltig_bis: "31.03.2026"
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
next to `invoice.yaml` works the same way.

## Custom Templates

Extract the built-in HTML template and customize it:

```bash
invoice init template
# Edit template.html — change colors, fonts, layout
invoice -t template.html -company company.yaml invoice.yaml
```

The template uses Go's `text/template` syntax with CSS custom properties for easy theming. All data is pre-formatted — the template only needs to place values, no logic required.

## E-Invoicing (ZUGFeRD / Factur-X)

Generate EN 16931 compliant electronic invoices:

```bash
invoice -zugferd -company company.yaml invoice.yaml
```

This creates:
- A PDF with the Factur-X XML embedded as an attachment
- A standalone `_factur-x.xml` file (CII XML, BASIC profile)

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
  dezimal: ","
  tausender: "."
  waehrung_vor: false
  waehrung_abstand: true
  datum: "02.01.2006"
```

## Project Structure

```
cmd/invoice/          CLI entry point, flag parsing, help/init templates
internal/config/      Config schema, YAML/TOML loading, merging, local overrides
internal/locale/      Language labels, number/currency/date formatting
internal/render/      HTML template rendering + headless Chrome → PDF
internal/pdfgen/      Built-in fpdf renderer (Chrome-free fallback)
internal/zugferd/     Factur-X / ZUGFeRD CII XML generation
```

Each package has its own test suite (`go test ./...`); `cmd/invoice`
carries end-to-end tests that build and exercise the real binary.

## Security & Privacy

This is an entirely local, offline tool — it never makes network
requests and never phones home. See [SECURITY.md](SECURITY.md) for the
full policy and hardening details. In short:

- Generated PDFs/HTML/XML (which contain customer and bank data) are
  written with owner-only file permissions where the OS supports it.
- Config files are size-limited to guard against malicious/huge input.
- Filenames derived from config data are sanitized against path traversal.
- Headless Chrome runs with a timeout, an isolated temp profile, and a
  sandbox that is only relaxed when unavoidable (running as root in a
  container).
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
