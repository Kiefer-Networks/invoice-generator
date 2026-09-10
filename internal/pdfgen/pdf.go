// Package pdfgen implements the built-in fpdf-based invoice renderer,
// used as a fallback when Chrome/Chromium is unavailable or when explicitly
// requested. It can embed a ZUGFeRD/Factur-X XML attachment directly.
package pdfgen

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-pdf/fpdf"
	"github.com/kiefer-networks/invoice-generator/internal/config"
	"github.com/kiefer-networks/invoice-generator/internal/locale"
)

// splitLines breaks text into wrapped lines that fit within width w.
func splitLines(pdf *fpdf.Fpdf, text string, w float64) []string {
	var result []string
	for _, para := range strings.Split(text, "\n") {
		if strings.TrimSpace(para) == "" {
			continue
		}
		result = append(result, wrapText(pdf, para, w)...)
	}
	if len(result) == 0 {
		result = append(result, "")
	}
	return result
}

// wrapText splits a single paragraph into lines that fit within maxW.
func wrapText(pdf *fpdf.Fpdf, text string, maxW float64) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	current := words[0]
	for _, word := range words[1:] {
		test := current + " " + word
		if pdf.GetStringWidth(test) <= maxW {
			current = test
		} else {
			lines = append(lines, current)
			current = word
		}
	}
	return append(lines, current)
}

// statusColor returns the (background, text) RGB pair for a status pill,
// matching the light/dezent pill styling of the HTML template's .pill
// classes (no status falls back to a plain gray pill).
func statusColor(status string) (bg, text [3]int) {
	switch strings.ToUpper(status) {
	case "PAID":
		return [3]int{227, 244, 234}, [3]int{26, 143, 82} // light green / green
	case "OVERDUE":
		return [3]int{251, 231, 231}, [3]int{200, 53, 47} // light red / red
	default:
		return [3]int{242, 244, 246}, [3]int{118, 127, 140} // light gray / soft gray
	}
}

// Generate creates a modern invoice/quote PDF using the built-in fpdf
// renderer. If zugferdXML is non-empty, it is embedded as a PDF file
// attachment (Factur-X / ZUGFeRD BASIC profile) — callers must not pass
// a non-empty zugferdXML for docType == config.DocQuote.
func Generate(cfg *config.Config, loc *locale.Locale, docType config.DocType, outputPath string, zugferdXML []byte) error {
	fontRegular, fontBold, err := config.FindFonts(cfg)
	if err != nil {
		return err
	}

	fontDir := filepath.Dir(fontRegular)
	pdf := fpdf.New("P", "mm", "A4", fontDir)
	pdf.AddUTF8Font("inv", "", filepath.Base(fontRegular))
	pdf.AddUTF8Font("inv", "B", filepath.Base(fontBold))

	lm, rm := 18.0, 18.0
	pdf.SetMargins(lm, 0, rm)
	pdf.SetAutoPageBreak(true, 22)

	pageW, pageH := pdf.GetPageSize()
	cw := pageW - lm - rm // usable content width

	// Accent color — used sparingly: the total-due bar and nothing else.
	aR, aG, aB := locale.ParseColor(cfg.Color)

	// Ink/soft/faint/line grays, matching the HTML template's palette.
	const (
		inkR, inkG, inkB          = 28, 33, 38
		softR, softG, softB       = 118, 127, 140
		faiR, faiG, faiB          = 164, 172, 184
		lineR, lineG, lineB       = 221, 226, 232
		headBgR, headBgG, headBgB = 242, 244, 246
	)

	// Currency
	curr := cfg.Currency
	if curr == "" {
		curr = "EUR"
	}
	if cfg.Formatting.CurrencySymbol != "" {
		locale.RegisterCurrencySymbol(curr, cfg.Formatting.CurrencySymbol)
	}

	// Tax calculations
	netTotal := config.CalcNet(cfg.Items)
	taxAmt := config.CalcTax(netTotal, cfg.VAT)
	grossTotal := math.Round((netTotal+taxAmt)*100) / 100

	lb := loc.Labels

	// Quote (Angebot): swap title/number/second-date-row labels for their
	// quote equivalents, and use ValidUntil instead of DueDate.
	docTitle := lb.InvoiceTitle
	docNrLabel := lb.InvoiceNr
	dueDateLabel := lb.DueDate
	dueDateValue := cfg.Invoice.DueDate
	if docType == config.DocQuote {
		docTitle = lb.QuoteTitle
		docNrLabel = lb.QuoteNr
		dueDateLabel = lb.ValidUntil
		dueDateValue = cfg.Invoice.ValidUntil
		if dueDateValue == "" {
			dueDateValue = cfg.Invoice.DueDate
		}
	}

	// ========== FOOTER ==========
	pdf.SetFooterFunc(func() {
		pdf.SetDrawColor(220, 220, 220)
		pdf.SetLineWidth(0.2)
		pdf.Line(lm, pageH-15, pageW-rm, pageH-15)
		pdf.SetFont("inv", "", 6.5)
		pdf.SetTextColor(150, 150, 150)
		co := cfg.Company
		line := fmt.Sprintf("%s  ·  %s, %s %s  ·  %s  ·  %s",
			co.Name, co.Address, co.ZIP, co.City, co.Email, co.Phone)
		pdf.SetXY(lm, pageH-13)
		pdf.CellFormat(cw, 3.5, line, "", 0, "C", false, 0, "")
		if co.Bank.IBAN != "" {
			bank := fmt.Sprintf("IBAN: %s  ·  BIC: %s  ·  %s", co.Bank.IBAN, co.Bank.BIC, co.Bank.Name)
			pdf.SetXY(lm, pageH-9.5)
			pdf.CellFormat(cw, 3.5, bank, "", 0, "C", false, 0, "")
		}
		pdf.SetTextColor(0, 0, 0)
	})

	pdf.AddPage()

	// ========== HEADER: plain title + logo ==========
	pdf.SetFont("inv", "B", 22)
	pdf.SetTextColor(inkR, inkG, inkB)
	pdf.SetXY(lm, 14)
	pdf.CellFormat(100, 10, docTitle, "", 0, "L", false, 0, "")

	if cfg.Logo != "" {
		if _, statErr := os.Stat(cfg.Logo); statErr == nil {
			opts := fpdf.ImageOptions{ReadDpi: true}
			info := pdf.RegisterImageOptions(cfg.Logo, opts)
			if info != nil {
				imgH := 14.0
				imgW := info.Width() / info.Height() * imgH
				if imgW > 60 {
					imgW = 60
					imgH = info.Height() / info.Width() * imgW
				}
				pdf.ImageOptions(cfg.Logo, pageW-rm-imgW, 14, imgW, imgH, false, opts, 0, "")
			}
		}
	}

	// ========== ONE-LINE SENDER IDENTITY ==========
	companyAddrLines := config.AddressLines(cfg.Company.Address, cfg.Company.ZIP, cfg.Company.City, cfg.Company.Country, "", cfg.Language)
	taxID := cfg.Company.VatID
	if taxID == "" {
		taxID = cfg.Company.TaxNumber
	}
	letterline := cfg.Company.Name
	for _, l := range companyAddrLines {
		letterline += ", " + l
	}
	if taxID != "" {
		letterline += "  ·  " + lb.TaxID + ": " + taxID
	}
	pdf.SetFont("inv", "", 8)
	pdf.SetTextColor(softR, softG, softB)
	pdf.SetXY(lm, 30)
	pdf.MultiCell(cw, 3.8, letterline, "", "L", false)

	// ========== BILL TO (left) + META (right) ==========
	topY := pdf.GetY() + 6

	// Customer lines: contact person, then Street / ZIP City / Country,
	// then VAT ID. No email/phone here — this is the postal address block.
	var custLines []string
	if cfg.Customer.Contact != "" {
		custLines = append(custLines, cfg.Customer.Contact)
	}
	custLines = append(custLines, config.AddressLines(
		cfg.Customer.Address, cfg.Customer.ZIP, cfg.Customer.City,
		cfg.Customer.Country, cfg.Customer.CountryName, cfg.Language,
	)...)
	if cfg.Customer.VatID != "" {
		custLines = append(custLines, lb.TaxID+": "+cfg.Customer.VatID)
	}

	pdf.SetFont("inv", "B", 9)
	pdf.SetTextColor(inkR, inkG, inkB)
	pdf.SetXY(lm, topY)
	pdf.CellFormat(cw*0.5, 4.5, lb.InvoiceTo, "", 2, "L", false, 0, "")

	pdf.SetFont("inv", "", 10.5)
	pdf.SetX(lm)
	pdf.CellFormat(cw*0.5, 5, cfg.Customer.Name, "", 2, "L", false, 0, "")

	pdf.SetFont("inv", "", 9)
	pdf.SetTextColor(softR, softG, softB)
	for _, line := range custLines {
		pdf.SetX(lm)
		pdf.CellFormat(cw*0.5, 4.3, line, "", 2, "L", false, 0, "")
	}
	billToBottomY := pdf.GetY()

	// Meta block: right-aligned mini table of label/value rows.
	metaW := 62.0
	metaX := pageW - rm - metaW
	metaY := topY
	type detRow struct{ label, value string }
	details := []detRow{
		{docNrLabel, fmt.Sprintf("%v", cfg.Invoice.Number)},
		{lb.InvoiceDate, loc.FormatDate(cfg.Invoice.Date)},
		{dueDateLabel, loc.FormatDate(dueDateValue)},
	}
	for _, d := range details {
		pdf.SetXY(metaX, metaY)
		pdf.SetFont("inv", "", 8.5)
		pdf.SetTextColor(softR, softG, softB)
		pdf.CellFormat(metaW*0.5, 5, d.label, "", 0, "L", false, 0, "")
		pdf.SetFont("inv", "B", 8.5)
		pdf.SetTextColor(inkR, inkG, inkB)
		pdf.CellFormat(metaW*0.5, 5, d.value, "", 0, "R", false, 0, "")
		metaY += 5.5
	}
	if cfg.Invoice.Status != "" {
		bg, txt := statusColor(cfg.Invoice.Status)
		badgeText := "  " + cfg.Invoice.Status + "  "
		pdf.SetFont("inv", "B", 7.5)
		badgeW := pdf.GetStringWidth(badgeText) + 2
		badgeX := metaX + metaW - badgeW
		pdf.SetFillColor(bg[0], bg[1], bg[2])
		pdf.RoundedRect(badgeX, metaY, badgeW, 5.5, 2.5, "1234", "F")
		pdf.SetTextColor(txt[0], txt[1], txt[2])
		pdf.SetXY(badgeX, metaY+0.4)
		pdf.CellFormat(badgeW, 5, badgeText, "", 0, "C", false, 0, "")
		metaY += 7
	}

	// ========== ITEMS TABLE ==========
	tableY := billToBottomY
	if metaY > tableY {
		tableY = metaY
	}
	tableY += 10
	pdf.SetY(tableY)

	// Column widths: description, qty, unit price, amount
	colW := []float64{90, 18, 33, 33} // = 174

	// Table header: light gray background, bold black uppercase labels —
	// matching the HTML template's thead styling.
	drawTableHdr := func() {
		y := pdf.GetY()
		pdf.SetFillColor(headBgR, headBgG, headBgB)
		pdf.Rect(lm, y, cw, 7, "F")
		pdf.SetTextColor(inkR, inkG, inkB)
		pdf.SetFont("inv", "B", 7.5)
		hdrs := []string{lb.Description, lb.Quantity, lb.UnitPrice, lb.Amount}
		aligns := []string{"L", "R", "R", "R"}
		x := lm
		for i, h := range hdrs {
			pdf.SetXY(x+2, y+0.5)
			pdf.CellFormat(colW[i]-4, 6, strings.ToUpper(h), "", 0, aligns[i], false, 0, "")
			x += colW[i]
		}
		pdf.SetDrawColor(lineR, lineG, lineB)
		pdf.SetLineWidth(0.2)
		pdf.Line(lm, y+7, lm+cw, y+7)
		pdf.SetXY(lm, y+7)
	}
	drawTableHdr()

	lineH := 4.0

	// Data rows with alternating backgrounds
	for _, item := range cfg.Items {
		amount := math.Round(item.Quantity*item.Price*100) / 100

		descW := colW[0] - 6
		pdf.SetFont("inv", "B", 7.5)
		titleLines := splitLines(pdf, item.Description, descW)
		var detLines []string
		if item.Details != "" {
			pdf.SetFont("inv", "", 7.5)
			detLines = splitLines(pdf, strings.TrimSpace(item.Details), descW)
		}

		nLines := len(titleLines) + len(detLines)
		rowH := float64(nLines)*lineH + 4

		// Page break check
		if pdf.GetY()+rowH > pageH-25 {
			pdf.AddPage()
			pdf.SetY(14)
			drawTableHdr()
		}

		x0, y0 := lm, pdf.GetY()

		// Hairline row separator (no zebra striping) — matching the
		// HTML template's plain, quiet table rows.
		pdf.SetDrawColor(lineR, lineG, lineB)
		pdf.SetLineWidth(0.15)
		pdf.Line(x0, y0+rowH, x0+cw, y0+rowH)

		// Description text
		yy := y0 + 2
		pdf.SetFont("inv", "", 7.5)
		pdf.SetTextColor(inkR, inkG, inkB)
		for _, l := range titleLines {
			pdf.SetXY(x0+2, yy)
			pdf.CellFormat(descW, lineH, l, "", 0, "L", false, 0, "")
			yy += lineH
		}
		if len(detLines) > 0 {
			pdf.SetFont("inv", "", 7.5)
			pdf.SetTextColor(softR, softG, softB)
			for _, l := range detLines {
				pdf.SetXY(x0+2, yy)
				pdf.CellFormat(descW, lineH, l, "", 0, "L", false, 0, "")
				yy += lineH
			}
		}

		// Numeric columns (vertically centered)
		midY := y0 + (rowH-lineH)/2
		pdf.SetFont("inv", "", 7.5)
		pdf.SetTextColor(inkR, inkG, inkB)

		x := x0 + colW[0]
		pdf.SetXY(x, midY)
		pdf.CellFormat(colW[1]-4, lineH, loc.FormatQuantity(item.Quantity), "", 0, "R", false, 0, "")

		x += colW[1]
		pdf.SetXY(x, midY)
		pdf.CellFormat(colW[2]-4, lineH, loc.FormatCurrency(item.Price, curr), "", 0, "R", false, 0, "")

		x += colW[2]
		pdf.SetXY(x, midY)
		pdf.SetFont("inv", "B", 7.5)
		pdf.CellFormat(colW[3]-4, lineH, loc.FormatCurrency(amount, curr), "", 0, "R", false, 0, "")

		pdf.SetXY(x0, y0+rowH)
	}

	// ========== SUMMARY SECTION ==========
	// A bordered box (matching the HTML template's .sum) with plain
	// subtotal/tax rows and a solid-accent "total due" row at the bottom.
	sumW := 78.0
	sumX := pageW - rm - sumW
	sumY := pdf.GetY() + 4

	sumRowH := 7.0
	totalBarH := 8.0
	boxH := totalBarH
	if cfg.VAT.Liable && cfg.VAT.Rate > 0 {
		boxH += sumRowH * 2
	}

	// Page break check
	if sumY+boxH > pageH-25 {
		pdf.AddPage()
		sumY = 14
	}

	boxY := sumY
	sumLabelW := sumW * 0.55
	sumValueW := sumW * 0.45

	if cfg.VAT.Liable && cfg.VAT.Rate > 0 {
		pdf.SetDrawColor(lineR, lineG, lineB)
		pdf.SetLineWidth(0.2)

		// Subtotal row
		pdf.SetXY(sumX+4, sumY+1.5)
		pdf.SetFont("inv", "B", 8)
		pdf.SetTextColor(softR, softG, softB)
		pdf.CellFormat(sumLabelW-4, sumRowH-3, lb.Subtotal, "", 0, "L", false, 0, "")
		pdf.SetTextColor(inkR, inkG, inkB)
		pdf.CellFormat(sumValueW-4, sumRowH-3, loc.FormatCurrency(netTotal, curr), "", 0, "R", false, 0, "")
		pdf.Line(sumX, sumY+sumRowH, sumX+sumW, sumY+sumRowH)
		sumY += sumRowH

		// Tax row
		taxLabel := fmt.Sprintf("%s (%s%%)", lb.Tax, loc.FormatQuantity(cfg.VAT.Rate))
		pdf.SetXY(sumX+4, sumY+1.5)
		pdf.SetTextColor(softR, softG, softB)
		pdf.CellFormat(sumLabelW-4, sumRowH-3, taxLabel, "", 0, "L", false, 0, "")
		pdf.SetTextColor(inkR, inkG, inkB)
		pdf.CellFormat(sumValueW-4, sumRowH-3, loc.FormatCurrency(taxAmt, curr), "", 0, "R", false, 0, "")
		pdf.Line(sumX, sumY+sumRowH, sumX+sumW, sumY+sumRowH)
		sumY += sumRowH
	}

	// Grand total row (solid accent background, white text)
	pdf.SetFillColor(aR, aG, aB)
	pdf.Rect(sumX, sumY, sumW, totalBarH, "F")
	pdf.SetFont("inv", "B", 9.5)
	pdf.SetTextColor(255, 255, 255)
	pdf.SetXY(sumX+4, sumY+0.5)
	pdf.CellFormat(sumLabelW-4, totalBarH-1, lb.Total, "", 0, "L", false, 0, "")
	pdf.CellFormat(sumValueW-4, totalBarH-1, loc.FormatCurrency(grossTotal, curr), "", 0, "R", false, 0, "")
	sumY += totalBarH

	// Outer box border
	pdf.SetDrawColor(lineR, lineG, lineB)
	pdf.SetLineWidth(0.2)
	pdf.Rect(sumX, boxY, sumW, sumY-boxY, "D")

	pdf.SetY(sumY + 3)

	// ========== TAX NOTICE (small business exemption etc.) ==========
	if cfg.Notice != "" {
		nY := pdf.GetY() + 2
		if nY > pageH-30 {
			pdf.AddPage()
			nY = 10
		}
		pdf.SetXY(lm, nY)
		pdf.SetFont("inv", "", 7)
		pdf.SetTextColor(130, 130, 130)
		pdf.MultiCell(cw, 3.5, cfg.Notice, "", "L", false)
	}

	// ========== NOTES ==========
	if cfg.Notes != "" {
		notesY := pdf.GetY() + 4
		if notesY > pageH-35 {
			pdf.AddPage()
			notesY = 14
		}

		pdf.SetFont("inv", "B", 7.5)
		pdf.SetTextColor(faiR, faiG, faiB)
		pdf.SetXY(lm, notesY)
		pdf.CellFormat(cw, 4, strings.ToUpper(lb.Notes), "", 0, "L", false, 0, "")
		notesY += 5

		pdf.SetFont("inv", "", 8.5)
		pdf.SetTextColor(softR, softG, softB)
		pdf.SetXY(lm, notesY)
		pdf.MultiCell(cw, 4.5, cfg.Notes, "", "L", false)
	}

	// ========== PAYMENT NOTE ==========
	// Plain sentence, no heading/box — bank details already live in the
	// page footer, so this is just the terms (and method, if set).
	if cfg.PayTerms != "" || cfg.PayMethod != "" {
		payNote := cfg.PayTerms
		if cfg.PayMethod != "" {
			if payNote != "" {
				payNote += " "
			}
			payNote += cfg.PayMethod + "."
		}
		payY := pdf.GetY() + 5
		if payY > pageH-30 {
			pdf.AddPage()
			payY = 10
		}
		pdf.SetXY(lm, payY)
		pdf.SetFont("inv", "", 8)
		pdf.SetTextColor(90, 90, 90)
		pdf.MultiCell(cw, 4, payNote, "", "L", false)
	}

	// ========== ZUGFERD ATTACHMENT ==========
	if len(zugferdXML) > 0 {
		pdf.SetAttachments([]fpdf.Attachment{
			{
				Content:     zugferdXML,
				Filename:    "factur-x.xml",
				Description: "Factur-X / ZUGFeRD e-invoice (CII XML, BASIC profile)",
			},
		})
	}

	return pdf.OutputFileAndClose(outputPath)
}
