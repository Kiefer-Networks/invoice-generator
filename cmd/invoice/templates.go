package main

import "fmt"

func companyTemplate(lang string) string {
	return fmt.Sprintf(`# ============================================================
# Company config (company.yaml)
# Create once, use with every invoice via -company:
#   invoice -company company.yaml invoice.yaml
#
# TIP: keep real bank/tax data out of version control by creating a
# "company.local.yaml" next to this file with the same keys — it is
# loaded automatically and overrides these values. It is gitignored
# by default (see .gitignore: *.local.yaml).
# ============================================================

# Logo (PNG/JPG, relative or absolute path)
# logo: "./logo.png"

# Language: de, en, fr, es, it, nl, pt
sprache: "%s"

# Accent color (hex)
farbe: "#5B9BD5"

# Currency
waehrung: "EUR"

firma:
  name: "My Company GmbH"
  adresse: "Sample Street 1"
  plz: "12345"
  ort: "Sample City"
  land: "DE"
  telefon: "+49 123 4567890"
  email: "info@mycompany.de"
  website: "www.mycompany.de"
  steuernummer: "12/345/67890"
  ust_id: "DE123456789"
  geschaeftsfuehrer: "John Doe"
  amtsgericht: "Sample City"
  bank:
    name: "Sample Bank"
    iban: "DE89 3704 0044 0532 0130 00"
    bic: "COBADEFFXXX"
    kontoinhaber: "John Doe"

# VAT settings
mwst:
  pflichtig: true         # false = small business (no VAT shown)
  satz: 19.0              # VAT rate in percent

# Tax notice (small business exemption)
# hinweis: "According to §19 UStG no VAT is charged."

# Payment information
zahlungsbedingungen: "Payable within 14 days of invoice date."
zahlungsmethode: "Bank transfer"

# Formatting overrides (optional, overrides language defaults)
# format:
#   datum: "02.01.2006"         # Go date format
#   dezimal: ","
#   tausender: "."
#   waehrung_vor: false
#   waehrung_abstand: true

# Custom fonts (optional)
# schrift:
#   normal: "/path/to/font.ttf"
#   fett: "/path/to/font-bold.ttf"
`, lang)
}

func invoiceTemplate(lang string) string {
	return fmt.Sprintf(`# ============================================================
# Invoice
# Usage: invoice invoice.yaml
# With company data: invoice -company company.yaml invoice.yaml
# With e-invoice:    invoice -zugferd -company company.yaml invoice.yaml
# ============================================================

# Language (or set in company.yaml)
sprache: "%s"

kunde:
  name: "Sample GmbH"
  ansprechpartner: "Jane Doe"
  email: "jane@sample.de"
  adresse: "Client Road 42"
  plz: "54321"
  ort: "Client City"
  land: "DE"
  land_name: "Germany"
  ust_id: "DE987654321"

rechnung:
  nummer: 2026-001
  datum: "01.03.2026"
  faelligkeit: "15.03.2026"
  # status: "SENT"

positionen:
  - beschreibung: "Web Development"
    details: "New landing page development"
    menge: 10
    einheit: "Stunde(n)"
    preis: 85.00

  - beschreibung: "Server Maintenance"
    details: "Monthly maintenance and updates"
    menge: 1
    einheit: "Pauschal"
    preis: 150.00

# Notes (shown at the bottom of the invoice)
notizen: "Thank you for your business."
`, lang)
}

func quoteTemplate(lang string) string {
	return fmt.Sprintf(`# ============================================================
# Quote / Angebot (non-binding offer)
# Usage: invoice quote quote.yaml
# With company data: invoice quote -company company.yaml quote.yaml
# Note: -zugferd is not available for quotes (e-invoicing applies to
# actual invoices only).
# ============================================================

# Language (or set in company.yaml)
sprache: "%s"

kunde:
  name: "Sample GmbH"
  ansprechpartner: "Jane Doe"
  email: "jane@sample.de"
  adresse: "Client Road 42"
  plz: "54321"
  ort: "Client City"
  land: "DE"
  land_name: "Germany"
  ust_id: "DE987654321"

rechnung:
  nummer: A-2026-001
  datum: "01.03.2026"
  gueltig_bis: "31.03.2026"
  # status: "DRAFT"

positionen:
  - beschreibung: "Web Development"
    details: "New landing page development"
    menge: 10
    einheit: "Stunde(n)"
    preis: 85.00

  - beschreibung: "Server Maintenance"
    details: "Monthly maintenance and updates"
    menge: 1
    einheit: "Pauschal"
    preis: 150.00

# Notes (shown at the bottom of the quote)
notizen: "This quote is non-binding and valid until the date above."
`, lang)
}
