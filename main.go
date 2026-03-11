package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

// ============================================================
// Types – English names, German YAML/TOML tags for config compat
// ============================================================

type Config struct {
	Logo         string    `yaml:"logo" toml:"logo"`
	Language     string    `yaml:"sprache" toml:"sprache"`
	Color        string    `yaml:"farbe" toml:"farbe"`
	Currency     string    `yaml:"waehrung" toml:"waehrung"`
	Company      Company   `yaml:"firma" toml:"firma"`
	Customer     Customer  `yaml:"kunde" toml:"kunde"`
	Invoice      InvInfo   `yaml:"rechnung" toml:"rechnung"`
	Items        []Item    `yaml:"positionen" toml:"positionen"`
	VAT          VATConfig `yaml:"mwst" toml:"mwst"`
	Notice       string    `yaml:"hinweis" toml:"hinweis"`
	Notes        string    `yaml:"notizen" toml:"notizen"`
	PayTerms     string    `yaml:"zahlungsbedingungen" toml:"zahlungsbedingungen"`
	PayMethod    string    `yaml:"zahlungsmethode" toml:"zahlungsmethode"`
	Font         FontCfg   `yaml:"schrift" toml:"schrift"`
	Formatting   FmtCfg    `yaml:"format" toml:"format"`
}

type Company struct {
	Name       string   `yaml:"name" toml:"name"`
	Address    string   `yaml:"adresse" toml:"adresse"`
	ZIP        string   `yaml:"plz" toml:"plz"`
	City       string   `yaml:"ort" toml:"ort"`
	Country    string   `yaml:"land" toml:"land"`
	Phone      string   `yaml:"telefon" toml:"telefon"`
	Email      string   `yaml:"email" toml:"email"`
	Website    string   `yaml:"website" toml:"website"`
	TaxNumber  string   `yaml:"steuernummer" toml:"steuernummer"`
	VatID      string   `yaml:"ust_id" toml:"ust_id"`
	CEO        string   `yaml:"geschaeftsfuehrer" toml:"geschaeftsfuehrer"`
	Court      string   `yaml:"amtsgericht" toml:"amtsgericht"`
	Bank       BankInfo `yaml:"bank" toml:"bank"`
}

type BankInfo struct {
	Name    string `yaml:"name" toml:"name"`
	BLZ     string `yaml:"blz" toml:"blz"`
	AcctNr  string `yaml:"kontonr" toml:"kontonr"`
	Holder  string `yaml:"kontoinhaber" toml:"kontoinhaber"`
	BIC     string `yaml:"bic" toml:"bic"`
	IBAN    string `yaml:"iban" toml:"iban"`
}

type Customer struct {
	Name        string `yaml:"name" toml:"name"`
	Contact     string `yaml:"ansprechpartner" toml:"ansprechpartner"`
	Email       string `yaml:"email" toml:"email"`
	Address     string `yaml:"adresse" toml:"adresse"`
	ZIP         string `yaml:"plz" toml:"plz"`
	City        string `yaml:"ort" toml:"ort"`
	Country     string `yaml:"land" toml:"land"`
	CountryName string `yaml:"land_name" toml:"land_name"`
	VatID       string `yaml:"ust_id" toml:"ust_id"`
}

type InvInfo struct {
	Number  any    `yaml:"nummer" toml:"nummer"`
	Date    string `yaml:"datum" toml:"datum"`
	DueDate string `yaml:"faelligkeit" toml:"faelligkeit"`
	Status  string `yaml:"status" toml:"status"`
}

type Item struct {
	Description string  `yaml:"beschreibung" toml:"beschreibung"`
	Details     string  `yaml:"details" toml:"details"`
	Quantity    float64 `yaml:"menge" toml:"menge"`
	Unit        string  `yaml:"einheit" toml:"einheit"`
	Price       float64 `yaml:"preis" toml:"preis"`
}

type FontCfg struct {
	Regular string `yaml:"normal" toml:"normal"`
	Bold    string `yaml:"fett" toml:"fett"`
}

type VATConfig struct {
	Liable bool    `yaml:"pflichtig" toml:"pflichtig"`
	Rate   float64 `yaml:"satz" toml:"satz"`
}

type FmtCfg struct {
	DateFmt        string `yaml:"datum" toml:"datum"`
	DecimalSep     string `yaml:"dezimal" toml:"dezimal"`
	ThousandSep    string `yaml:"tausender" toml:"tausender"`
	CurrencySymbol string `yaml:"waehrung_symbol" toml:"waehrung_symbol"`
	CurrencyBefore *bool  `yaml:"waehrung_vor" toml:"waehrung_vor"`
	CurrencySpace  *bool  `yaml:"waehrung_abstand" toml:"waehrung_abstand"`
}

// ============================================================
// Tax calculation
// ============================================================

func calcNet(items []Item) float64 {
	total := 0.0
	for _, it := range items {
		total += math.Round(it.Quantity*it.Price*100) / 100
	}
	return total
}

func calcTax(net float64, vat VATConfig) float64 {
	if !vat.Liable || vat.Rate <= 0 {
		return 0
	}
	return math.Round(net*vat.Rate) / 100
}

// ============================================================
// Config loading
// ============================================================

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := &Config{}
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".yaml", ".yml":
		err = yaml.Unmarshal(data, cfg)
	case ".toml":
		err = toml.Unmarshal(data, cfg)
	default:
		if yamlErr := yaml.Unmarshal(data, cfg); yamlErr != nil {
			if tomlErr := toml.Unmarshal(data, cfg); tomlErr != nil {
				return nil, fmt.Errorf("could not parse file as YAML or TOML")
			}
		}
	}
	return cfg, err
}

// ============================================================
// Font discovery
// ============================================================

var fontCandidates = []struct{ regular, bold string }{
	{"/usr/share/fonts/liberation-sans-fonts/LiberationSans-Regular.ttf", "/usr/share/fonts/liberation-sans-fonts/LiberationSans-Bold.ttf"},
	{"/usr/share/fonts/dejavu-sans-fonts/DejaVuSans.ttf", "/usr/share/fonts/dejavu-sans-fonts/DejaVuSans-Bold.ttf"},
	{"/usr/share/fonts/google-noto-sans-fonts/NotoSans-Regular.ttf", "/usr/share/fonts/google-noto-sans-fonts/NotoSans-Bold.ttf"},
	{"/usr/share/fonts/truetype/liberation/LiberationSans-Regular.ttf", "/usr/share/fonts/truetype/liberation/LiberationSans-Bold.ttf"},
	{"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf", "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf"},
	{"/usr/share/fonts/truetype/noto/NotoSans-Regular.ttf", "/usr/share/fonts/truetype/noto/NotoSans-Bold.ttf"},
	{"/usr/share/fonts/TTF/DejaVuSans.ttf", "/usr/share/fonts/TTF/DejaVuSans-Bold.ttf"},
	{"/Library/Fonts/Arial.ttf", "/Library/Fonts/Arial Bold.ttf"},
}

func findFonts(cfg *Config) (string, string, error) {
	if cfg.Font.Regular != "" && cfg.Font.Bold != "" {
		if _, err := os.Stat(cfg.Font.Regular); err != nil {
			return "", "", fmt.Errorf("font regular not found: %s", cfg.Font.Regular)
		}
		if _, err := os.Stat(cfg.Font.Bold); err != nil {
			return "", "", fmt.Errorf("font bold not found: %s", cfg.Font.Bold)
		}
		return cfg.Font.Regular, cfg.Font.Bold, nil
	}
	for _, c := range fontCandidates {
		if _, err := os.Stat(c.regular); err == nil {
			if _, err := os.Stat(c.bold); err == nil {
				return c.regular, c.bold, nil
			}
		}
	}
	return "", "", fmt.Errorf("no TTF fonts found – install e.g.: sudo dnf install liberation-sans-fonts")
}

// ============================================================
// CLI: Help
// ============================================================

func showHelp() {
	fmt.Print(`invoice – PDF invoice generator with E-Invoice / ZUGFeRD support

USAGE
  invoice <invoice.yaml|.toml> [flags]     Generate invoice PDF
  invoice init company [--lang <code>]     Create company config template
  invoice init invoice [--lang <code>]     Create invoice template
  invoice init template                    Extract HTML template for customization
  invoice help                             Show this help

FLAGS
  -company <path>  Load separate company config file
  -o <path>        Output PDF path (default: Rechnung_<nr>.pdf)
  -zugferd         Embed ZUGFeRD/Factur-X XML (BASIC profile, EN 16931)
  -lang <code>     Override language
  -html            Also save the rendered HTML file
  -t <path>        Use custom HTML template (default: embedded)
  -fpdf            Use built-in renderer (no Chrome needed)

RENDERERS
  Default: HTML template → Chrome/Chromium → PDF (best quality)
  If Chrome is not found, falls back to the built-in renderer.
  Use -fpdf to force the built-in renderer.
  Use -zugferd to embed e-invoice XML (always uses built-in).

  Customize the design:
    invoice init template        Extract template.html
    (edit template.html)         Customize colors, fonts, layout
    invoice -t template.html ... Use your custom template

LANGUAGES
  de  Deutsch          en  English         fr  Français
  es  Español          it  Italiano        nl  Nederlands
  pt  Português

CONFIGURATION
  Formats: YAML (.yaml) and TOML (.toml)
  Put company data in company.yaml once, then per-invoice only
  customer and line items in invoice.yaml:

    invoice -company company.yaml invoice.yaml

  Generate templates:
    invoice init company
    invoice init invoice

CURRENCIES
  EUR, USD, GBP, CHF, JPY, SEK, NOK, DKK, PLN, CZK, HUF, TRY,
  INR, BRL, AUD, CAD, NZD, MXN, ZAR, KRW, THB, and more.

NUMBER FORMATS
  The 'sprache:' field auto-sets decimal/thousand separators and
  currency position. Override individual values under 'format:':

    format:
      dezimal: ","
      tausender: "."
      waehrung_vor: false
      waehrung_abstand: true
      datum: "02.01.2006"

E-INVOICE
  With -zugferd an EN 16931 compliant e-invoice is generated:
  - Factur-X / ZUGFeRD BASIC profile
  - CII XML embedded as factur-x.xml inside the PDF
  Mandatory in DE: reception since 2025, sending B2B from 2027-2028

EXAMPLES
  invoice invoice.yaml
  invoice -zugferd -company company.yaml invoice.yaml
  invoice -html -company company.yaml invoice.yaml
  invoice -t custom.html -company company.yaml invoice.yaml
  invoice init template
`)
}

// ============================================================
// CLI: Init templates
// ============================================================

func handleInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	lang := fs.String("lang", "de", "Template language")
	fs.Parse(args)

	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "Usage: invoice init company|invoice|template [--lang de]")
		os.Exit(1)
	}

	switch fs.Arg(0) {
	case "company", "firma":
		writeTemplate("company.yaml", companyTemplate(*lang))
	case "invoice", "rechnung":
		writeTemplate("invoice.yaml", invoiceTemplate(*lang))
	case "template":
		writeTemplate("template.html", defaultTemplateHTML)
	default:
		fmt.Fprintf(os.Stderr, "Unknown template: %s (expected: company, invoice, or template)\n", fs.Arg(0))
		os.Exit(1)
	}
}

func writeTemplate(filename, content string) {
	if _, err := os.Stat(filename); err == nil {
		fmt.Fprintf(os.Stderr, "%s already exists. Delete or rename it, then try again.\n", filename)
		os.Exit(1)
	}
	if err := os.WriteFile(filename, []byte(content), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Template created: %s\n", filename)
}

func companyTemplate(lang string) string {
	return fmt.Sprintf(`# ============================================================
# Company config (company.yaml)
# Create once, use with every invoice via -company:
#   invoice -company company.yaml invoice.yaml
# ============================================================

# Logo (PNG/JPG, relative or absolute path)
# logo: "./logo.png"

# Language: de, en, fr, es, it, nl, pt
sprache: "%s"

# Accent color (hex)
farbe: "#5B9BD5"

# Currency
waehrung: "EUR"

firma:
  name: "My Company GmbH"
  adresse: "Sample Street 1"
  plz: "12345"
  ort: "Sample City"
  land: "DE"
  telefon: "+49 123 4567890"
  email: "info@mycompany.de"
  website: "www.mycompany.de"
  steuernummer: "12/345/67890"
  ust_id: "DE123456789"
  geschaeftsfuehrer: "John Doe"
  amtsgericht: "Sample City"
  bank:
    name: "Sample Bank"
    iban: "DE89 3704 0044 0532 0130 00"
    bic: "COBADEFFXXX"
    kontoinhaber: "John Doe"

# VAT settings
mwst:
  pflichtig: true         # false = small business (no VAT shown)
  satz: 19.0              # VAT rate in percent

# Tax notice (small business exemption)
# hinweis: "According to §19 UStG no VAT is charged."

# Payment information
zahlungsbedingungen: "Payable within 14 days of invoice date."
zahlungsmethode: "Bank transfer"

# Formatting overrides (optional, overrides language defaults)
# format:
#   datum: "02.01.2006"         # Go date format
#   dezimal: ","
#   tausender: "."
#   waehrung_vor: false
#   waehrung_abstand: true

# Custom fonts (optional)
# schrift:
#   normal: "/path/to/font.ttf"
#   fett: "/path/to/font-bold.ttf"
`, lang)
}

func invoiceTemplate(lang string) string {
	return fmt.Sprintf(`# ============================================================
# Invoice
# Usage: invoice invoice.yaml
# With company data: invoice -company company.yaml invoice.yaml
# With e-invoice:    invoice -zugferd -company company.yaml invoice.yaml
# ============================================================

# Language (or set in company.yaml)
sprache: "%s"

kunde:
  name: "Sample GmbH"
  ansprechpartner: "Jane Doe"
  email: "jane@sample.de"
  adresse: "Client Road 42"
  plz: "54321"
  ort: "Client City"
  land: "DE"
  land_name: "Germany"
  ust_id: "DE987654321"

rechnung:
  nummer: 2026-001
  datum: "01.03.2026"
  faelligkeit: "15.03.2026"
  # status: "SENT"

positionen:
  - beschreibung: "Web Development"
    details: "New landing page development"
    menge: 10
    einheit: "Stunde(n)"
    preis: 85.00

  - beschreibung: "Server Maintenance"
    details: "Monthly maintenance and updates"
    menge: 1
    einheit: "Pauschal"
    preis: 150.00

# Notes (shown at the bottom of the invoice)
notizen: "Thank you for your business."
`, lang)
}

// ============================================================
// CLI: Generate invoice
// ============================================================

func handleGenerate(args []string) {
	fs := flag.NewFlagSet("invoice", flag.ExitOnError)
	companyPath := fs.String("company", "", "Company config file")
	outputPath := fs.String("o", "", "Output PDF path")
	zugferd := fs.Bool("zugferd", false, "Embed ZUGFeRD/Factur-X XML")
	lang := fs.String("lang", "", "Override language")
	htmlOut := fs.Bool("html", false, "Also save HTML file")
	tmplPath := fs.String("t", "", "Custom HTML template path")
	useFpdf := fs.Bool("fpdf", false, "Use built-in renderer (no Chrome)")
	fs.Parse(args)

	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "Error: specify an invoice config file\nHelp: invoice help")
		os.Exit(1)
	}

	configPath, err := filepath.Abs(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if _, err := os.Stat(configPath); err != nil {
		fmt.Fprintf(os.Stderr, "File not found: %s\n", configPath)
		os.Exit(1)
	}

	cfg, err := loadConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	configDir := filepath.Dir(configPath)

	// Merge company config
	if *companyPath != "" {
		absP, _ := filepath.Abs(*companyPath)
		companyCfg, err := loadConfig(absP)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading company config: %v\n", err)
			os.Exit(1)
		}
		mergeConfig(cfg, companyCfg)
	}

	// Override language
	if *lang != "" {
		cfg.Language = *lang
	}

	// Resolve relative logo path
	if cfg.Logo != "" && !filepath.IsAbs(cfg.Logo) {
		cfg.Logo = filepath.Join(configDir, cfg.Logo)
	}

	// Defaults
	if cfg.Currency == "" {
		cfg.Currency = "EUR"
	}

	// Validate
	if cfg.Company.Name == "" {
		fmt.Fprintln(os.Stderr, "Error: firma.name is required")
		os.Exit(1)
	}
	if cfg.Customer.Name == "" {
		fmt.Fprintln(os.Stderr, "Error: kunde.name is required")
		os.Exit(1)
	}
	if len(cfg.Items) == 0 {
		fmt.Fprintln(os.Stderr, "Error: at least one position is required")
		os.Exit(1)
	}

	loc := resolveLocale(cfg)

	// Output path
	out := *outputPath
	if out == "" {
		nr := fmt.Sprintf("%v", cfg.Invoice.Number)
		out = filepath.Join(configDir, fmt.Sprintf("Rechnung_%s.pdf", nr))
	}

	// ZUGFeRD XML
	var zugferdXML []byte
	if *zugferd {
		zugferdXML, err = generateCII(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error generating ZUGFeRD XML: %v\n", err)
			os.Exit(1)
		}
		xmlPath := strings.TrimSuffix(out, filepath.Ext(out)) + "_factur-x.xml"
		if writeErr := os.WriteFile(xmlPath, zugferdXML, 0644); writeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not save XML: %v\n", writeErr)
		} else {
			fmt.Printf("ZUGFeRD XML:      %s\n", xmlPath)
		}
	}

	// Choose renderer
	usedFpdf := false
	if *useFpdf || *zugferd {
		// Built-in fpdf renderer (required for ZUGFeRD attachment)
		if err := generatePDF(cfg, loc, out, zugferdXML); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		usedFpdf = true
	} else {
		// HTML template → Chrome → PDF
		err := generateFromTemplate(cfg, loc, out, *tmplPath, configDir)
		if err != nil {
			if strings.Contains(err.Error(), "Chrome") {
				fmt.Fprintf(os.Stderr, "Note: %v\n", err)
				fmt.Fprintln(os.Stderr, "Falling back to built-in renderer.")
				if err := generatePDF(cfg, loc, out, nil); err != nil {
					fmt.Fprintf(os.Stderr, "Error: %v\n", err)
					os.Exit(1)
				}
				usedFpdf = true
			} else {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		}
	}

	// Optionally save HTML
	if *htmlOut {
		htmlPath := strings.TrimSuffix(out, filepath.Ext(out)) + ".html"
		html, err := renderHTML(cfg, loc, *tmplPath, configDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not render HTML: %v\n", err)
		} else {
			if err := os.WriteFile(htmlPath, []byte(html), 0644); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not save HTML: %v\n", err)
			} else {
				fmt.Printf("HTML saved:       %s\n", htmlPath)
			}
		}
	}

	fmt.Printf("Invoice created:  %s\n", out)
	if *zugferd {
		fmt.Println("  (with Factur-X/ZUGFeRD e-invoice, BASIC profile)")
	}
	if !usedFpdf {
		fmt.Println("  (rendered via HTML template + Chrome)")
	}
}

func mergeConfig(cfg, co *Config) {
	if cfg.Company.Name == "" {
		cfg.Company = co.Company
	}
	if cfg.Logo == "" {
		cfg.Logo = co.Logo
	}
	if cfg.Notice == "" {
		cfg.Notice = co.Notice
	}
	if cfg.Notes == "" {
		cfg.Notes = co.Notes
	}
	if cfg.PayTerms == "" {
		cfg.PayTerms = co.PayTerms
	}
	if cfg.PayMethod == "" {
		cfg.PayMethod = co.PayMethod
	}
	if cfg.Font.Regular == "" {
		cfg.Font = co.Font
	}
	if cfg.Currency == "" {
		cfg.Currency = co.Currency
	}
	if cfg.Language == "" {
		cfg.Language = co.Language
	}
	if cfg.Color == "" {
		cfg.Color = co.Color
	}
	if !cfg.VAT.Liable && cfg.VAT.Rate == 0 {
		cfg.VAT = co.VAT
	}
	if cfg.Formatting.DateFmt == "" && cfg.Formatting.DecimalSep == "" {
		cfg.Formatting = co.Formatting
	}
}

// ============================================================
// Main
// ============================================================

func main() {
	if len(os.Args) < 2 {
		showHelp()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "help", "-h", "--help":
		showHelp()
	case "init":
		handleInit(os.Args[2:])
	default:
		handleGenerate(os.Args[1:])
	}
}
