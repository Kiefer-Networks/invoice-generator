package zugferd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kiefer-networks/invoice-generator/internal/invoicing"
	"github.com/kiefer-networks/invoice-generator/internal/store"
)

func exactFixture() invoicing.Snapshot {
	return invoicing.Snapshot{Kind: "correction", Correction: invoicing.CorrectionReference{OriginalID: "original", OriginalNumber: "INV-1"}, Notes: "Thanks & regards", Language: "en", Company: store.CompanyInput{Currency: "EUR", DefaultLanguage: "en", InvoicePrefix: "INV", BrandColor: "#123456", LegalName: "Issuer", AddressLine1: "Main 1", PostalCode: "10115", City: "Berlin", Country: "DE", VATIdentifier: "DE123456789", TaxNumber: "12/34/567", IBAN: "DE123", BankName: "Bank"}, Draft: invoicing.Draft{Number: "INV-2", Currency: "EUR", ServiceDate: "2026-08-31", IssueDate: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), DueDate: time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC), Customer: store.CustomerInput{Currency: "EUR", PreferredLanguage: "en", Number: "C-1", DisplayName: "Buyer", LegalName: "Buyer GmbH", ContactName: "Person", Email: "buyer@example.com", AddressLine1: "Street 2", PostalCode: "10117", City: "Berlin", Country: "DE", VATIdentifier: "DE987654321", PaymentTermsDays: 14}, Lines: []invoicing.DraftLine{{Title: "Discount", Description: "<script>", Unit: "hour", QuantityScaled: 10000, UnitPriceMinor: 10000, DiscountBasisPoints: 1000, TaxRateBasisPoints: 1900, NetMinor: 9000, TaxMinor: 1710, GrossMinor: 10710}, {Title: "Reduced", Unit: "piece", QuantityScaled: 10000, UnitPriceMinor: 1000, TaxRateBasisPoints: 700, NetMinor: 1000, TaxMinor: 70, GrossMinor: 1070}}, NetMinor: 10000, TaxMinor: 1780, GrossMinor: 11780, TaxGroups: []invoicing.TaxGroup{{TaxRateBasisPoints: 700, NetMinor: 1000, TaxMinor: 70, GrossMinor: 1070}, {TaxRateBasisPoints: 1900, NetMinor: 9000, TaxMinor: 1710, GrossMinor: 10710}}}}
}
func TestGenerateExactCIIPreservesSnapshotAndSchema(t *testing.T) {
	s := exactFixture()
	b, e := GenerateSnapshot(s)
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range []string{"<ram:TypeCode>384</ram:TypeCode>", "INV-1", "20260831", "Buyer GmbH", "Person", "buyer@example.com", "19.00", "7.00", "90.00", "117.80", "&lt;script&gt;", "12/34/567"} {
		if !strings.Contains(string(b), v) {
			t.Errorf("missing %s", v)
		}
	}
	if e = ValidateCII(context.Background(), b); e != nil {
		t.Fatal(e)
	}
	bad := strings.Replace(string(b), "<ram:TypeCode>384</ram:TypeCode>", "<ram:Unknown>384</ram:Unknown>", 1)
	if e = ValidateCII(context.Background(), []byte(bad)); e == nil {
		t.Fatal("schema-invalid XML accepted")
	}
	if e = ValidateCII(context.Background(), []byte(`<!DOCTYPE x [<!ENTITY e SYSTEM "file:///etc/passwd">]><x>&e;</x>`)); e == nil {
		t.Fatal("DTD accepted")
	}
}

func TestGenerateExactCIIDiscountAllowance(t *testing.T) {
	s := exactFixture()
	b, e := GenerateSnapshot(s)
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range []string{"<ram:ChargeAmount>100.00</ram:ChargeAmount>", "<ram:CalculationPercent>10.00</ram:CalculationPercent>", "<ram:ActualAmount>10.00</ram:ActualAmount>", "<ram:BasisAmount>100.00</ram:BasisAmount>"} {
		if !strings.Contains(string(b), v) {
			t.Errorf("missing discount accounting %s", v)
		}
	}
}
func TestSchemaProvenanceChecksums(t *testing.T) {
	data, e := os.ReadFile("schema/manifest.json")
	if e != nil {
		t.Fatal(e)
	}
	var manifest struct {
		Files []struct{ File, SHA256, Source string }
	}
	if e = json.Unmarshal(data, &manifest); e != nil {
		t.Fatal(e)
	}
	if len(manifest.Files) != 4 {
		t.Fatal("missing pinned schemas")
	}
	for _, entry := range manifest.Files {
		b, e := exactResources.ReadFile("schema/" + entry.File)
		if e != nil {
			t.Fatal(e)
		}
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != entry.SHA256 {
			t.Fatal("schema drift", entry.File)
		}
	}
}
