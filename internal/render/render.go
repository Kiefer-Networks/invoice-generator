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

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"

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
	CompanyName      string
	CompanyAddr      string   // single-line (Street, ZIP City), for the footer's address column
	CompanyAddrLines []string // Street / ZIP City / Country, for the "from" block
	CompanyContact   string   // "email  ·  phone", for custom templates (not used by the default one — see CompanyEmail/CompanyPhone)
	CompanyWebsite   string
	CompanyEmail     string
	CompanyPhone     string
	TaxID            string

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

	// Company address: multi-line (Street / ZIP City / Country) for the
	// "from" block, and a single comma-joined line for the compact footer.
	companyAddrLines := config.AddressLines(cfg.Company.Address, cfg.Company.ZIP, cfg.Company.City, cfg.Company.Country, "", cfg.Language)
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

	// Customer lines: contact person, then the postal address as
	// Street / ZIP City / Country, then the VAT ID. No email/phone here —
	// this is the postal address block, not a contact card.
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

		CompanyName:      cfg.Company.Name,
		CompanyAddr:      compAddr,
		CompanyAddrLines: companyAddrLines,
		CompanyContact:   contact,
		CompanyWebsite:   cfg.Company.Website,
		CompanyEmail:     cfg.Company.Email,
		CompanyPhone:     cfg.Company.Phone,
		TaxID:            taxID,

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

// footerTemplateSrc is Chrome's native print footer template (see
// Page.printToPDF's footerTemplate parameter). It runs in an isolated
// context with no access to the main document's stylesheet, so every
// style needed has to be inlined here. The "pageNumber"/"totalPages"
// classes are filled in by Chrome itself on every page — see
// https://chromedevtools.github.io/devtools-protocol/tot/Page#method-printToPDF
const footerTemplateSrc = `
<div style="width:100%; font-size:7.5px; font-family:Helvetica,Arial,sans-serif; color:#9aacbd; padding:0 48px; display:flex; justify-content:space-between; gap:16px; box-sizing:border-box;">
  <div style="flex:1;">
    <span style="color:#6b7f94; font-weight:700;">{{.CompanyName}}</span><br>
    {{.CompanyAddr}}
  </div>
  <div style="flex:1;">
    {{if .CompanyWebsite}}{{.CompanyWebsite}}<br>{{end}}
    {{if .CompanyEmail}}{{.CompanyEmail}}<br>{{end}}
    {{.CompanyPhone}}
  </div>
  <div style="flex:1;">
    {{if .HasBank}}{{.BankName}} &middot; IBAN: {{.BankIBAN}}{{if .BankBIC}} &middot; BIC: {{.BankBIC}}{{end}}<br>{{end}}
    {{if .TaxID}}{{.LB.TaxID}}: {{.TaxID}}{{end}}
  </div>
  <div style="text-align:right; white-space:nowrap;">
    {{.LB.Page}} <span class="pageNumber"></span>/<span class="totalPages"></span>
  </div>
</div>
`

// footerTemplateHTML renders Chrome's native per-page footer template
// from the same TplData used for the invoice body. Uses text/template
// like the main body template (not html/template — its contextual
// autoescaping mangled plain content such as phone numbers, e.g.
// "+49 30 123456" into "&#43;49 30 123456"). Company/bank data comes
// from the user's own config file for their own output, the same trust
// level as the rest of this tool, so this matches existing precedent
// rather than being a new gap.
func footerTemplateHTML(data *TplData) (string, error) {
	tmpl, err := template.New("footer").Parse(footerTemplateSrc)
	if err != nil {
		return "", fmt.Errorf("footer template parse error: %w", err)
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("footer template execute error: %w", err)
	}
	return buf.String(), nil
}

// FromTemplate renders the invoice/quote via HTML template + Chrome headless PDF.
//
// The repeating per-page footer is produced by Chrome's own print
// header/footer mechanism (Page.printToPDF's footerTemplate) rather than
// CSS position:fixed. That was tried first and rejected: Chrome's print
// pagination does not reserve layout space for position:fixed content,
// so on some invoices the last table row (or even the grand-total bar)
// rendered underneath the fixed footer, clipping real invoice amounts.
// printToPDF's native template mechanism reserves its own margin box
// as part of the same pagination pass, so it cannot collide with body
// content the way the CSS approach did.
func FromTemplate(cfg *config.Config, loc *locale.Locale, docType config.DocType, outputPath, tmplPath, configDir string) error {
	chrome := FindChrome()
	if chrome == "" {
		return fmt.Errorf("Chrome/Chromium/Edge not found – install one, set INVOICE_CHROME, or use -fpdf flag")
	}

	html, err := HTML(cfg, loc, docType, tmplPath, configDir)
	if err != nil {
		return err
	}
	footerHTML, err := footerTemplateHTML(PrepareTplData(cfg, loc, docType))
	if err != nil {
		return err
	}

	// Write HTML to temp file
	tmpFile, err := os.CreateTemp("", "invoice-*.html")
	if err != nil {
		return fmt.Errorf("could not create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmpFile.WriteString(html); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("could not close temp file: %w", err)
	}

	abs, err := filepath.Abs(outputPath)
	if err != nil {
		return fmt.Errorf("could not resolve output path: %w", err)
	}

	// Chrome headless → PDF (bounded by chromeTimeout to avoid hangs).
	// chromedp's exec allocator creates and cleans up its own isolated
	// temporary user-data-dir, so concurrent/parallel runs never share
	// browser profile state.
	allocOpts := append([]chromedp.ExecAllocatorOption{},
		chromedp.DefaultExecAllocatorOptions[:]...)
	allocOpts = append(allocOpts,
		chromedp.ExecPath(chrome),
		chromedp.DisableGPU,
	)
	// Chrome refuses to start its sandbox as root (common in containers).
	// Only relax the sandbox in that specific, already-unprivileged-boundary case.
	if runtime.GOOS != "windows" && os.Geteuid() == 0 {
		allocOpts = append(allocOpts, chromedp.NoSandbox)
	}

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), allocOpts...)
	defer cancelAlloc()
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()
	ctx, cancel := context.WithTimeout(browserCtx, chromeTimeout)
	defer cancel()

	const (
		mmPerInch     = 25.4
		marginTopMM   = 10.0
		marginBotMM   = 20.0              // room for the native footer template above
		paperWidthIn  = 210.0 / mmPerInch // A4
		paperHeightIn = 297.0 / mmPerInch
	)

	var pdfBytes []byte
	err = chromedp.Run(ctx,
		chromedp.Navigate("file://"+filepath.ToSlash(tmpPath)),
		chromedp.ActionFunc(func(ctx context.Context) error {
			data, _, err := page.PrintToPDF().
				WithDisplayHeaderFooter(true).
				WithHeaderTemplate("<span></span>").
				WithFooterTemplate(footerHTML).
				WithPrintBackground(true).
				WithPaperWidth(paperWidthIn).
				WithPaperHeight(paperHeightIn).
				WithMarginTop(marginTopMM / mmPerInch).
				WithMarginBottom(marginBotMM / mmPerInch).
				WithMarginLeft(0).
				WithMarginRight(0).
				Do(ctx)
			if err != nil {
				return err
			}
			pdfBytes = data
			return nil
		}),
	)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("rendering PDF via Chrome timed out after %s", chromeTimeout)
		}
		return fmt.Errorf("rendering PDF via Chrome failed: %w", err)
	}

	// 0600: the PDF contains customer/company PII and financial data.
	if err := os.WriteFile(abs, pdfBytes, 0600); err != nil {
		return fmt.Errorf("could not write PDF: %w", err)
	}
	return nil
}
