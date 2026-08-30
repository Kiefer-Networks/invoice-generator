package zugferd

import (
	"encoding/xml"
	"strings"
	"testing"

	"github.com/kiefer-networks/invoice-generator/internal/config"
)

func sampleConfig() *config.Config {
	return &config.Config{
		Currency: "EUR",
		Company: config.Company{
			Name:    "Test GmbH",
			Address: "Main St 1",
			ZIP:     "12345",
			City:    "Berlin",
			Country: "DE",
			Email:   "info@test.de",
			VatID:   "DE123456789",
			Bank:    config.BankInfo{IBAN: "DE89370400440532013000", BIC: "COBADEFFXXX"},
		},
		Customer: config.Customer{
			Name:    "Client GmbH",
			Address: "Client Rd 42",
			ZIP:     "54321",
			City:    "Munich",
			Country: "DE",
			VatID:   "DE987654321",
		},
		Invoice: config.InvInfo{
			Number:  "2026-001",
			Date:    "01.03.2026",
			DueDate: "15.03.2026",
		},
		Items: []config.Item{
			{Description: "Consulting", Quantity: 10, Unit: "hours", Price: 85.00},
		},
		VAT: config.VATConfig{Liable: true, Rate: 19.0},
	}
}

func TestGenerateCIIWellFormedXML(t *testing.T) {
	xmlBytes, err := GenerateCII(sampleConfig())
	if err != nil {
		t.Fatalf("GenerateCII failed: %v", err)
	}

	// Well-formedness check: walk every token; any syntax error surfaces here.
	dec := xml.NewDecoder(strings.NewReader(string(xmlBytes)))
	for {
		_, err := dec.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			t.Fatalf("generated XML is not well-formed: %v", err)
		}
	}
}

func TestGenerateCIIContainsCoreFields(t *testing.T) {
	xmlBytes, err := GenerateCII(sampleConfig())
	if err != nil {
		t.Fatalf("GenerateCII failed: %v", err)
	}
	out := string(xmlBytes)

	for _, want := range []string{
		"<?xml version=\"1.0\" encoding=\"UTF-8\"?>",
		"urn:factur-x.eu:1p0:basic",
		"<ram:ID>2026-001</ram:ID>",
		"<ram:Name>Test GmbH</ram:Name>",
		"<ram:Name>Client GmbH</ram:Name>",
		"<ram:IBANID>DE89370400440532013000</ram:IBANID>",
		"<ram:GrandTotalAmount>1011.50</ram:GrandTotalAmount>", // 850 net + 19% VAT
	} {
		if !strings.Contains(out, want) {
			t.Errorf("generated CII XML missing expected fragment: %q", want)
		}
	}
}

func TestGenerateCIIVatExemption(t *testing.T) {
	cfg := sampleConfig()
	cfg.VAT = config.VATConfig{Liable: false}
	cfg.Notice = "Small business exemption §19 UStG"

	xmlBytes, err := GenerateCII(cfg)
	if err != nil {
		t.Fatalf("GenerateCII failed: %v", err)
	}
	out := string(xmlBytes)
	if !strings.Contains(out, "<ram:CategoryCode>E</ram:CategoryCode>") {
		t.Error("expected exemption category code 'E' when not VAT liable")
	}
	if !strings.Contains(out, "Small business exemption") {
		t.Error("expected custom notice to be used as exemption reason")
	}
	if !strings.Contains(out, "<ram:CalculatedAmount>0.00</ram:CalculatedAmount>") {
		t.Error("expected zero tax amount when VAT exempt")
	}
}

func TestGenerateCIIEscapesXML(t *testing.T) {
	cfg := sampleConfig()
	cfg.Company.Name = `A & B <GmbH>`
	xmlBytes, err := GenerateCII(cfg)
	if err != nil {
		t.Fatalf("GenerateCII failed: %v", err)
	}
	out := string(xmlBytes)
	if strings.Contains(out, "<GmbH>") {
		t.Error("company name with XML special characters was not escaped")
	}
	if !strings.Contains(out, "&amp;") {
		t.Error("expected '&' to be escaped as '&amp;'")
	}
}

func TestMapUnitCode(t *testing.T) {
	cases := map[string]string{
		"Stunde(n)": "HUR",
		"hours":     "HUR",
		"Stück":     "C62",
		"kg":        "KGM",
		"unknown-x": "C62", // default fallback
		"":          "C62",
	}
	for in, want := range cases {
		if got := mapUnitCode(in); got != want {
			t.Errorf("mapUnitCode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestConvertDate(t *testing.T) {
	cases := map[string]string{
		"01.03.2026": "20260301",
		"2026-03-01": "20260301",
		"03/01/2026": "20260301",
	}
	for in, want := range cases {
		if got := convertDate(in); got != want {
			t.Errorf("convertDate(%q) = %q, want %q", in, got, want)
		}
	}
	// Unparseable input passes through unchanged rather than crashing.
	if got := convertDate("garbage"); got != "garbage" {
		t.Errorf("convertDate(garbage) = %q, want passthrough", got)
	}
}

func TestFmtAmountAndQuantity(t *testing.T) {
	if got := fmtAmount(85); got != "85.00" {
		t.Errorf("fmtAmount(85) = %q, want %q", got, "85.00")
	}
	if got := fmtAmount(85.005); got != "85.01" {
		t.Errorf("fmtAmount(85.005) rounding = %q, want %q", got, "85.01")
	}
	if got := fmtQuantity(10); got != "10" {
		t.Errorf("fmtQuantity(10) = %q, want %q", got, "10")
	}
	if got := fmtQuantity(1.5); got != "1.5000" {
		t.Errorf("fmtQuantity(1.5) = %q, want %q", got, "1.5000")
	}
}
