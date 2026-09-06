package render

import (
	"strings"
	"testing"
)

func TestSnapshotHTMLExactFinancialBreakdown(t *testing.T) {
	p := &TplData{Currency: "EUR", Rows: []TplRow{{Desc: "Work", Price: "100.00", Amt: "90.00", Qty: "1", Unit: "hour", Discount: "10.00", TaxRate: "19.00"}}, TaxGroups: []TplTaxGroup{{Rate: "19.00", Net: "90.00", Tax: "17.10", Gross: "107.10"}}, GrossTotal: "107.10"}
	b, e := SnapshotHTML(p)
	if e != nil {
		t.Fatal(e)
	}
	for _, want := range []string{"EUR", "10.00%", "19.00%", "hour", "17.10"} {
		if !strings.Contains(b, want) {
			t.Errorf("missing exact invoice value %s", want)
		}
	}
}
func TestSnapshotHTMLIssuerTaxIdentity(t *testing.T) {
	p := &TplData{CompanyTaxNumber: "12/34/567", TaxID: "DE123456789", CompanyContact: "Issuer Contact"}
	b, e := SnapshotHTML(p)
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range []string{"12/34/567", "DE123456789", "Issuer Contact"} {
		if !strings.Contains(b, v) {
			t.Errorf("missing %s", v)
		}
	}
}
