# invoice-generator

A fast, single-binary CLI tool that generates professional invoice PDFs from simple YAML or TOML config files. Supports 7 languages, customizable HTML templates, and ZUGFeRD/Factur-X e-invoicing.

## Features

- **PDF generation** from YAML/TOML configuration files
- **Dual renderer**: HTML template + Chrome/Chromium (best quality) with built-in fpdf fallback
- **ZUGFeRD / Factur-X** e-invoice XML embedding (BASIC profile, EN 16931)
- **7 languages**: German, English, French, Spanish, Italian, Dutch, Portuguese
- **Customizable HTML template** — extract, edit CSS/layout, use your own design
- **Locale-aware formatting** — currency symbols, decimal/thousand separators, date formats
- **20+ currencies** supported out of the box
- **Logo support** — SVG, PNG, JPG
- **Single binary**, no runtime dependencies (Chrome optional for HTML renderer)

## Quick Start

```bash
# Build
go build -o invoice .

# Generate starter configs
invoice init company
invoice init invoice

# Edit company.yaml and invoice.yaml with your data, then:
invoice -company company.yaml invoice.yaml
```

## Installation

```bash
go install github.com/kiefer-networks/invoice-generator@latest
```

Or build from source:

```bash
git clone https://github.com/kiefer-networks/invoice-generator.git
cd invoice-generator
go build -o invoice .
```

### Requirements

- **Go 1.21+** to build
- **Chrome/Chromium** (optional) — for HTML template rendering. Falls back to the built-in renderer automatically if not found.

## Usage

```
invoice <invoice.yaml|.toml> [flags]     Generate invoice PDF
invoice init company [--lang <code>]     Create company config template
invoice init invoice [--lang <code>]     Create invoice template
invoice init template                    Extract HTML template for customization
invoice help                             Show this help
```

### Flags

| Flag | Description |
|------|-------------|
| `-company <path>` | Load separate company config file |
| `-o <path>` | Output PDF path (default: `Rechnung_<nr>.pdf`) |
| `-zugferd` | Embed ZUGFeRD/Factur-X XML (BASIC profile) |
| `-lang <code>` | Override language (`de`, `en`, `fr`, `es`, `it`, `nl`, `pt`) |
| `-html` | Also save the rendered HTML file |
| `-t <path>` | Use custom HTML template |
| `-fpdf` | Force built-in renderer (no Chrome needed) |

### Examples

```bash
# Basic invoice
invoice invoice.yaml

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
```

## Configuration

Split your data into two files: a **company config** (reused across all invoices) and an **invoice file** (per invoice).

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

## License

MIT
