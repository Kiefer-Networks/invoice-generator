package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCalcNet(t *testing.T) {
	items := []Item{
		{Quantity: 10, Price: 85.00},
		{Quantity: 1, Price: 150.00},
	}
	if got := CalcNet(items); got != 1000.00 {
		t.Errorf("CalcNet = %v, want 1000.00", got)
	}
}

func TestCalcNetRoundsPerLine(t *testing.T) {
	items := []Item{
		{Quantity: 3, Price: 0.1},
		{Quantity: 1, Price: 0.05},
	}
	got := CalcNet(items)
	want := 0.35
	if diff := got - want; diff > 0.001 || diff < -0.001 {
		t.Errorf("CalcNet = %v, want %v", got, want)
	}
}

func TestCalcTax(t *testing.T) {
	vat := VATConfig{Liable: true, Rate: 19.0}
	if got := CalcTax(1000, vat); got != 190.00 {
		t.Errorf("CalcTax = %v, want 190.00", got)
	}
	if got := CalcTax(1000, VATConfig{Liable: false, Rate: 19.0}); got != 0 {
		t.Errorf("CalcTax for non-liable should be 0, got %v", got)
	}
	if got := CalcTax(1000, VATConfig{Liable: true, Rate: 0}); got != 0 {
		t.Errorf("CalcTax with zero rate should be 0, got %v", got)
	}
	if got := CalcTax(1000, VATConfig{Liable: true, Rate: -5}); got != 0 {
		t.Errorf("CalcTax with negative rate should be 0, got %v", got)
	}
}

func writeTempConfig(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}
	return path
}

const sampleYAML = `
language: "en"
company:
  name: "Test GmbH"
customer:
  name: "Client GmbH"
invoice:
  number: 2026-001
  date: "01.03.2026"
items:
  - description: "Work"
    quantity: 2
    price: 50.0
`

const sampleTOML = `
language = "en"

[company]
name = "Test GmbH"

[customer]
name = "Client GmbH"

[invoice]
number = "2026-001"
date = "01.03.2026"

[[items]]
description = "Work"
quantity = 2
price = 50.0
`

func TestLoadYAML(t *testing.T) {
	path := writeTempConfig(t, "invoice.yaml", sampleYAML)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load(yaml) failed: %v", err)
	}
	if cfg.Company.Name != "Test GmbH" || cfg.Customer.Name != "Client GmbH" {
		t.Errorf("unexpected config: %+v", cfg)
	}
	if len(cfg.Items) != 1 || cfg.Items[0].Quantity != 2 {
		t.Errorf("unexpected items: %+v", cfg.Items)
	}
}

func TestLoadTOML(t *testing.T) {
	path := writeTempConfig(t, "invoice.toml", sampleTOML)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load(toml) failed: %v", err)
	}
	if cfg.Company.Name != "Test GmbH" {
		t.Errorf("unexpected config: %+v", cfg)
	}
}

func TestLoadExtensionlessDetection(t *testing.T) {
	path := writeTempConfig(t, "invoice.conf", sampleYAML)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load(extensionless yaml) failed: %v", err)
	}
	if cfg.Company.Name != "Test GmbH" {
		t.Errorf("unexpected config: %+v", cfg)
	}
}

func TestLoadInvalid(t *testing.T) {
	path := writeTempConfig(t, "invoice.conf", "{{{ not valid : yaml or toml @@@")
	if _, err := Load(path); err == nil {
		t.Error("expected error loading invalid config, got nil")
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml")); err == nil {
		t.Error("expected error loading missing file, got nil")
	}
}

func TestLoadTooLarge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "huge.yaml")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("could not create file: %v", err)
	}
	if err := f.Truncate(MaxFileSize + 1); err != nil {
		f.Close()
		t.Fatalf("could not truncate file: %v", err)
	}
	f.Close()

	if _, err := Load(path); err == nil {
		t.Error("expected error for oversized config file, got nil")
	} else if !strings.Contains(err.Error(), "too large") {
		t.Errorf("expected 'too large' error, got: %v", err)
	}
}

func TestLocalOverridePath(t *testing.T) {
	cases := map[string]string{
		"company.yaml":      "company.local.yaml",
		"/a/b/company.toml": "/a/b/company.local.toml",
		"invoice.yml":       "invoice.local.yml",
	}
	for in, want := range cases {
		if got := LocalOverridePath(in); filepath.ToSlash(got) != filepath.ToSlash(want) {
			t.Errorf("LocalOverridePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoadWithLocalOverrideAppliesWhenPresent(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "company.yaml")
	local := filepath.Join(dir, "company.local.yaml")

	os.WriteFile(base, []byte(`
company:
  name: "Template GmbH"
currency: "EUR"
`), 0600)
	os.WriteFile(local, []byte(`
company:
  name: "Real Company GmbH"
  vat_id: "DE999999999"
`), 0600)

	cfg, overridePath, err := LoadWithLocalOverride(base)
	if err != nil {
		t.Fatalf("LoadWithLocalOverride failed: %v", err)
	}
	if overridePath != local {
		t.Errorf("expected override path %q, got %q", local, overridePath)
	}
	if cfg.Company.Name != "Real Company GmbH" {
		t.Errorf("local override should take precedence, got company name %q", cfg.Company.Name)
	}
	if cfg.Company.VatID != "DE999999999" {
		t.Errorf("expected VAT ID from override, got %q", cfg.Company.VatID)
	}
	if cfg.Currency != "EUR" {
		t.Errorf("base-only field should fall through from base, got currency %q", cfg.Currency)
	}
}

func TestLoadWithLocalOverrideAbsentIsNoop(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "company.yaml")
	os.WriteFile(base, []byte(`company:
  name: "Only Base GmbH"
`), 0600)

	cfg, overridePath, err := LoadWithLocalOverride(base)
	if err != nil {
		t.Fatalf("LoadWithLocalOverride failed: %v", err)
	}
	if overridePath != "" {
		t.Errorf("expected no override path, got %q", overridePath)
	}
	if cfg.Company.Name != "Only Base GmbH" {
		t.Errorf("unexpected company name: %q", cfg.Company.Name)
	}
}

func TestLoadWithLocalOverrideMergesPerKey(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "company.yaml")
	os.WriteFile(base, []byte(`
company:
  name: "Template GmbH"
  address: "Sample Street 1"
  bank:
    iban: "DE00 SAMPLE"
    bic: "SAMPLEBIC"
vat:
  liable: true
  rate: 19
`), 0600)
	// Only the secret bank data and the small-business VAT status — no
	// company.name — must still be applied, key by key.
	os.WriteFile(filepath.Join(dir, "company.local.yaml"), []byte(`
company:
  bank:
    iban: "DE99 REAL"
vat:
  liable: false
`), 0600)

	cfg, _, err := LoadWithLocalOverride(base)
	if err != nil {
		t.Fatalf("LoadWithLocalOverride failed: %v", err)
	}
	if cfg.Company.Bank.IBAN != "DE99 REAL" {
		t.Errorf("override IBAN must win, got %q", cfg.Company.Bank.IBAN)
	}
	if cfg.Company.Bank.BIC != "SAMPLEBIC" || cfg.Company.Address != "Sample Street 1" || cfg.Company.Name != "Template GmbH" {
		t.Errorf("keys the override leaves out must keep base values, got %+v", cfg.Company)
	}
	if cfg.VAT.Liable {
		t.Errorf("explicit liable: false must override base, got %+v", cfg.VAT)
	}
}

func TestLoadIntoLayersCompanyThenInvoice(t *testing.T) {
	dir := t.TempDir()
	company := filepath.Join(dir, "co", "company.yaml")
	invoice := filepath.Join(dir, "inv", "invoice.yaml")
	os.MkdirAll(filepath.Dir(company), 0700)
	os.MkdirAll(filepath.Dir(invoice), 0700)
	os.WriteFile(company, []byte("logo: \"logo.png\"\ncurrency: EUR\nvat:\n  liable: true\n  rate: 19\n"), 0600)
	os.WriteFile(invoice, []byte("currency: USD\nvat:\n  liable: false\ninvoice:\n  number: 0042\n"), 0600)

	cfg := &Config{}
	if _, err := LoadInto(cfg, company); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadInto(cfg, invoice); err != nil {
		t.Fatal(err)
	}
	if cfg.Currency != "USD" || cfg.VAT.Liable {
		t.Errorf("invoice keys must win over company keys, got currency=%q vat=%+v", cfg.Currency, cfg.VAT)
	}
	if want := filepath.Join(dir, "co", "logo.png"); cfg.Logo != want {
		t.Errorf("relative logo must resolve against the company file's directory: got %q, want %q", cfg.Logo, want)
	}
	if cfg.Invoice.Number != "0042" {
		t.Errorf("invoice number must be kept literally, got %q", cfg.Invoice.Number)
	}
}

func TestDocNumberTOMLInteger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "i.toml")
	os.WriteFile(path, []byte("[invoice]\nnumber = 12\n"), 0600)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Invoice.Number != "12" {
		t.Errorf("got %q", cfg.Invoice.Number)
	}
}

func TestRoundCentsHalfUp(t *testing.T) {
	cases := map[float64]float64{1.005: 1.01, 2.675: 2.68, -1.005: -1.01, -0.001: 0}
	for in, want := range cases {
		if got := RoundCents(in); got != want {
			t.Errorf("RoundCents(%v) = %v, want %v", in, got, want)
		}
	}
	if got := LineTotal(Item{Quantity: 0.5, Price: 2.01}); got != 1.01 {
		t.Errorf("LineTotal(0.5 × 2.01) = %v, want 1.01", got)
	}
	// net 3084.50 at 19% is 586.055 → 586.06 (the summed float used to be 3084.4999…)
	items := []Item{{Quantity: 2, Price: 405.08}, {Quantity: 2, Price: 79.34}, {Quantity: 2, Price: 884.53}, {Quantity: 2, Price: 173.30}}
	if tax := CalcTax(CalcNet(items), VATConfig{Liable: true, Rate: 19}); tax != 586.06 {
		t.Errorf("CalcTax = %v, want 586.06", tax)
	}
}

func TestSanitizeFilenamePart(t *testing.T) {
	cases := map[string]string{
		"2026-001":             "2026-001",
		"a/b\\c":               "a-b-c",
		"":                     "unnamed",
		"  ":                   "unnamed",
		"foo\x00bar":           "foobar",
		`Acme: "Best" <Co>?*|`: "Acme Best Co",
	}
	for in, want := range cases {
		if got := SanitizeFilenamePart(in); got != want {
			t.Errorf("SanitizeFilenamePart(%q) = %q, want %q", in, got, want)
		}
	}

	dangerous := []string{
		"../../etc/passwd", "..\\..\\windows\\win",
		"../../../etc/passwd", "..\\..\\secrets", "a/../b",
	}
	for _, in := range dangerous {
		got := SanitizeFilenamePart(in)
		if strings.Contains(got, "/") || strings.Contains(got, "\\") || strings.Contains(got, "..") {
			t.Errorf("SanitizeFilenamePart(%q) = %q still contains unsafe path characters", in, got)
		}
	}
}

func TestFindFontsExplicitConfig(t *testing.T) {
	dir := t.TempDir()
	reg := filepath.Join(dir, "regular.ttf")
	bold := filepath.Join(dir, "bold.ttf")
	os.WriteFile(reg, []byte("fake"), 0600)
	os.WriteFile(bold, []byte("fake"), 0600)

	cfg := &Config{Font: FontCfg{Regular: reg, Bold: bold}}
	gotReg, gotBold, err := FindFonts(cfg)
	if err != nil {
		t.Fatalf("FindFonts failed: %v", err)
	}
	if gotReg != reg || gotBold != bold {
		t.Errorf("FindFonts = (%q, %q), want (%q, %q)", gotReg, gotBold, reg, bold)
	}
}

func TestFindFontsExplicitConfigMissing(t *testing.T) {
	cfg := &Config{Font: FontCfg{Regular: "/does/not/exist/reg.ttf", Bold: "/does/not/exist/bold.ttf"}}
	if _, _, err := FindFonts(cfg); err == nil {
		t.Error("expected error for missing configured font, got nil")
	}
}

func TestFindFontsEnvOverride(t *testing.T) {
	dir := t.TempDir()
	reg := filepath.Join(dir, "regular.ttf")
	bold := filepath.Join(dir, "bold.ttf")
	os.WriteFile(reg, []byte("fake"), 0600)
	os.WriteFile(bold, []byte("fake"), 0600)

	t.Setenv("INVOICE_FONT_REGULAR", reg)
	t.Setenv("INVOICE_FONT_BOLD", bold)

	cfg := &Config{}
	gotReg, gotBold, err := FindFonts(cfg)
	if err != nil {
		t.Fatalf("FindFonts via env failed: %v", err)
	}
	if gotReg != reg || gotBold != bold {
		t.Errorf("FindFonts via env = (%q, %q), want (%q, %q)", gotReg, gotBold, reg, bold)
	}
}
