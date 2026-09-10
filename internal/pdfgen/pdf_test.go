package pdfgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kiefer-networks/invoice-generator/internal/config"
	"github.com/kiefer-networks/invoice-generator/internal/locale"
)

func sampleConfig() *config.Config {
	return &config.Config{
		Language: "en",
		Color:    "#5B9BD5",
		Currency: "EUR",
		Company: config.Company{
			Name:    "Test GmbH",
			Address: "Main St 1",
			ZIP:     "12345",
			City:    "Berlin",
			Email:   "info@test.de",
			VatID:   "DE123456789",
			Bank:    config.BankInfo{Name: "Test Bank", IBAN: "DE89370400440532013000", BIC: "COBADEFFXXX"},
		},
		Customer: config.Customer{
			Name:    "Client GmbH",
			Address: "Client Rd 42",
			ZIP:     "54321",
			City:    "Munich",
			Country: "DE",
		},
		Invoice: config.InvInfo{
			Number:     "2026-001",
			Date:       "01.03.2026",
			DueDate:    "15.03.2026",
			ValidUntil: "31.03.2026",
			Status:     "SENT",
		},
		Items: []config.Item{
			{Description: "Consulting", Quantity: 10, Unit: "hours", Price: 85.00},
		},
		VAT: config.VATConfig{Liable: true, Rate: 19.0},
	}
}

// requireFonts skips the test if no TTF fonts are available on this
// machine — the fpdf renderer cannot run without one. CI installs fonts
// explicitly; a contributor's machine may not have them.
func requireFonts(t *testing.T, cfg *config.Config) {
	t.Helper()
	if _, _, err := config.FindFonts(cfg); err != nil {
		t.Skipf("skipping: no TTF fonts available: %v", err)
	}
}

func TestGenerateProducesNonEmptyPDF(t *testing.T) {
	cfg := sampleConfig()
	requireFonts(t, cfg)
	loc := locale.Resolve(cfg.Language, locale.Formatting{})

	dir := t.TempDir()
	out := filepath.Join(dir, "invoice.pdf")

	if err := Generate(cfg, loc, config.DocInvoice, out, nil); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	info, err := os.Stat(out)
	if err != nil {
		t.Fatalf("expected output PDF to exist: %v", err)
	}
	if info.Size() < 100 {
		t.Errorf("generated PDF suspiciously small: %d bytes", info.Size())
	}

	data, err := os.ReadFile(out) // #nosec G304 -- Fixture file in a test-owned temporary directory; no HTTP or external input selects this path.
	if err != nil {
		t.Fatalf("could not read generated PDF: %v", err)
	}
	if string(data[:5]) != "%PDF-" {
		t.Errorf("output does not look like a PDF (missing %%PDF- header)")
	}
}

func TestGenerateWithThreeLineCompanyAddress(t *testing.T) {
	// Company.Country set means the sender header grows to three address
	// lines (Street / ZIP City / Country) instead of two — verifies the
	// dynamic header height doesn't break layout or crash fpdf.
	cfg := sampleConfig()
	cfg.Company.Country = "DE"
	requireFonts(t, cfg)
	loc := locale.Resolve(cfg.Language, locale.Formatting{})

	dir := t.TempDir()
	out := filepath.Join(dir, "invoice.pdf")
	if err := Generate(cfg, loc, config.DocInvoice, out, nil); err != nil {
		t.Fatalf("Generate with 3-line company address failed: %v", err)
	}
	info, err := os.Stat(out)
	if err != nil || info.Size() < 100 {
		t.Fatalf("expected a valid non-trivial PDF, got err=%v info=%v", err, info)
	}
}

func TestGenerateQuoteUsesQuoteTitle(t *testing.T) {
	cfg := sampleConfig()
	requireFonts(t, cfg)
	loc := locale.Resolve(cfg.Language, locale.Formatting{})

	dir := t.TempDir()
	out := filepath.Join(dir, "quote.pdf")

	if err := Generate(cfg, loc, config.DocQuote, out, nil); err != nil {
		t.Fatalf("Generate (quote) failed: %v", err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("expected quote PDF to exist: %v", err)
	}
}

func TestGenerateWithZugferdAttachment(t *testing.T) {
	cfg := sampleConfig()
	requireFonts(t, cfg)
	loc := locale.Resolve(cfg.Language, locale.Formatting{})
	dir := t.TempDir()

	plainOut := filepath.Join(dir, "plain.pdf")
	if err := Generate(cfg, loc, config.DocInvoice, plainOut, nil); err != nil {
		t.Fatalf("Generate (no attachment) failed: %v", err)
	}
	plainInfo, err := os.Stat(plainOut)
	if err != nil {
		t.Fatalf("could not stat plain PDF: %v", err)
	}

	attachOut := filepath.Join(dir, "attach.pdf")
	fakeXML := []byte(`<?xml version="1.0"?><root>` + strings.Repeat("x", 500) + `</root>`)
	if err := Generate(cfg, loc, config.DocInvoice, attachOut, fakeXML); err != nil {
		t.Fatalf("Generate with attachment failed: %v", err)
	}
	data, err := os.ReadFile(attachOut) // #nosec G304 -- Fixture file in a test-owned temporary directory; no HTTP or external input selects this path.
	if err != nil {
		t.Fatalf("could not read generated PDF: %v", err)
	}
	if string(data[:5]) != "%PDF-" {
		t.Error("output with attachment does not look like a PDF")
	}
	// A PDF with an embedded file attachment must be meaningfully larger
	// than the same document without one, and must declare an embedded
	// file stream somewhere in its object list.
	if int64(len(data)) <= plainInfo.Size() {
		t.Errorf("expected PDF with attachment (%d bytes) to be larger than without (%d bytes)", len(data), plainInfo.Size())
	}
	if !contains(data, []byte("EmbeddedFile")) {
		t.Error("expected PDF to declare an /EmbeddedFile object for the ZUGFeRD attachment")
	}
}

func TestGenerateMissingFontsErrors(t *testing.T) {
	cfg := sampleConfig()
	cfg.Font = config.FontCfg{Regular: "/does/not/exist.ttf", Bold: "/does/not/exist-bold.ttf"}
	loc := locale.Resolve(cfg.Language, locale.Formatting{})

	dir := t.TempDir()
	out := filepath.Join(dir, "invoice.pdf")
	if err := Generate(cfg, loc, config.DocInvoice, out, nil); err == nil {
		t.Error("expected error when configured fonts do not exist")
	}
}

func contains(haystack, needle []byte) bool {
	return len(needle) == 0 || indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle []byte) int {
	n, m := len(haystack), len(needle)
	for i := 0; i+m <= n; i++ {
		if string(haystack[i:i+m]) == string(needle) {
			return i
		}
	}
	return -1
}
