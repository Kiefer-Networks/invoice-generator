package render

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
			Phone:   "+49 30 123456",
			VatID:   "DE123456789",
			Bank:    config.BankInfo{Name: "Test Bank", IBAN: "DE89370400440532013000", BIC: "COBADEFFXXX"},
		},
		Customer: config.Customer{
			Name:    "Client GmbH",
			Contact: "Jane Doe",
			Email:   "jane@client.de",
			Address: "Client Rd 42",
			ZIP:     "54321",
			City:    "Munich",
			Country: "DE",
			VatID:   "DE987654321",
		},
		Invoice: config.InvInfo{
			Number:     "2026-001",
			Date:       "01.03.2026",
			DueDate:    "15.03.2026",
			ValidUntil: "31.03.2026",
			Status:     "SENT",
		},
		Items: []config.Item{
			{Description: "Web Development", Details: "Landing page", Quantity: 10, Unit: "hours", Price: 85.00},
			{Description: "Server Maintenance", Quantity: 1, Unit: "flat", Price: 150.00},
		},
		VAT:      config.VATConfig{Liable: true, Rate: 19.0},
		Notes:    "Thank you.",
		PayTerms: "Payable within 14 days.",
	}
}

func resolveLoc(cfg *config.Config) *locale.Locale {
	return locale.Resolve(cfg.Language, locale.Formatting{
		DateFmt:        cfg.Formatting.DateFmt,
		DecimalSep:     cfg.Formatting.DecimalSep,
		ThousandSep:    cfg.Formatting.ThousandSep,
		CurrencySymbol: cfg.Formatting.CurrencySymbol,
		CurrencyBefore: cfg.Formatting.CurrencyBefore,
		CurrencySpace:  cfg.Formatting.CurrencySpace,
	})
}

func TestPrepareTplDataTotals(t *testing.T) {
	cfg := sampleConfig()
	loc := resolveLoc(cfg)
	d := PrepareTplData(cfg, loc, config.DocInvoice)

	if !d.HasVAT {
		t.Error("expected HasVAT to be true")
	}
	if d.Subtotal != "€1,000.00" {
		t.Errorf("Subtotal = %q, want %q", d.Subtotal, "€1,000.00")
	}
	if d.GrossTotal != "€1,190.00" {
		t.Errorf("GrossTotal = %q, want %q", d.GrossTotal, "€1,190.00")
	}
	if len(d.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(d.Rows))
	}
	if d.Rows[0].Amt != "€850.00" {
		t.Errorf("row 0 amount = %q, want %q", d.Rows[0].Amt, "€850.00")
	}
	if !d.HasBank {
		t.Error("expected HasBank to be true (IBAN set)")
	}
	if d.Title != "Invoice" {
		t.Errorf("expected invoice title, got %q", d.Title)
	}
	if d.DueDate == "" {
		t.Error("expected due date to be set for an invoice")
	}
}

func TestPrepareTplDataQuoteUsesValidUntil(t *testing.T) {
	cfg := sampleConfig()
	loc := resolveLoc(cfg)
	d := PrepareTplData(cfg, loc, config.DocQuote)

	if d.Title != "Quote" {
		t.Errorf("expected quote title, got %q", d.Title)
	}
	if d.LB.DueDate != "Valid Until" {
		t.Errorf("expected LB.DueDate swapped to 'Valid Until' label, got %q", d.LB.DueDate)
	}
	wantDate := loc.FormatDate(cfg.Invoice.ValidUntil)
	if d.DueDate != wantDate {
		t.Errorf("expected quote date value from ValidUntil (%q), got %q", wantDate, d.DueDate)
	}
}

func TestPrepareTplDataQuoteFallsBackToDueDate(t *testing.T) {
	cfg := sampleConfig()
	cfg.Invoice.ValidUntil = ""
	loc := resolveLoc(cfg)
	d := PrepareTplData(cfg, loc, config.DocQuote)

	wantDate := loc.FormatDate(cfg.Invoice.DueDate)
	if d.DueDate != wantDate {
		t.Errorf("expected quote to fall back to DueDate (%q) when ValidUntil is empty, got %q", wantDate, d.DueDate)
	}
}

func TestPrepareTplDataNoVAT(t *testing.T) {
	cfg := sampleConfig()
	cfg.VAT = config.VATConfig{Liable: false}
	loc := resolveLoc(cfg)
	d := PrepareTplData(cfg, loc, config.DocInvoice)
	if d.HasVAT {
		t.Error("expected HasVAT to be false when not VAT liable")
	}
	if d.GrossTotal != d.Subtotal {
		t.Errorf("without VAT, gross (%q) should equal subtotal (%q)", d.GrossTotal, d.Subtotal)
	}
}

func TestPrepareTplDataStatusClasses(t *testing.T) {
	cases := map[string]string{"PAID": "paid", "OVERDUE": "overdue", "DRAFT": "draft", "SENT": "", "": ""}
	for status, want := range cases {
		cfg := sampleConfig()
		cfg.Invoice.Status = status
		loc := resolveLoc(cfg)
		d := PrepareTplData(cfg, loc, config.DocInvoice)
		if d.StatusClass != want {
			t.Errorf("status %q => class %q, want %q", status, d.StatusClass, want)
		}
	}
}

func TestLoadTemplateSrcPriority(t *testing.T) {
	dir := t.TempDir()

	src, err := loadTemplateSrc("", dir)
	if err != nil {
		t.Fatalf("loadTemplateSrc failed: %v", err)
	}
	if src != defaultTemplateHTML {
		t.Error("expected embedded default template when none provided")
	}

	localTpl := filepath.Join(dir, "template.html")
	if err := os.WriteFile(localTpl, []byte("<html>LOCAL</html>"), 0600); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	src, err = loadTemplateSrc("", dir)
	if err != nil {
		t.Fatalf("loadTemplateSrc failed: %v", err)
	}
	if src != "<html>LOCAL</html>" {
		t.Errorf("expected local template.html to be used, got %q", src)
	}

	explicit := filepath.Join(dir, "custom.html")
	if err := os.WriteFile(explicit, []byte("<html>EXPLICIT</html>"), 0600); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	src, err = loadTemplateSrc(explicit, dir)
	if err != nil {
		t.Fatalf("loadTemplateSrc failed: %v", err)
	}
	if src != "<html>EXPLICIT</html>" {
		t.Errorf("expected explicit template to be used, got %q", src)
	}

	if _, err := loadTemplateSrc(filepath.Join(dir, "missing.html"), dir); err == nil {
		t.Error("expected error for missing explicit template path")
	}
}

func TestHTMLProducesValidOutput(t *testing.T) {
	cfg := sampleConfig()
	loc := resolveLoc(cfg)
	dir := t.TempDir()

	html, err := HTML(cfg, loc, config.DocInvoice, "", dir)
	if err != nil {
		t.Fatalf("HTML failed: %v", err)
	}
	for _, want := range []string{"Test GmbH", "Client GmbH", "Web Development", "€1,190.00"} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered HTML missing expected content %q", want)
		}
	}
	if strings.Contains(html, "{{") || strings.Contains(html, "}}") {
		t.Error("rendered HTML still contains unresolved template directives")
	}
}

func TestHTMLQuoteTitle(t *testing.T) {
	cfg := sampleConfig()
	loc := resolveLoc(cfg)
	dir := t.TempDir()

	html, err := HTML(cfg, loc, config.DocQuote, "", dir)
	if err != nil {
		t.Fatalf("HTML failed: %v", err)
	}
	if !strings.Contains(html, "Quote") {
		t.Error("expected rendered quote HTML to contain the quote title")
	}
}

func TestHTMLInvalidTemplate(t *testing.T) {
	cfg := sampleConfig()
	loc := resolveLoc(cfg)
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.html")
	os.WriteFile(bad, []byte("{{ .DoesNotExist.Nested }}"), 0600)

	if _, err := HTML(cfg, loc, config.DocInvoice, bad, dir); err == nil {
		t.Error("expected error for template referencing unknown field")
	}
}

func TestFindChromeEnvOverride(t *testing.T) {
	dir := t.TempDir()
	fakeChrome := filepath.Join(dir, "fake-chrome")
	if err := os.WriteFile(fakeChrome, []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	t.Setenv("INVOICE_CHROME", fakeChrome)
	if got := FindChrome(); got != fakeChrome {
		t.Errorf("FindChrome() = %q, want %q (INVOICE_CHROME override)", got, fakeChrome)
	}
}

func TestFindChromeEnvOverrideIgnoredWhenMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	t.Setenv("INVOICE_CHROME", missing)
	if got := FindChrome(); got == missing {
		t.Error("FindChrome should not return a nonexistent INVOICE_CHROME path")
	}
}
