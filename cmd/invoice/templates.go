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
language: "%s"

# Accent color (hex)
color: "#5B9BD5"

# Currency
currency: "EUR"

company:
  name: "My Company GmbH"
  address: "Sample Street 1"
  zip: "12345"
  city: "Sample City"
  country: "DE"
  phone: "+49 123 4567890"
  email: "info@mycompany.de"
  website: "www.mycompany.de"
  tax_number: "12/345/67890"
  vat_id: "DE123456789"
  ceo: "John Doe"
  court: "Sample City"
  bank:
    name: "Sample Bank"
    iban: "DE89 3704 0044 0532 0130 00"
    bic: "COBADEFFXXX"
    account_holder: "John Doe"

# VAT settings
vat:
  liable: true            # false = small business (no VAT shown)
  rate: 19.0               # VAT rate in percent

# Tax notice (small business exemption)
# notice: "According to §19 UStG no VAT is charged."

# Payment information
payment_terms: "Payable within 14 days of invoice date."
payment_method: "Bank transfer"

# Formatting overrides (optional, overrides language defaults)
# format:
#   date: "02.01.2006"          # Go date format
#   decimal_separator: ","
#   thousand_separator: "."
#   currency_before: false
#   currency_space: true

# Custom fonts (optional)
# font:
#   regular: "/path/to/font.ttf"
#   bold: "/path/to/font-bold.ttf"
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
language: "%s"

customer:
  name: "Sample GmbH"
  contact: "Jane Doe"
  email: "jane@sample.de"
  address: "Client Road 42"
  zip: "54321"
  city: "Client City"
  country: "DE"
  country_name: "Germany"
  vat_id: "DE987654321"

invoice:
  number: 2026-001
  date: "01.03.2026"
  due_date: "15.03.2026"
  # status: "SENT"

items:
  - description: "Web Development"
    details: "New landing page development"
    quantity: 10
    unit: "hour(s)"
    price: 85.00

  - description: "Server Maintenance"
    details: "Monthly maintenance and updates"
    quantity: 1
    unit: "flat"
    price: 150.00

# Notes (shown at the bottom of the invoice)
notes: "Thank you for your business."
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
language: "%s"

customer:
  name: "Sample GmbH"
  contact: "Jane Doe"
  email: "jane@sample.de"
  address: "Client Road 42"
  zip: "54321"
  city: "Client City"
  country: "DE"
  country_name: "Germany"
  vat_id: "DE987654321"

invoice:
  number: A-2026-001
  date: "01.03.2026"
  valid_until: "31.03.2026"
  # status: "DRAFT"

items:
  - description: "Web Development"
    details: "New landing page development"
    quantity: 10
    unit: "hour(s)"
    price: 85.00

  - description: "Server Maintenance"
    details: "Monthly maintenance and updates"
    quantity: 1
    unit: "flat"
    price: 150.00

# Notes (shown at the bottom of the quote)
notes: "This quote is non-binding and valid until the date above."
`, lang)
}

func paperlessTemplate() string {
	return `# ============================================================
# Paperless-ngx upload config (paperless.yaml)
# Usage: invoice -paperless -company company.yaml invoice.yaml
#
# TIP: the API key is a real secret. Keep it out of version control by
# creating a "paperless.local.yaml" next to this file with the same
# keys — it is loaded automatically and overrides these values. It is
# gitignored by default (see .gitignore: *.local.yaml).
# ============================================================

url: "https://paperless.example.com"
api_key: "your-paperless-api-key"

# Tags to apply to the uploaded document (created automatically in
# Paperless if they don't exist yet).
tags:
  - "Invoices"
`
}
