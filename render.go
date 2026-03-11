package main

import (
	_ "embed"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

//go:embed template_default.html
var defaultTemplateHTML string

// TplData holds pre-formatted values for the HTML template.
type TplData struct {
	Lang       string
	Color      string
	ColorDark  string
	Title      string
	LB         Labels
	LogoPath   string

	// Company
	CompanyName    string
	CompanyAddr    string
	CompanyContact string
	TaxID          string

	// Invoice
	InvNumber   string
	InvDate     string
	DueDate     string
	Status      string
	StatusClass string

	// Customer
	CustName  string
	CustLines []string

	// Items
	Rows []TplRow

	// Summary
	HasVAT     bool
	Subtotal   string
	TaxLabel   string
	TaxAmt     string
	TaxRate    string
	GrossTotal string

	// Notes
	Notes  string
	Notice string

	// Payment
	HasPay    bool
	PayTerms  string
	PayMethod string
	HasBank   bool
	BankName  string
	BankIBAN  string
	BankBIC   string
}

// TplRow is a single line item for the HTML template.
type TplRow struct {
	Desc  string
	Det   string
	Qty   string
	Price string
	Amt   string
}

// prepareTplData converts Config into pre-formatted template data.
func prepareTplData(cfg *Config, loc *Locale) *TplData {
	curr := cfg.Currency
	if curr == "" {
		curr = "EUR"
	}
	if cfg.Formatting.CurrencySymbol != "" {
		currencySymbols[curr] = cfg.Formatting.CurrencySymbol
	}

	net := calcNet(cfg.Items)
	tax := calcTax(net, cfg.VAT)
	gross := math.Round((net+tax)*100) / 100
	lb := loc.Labels

	// Company address line
	compAddr := cfg.Company.Address
	if cfg.Company.ZIP != "" || cfg.Company.City != "" {
		compAddr += ", " + strings.TrimSpace(cfg.Company.ZIP+" "+cfg.Company.City)
	}

	// Company contact line
	contact := cfg.Company.Email
	if cfg.Company.Phone != "" {
		if contact != "" {
			contact += "  ·  "
		}
		contact += cfg.Company.Phone
	}

	// Tax ID
	taxID := cfg.Company.VatID
	if taxID == "" {
		taxID = cfg.Company.TaxNumber
	}

	// Customer lines
	var custLines []string
	if cfg.Customer.Contact != "" {
		custLines = append(custLines, cfg.Customer.Contact)
	}
	if cfg.Customer.Email != "" {
		custLines = append(custLines, cfg.Customer.Email)
	}
	zipCity := strings.TrimSpace(cfg.Customer.ZIP + " " + cfg.Customer.City)
	addr := cfg.Customer.Address
	if zipCity != "" && !strings.Contains(addr, zipCity) {
		if addr != "" {
			addr += ", " + zipCity
		} else {
			addr = zipCity
		}
	}
	if addr != "" {
		custLines = append(custLines, addr)
	}
	if cfg.Customer.CountryName != "" {
		custLines = append(custLines, cfg.Customer.CountryName)
	} else if cfg.Customer.Country != "" {
		custLines = append(custLines, cfg.Customer.Country)
	}
	if cfg.Customer.VatID != "" {
		custLines = append(custLines, lb.TaxID+": "+cfg.Customer.VatID)
	}

	// Item rows
	var rows []TplRow
	for _, it := range cfg.Items {
		amt := math.Round(it.Quantity*it.Price*100) / 100
		rows = append(rows, TplRow{
			Desc:  it.Description,
			Det:   it.Details,
			Qty:   loc.FormatQuantity(it.Quantity),
			Price: loc.FormatCurrency(it.Price, curr),
			Amt:   loc.FormatCurrency(amt, curr),
		})
	}

	// Status CSS class
	statusClass := ""
	switch strings.ToUpper(cfg.Invoice.Status) {
	case "PAID":
		statusClass = "paid"
	case "OVERDUE":
		statusClass = "overdue"
	case "DRAFT":
		statusClass = "draft"
	}

	// Logo path (file:// URI for Chrome)
	logoPath := ""
	if cfg.Logo != "" {
		if abs, err := filepath.Abs(cfg.Logo); err == nil {
			if _, err := os.Stat(abs); err == nil {
				logoPath = "file://" + abs
			}
		}
	}

	// Color
	color := cfg.Color
	if color == "" {
		color = "#5B9BD5"
	}

	lang := cfg.Language
	if lang == "" {
		lang = "de"
	}

	d := &TplData{
		Lang:      lang,
		Color:     color,
		ColorDark: darkenColor(color),
		Title:     lb.InvoiceTitle,
		LB:        lb,
		LogoPath:  logoPath,

		CompanyName:    cfg.Company.Name,
		CompanyAddr:    compAddr,
		CompanyContact: contact,
		TaxID:          taxID,

		InvNumber:   fmt.Sprintf("%v", cfg.Invoice.Number),
		InvDate:     loc.FormatDate(cfg.Invoice.Date),
		DueDate:     loc.FormatDate(cfg.Invoice.DueDate),
		Status:      cfg.Invoice.Status,
		StatusClass: statusClass,

		CustName:  cfg.Customer.Name,
		CustLines: custLines,
		Rows:      rows,

		HasVAT:     cfg.VAT.Liable && cfg.VAT.Rate > 0,
		Subtotal:   loc.FormatCurrency(net, curr),
		TaxLabel:   fmt.Sprintf("%s (%s%%)", lb.Tax, loc.FormatQuantity(cfg.VAT.Rate)),
		TaxAmt:     loc.FormatCurrency(tax, curr),
		TaxRate:    loc.FormatQuantity(cfg.VAT.Rate),
		GrossTotal: loc.FormatCurrency(gross, curr),

		Notes:  cfg.Notes,
		Notice: cfg.Notice,

		HasPay:    cfg.PayTerms != "" || cfg.PayMethod != "" || cfg.Company.Bank.IBAN != "",
		PayTerms:  cfg.PayTerms,
		PayMethod: cfg.PayMethod,
		HasBank:   cfg.Company.Bank.IBAN != "",
		BankName:  cfg.Company.Bank.Name,
		BankIBAN:  cfg.Company.Bank.IBAN,
		BankBIC:   cfg.Company.Bank.BIC,
	}
	return d
}

// darkenColor produces a slightly darker shade of a hex color.
func darkenColor(hex string) string {
	r, g, b := parseColor(hex)
	f := 0.82
	return fmt.Sprintf("#%02x%02x%02x", int(float64(r)*f), int(float64(g)*f), int(float64(b)*f))
}

// renderHTML renders the invoice as an HTML string.
func renderHTML(cfg *Config, loc *Locale, tmplPath string, configDir string) (string, error) {
	src, err := loadTemplateSrc(tmplPath, configDir)
	if err != nil {
		return "", err
	}

	tmpl, err := template.New("invoice").Parse(src)
	if err != nil {
		return "", fmt.Errorf("template parse error: %w", err)
	}

	data := prepareTplData(cfg, loc)
	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("template execute error: %w", err)
	}
	return buf.String(), nil
}

// loadTemplateSrc loads the HTML template source.
// Priority: 1) explicit path, 2) template.html next to config, 3) embedded default.
func loadTemplateSrc(tmplPath, configDir string) (string, error) {
	if tmplPath != "" {
		data, err := os.ReadFile(tmplPath)
		if err != nil {
			return "", fmt.Errorf("template not found: %s", tmplPath)
		}
		return string(data), nil
	}
	local := filepath.Join(configDir, "template.html")
	if data, err := os.ReadFile(local); err == nil {
		return string(data), nil
	}
	return defaultTemplateHTML, nil
}

// findChrome locates a Chrome/Chromium binary on the system.
func findChrome() string {
	names := []string{
		"chromium-browser", "chromium", "google-chrome-stable",
		"google-chrome", "chrome",
	}
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	paths := []string{
		"/usr/bin/chromium-browser", "/usr/bin/chromium",
		"/usr/bin/google-chrome-stable", "/usr/bin/google-chrome",
		"/snap/bin/chromium",
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// generateFromTemplate renders the invoice via HTML template + Chrome headless PDF.
func generateFromTemplate(cfg *Config, loc *Locale, outputPath, tmplPath, configDir string) error {
	chrome := findChrome()
	if chrome == "" {
		return fmt.Errorf("Chrome/Chromium not found – install chromium or use -fpdf flag")
	}

	html, err := renderHTML(cfg, loc, tmplPath, configDir)
	if err != nil {
		return err
	}

	// Write HTML to temp file
	tmpFile, err := os.CreateTemp("", "invoice-*.html")
	if err != nil {
		return fmt.Errorf("could not create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.WriteString(html); err != nil {
		tmpFile.Close()
		return err
	}
	tmpFile.Close()

	// Chrome headless → PDF
	abs, _ := filepath.Abs(outputPath)
	cmd := exec.Command(chrome,
		"--headless",
		"--disable-gpu",
		"--no-sandbox",
		"--disable-software-rasterizer",
		"--run-all-compositor-stages-before-draw",
		"--no-pdf-header-footer",
		"--print-to-pdf="+abs,
		"file://"+tmpPath,
	)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Chrome PDF generation failed: %w", err)
	}
	return nil
}
