package locale

import "testing"

func TestGetFallback(t *testing.T) {
	if loc := Get("xx"); loc != locales["de"] {
		t.Errorf("expected fallback to 'de' locale for unknown code, got %+v", loc)
	}
	for _, code := range SupportedLanguages() {
		if loc := Get(code); loc == nil {
			t.Errorf("locale %q should be defined", code)
		}
	}
}

func TestQuoteLabelsPresent(t *testing.T) {
	for _, code := range SupportedLanguages() {
		loc := Get(code)
		if loc.Labels.QuoteTitle == "" || loc.Labels.QuoteNr == "" || loc.Labels.ValidUntil == "" {
			t.Errorf("locale %q is missing quote labels: %+v", code, loc.Labels)
		}
	}
}

func TestFormatNumber(t *testing.T) {
	de := Get("de")
	en := Get("en")

	cases := []struct {
		loc      *Locale
		f        float64
		decimals int
		want     string
	}{
		{de, 1234.5, 2, "1.234,50"},
		{en, 1234.5, 2, "1,234.50"},
		{de, -1234.5, 2, "-1.234,50"},
		{de, 0, 2, "0,00"},
		{de, 999, 0, "999"},
		{de, 1000000, 0, "1.000.000"},
	}
	for _, c := range cases {
		got := c.loc.FormatNumber(c.f, c.decimals)
		if got != c.want {
			t.Errorf("FormatNumber(%v, %d) = %q, want %q", c.f, c.decimals, got, c.want)
		}
	}
}

func TestFormatCurrency(t *testing.T) {
	de := Get("de")
	en := Get("en")

	if got := de.FormatCurrency(85, "EUR"); got != "85,00 €" {
		t.Errorf("de FormatCurrency(85, EUR) = %q, want %q", got, "85,00 €")
	}
	if got := en.FormatCurrency(85, "USD"); got != "$85.00" {
		t.Errorf("en FormatCurrency(85, USD) = %q, want %q", got, "$85.00")
	}
	if got := en.FormatCurrency(85, "XYZ"); got != "XYZ85.00" {
		t.Errorf("en FormatCurrency with unknown currency = %q, want %q", got, "XYZ85.00")
	}
}

func TestFormatQuantity(t *testing.T) {
	de := Get("de")
	cases := []struct {
		f    float64
		want string
	}{
		{10, "10"},
		{1.5, "1,5"},
		{1.25, "1,25"},
		{0, "0"},
	}
	for _, c := range cases {
		if got := de.FormatQuantity(c.f); got != c.want {
			t.Errorf("FormatQuantity(%v) = %q, want %q", c.f, got, c.want)
		}
	}
}

func TestParseDateFormats(t *testing.T) {
	inputs := []string{"01.03.2026", "1.3.2026", "2026-03-01", "03/01/2026", "01-03-2026"}
	for _, in := range inputs {
		if _, err := ParseDate(in); err != nil {
			t.Errorf("ParseDate(%q) failed: %v", in, err)
		}
	}
	if _, err := ParseDate("not-a-date"); err == nil {
		t.Error("ParseDate should fail on garbage input")
	}
}

func TestFormatDate(t *testing.T) {
	de := Get("de")
	en := Get("en")
	if got := de.FormatDate("2026-03-01"); got != "01.03.2026" {
		t.Errorf("de FormatDate = %q, want %q", got, "01.03.2026")
	}
	if got := en.FormatDate("01.03.2026"); got != "03/01/2026" {
		t.Errorf("en FormatDate = %q, want %q", got, "03/01/2026")
	}
	if got := de.FormatDate("garbage"); got != "garbage" {
		t.Errorf("FormatDate on garbage input = %q, want passthrough", got)
	}
}

func TestResolveOverrides(t *testing.T) {
	trueVal := true
	loc := Resolve("en", Formatting{
		DecimalSep:     ",",
		ThousandSep:    ".",
		CurrencyBefore: &trueVal,
	})
	if loc.DecimalSep != "," || loc.ThousandSep != "." {
		t.Errorf("format overrides not applied: %+v", loc)
	}
	if loc.CurrencyPos != "before" {
		t.Errorf("expected currency position override to 'before', got %q", loc.CurrencyPos)
	}

	base := Get("en")
	if base.DecimalSep == "," {
		t.Error("Resolve must not mutate the shared base locale")
	}
}

func TestResolveDefaultsToDE(t *testing.T) {
	loc := Resolve("", Formatting{})
	if loc.Labels.InvoiceTitle != "Rechnung" {
		t.Errorf("expected default language 'de', got labels %+v", loc.Labels)
	}
}

func TestCurrencySymbol(t *testing.T) {
	if sym := CurrencySymbol("eur"); sym != "€" {
		t.Errorf("CurrencySymbol is case-sensitive, got %q", sym)
	}
	if sym := CurrencySymbol("XAU"); sym != "XAU" {
		t.Errorf("unknown currency should fall back to its own code, got %q", sym)
	}
	if sym := CurrencySymbol(""); sym != "€" {
		t.Errorf("empty currency should default to EUR symbol, got %q", sym)
	}
}

func TestRegisterCurrencySymbol(t *testing.T) {
	RegisterCurrencySymbol("XTS", "T$")
	if sym := CurrencySymbol("xts"); sym != "T$" {
		t.Errorf("registered symbol not honored, got %q", sym)
	}
	// no-ops must not panic or clear existing entries
	RegisterCurrencySymbol("", "X")
	RegisterCurrencySymbol("XTS", "")
	if sym := CurrencySymbol("xts"); sym != "T$" {
		t.Errorf("no-op RegisterCurrencySymbol calls must not overwrite, got %q", sym)
	}
}

func TestParseColor(t *testing.T) {
	cases := []struct {
		hex     string
		r, g, b int
	}{
		{"#5B9BD5", 91, 155, 213},
		{"5B9BD5", 91, 155, 213},
		{"#000000", 0, 0, 0},
		{"#FFFFFF", 255, 255, 255},
	}
	for _, c := range cases {
		r, g, b := ParseColor(c.hex)
		if r != c.r || g != c.g || b != c.b {
			t.Errorf("ParseColor(%q) = (%d,%d,%d), want (%d,%d,%d)", c.hex, r, g, b, c.r, c.g, c.b)
		}
	}

	malformed := []string{"", "#zzzzzz", "#fff", "not-a-color", "#12345g"}
	for _, hex := range malformed {
		r, g, b := ParseColor(hex)
		if r != 91 || g != 155 || b != 213 {
			t.Errorf("ParseColor(%q) should fall back to default, got (%d,%d,%d)", hex, r, g, b)
		}
	}
}

func TestDarkenColor(t *testing.T) {
	got := DarkenColor("#5B9BD5")
	if len(got) != 7 || got[0] != '#' {
		t.Errorf("DarkenColor should return a 7-char hex string, got %q", got)
	}
	r, g, b := ParseColor(got)
	origR, origG, origB := ParseColor("#5B9BD5")
	if r >= origR || g >= origG || b >= origB {
		t.Errorf("DarkenColor should produce a strictly darker color: got (%d,%d,%d) from (%d,%d,%d)", r, g, b, origR, origG, origB)
	}
}

func TestCountryName(t *testing.T) {
	cases := []struct{ code, lang, want string }{
		{"DE", "de", "Deutschland"},
		{"DE", "en", "Germany"},
		{"de", "en", "Germany"}, // lowercase code input
		{"AT", "fr", "Autriche"},
		{"XX", "de", "XX"}, // unmapped code falls back to itself
		{"DE", "xx", "DE"}, // unmapped language falls back to the code
		{"", "de", ""},
		{"  fr  ", "en", "France"}, // surrounding whitespace trimmed
	}
	for _, c := range cases {
		if got := CountryName(c.code, c.lang); got != c.want {
			t.Errorf("CountryName(%q, %q) = %q, want %q", c.code, c.lang, got, c.want)
		}
	}
}
