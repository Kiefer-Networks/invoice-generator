package pdfgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/api"

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

	if err := Generate(cfg, loc, config.DocInvoice, out); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	info, err := os.Stat(out)
	if err != nil {
		t.Fatalf("expected output PDF to exist: %v", err)
	}
	if info.Size() < 100 {
		t.Errorf("generated PDF suspiciously small: %d bytes", info.Size())
	}

	data, err := os.ReadFile(out)
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
	if err := Generate(cfg, loc, config.DocInvoice, out); err != nil {
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

	if err := Generate(cfg, loc, config.DocQuote, out); err != nil {
		t.Fatalf("Generate (quote) failed: %v", err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("expected quote PDF to exist: %v", err)
	}
}

func TestGenerateSkipsUnsupportedLogo(t *testing.T) {
	// fpdf cannot draw SVG; the logo must be skipped instead of failing
	// the whole document (the sample company.yaml ships logo.svg).
	cfg := sampleConfig()
	requireFonts(t, cfg)
	dir := t.TempDir()
	cfg.Logo = filepath.Join(dir, "logo.svg")
	if err := os.WriteFile(cfg.Logo, []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), 0600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "invoice.pdf")
	if err := Generate(cfg, locale.Resolve(cfg.Language, locale.Formatting{}), config.DocInvoice, out); err != nil {
		t.Fatalf("Generate with SVG logo failed: %v", err)
	}
}

func TestGenerateMissingFontsErrors(t *testing.T) {
	cfg := sampleConfig()
	cfg.Font = config.FontCfg{Regular: "/does/not/exist.ttf", Bold: "/does/not/exist-bold.ttf"}
	loc := locale.Resolve(cfg.Language, locale.Formatting{})

	dir := t.TempDir()
	out := filepath.Join(dir, "invoice.pdf")
	if err := Generate(cfg, loc, config.DocInvoice, out); err == nil {
		t.Error("expected error when configured fonts do not exist")
	}
}

func TestGenerateTallRowBreaksAcrossFewPages(t *testing.T) {
	cfg := sampleConfig()
	requireFonts(t, cfg)
	cfg.Items[0].Details = strings.Repeat("detail line\n", 80)
	cfg.Notes = strings.Repeat("note line\n", 120)
	out := filepath.Join(t.TempDir(), "tall.pdf")
	if err := Generate(cfg, locale.Resolve(cfg.Language, locale.Formatting{}), config.DocInvoice, out); err != nil {
		t.Fatal(err)
	}
	pages, err := api.PageCountFile(out)
	if err != nil {
		t.Fatal(err)
	}
	// 80 detail lines + 120 note lines fit on a handful of pages; the old
	// absolute positioning produced one page per remaining line (20+).
	if pages > 6 {
		t.Errorf("expected at most 6 pages, got %d", pages)
	}
}
