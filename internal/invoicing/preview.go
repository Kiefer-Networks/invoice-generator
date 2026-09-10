package invoicing

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/kiefer-networks/invoice-generator/internal/config"
	"github.com/kiefer-networks/invoice-generator/internal/locale"
	"github.com/kiefer-networks/invoice-generator/internal/render"
	"github.com/kiefer-networks/invoice-generator/internal/store"
)

// PreviewData is the document rendering boundary. It reuses the renderer's
// preformatted model and address rules without recalculating integer totals
// through the legacy single-rate floating-point configuration calculator.
func PreviewData(d Draft, company store.CompanyInput) *render.TplData {
	p := &render.TplData{Title: "Invoice draft", Status: "DRAFT", StatusClass: "draft", Currency: d.Currency, CompanyName: company.LegalName, CompanyEmail: company.Email, CompanyPhone: company.Phone, TaxID: company.VATIdentifier, CustName: d.Customer.DisplayName, DueDate: d.DueDate.Format("2006-01-02"), Subtotal: minorText(d.NetMinor), TaxAmt: minorText(d.TaxMinor), GrossTotal: minorText(d.GrossMinor)}
	p.Lang = d.Customer.PreferredLanguage
	if p.Lang == "" {
		p.Lang = company.DefaultLanguage
	}
	p.LB = locale.Get(p.Lang).Labels
	p.TaxLabel = p.LB.Tax
	p.Color = company.BrandColor
	if p.Color == "" {
		p.Color = "#5B9BD5"
	}
	p.ColorDark = locale.DarkenColor(p.Color)
	p.CompanyAddrLines = config.AddressLines(strings.TrimSpace(company.AddressLine1+"\n"+company.AddressLine2), company.PostalCode, company.City, company.Country, "", d.Customer.PreferredLanguage)
	p.CustLines = config.AddressLines(strings.TrimSpace(d.Customer.AddressLine1+"\n"+d.Customer.AddressLine2), d.Customer.PostalCode, d.Customer.City, d.Customer.Country, "", d.Customer.PreferredLanguage)
	if !d.IssueDate.IsZero() {
		p.InvDate = d.IssueDate.Format("2006-01-02")
	}
	for _, l := range d.Lines {
		p.Rows = append(p.Rows, render.TplRow{Desc: l.Title, Det: l.Description, Unit: l.Unit, Qty: quantityText(l.QuantityScaled), Price: minorText(l.UnitPriceMinor), Discount: minorText(l.DiscountBasisPoints), TaxRate: minorText(l.TaxRateBasisPoints), Amt: minorText(l.NetMinor)})
	}
	for _, g := range d.TaxGroups {
		if g.TaxRateBasisPoints > 0 {
			p.HasVAT = true
		}
		p.TaxGroups = append(p.TaxGroups, render.TplTaxGroup{Rate: minorText(g.TaxRateBasisPoints), Net: minorText(g.NetMinor), Tax: minorText(g.TaxMinor), Gross: minorText(g.GrossMinor)})
	}
	return p
}
func minorText(v int64) string { return fmt.Sprintf("%d.%02d", v/100, v%100) }
func quantityText(v int64) string {
	if v%10000 == 0 {
		return strconv.FormatInt(v/10000, 10)
	}
	return strconv.FormatInt(v/10000, 10) + "." + strings.TrimRight(fmt.Sprintf("%04d", v%10000), "0")
}
