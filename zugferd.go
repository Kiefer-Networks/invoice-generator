package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"math"
	"strings"
	"time"
)

// Unit code mapping: German + English unit names → UN/ECE Recommendation 20 codes
var unitCodes = map[string]string{
	"stunde": "HUR", "stunde(n)": "HUR", "stunden": "HUR", "h": "HUR",
	"stück": "C62", "stueck": "C62", "stk": "C62", "stk.": "C62",
	"pauschal": "C62", "pausch.": "C62",
	"tag": "DAY", "tag(e)": "DAY", "tage": "DAY",
	"monat": "MON", "monat(e)": "MON", "monate": "MON",
	"kg": "KGM", "km": "KMT", "m": "MTR", "l": "LTR", "liter": "LTR",
	"m2": "MTK", "m3": "MTQ", "kwh": "KWH",
	// English
	"hour": "HUR", "hours": "HUR", "piece": "C62", "pieces": "C62",
	"day": "DAY", "days": "DAY", "month": "MON", "months": "MON",
	"flat": "C62", "unit": "C62",
}

func mapUnitCode(unit string) string {
	key := strings.ToLower(strings.TrimSpace(unit))
	if code, ok := unitCodes[key]; ok {
		return code
	}
	return "C62" // default: piece/unit
}

func convertDate(dateStr string) string {
	for _, layout := range []string{"02.01.2006", "2.1.2006", "2.01.2006", "02.1.2006", "2006-01-02", "01/02/2006"} {
		if t, err := time.Parse(layout, dateStr); err == nil {
			return t.Format("20060102")
		}
	}
	return dateStr
}

func xmlEsc(s string) string {
	var buf bytes.Buffer
	xml.EscapeText(&buf, []byte(s))
	return buf.String()
}

func fmtAmount(f float64) string {
	return fmt.Sprintf("%.2f", math.Round(f*100)/100)
}

func fmtQuantity(f float64) string {
	if f == math.Floor(f) {
		return fmt.Sprintf("%.0f", f)
	}
	return fmt.Sprintf("%.4f", f)
}

// generateCII produces a Factur-X / ZUGFeRD CII XML (BASIC profile, EN 16931).
func generateCII(cfg *Config) ([]byte, error) {
	var b bytes.Buffer
	ind := 0

	w := func(format string, args ...any) {
		for i := 0; i < ind; i++ {
			b.WriteString("  ")
		}
		fmt.Fprintf(&b, format, args...)
		b.WriteByte('\n')
	}
	open := func(tag string) { w("<%s>", tag); ind++ }
	close := func(tag string) { ind--; w("</%s>", tag) }

	// Defaults
	currency := cfg.Currency
	if currency == "" {
		currency = "EUR"
	}
	sellerCountry := cfg.Company.Country
	if sellerCountry == "" {
		sellerCountry = "DE"
	}
	buyerCountry := cfg.Customer.Country
	if buyerCountry == "" {
		buyerCountry = "DE"
	}

	invoiceID := fmt.Sprintf("%v", cfg.Invoice.Number)
	issueDate := convertDate(cfg.Invoice.Date)
	dueDate := convertDate(cfg.Invoice.DueDate)

	// Tax settings
	isExempt := !cfg.VAT.Liable
	vatRate := cfg.VAT.Rate
	if isExempt {
		vatRate = 0
	}
	catCode := "S"
	if isExempt {
		catCode = "E"
	}

	lineTotal := 0.0
	type lineData struct {
		id, name            string
		qty, price, total   float64
		unit, vCode         string
		vRate               float64
	}
	var lines []lineData

	for i, it := range cfg.Items {
		total := math.Round(it.Quantity*it.Price*100) / 100
		lineTotal += total
		name := it.Description
		if it.Details != "" {
			name += " - " + strings.Join(strings.Fields(strings.TrimSpace(it.Details)), " ")
		}
		lines = append(lines, lineData{
			id:    fmt.Sprintf("%d", i+1),
			name:  name,
			qty:   it.Quantity,
			unit:  mapUnitCode(it.Unit),
			price: it.Price,
			total: total,
			vCode: catCode,
			vRate: vatRate,
		})
	}

	taxBasis := math.Round(lineTotal*100) / 100
	taxAmount := 0.0
	if !isExempt && vatRate > 0 {
		taxAmount = math.Round(taxBasis*vatRate) / 100
	}
	grandTotal := math.Round((taxBasis+taxAmount)*100) / 100

	// === XML output ===
	w(`<?xml version="1.0" encoding="UTF-8"?>`)
	b.WriteString("<rsm:CrossIndustryInvoice\n")
	b.WriteString(`    xmlns:rsm="urn:un:unece:uncefact:data:standard:CrossIndustryInvoice:100"` + "\n")
	b.WriteString(`    xmlns:ram="urn:un:unece:uncefact:data:standard:ReusableAggregateBusinessInformationEntity:100"` + "\n")
	b.WriteString(`    xmlns:udt="urn:un:unece:uncefact:data:standard:UnqualifiedDataType:100"` + "\n")
	b.WriteString(`    xmlns:qdt="urn:un:unece:uncefact:data:standard:QualifiedDataType:100">` + "\n")
	ind = 1

	// Document context
	open("rsm:ExchangedDocumentContext")
	open("ram:GuidelineSpecifiedDocumentContextParameter")
	w("<ram:ID>urn:cen.eu:en16931:2017#compliant#urn:factur-x.eu:1p0:basic</ram:ID>")
	close("ram:GuidelineSpecifiedDocumentContextParameter")
	close("rsm:ExchangedDocumentContext")

	// Document header
	open("rsm:ExchangedDocument")
	w("<ram:ID>%s</ram:ID>", xmlEsc(invoiceID))
	w("<ram:TypeCode>380</ram:TypeCode>")
	open("ram:IssueDateTime")
	w(`<udt:DateTimeString format="102">%s</udt:DateTimeString>`, issueDate)
	close("ram:IssueDateTime")
	close("rsm:ExchangedDocument")

	// Trade transaction
	open("rsm:SupplyChainTradeTransaction")

	// Line items
	for _, ln := range lines {
		open("ram:IncludedSupplyChainTradeLineItem")

		open("ram:AssociatedDocumentLineDocument")
		w("<ram:LineID>%s</ram:LineID>", ln.id)
		close("ram:AssociatedDocumentLineDocument")

		open("ram:SpecifiedTradeProduct")
		w("<ram:Name>%s</ram:Name>", xmlEsc(ln.name))
		close("ram:SpecifiedTradeProduct")

		open("ram:SpecifiedLineTradeAgreement")
		open("ram:NetPriceProductTradePrice")
		w("<ram:ChargeAmount>%s</ram:ChargeAmount>", fmtAmount(ln.price))
		close("ram:NetPriceProductTradePrice")
		close("ram:SpecifiedLineTradeAgreement")

		open("ram:SpecifiedLineTradeDelivery")
		w(`<ram:BilledQuantity unitCode="%s">%s</ram:BilledQuantity>`, ln.unit, fmtQuantity(ln.qty))
		close("ram:SpecifiedLineTradeDelivery")

		open("ram:SpecifiedLineTradeSettlement")
		open("ram:ApplicableTradeTax")
		w("<ram:TypeCode>VAT</ram:TypeCode>")
		w("<ram:CategoryCode>%s</ram:CategoryCode>", ln.vCode)
		w("<ram:RateApplicablePercent>%s</ram:RateApplicablePercent>", fmtAmount(ln.vRate))
		close("ram:ApplicableTradeTax")
		open("ram:SpecifiedTradeSettlementLineMonetarySummation")
		w("<ram:LineTotalAmount>%s</ram:LineTotalAmount>", fmtAmount(ln.total))
		close("ram:SpecifiedTradeSettlementLineMonetarySummation")
		close("ram:SpecifiedLineTradeSettlement")

		close("ram:IncludedSupplyChainTradeLineItem")
	}

	// Seller
	open("ram:ApplicableHeaderTradeAgreement")
	open("ram:SellerTradeParty")
	w("<ram:Name>%s</ram:Name>", xmlEsc(cfg.Company.Name))
	open("ram:PostalTradeAddress")
	w("<ram:PostcodeCode>%s</ram:PostcodeCode>", xmlEsc(cfg.Company.ZIP))
	w("<ram:LineOne>%s</ram:LineOne>", xmlEsc(cfg.Company.Address))
	w("<ram:CityName>%s</ram:CityName>", xmlEsc(cfg.Company.City))
	w("<ram:CountryID>%s</ram:CountryID>", xmlEsc(sellerCountry))
	close("ram:PostalTradeAddress")
	if cfg.Company.Email != "" {
		open("ram:URIUniversalCommunication")
		w(`<ram:URIID schemeID="EM">%s</ram:URIID>`, xmlEsc(cfg.Company.Email))
		close("ram:URIUniversalCommunication")
	}
	if cfg.Company.VatID != "" {
		open("ram:SpecifiedTaxRegistration")
		w(`<ram:ID schemeID="VA">%s</ram:ID>`, xmlEsc(cfg.Company.VatID))
		close("ram:SpecifiedTaxRegistration")
	}
	if cfg.Company.TaxNumber != "" {
		open("ram:SpecifiedTaxRegistration")
		w(`<ram:ID schemeID="FC">%s</ram:ID>`, xmlEsc(cfg.Company.TaxNumber))
		close("ram:SpecifiedTaxRegistration")
	}
	close("ram:SellerTradeParty")

	// Buyer
	open("ram:BuyerTradeParty")
	w("<ram:Name>%s</ram:Name>", xmlEsc(cfg.Customer.Name))
	open("ram:PostalTradeAddress")
	zip := cfg.Customer.ZIP
	zip = strings.TrimPrefix(zip, "D-")
	zip = strings.TrimPrefix(zip, "DE-")
	w("<ram:PostcodeCode>%s</ram:PostcodeCode>", xmlEsc(zip))
	w("<ram:LineOne>%s</ram:LineOne>", xmlEsc(cfg.Customer.Address))
	w("<ram:CityName>%s</ram:CityName>", xmlEsc(cfg.Customer.City))
	w("<ram:CountryID>%s</ram:CountryID>", xmlEsc(buyerCountry))
	close("ram:PostalTradeAddress")
	if cfg.Customer.VatID != "" {
		open("ram:SpecifiedTaxRegistration")
		w(`<ram:ID schemeID="VA">%s</ram:ID>`, xmlEsc(cfg.Customer.VatID))
		close("ram:SpecifiedTaxRegistration")
	}
	close("ram:BuyerTradeParty")
	close("ram:ApplicableHeaderTradeAgreement")

	// Delivery
	open("ram:ApplicableHeaderTradeDelivery")
	open("ram:ActualDeliverySupplyChainEvent")
	open("ram:OccurrenceDateTime")
	w(`<udt:DateTimeString format="102">%s</udt:DateTimeString>`, issueDate)
	close("ram:OccurrenceDateTime")
	close("ram:ActualDeliverySupplyChainEvent")
	close("ram:ApplicableHeaderTradeDelivery")

	// Settlement
	open("ram:ApplicableHeaderTradeSettlement")
	w("<ram:InvoiceCurrencyCode>%s</ram:InvoiceCurrencyCode>", xmlEsc(currency))

	// Payment means (SEPA)
	if cfg.Company.Bank.IBAN != "" {
		open("ram:SpecifiedTradeSettlementPaymentMeans")
		w("<ram:TypeCode>58</ram:TypeCode>")
		open("ram:PayeePartyCreditorFinancialAccount")
		w("<ram:IBANID>%s</ram:IBANID>", xmlEsc(cfg.Company.Bank.IBAN))
		close("ram:PayeePartyCreditorFinancialAccount")
		if cfg.Company.Bank.BIC != "" {
			open("ram:PayeeSpecifiedCreditorFinancialInstitution")
			w("<ram:BICID>%s</ram:BICID>", xmlEsc(cfg.Company.Bank.BIC))
			close("ram:PayeeSpecifiedCreditorFinancialInstitution")
		}
		close("ram:SpecifiedTradeSettlementPaymentMeans")
	}

	// Tax breakdown
	open("ram:ApplicableTradeTax")
	w("<ram:CalculatedAmount>%s</ram:CalculatedAmount>", fmtAmount(taxAmount))
	w("<ram:TypeCode>VAT</ram:TypeCode>")
	if isExempt {
		reason := "VAT exempt pursuant to § 19 UStG (small business exemption)."
		if cfg.Notice != "" {
			reason = cfg.Notice
		}
		w("<ram:ExemptionReason>%s</ram:ExemptionReason>", xmlEsc(reason))
	}
	w("<ram:BasisAmount>%s</ram:BasisAmount>", fmtAmount(taxBasis))
	w("<ram:CategoryCode>%s</ram:CategoryCode>", catCode)
	w("<ram:RateApplicablePercent>%s</ram:RateApplicablePercent>", fmtAmount(vatRate))
	close("ram:ApplicableTradeTax")

	// Payment terms
	if dueDate != "" {
		open("ram:SpecifiedTradePaymentTerms")
		open("ram:DueDateDateTime")
		w(`<udt:DateTimeString format="102">%s</udt:DateTimeString>`, dueDate)
		close("ram:DueDateDateTime")
		close("ram:SpecifiedTradePaymentTerms")
	}

	// Monetary totals
	open("ram:SpecifiedTradeSettlementHeaderMonetarySummation")
	w("<ram:LineTotalAmount>%s</ram:LineTotalAmount>", fmtAmount(lineTotal))
	w("<ram:TaxBasisTotalAmount>%s</ram:TaxBasisTotalAmount>", fmtAmount(taxBasis))
	w(`<ram:TaxTotalAmount currencyID="%s">%s</ram:TaxTotalAmount>`, xmlEsc(currency), fmtAmount(taxAmount))
	w("<ram:GrandTotalAmount>%s</ram:GrandTotalAmount>", fmtAmount(grandTotal))
	w("<ram:DuePayableAmount>%s</ram:DuePayableAmount>", fmtAmount(grandTotal))
	close("ram:SpecifiedTradeSettlementHeaderMonetarySummation")

	close("ram:ApplicableHeaderTradeSettlement")
	close("rsm:SupplyChainTradeTransaction")

	ind = 0
	w("</rsm:CrossIndustryInvoice>")

	return b.Bytes(), nil
}
