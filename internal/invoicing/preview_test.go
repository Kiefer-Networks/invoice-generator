package invoicing

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
	"time"

	"github.com/kiefer-networks/invoice-generator/internal/config"
	"github.com/kiefer-networks/invoice-generator/internal/locale"
	"github.com/kiefer-networks/invoice-generator/internal/render"
	"github.com/kiefer-networks/invoice-generator/internal/store"
)

func TestDraftPreviewUsesRendererBoundaryAndExactTotals(t *testing.T) {
	d := Draft{IssueDate: time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC), Currency: "EUR", Customer: store.CustomerInput{DisplayName: "Recipient", AddressLine1: "Street 7", PostalCode: "10115", City: "Berlin", Country: "DE"}, DueDate: time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC), NetMinor: 90, TaxMinor: 17, GrossMinor: 107, TaxGroups: []TaxGroup{{TaxRateBasisPoints: 1900, NetMinor: 90, TaxMinor: 17, GrossMinor: 107}}, Lines: []DraftLine{{Title: "Advice", Description: "Details", Unit: "hour", QuantityScaled: 3333, UnitPriceMinor: 300, DiscountBasisPoints: 1000, TaxRateBasisPoints: 1900, NetMinor: 90, TaxMinor: 17, GrossMinor: 107}}}
	p := PreviewData(d, store.CompanyInput{LegalName: "Issuer", AddressLine1: "Issuer Street", City: "Hamburg", Country: "DE"})
	if p.CompanyName != "Issuer" || p.CustName != "Recipient" || len(p.CustLines) < 2 || p.InvDate != "2026-09-06" || p.DueDate != "2026-09-20" || p.Currency != "EUR" || p.Subtotal != "0.90" || p.TaxAmt != "0.17" || p.GrossTotal != "1.07" {
		t.Fatalf("boundary: %#v", p)
	}
	if len(p.Rows) != 1 || p.Rows[0].Desc != "Advice" || p.Rows[0].Det != "Details" || p.Rows[0].Qty != "0.3333" || p.Rows[0].Unit != "hour" || p.Rows[0].Price != "3.00" || p.Rows[0].Discount != "10.00" || p.Rows[0].Amt != "0.90" {
		t.Fatalf("row: %#v", p.Rows)
	}
	if len(p.TaxGroups) != 1 || p.TaxGroups[0].Rate != "19.00" || p.TaxGroups[0].Net != "0.90" || p.TaxGroups[0].Tax != "0.17" || p.TaxGroups[0].Gross != "1.07" {
		t.Fatalf("VAT: %#v", p.TaxGroups)
	}
	tmpl, err := template.New("document").Parse(render.DefaultTemplate())
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err = tmpl.Execute(&output, p); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Issuer", "Issuer Street", "Hamburg", "Recipient", "Street 7", "10115 Berlin", "2026-09-06", "2026-09-20", "Details", "0.3333", "0.90", "0.17", "1.07"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("existing renderer template missing %s", want)
		}
	}
}

func TestDraftPreviewKeepsMinorUnitsBeyondFloatPrecision(t *testing.T) {
	p := PreviewData(Draft{NetMinor: 9007199254740993, GrossMinor: 9007199254740993}, store.CompanyInput{})
	if p.Subtotal != "90071992547409.93" || p.GrossTotal != "90071992547409.93" {
		t.Fatalf("render boundary lost cents: %#v", p)
	}
}
func TestFinalizedDefaultRendererIncludesCorrectionRecipientAndServiceDate(t *testing.T) {
	s := Snapshot{Kind: "correction", Correction: CorrectionReference{OriginalID: "original-id", OriginalNumber: "INV-2026-1"}, Draft: Draft{ServiceDate: "2026-08-31", Customer: store.CustomerInput{DisplayName: "Buyer Alias", LegalName: "Buyer Legal GmbH", ContactName: "Pat", Email: "buyer@example.test", VATIdentifier: "DE123", AddressLine1: "Buyer Street", City: "Berlin", Country: "DE"}}, Company: store.CompanyInput{LegalName: "Issuer"}}
	p := s.RenderData()
	tmpl, err := template.New("default").Parse(render.DefaultTemplate())
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err = tmpl.Execute(&b, p); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"INV-2026-1", "Correction", "2026-08-31", "Buyer Alias", "Buyer Legal GmbH", "Pat", "buyer@example.test", "DE123", "Buyer Street"} {
		if !strings.Contains(b.String(), value) {
			t.Errorf("default renderer missing %s", value)
		}
	}
}
func TestCorrectionConfigRendererRetainsDocumentSemantics(t *testing.T) {
	snap := Snapshot{Kind: "correction", Correction: CorrectionReference{OriginalID: "old-id", OriginalNumber: "INV-2026-1"}, Language: "en", Draft: Draft{ServiceDate: "2026-08-31", Customer: store.CustomerInput{DisplayName: "Alias", LegalName: "Legal Buyer", ContactName: "Contact", Email: "buyer@example.test", VATIdentifier: "VAT-ID"}}}
	cfg, err := snap.Config()
	if err != nil {
		t.Fatal(err)
	}
	data := render.PrepareTplData(&cfg, locale.Get("en"), config.DocInvoice)
	if data.CustDisplayName != "Alias" || data.CustEmail != "buyer@example.test" || !strings.Contains(data.Title, "Correction") || data.ServiceDate != "2026-08-31" || data.CorrectionOfNumber != "INV-2026-1" {
		t.Fatalf("legacy correction renderer=%+v", data)
	}
}
