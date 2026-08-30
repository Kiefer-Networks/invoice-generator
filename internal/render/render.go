// Package render turns a Config into invoice HTML (via Go's text/template)
// and, optionally, into a PDF by driving headless Chrome/Chromium/Edge.
package render

import (
	"context"
	_ "embed"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"
	"time"

	"github.com/kiefer-networks/invoice-generator/internal/config"
	"github.com/kiefer-networks/invoice-generator/internal/locale"
)

//go:embed template_default.html
var defaultTemplateHTML string

// DefaultTemplate returns the embedded default HTML template source,
// e.g. for `invoice init template` to extract it for customization.
func DefaultTemplate() string {
	return defaultTemplateHTML
}

// chromeTimeout bounds how long headless Chrome may run for a single
// PDF render, so a stuck/hanging browser process cannot block indefinitely.
const chromeTimeout = 60 * time.Second

// TplData holds pre-formatted values for the HTML template.
type TplData struct {
	Lang      string
	Color     string
	ColorDark string
	Title     string
	LB        locale.Labels
	LogoPath  string

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

// PrepareTplData converts Config into pre-formatted template data for the
// given document type (invoice or quote).
func PrepareTplData(cfg *config.Config, loc *locale.Locale, docType config.DocType) *TplData {
	curr := cfg.Currency
	if curr == "" {
		curr = "EUR"
	}
	if cfg.Formatting.CurrencySymbol != "" {
		locale.RegisterCurrencySymbol(curr, cfg.Formatting.CurrencySymbol)
	}

	net := config.CalcNet(cfg.Items)
	tax := config.CalcTax(net, cfg.VAT)
	gross := math.Round((net+tax)*100) / 100
	lb := loc.Labels

	// Quote (Angebot): swap title/second-date-row for their quote
	// equivalents. The template only ever reads lb.DueDate/DueDate, so
	// substituting these values is enough — no template changes needed.
	title := lb.InvoiceTitle
	dueDateValue := cfg.Invoice.DueDate
	if docType == config.DocQuote {
		title = lb.QuoteTitle
		lb.DueDate = lb.ValidUntil
		dueDateValue = cfg.Invoice.ValidUntil
		if dueDateValue == "" {
			dueDateValue = cfg.Invoice.DueDate
		}
	}

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
				logoPath = "file://" + filepath.ToSlash(abs)
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
		ColorDark: locale.DarkenColor(color),
		Title:     title,
		LB:        lb,
		LogoPath:  logoPath,

		CompanyName:    cfg.Company.Name,
		CompanyAddr:    compAddr,
		CompanyContact: contact,
		TaxID:          taxID,

		InvNumber:   fmt.Sprintf("%v", cfg.Invoice.Number),
		InvDate:     loc.FormatDate(cfg.Invoice.Date),
		DueDate:     loc.FormatDate(dueDateValue),
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

// HTML renders the invoice (or quote) as an HTML string.
func HTML(cfg *config.Config, loc *locale.Locale, docType config.DocType, tmplPath string, configDir string) (string, error) {
	src, err := loadTemplateSrc(tmplPath, configDir)
	if err != nil {
		return "", err
	}

	tmpl, err := template.New("invoice").Parse(src)
	if err != nil {
		return "", fmt.Errorf("template parse error: %w", err)
	}

	data := PrepareTplData(cfg, loc, docType)
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

// FindChrome locates a Chrome/Chromium/Edge binary on the system.
// Checks the INVOICE_CHROME env var first, then PATH, then common
// per-OS installation locations (Linux, macOS, Windows).
func FindChrome() string {
	if p := os.Getenv("INVOICE_CHROME"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	names := []string{
		"chromium-browser", "chromium", "google-chrome-stable",
		"google-chrome", "chrome", "google-chrome.exe", "chrome.exe",
		"msedge", "msedge.exe",
	}
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}

	var paths []string
	switch runtime.GOOS {
	case "windows":
		programFiles := os.Getenv("ProgramFiles")
		programFilesX86 := os.Getenv("ProgramFiles(x86)")
		localAppData := os.Getenv("LocalAppData")
		for _, base := range []string{programFiles, programFilesX86, localAppData} {
			if base == "" {
				continue
			}
			paths = append(paths,
				filepath.Join(base, "Google", "Chrome", "Application", "chrome.exe"),
				filepath.Join(base, "Chromium", "Application", "chrome.exe"),
				filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe"),
			)
		}
	case "darwin":
		paths = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		}
	default: // linux and others
		paths = []string{
			"/usr/bin/chromium-browser", "/usr/bin/chromium",
			"/usr/bin/google-chrome-stable", "/usr/bin/google-chrome",
			"/snap/bin/chromium",
			"/usr/bin/microsoft-edge-stable", "/usr/bin/microsoft-edge",
		}
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// FromTemplate renders the invoice/quote via HTML template + Chrome headless PDF.
func FromTemplate(cfg *config.Config, loc *locale.Locale, docType config.DocType, outputPath, tmplPath, configDir string) error {
	chrome := FindChrome()
	if chrome == "" {
		return fmt.Errorf("Chrome/Chromium/Edge not found – install one, set INVOICE_CHROME, or use -fpdf flag")
	}

	html, err := HTML(cfg, loc, docType, tmplPath, configDir)
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

	// Chrome headless → PDF (bounded by chromeTimeout to avoid hangs)
	ctx, cancel := context.WithTimeout(context.Background(), chromeTimeout)
	defer cancel()

	abs, err := filepath.Abs(outputPath)
	if err != nil {
		return fmt.Errorf("could not resolve output path: %w", err)
	}

	// Use a dedicated, isolated user-data-dir so concurrent/parallel runs
	// never share browser profile state (avoids cross-process interference).
	profileDir, err := os.MkdirTemp("", "invoice-chrome-profile-*")
	if err != nil {
		return fmt.Errorf("could not create Chrome profile dir: %w", err)
	}
	defer os.RemoveAll(profileDir)

	args := []string{
		"--headless",
		"--disable-gpu",
		"--disable-software-rasterizer",
		"--disable-extensions",
		"--disable-sync",
		"--no-first-run",
		"--user-data-dir=" + profileDir,
		"--run-all-compositor-stages-before-draw",
		"--no-pdf-header-footer",
	}
	// Chrome refuses to start its sandbox as root (common in containers).
	// Only relax the sandbox in that specific, already-unprivileged-boundary case.
	if runtime.GOOS != "windows" && os.Geteuid() == 0 {
		args = append(args, "--no-sandbox")
	}
	args = append(args, "--print-to-pdf="+abs, "file://"+tmpPath)

	cmd := exec.CommandContext(ctx, chrome, args...)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("Chrome PDF generation timed out after %s", chromeTimeout)
		}
		return fmt.Errorf("Chrome PDF generation failed: %w", err)
	}
	return nil
}
