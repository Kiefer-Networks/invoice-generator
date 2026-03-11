package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-pdf/fpdf"
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

// statusColor returns an RGB color for known invoice statuses.
func statusColor(status string) (int, int, int) {
	switch strings.ToUpper(status) {
	case "PAID":
		return 34, 153, 84 // green
	case "SENT":
		return 255, 255, 255 // white text on header
	case "OVERDUE":
		return 220, 53, 69 // red
	case "DRAFT":
		return 200, 200, 210 // gray
	default:
		return 255, 193, 7 // amber
	}
}

// generatePDF creates a modern invoice PDF.
func generatePDF(cfg *Config, loc *Locale, outputPath string, zugferdXML []byte) error {
	fontRegular, fontBold, err := findFonts(cfg)
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

	// Accent color
	aR, aG, aB := parseColor(cfg.Color)

	// Darker shade of accent for header gradient effect
	dR := int(float64(aR) * 0.85)
	dG := int(float64(aG) * 0.85)
	dB := int(float64(aB) * 0.85)

	// Currency
	curr := cfg.Currency
	if curr == "" {
		curr = "EUR"
	}
	if cfg.Formatting.CurrencySymbol != "" {
		currencySymbols[curr] = cfg.Formatting.CurrencySymbol
	}

	// Tax calculations
	netTotal := calcNet(cfg.Items)
	taxAmt := calcTax(netTotal, cfg.VAT)
	grossTotal := math.Round((netTotal+taxAmt)*100) / 100

	lb := loc.Labels

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

	// ========== COLORED HEADER BAND ==========
	headerH := 42.0
	pdf.SetFillColor(aR, aG, aB)
	pdf.Rect(0, 0, pageW, headerH, "F")
	// Subtle darker strip at bottom of header
	pdf.SetFillColor(dR, dG, dB)
	pdf.Rect(0, headerH-2, pageW, 2, "F")

	// Invoice title (white, large)
	pdf.SetFont("inv", "B", 30)
	pdf.SetTextColor(255, 255, 255)
	pdf.SetXY(lm, 6)
	pdf.CellFormat(100, 12, lb.InvoiceTitle, "", 0, "L", false, 0, "")

	// Company name (white)
	pdf.SetFont("inv", "B", 11)
	pdf.SetXY(lm, 19)
	pdf.CellFormat(100, 5, cfg.Company.Name, "", 2, "L", false, 0, "")

	// Company address details (white, smaller)
	pdf.SetFont("inv", "", 8)
	pdf.SetTextColor(230, 240, 255)
	compAddr := cfg.Company.Address
	if cfg.Company.ZIP != "" || cfg.Company.City != "" {
		compAddr += ", " + strings.TrimSpace(cfg.Company.ZIP+" "+cfg.Company.City)
	}
	pdf.SetX(lm)
	pdf.CellFormat(100, 3.8, compAddr, "", 2, "L", false, 0, "")
	if cfg.Company.Email != "" || cfg.Company.Phone != "" {
		pdf.SetX(lm)
		contact := cfg.Company.Email
		if cfg.Company.Phone != "" {
			if contact != "" {
				contact += "  ·  "
			}
			contact += cfg.Company.Phone
		}
		pdf.CellFormat(100, 3.8, contact, "", 2, "L", false, 0, "")
	}

	// Right side: invoice details (white text on header)
	rightX := pageW - rm - 68.0
	detY := 8.0
	pdf.SetFont("inv", "", 8.5)
	pdf.SetTextColor(220, 230, 245)

	type detRow struct {
		label, value string
	}
	details := []detRow{
		{lb.InvoiceNr, fmt.Sprintf("%v", cfg.Invoice.Number)},
		{lb.InvoiceDate, loc.FormatDate(cfg.Invoice.Date)},
		{lb.DueDate, loc.FormatDate(cfg.Invoice.DueDate)},
	}
	for _, d := range details {
		pdf.SetXY(rightX, detY)
		pdf.SetFont("inv", "", 8)
		pdf.CellFormat(30, 5, d.label+":", "", 0, "L", false, 0, "")
		pdf.SetFont("inv", "B", 8.5)
		pdf.SetTextColor(255, 255, 255)
		pdf.CellFormat(38, 5, d.value, "", 0, "R", false, 0, "")
		pdf.SetTextColor(220, 230, 245)
		detY += 6
	}

	// Status badge
	if cfg.Invoice.Status != "" {
		detY += 1
		badgeText := "  " + cfg.Invoice.Status + "  "
		pdf.SetFont("inv", "B", 8)
		badgeW := pdf.GetStringWidth(badgeText) + 4
		badgeX := pageW - rm - badgeW
		// White pill-shaped badge
		pdf.SetFillColor(255, 255, 255)
		pdf.RoundedRect(badgeX, detY, badgeW, 6.5, 1.5, "1234", "F")
		sR, sG, sB := statusColor(cfg.Invoice.Status)
		if sR == 255 && sG == 255 && sB == 255 {
			// White status → use accent color text instead
			pdf.SetTextColor(aR, aG, aB)
		} else {
			pdf.SetTextColor(sR, sG, sB)
		}
		pdf.SetXY(badgeX, detY+0.5)
		pdf.CellFormat(badgeW, 5.5, badgeText, "", 0, "C", false, 0, "")
	}

	// Logo overlay (if present, positioned in header area)
	if cfg.Logo != "" {
		if _, statErr := os.Stat(cfg.Logo); statErr == nil {
			opts := fpdf.ImageOptions{ReadDpi: true}
			info := pdf.RegisterImageOptions(cfg.Logo, opts)
			if info != nil {
				imgH := 16.0
				imgW := info.Width() / info.Height() * imgH
				if imgW > 24 {
					imgW = 24
					imgH = info.Height() / info.Width() * imgW
				}
				// Place at right side before the details
				logoX := rightX - imgW - 6
				logoY := 8.0
				// White background circle/rect behind logo
				pdf.SetFillColor(255, 255, 255)
				pdf.RoundedRect(logoX-2, logoY-1, imgW+4, imgH+2, 2, "1234", "F")
				pdf.ImageOptions(cfg.Logo, logoX, logoY, imgW, imgH, false, opts, 0, "")
			}
		}
	}

	// ========== TAX ID LINE (below header) ==========
	taxIDy := headerH + 2
	taxID := cfg.Company.VatID
	if taxID == "" {
		taxID = cfg.Company.TaxNumber
	}
	if taxID != "" {
		pdf.SetFont("inv", "", 7.5)
		pdf.SetTextColor(140, 140, 140)
		pdf.SetXY(lm, taxIDy)
		pdf.CellFormat(cw, 4, lb.TaxID+": "+taxID, "", 0, "L", false, 0, "")
		taxIDy += 5
	}

	// ========== CUSTOMER SECTION ==========
	custStartY := taxIDy + 1
	pdf.SetFont("inv", "B", 8.5)
	pdf.SetTextColor(aR, aG, aB)
	pdf.SetXY(lm, custStartY)
	pdf.CellFormat(60, 4.5, lb.InvoiceTo, "", 0, "L", false, 0, "")

	// Build customer lines
	var custLines []string
	if cfg.Customer.Contact != "" {
		custLines = append(custLines, cfg.Customer.Contact)
	}
	if cfg.Customer.Email != "" {
		custLines = append(custLines, cfg.Customer.Email)
	}
	zipCity := strings.TrimSpace(cfg.Customer.ZIP + " " + cfg.Customer.City)
	addrLine := cfg.Customer.Address
	if zipCity != "" && !strings.Contains(addrLine, zipCity) {
		if addrLine != "" {
			addrLine += ", " + zipCity
		} else {
			addrLine = zipCity
		}
	}
	if addrLine != "" {
		custLines = append(custLines, addrLine)
	}
	if cfg.Customer.CountryName != "" {
		custLines = append(custLines, cfg.Customer.CountryName)
	} else if cfg.Customer.Country != "" {
		custLines = append(custLines, cfg.Customer.Country)
	}
	if cfg.Customer.VatID != "" {
		custLines = append(custLines, lb.TaxID+": "+cfg.Customer.VatID)
	}

	// Customer card with light background and left accent border
	cardY := custStartY + 5.5
	cardH := 6 + float64(len(custLines))*4.0 + 2
	cardW := cw*0.55 + 10

	// Light gray background
	pdf.SetFillColor(247, 248, 250)
	pdf.Rect(lm, cardY, cardW, cardH, "F")
	// Left accent border
	pdf.SetFillColor(aR, aG, aB)
	pdf.Rect(lm, cardY, 1.2, cardH, "F")

	// Customer name
	pdf.SetFont("inv", "B", 11)
	pdf.SetTextColor(40, 40, 40)
	pdf.SetXY(lm+5, cardY+2)
	pdf.CellFormat(cardW-8, 5.5, cfg.Customer.Name, "", 2, "L", false, 0, "")

	// Customer details
	pdf.SetFont("inv", "", 8)
	pdf.SetTextColor(80, 80, 80)
	for _, line := range custLines {
		pdf.SetX(lm + 5)
		pdf.CellFormat(cardW-8, 4.0, line, "", 2, "L", false, 0, "")
	}

	// ========== ITEMS TABLE ==========
	tableStartY := cardY + cardH + 5
	pdf.SetXY(lm, tableStartY)
	pdf.SetFont("inv", "B", 8.5)
	pdf.SetTextColor(aR, aG, aB)
	pdf.CellFormat(cw, 4.5, lb.Details, "", 0, "L", false, 0, "")

	tableY := tableStartY + 6
	pdf.SetY(tableY)

	// Column widths: description, qty, unit price, amount
	colW := []float64{90, 18, 33, 33} // = 174

	// Table header
	drawTableHdr := func() {
		y := pdf.GetY()
		pdf.SetFillColor(aR, aG, aB)
		pdf.SetTextColor(255, 255, 255)
		pdf.SetFont("inv", "B", 7.5)
		// Rounded-ish header (top corners)
		pdf.RoundedRect(lm, y, cw, 7, 1.5, "12", "F")
		hdrs := []string{lb.Description, lb.Quantity, lb.UnitPrice, lb.Amount}
		aligns := []string{"L", "R", "R", "R"}
		x := lm
		for i, h := range hdrs {
			pdf.SetXY(x+2, y+0.5)
			pdf.CellFormat(colW[i]-4, 6, h, "", 0, aligns[i], false, 0, "")
			x += colW[i]
		}
		pdf.SetXY(lm, y+7)
	}
	drawTableHdr()

	lineH := 4.0

	// Data rows with alternating backgrounds
	for idx, item := range cfg.Items {
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
			// Re-draw header band on continuation pages (thin accent bar)
			pdf.SetFillColor(aR, aG, aB)
			pdf.Rect(0, 0, pageW, 3, "F")
			pdf.SetY(8)
			drawTableHdr()
		}

		x0, y0 := lm, pdf.GetY()

		// Alternating row background
		if idx%2 == 1 {
			pdf.SetFillColor(247, 248, 252)
			pdf.Rect(x0, y0, cw, rowH, "F")
		}

		// Subtle bottom border
		pdf.SetDrawColor(230, 230, 235)
		pdf.SetLineWidth(0.15)
		pdf.Line(x0, y0+rowH, x0+cw, y0+rowH)

		// Description text
		yy := y0 + 2
		pdf.SetFont("inv", "B", 7.5)
		pdf.SetTextColor(40, 40, 40)
		for _, l := range titleLines {
			pdf.SetXY(x0+3, yy)
			pdf.CellFormat(descW, lineH, l, "", 0, "L", false, 0, "")
			yy += lineH
		}
		if len(detLines) > 0 {
			pdf.SetFont("inv", "", 7.5)
			pdf.SetTextColor(100, 100, 100)
			for _, l := range detLines {
				pdf.SetXY(x0+3, yy)
				pdf.CellFormat(descW, lineH, l, "", 0, "L", false, 0, "")
				yy += lineH
			}
		}

		// Numeric columns (vertically centered)
		midY := y0 + (rowH-lineH)/2
		pdf.SetFont("inv", "", 7.5)
		pdf.SetTextColor(60, 60, 60)

		x := x0 + colW[0]
		pdf.SetXY(x, midY)
		pdf.CellFormat(colW[1]-4, lineH, loc.FormatQuantity(item.Quantity), "", 0, "R", false, 0, "")

		x += colW[1]
		pdf.SetXY(x, midY)
		pdf.CellFormat(colW[2]-4, lineH, loc.FormatCurrency(item.Price, curr), "", 0, "R", false, 0, "")

		x += colW[2]
		pdf.SetXY(x, midY)
		pdf.SetFont("inv", "B", 7.5)
		pdf.SetTextColor(40, 40, 40)
		pdf.CellFormat(colW[3]-4, lineH, loc.FormatCurrency(amount, curr), "", 0, "R", false, 0, "")

		pdf.SetXY(x0, y0+rowH)
	}

	// ========== SUMMARY SECTION ==========
	sumW := 78.0
	sumX := pageW - rm - sumW
	sumY := pdf.GetY() + 4

	// Page break check
	if sumY+55 > pageH-25 {
		pdf.AddPage()
		pdf.SetFillColor(aR, aG, aB)
		pdf.Rect(0, 0, pageW, 3, "F")
		sumY = 10
	}

	sumLabelW := sumW * 0.55
	sumValueW := sumW * 0.45
	sumRowH := 6.0

	if cfg.VAT.Liable && cfg.VAT.Rate > 0 {
		// Subtotal row
		pdf.SetXY(sumX, sumY)
		pdf.SetFont("inv", "", 8.5)
		pdf.SetTextColor(100, 100, 100)
		pdf.CellFormat(sumLabelW, sumRowH, lb.Subtotal, "", 0, "L", false, 0, "")
		pdf.SetFont("inv", "", 8.5)
		pdf.SetTextColor(60, 60, 60)
		pdf.CellFormat(sumValueW, sumRowH, loc.FormatCurrency(netTotal, curr), "", 0, "R", false, 0, "")
		sumY += sumRowH

		// Tax row
		taxLabel := fmt.Sprintf("%s (%s%%)", lb.Tax, loc.FormatQuantity(cfg.VAT.Rate))
		pdf.SetXY(sumX, sumY)
		pdf.SetFont("inv", "", 8.5)
		pdf.SetTextColor(100, 100, 100)
		pdf.CellFormat(sumLabelW, sumRowH, taxLabel, "", 0, "L", false, 0, "")
		pdf.SetFont("inv", "", 8.5)
		pdf.SetTextColor(60, 60, 60)
		pdf.CellFormat(sumValueW, sumRowH, loc.FormatCurrency(taxAmt, curr), "", 0, "R", false, 0, "")
		sumY += sumRowH + 2

		// Thin separator
		pdf.SetDrawColor(200, 200, 200)
		pdf.SetLineWidth(0.3)
		pdf.Line(sumX, sumY, sumX+sumW, sumY)
		sumY += 3
	}

	// Grand total bar (accent color background, white text)
	totalBarH := 10.0
	pdf.SetFillColor(aR, aG, aB)
	pdf.RoundedRect(sumX, sumY, sumW, totalBarH, 1.5, "1234", "F")
	pdf.SetFont("inv", "B", 11)
	pdf.SetTextColor(255, 255, 255)
	pdf.SetXY(sumX+4, sumY+1)
	pdf.CellFormat(sumLabelW-4, totalBarH-2, lb.Total, "", 0, "L", false, 0, "")
	pdf.SetFont("inv", "B", 12)
	pdf.CellFormat(sumValueW-4, totalBarH-2, loc.FormatCurrency(grossTotal, curr), "", 0, "R", false, 0, "")
	sumY += totalBarH

	// Tax overview (small, below total)
	if cfg.VAT.Liable && cfg.VAT.Rate > 0 {
		sumY += 3
		pdf.SetFont("inv", "", 6.5)
		pdf.SetTextColor(140, 140, 140)
		overW := sumW / 3
		pdf.SetXY(sumX, sumY)
		pdf.CellFormat(overW, 3.5, lb.Tax, "", 0, "L", false, 0, "")
		pdf.CellFormat(overW, 3.5, lb.Taxable, "", 0, "R", false, 0, "")
		pdf.CellFormat(overW, 3.5, lb.TaxAmount, "", 0, "R", false, 0, "")
		sumY += 3.5
		pdf.SetXY(sumX, sumY)
		pdf.SetFont("inv", "", 7)
		pdf.SetTextColor(100, 100, 100)
		pdf.CellFormat(overW, 3.5, loc.FormatQuantity(cfg.VAT.Rate)+"%", "", 0, "L", false, 0, "")
		pdf.CellFormat(overW, 3.5, loc.FormatCurrency(netTotal, curr), "", 0, "R", false, 0, "")
		pdf.CellFormat(overW, 3.5, loc.FormatCurrency(taxAmt, curr), "", 0, "R", false, 0, "")
		sumY += 5
	}

	pdf.SetY(sumY + 2)

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
			pdf.SetFillColor(aR, aG, aB)
			pdf.Rect(0, 0, pageW, 3, "F")
			notesY = 10
		}

		pdf.SetFont("inv", "B", 8.5)
		pdf.SetTextColor(aR, aG, aB)
		pdf.SetXY(lm, notesY)
		pdf.CellFormat(cw, 4.5, lb.Notes, "", 0, "L", false, 0, "")
		notesY += 6

		// Left accent line + text
		pdf.SetFont("inv", "", 8.5)
		pdf.SetTextColor(80, 80, 80)
		pdf.SetXY(lm+5, notesY)
		pdf.MultiCell(cw-5, 4.5, cfg.Notes, "", "L", false)
		endY := pdf.GetY()

		pdf.SetFillColor(aR, aG, aB)
		pdf.Rect(lm+0.5, notesY-1, 1.2, endY-notesY+2, "F")
	}

	// ========== PAYMENT SECTION ==========
	hasPayment := cfg.PayTerms != "" || cfg.PayMethod != "" || cfg.Company.Bank.IBAN != ""
	if hasPayment {
		payY := pdf.GetY() + 5
		// Payment section needs ~30mm; ensure it fits on current page
		if payY+30 > pageH-22 {
			pdf.AddPage()
			pdf.SetFillColor(aR, aG, aB)
			pdf.Rect(0, 0, pageW, 3, "F")
			payY = 10
		}

		// Disable auto page break to keep columns aligned
		pdf.SetAutoPageBreak(false, 0)

		// Thin divider
		pdf.SetDrawColor(220, 220, 225)
		pdf.SetLineWidth(0.2)
		pdf.Line(lm, payY, pageW-rm, payY)
		payY += 5

		colWidth := cw / 3
		lnH := 3.8

		// Column 1: Payment terms
		if cfg.PayTerms != "" {
			pdf.SetFont("inv", "B", 7.5)
			pdf.SetTextColor(aR, aG, aB)
			pdf.SetXY(lm, payY)
			pdf.CellFormat(colWidth, 4.5, lb.PaymentTerms, "", 0, "L", false, 0, "")
			pdf.SetFont("inv", "", 7.5)
			pdf.SetTextColor(90, 90, 90)
			lines := splitLines(pdf, cfg.PayTerms, colWidth-4)
			for i, l := range lines {
				pdf.SetXY(lm, payY+5.5+float64(i)*lnH)
				pdf.CellFormat(colWidth-4, lnH, l, "", 0, "L", false, 0, "")
			}
		}

		// Column 2: Payment method
		if cfg.PayMethod != "" {
			pdf.SetFont("inv", "B", 7.5)
			pdf.SetTextColor(aR, aG, aB)
			pdf.SetXY(lm+colWidth, payY)
			pdf.CellFormat(colWidth, 4.5, lb.PaymentMethods, "", 0, "L", false, 0, "")
			pdf.SetFont("inv", "", 7.5)
			pdf.SetTextColor(90, 90, 90)
			pdf.SetXY(lm+colWidth, payY+5.5)
			pdf.CellFormat(colWidth-4, lnH, cfg.PayMethod, "", 0, "L", false, 0, "")
		}

		// Column 3: Bank details
		if cfg.Company.Bank.IBAN != "" {
			pdf.SetFont("inv", "B", 7.5)
			pdf.SetTextColor(aR, aG, aB)
			pdf.SetXY(lm+2*colWidth, payY)
			pdf.CellFormat(colWidth, 4.5, lb.BankAccount, "", 0, "L", false, 0, "")
			pdf.SetFont("inv", "", 7.5)
			pdf.SetTextColor(90, 90, 90)
			bY := payY + 5.5
			pdf.SetXY(lm+2*colWidth, bY)
			pdf.CellFormat(colWidth, lnH, cfg.Company.Bank.Name, "", 0, "L", false, 0, "")
			bY += lnH
			pdf.SetXY(lm+2*colWidth, bY)
			pdf.CellFormat(colWidth, lnH, "IBAN: "+cfg.Company.Bank.IBAN, "", 0, "L", false, 0, "")
			if cfg.Company.Bank.BIC != "" {
				bY += lnH
				pdf.SetXY(lm+2*colWidth, bY)
				pdf.CellFormat(colWidth, lnH, "BIC: "+cfg.Company.Bank.BIC, "", 0, "L", false, 0, "")
			}
		}

		// Re-enable auto page break
		pdf.SetAutoPageBreak(true, 22)
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
