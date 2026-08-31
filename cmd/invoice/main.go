// Command invoice generates invoice and quote PDFs from YAML/TOML config
// files, with optional ZUGFeRD/Factur-X e-invoice XML embedding.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/kiefer-networks/invoice-generator/internal/config"
	"github.com/kiefer-networks/invoice-generator/internal/locale"
	"github.com/kiefer-networks/invoice-generator/internal/paperless"
	"github.com/kiefer-networks/invoice-generator/internal/pdfgen"
	"github.com/kiefer-networks/invoice-generator/internal/render"
	"github.com/kiefer-networks/invoice-generator/internal/zugferd"
)

// version is set via -ldflags "-X main.version=..." at release build time.
var version = "dev"

// ============================================================
// CLI: Help
// ============================================================

func showHelp() {
	fmt.Print(`invoice – PDF invoice & quote generator with E-Invoice / ZUGFeRD support

USAGE
  invoice <invoice.yaml|.toml> [flags]     Generate invoice PDF
  invoice quote <quote.yaml|.toml> [flags] Generate quote (Angebot) PDF
  invoice init company [--lang <code>]     Create company config template
  invoice init invoice [--lang <code>]     Create invoice template
  invoice init quote [--lang <code>]       Create quote template
  invoice init paperless                   Create paperless.yaml upload config template
  invoice init template                    Extract HTML template for customization
  invoice version                          Show version
  invoice help                             Show this help

FLAGS
  -company <path>          Load separate company config file
  -o <path>                Output PDF path (default: "<date>; <company>; Rechnung <nr>.pdf")
  -zugferd                 Embed ZUGFeRD/Factur-X XML (BASIC profile, EN 16931) — invoices only
  -lang <code>             Override language
  -html                    Also save the rendered HTML file
  -t <path>                Use custom HTML template (default: embedded)
  -fpdf                    Use built-in renderer (no Chrome needed)
  -paperless               Upload the generated PDF to Paperless-ngx
  -paperless-config <path> Paperless config file (default: paperless.yaml next to -company, or the document)

RENDERERS
  Default: HTML template → Chrome/Chromium/Edge → PDF (best quality)
  If no browser is found, falls back to the built-in renderer.
  Use -fpdf to force the built-in renderer.
  Use -zugferd to embed e-invoice XML (always uses built-in).

  Customize the design:
    invoice init template        Extract template.html
    (edit template.html)         Customize colors, fonts, layout
    invoice -t template.html ... Use your custom template

LOCAL CONFIG OVERRIDES
  Keep real company/bank data out of version control: next to
  company.yaml, create company.local.yaml (or .toml) with the same
  keys — it is loaded automatically and takes precedence over the
  tracked template. Add "*.local.*" to .gitignore (done automatically
  by "invoice init company").

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
    invoice init quote

CURRENCIES
  EUR, USD, GBP, CHF, JPY, SEK, NOK, DKK, PLN, CZK, HUF, TRY,
  INR, BRL, AUD, CAD, NZD, MXN, ZAR, KRW, THB, and more.

NUMBER FORMATS
  The 'language:' field auto-sets decimal/thousand separators and
  currency position. Override individual values under 'format:':

    format:
      decimal_separator: ","
      thousand_separator: "."
      currency_before: false
      currency_space: true
      date: "02.01.2006"

PAPERLESS UPLOAD
  With -paperless the generated PDF is automatically uploaded to a
  Paperless-ngx instance. Configure it once in paperless.yaml (url,
  api_key, tags):

    invoice init paperless
    (edit paperless.yaml, or keep the key in paperless.local.yaml)
    invoice -paperless -company company.yaml invoice.yaml

  By default paperless.yaml is looked up next to -company (or next to
  the document if -company isn't used); override with -paperless-config.
  A failed upload is a warning, not an error — the PDF is always kept
  locally either way.

E-INVOICE
  With -zugferd an EN 16931 compliant e-invoice is generated (invoices
  only, not quotes):
  - Factur-X / ZUGFeRD BASIC profile
  - CII XML embedded as factur-x.xml inside the PDF
  Mandatory in DE: reception since 2025, sending B2B from 2027-2028

ENVIRONMENT VARIABLES
  INVOICE_CHROME          Path to a specific Chrome/Chromium/Edge binary
  INVOICE_FONT_REGULAR    Path to a TTF font for the built-in renderer
  INVOICE_FONT_BOLD       Path to the matching bold TTF font

EXAMPLES
  invoice invoice.yaml
  invoice quote quote.yaml
  invoice -zugferd -company company.yaml invoice.yaml
  invoice -html -company company.yaml invoice.yaml
  invoice -t custom.html -company company.yaml invoice.yaml
  invoice -paperless -company company.yaml invoice.yaml
  invoice init template
`)
}

// ============================================================
// CLI: Init templates
// ============================================================

func handleInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	lang := fs.String("lang", "de", "Template language")
	_ = fs.Parse(args) // flag.ExitOnError already terminates the process on a parse error

	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "Usage: invoice init company|invoice|quote|paperless|template [--lang de]")
		os.Exit(1)
	}

	switch fs.Arg(0) {
	case "company":
		writeTemplate("company.yaml", companyTemplate(*lang))
		ensureGitignoreHasLocalOverridePattern()
	case "invoice":
		writeTemplate("invoice.yaml", invoiceTemplate(*lang))
	case "quote":
		writeTemplate("quote.yaml", quoteTemplate(*lang))
	case "paperless":
		writeTemplate("paperless.yaml", paperlessTemplate())
		ensureGitignoreHasLocalOverridePattern()
	case "template":
		writeTemplate("template.html", render.DefaultTemplate())
	default:
		fmt.Fprintf(os.Stderr, "Unknown template: %s (expected: company, invoice, quote, paperless, or template)\n", fs.Arg(0))
		os.Exit(1)
	}
}

func writeTemplate(filename, content string) {
	if _, err := os.Stat(filename); err == nil {
		fmt.Fprintf(os.Stderr, "%s already exists. Delete or rename it, then try again.\n", filename)
		os.Exit(1)
	}
	if err := os.WriteFile(filename, []byte(content), 0600); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Template created: %s\n", filename)
}

// ensureGitignoreHasLocalOverridePattern appends the local-override and
// personal-logo ignore patterns to a .gitignore in the current directory,
// if one exists and doesn't already have them. Best-effort: silently
// does nothing if there is no .gitignore or it can't be written.
func ensureGitignoreHasLocalOverridePattern() {
	const marker = "*.local.yaml"
	data, err := os.ReadFile(".gitignore")
	if err != nil {
		return
	}
	if strings.Contains(string(data), marker) {
		return
	}
	addition := "\n# Local config overrides — never commit real company/bank data\n" +
		"*.local.yaml\n*.local.yml\n*.local.toml\n"
	f, err := os.OpenFile(".gitignore", os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(addition); err != nil {
		return
	}
	fmt.Println("Updated .gitignore: local config overrides (*.local.yaml etc.) are now excluded.")
}

// ============================================================
// CLI: Generate invoice or quote
// ============================================================

func handleGenerate(docType config.DocType, args []string) {
	fsName := "invoice"
	if docType == config.DocQuote {
		fsName = "quote"
	}
	fs := flag.NewFlagSet(fsName, flag.ExitOnError)
	companyPath := fs.String("company", "", "Company config file")
	outputPath := fs.String("o", "", "Output PDF path")
	zugferdFlag := fs.Bool("zugferd", false, "Embed ZUGFeRD/Factur-X XML")
	lang := fs.String("lang", "", "Override language")
	htmlOut := fs.Bool("html", false, "Also save HTML file")
	tmplPath := fs.String("t", "", "Custom HTML template path")
	useFpdf := fs.Bool("fpdf", false, "Use built-in renderer (no Chrome)")
	paperlessFlag := fs.Bool("paperless", false, "Upload the generated PDF to Paperless-ngx")
	paperlessConfigPath := fs.String("paperless-config", "", "Paperless config file (default: paperless.yaml next to -company, or the document)")
	_ = fs.Parse(args) // flag.ExitOnError already terminates the process on a parse error

	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "Error: specify a config file\nHelp: invoice help")
		os.Exit(1)
	}
	if *zugferdFlag && docType == config.DocQuote {
		fmt.Fprintln(os.Stderr, "Error: -zugferd is only valid for invoices, not quotes (ZUGFeRD/EN 16931 covers invoices only)")
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

	cfg, _, err := config.LoadWithLocalOverride(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	configDir := filepath.Dir(configPath)
	paperlessSearchDir := configDir

	// Merge company config
	if *companyPath != "" {
		absP, absErr := filepath.Abs(*companyPath)
		if absErr != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", absErr)
			os.Exit(1)
		}
		companyCfg, overridePath, companyErr := config.LoadWithLocalOverride(absP)
		if companyErr != nil {
			fmt.Fprintf(os.Stderr, "Error loading company config: %v\n", companyErr)
			os.Exit(1)
		}
		if overridePath != "" {
			fmt.Printf("Using local override:  %s\n", overridePath)
		}
		config.Merge(cfg, companyCfg)
		paperlessSearchDir = filepath.Dir(absP)
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
		fmt.Fprintln(os.Stderr, "Error: company.name is required")
		os.Exit(1)
	}
	if cfg.Customer.Name == "" {
		fmt.Fprintln(os.Stderr, "Error: customer.name is required")
		os.Exit(1)
	}
	if len(cfg.Items) == 0 {
		fmt.Fprintln(os.Stderr, "Error: at least one position is required")
		os.Exit(1)
	}

	loc := locale.Resolve(cfg.Language, locale.Formatting{
		DateFmt:        cfg.Formatting.DateFmt,
		DecimalSep:     cfg.Formatting.DecimalSep,
		ThousandSep:    cfg.Formatting.ThousandSep,
		CurrencySymbol: cfg.Formatting.CurrencySymbol,
		CurrencyBefore: cfg.Formatting.CurrencyBefore,
		CurrencySpace:  cfg.Formatting.CurrencySpace,
	})

	// Output path: "YYYYMMDD; <sender company>; Rechnung <nr>.pdf" (or
	// "Angebot <nr>" for a quote) — YYYYMMDD is the generation date, not
	// the invoice's own "datum" field, so re-running the tool later for
	// the same invoice number produces a distinctly named file rather
	// than silently overwriting an earlier draft.
	out := *outputPath
	if out == "" {
		prefix := "Rechnung"
		if docType == config.DocQuote {
			prefix = "Angebot"
		}
		created := time.Now().Format("20060102")
		company := config.SanitizeFilenamePart(cfg.Company.Name)
		nr := config.SanitizeFilenamePart(fmt.Sprintf("%v", cfg.Invoice.Number))
		filename := fmt.Sprintf("%s; %s; %s %s.pdf", created, company, prefix, nr)
		out = filepath.Join(configDir, filename)
	}

	// ZUGFeRD XML (invoices only — enforced above)
	var zugferdXML []byte
	if *zugferdFlag {
		zugferdXML, err = zugferd.GenerateCII(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error generating ZUGFeRD XML: %v\n", err)
			os.Exit(1)
		}
		xmlPath := strings.TrimSuffix(out, filepath.Ext(out)) + "_factur-x.xml"
		// 0600: this XML contains customer/company PII and financial data.
		if writeErr := os.WriteFile(xmlPath, zugferdXML, 0600); writeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not save XML: %v\n", writeErr)
		} else {
			fmt.Printf("ZUGFeRD XML:      %s\n", xmlPath)
		}
	}

	// Choose renderer
	usedFpdf := false
	if *useFpdf || *zugferdFlag {
		// Built-in fpdf renderer (required for ZUGFeRD attachment)
		if err := pdfgen.Generate(cfg, loc, docType, out, zugferdXML); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		usedFpdf = true
	} else {
		// HTML template → Chrome → PDF
		err := render.FromTemplate(cfg, loc, docType, out, *tmplPath, configDir)
		if err != nil {
			if strings.Contains(err.Error(), "Chrome") {
				fmt.Fprintf(os.Stderr, "Note: %v\n", err)
				fmt.Fprintln(os.Stderr, "Falling back to built-in renderer.")
				if err := pdfgen.Generate(cfg, loc, docType, out, nil); err != nil {
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
		html, err := render.HTML(cfg, loc, docType, *tmplPath, configDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not render HTML: %v\n", err)
		} else {
			// 0600: rendered HTML contains customer/company PII and financial data.
			if err := os.WriteFile(htmlPath, []byte(html), 0600); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not save HTML: %v\n", err)
			} else {
				fmt.Printf("HTML saved:       %s\n", htmlPath)
			}
		}
	}

	// Restrict the generated PDF to the owner: it contains customer/company
	// PII and financial data. Best-effort — permission bits are largely a
	// no-op on Windows/NTFS, but this is a real hardening on Unix systems.
	_ = os.Chmod(out, 0600)

	// Optionally upload to Paperless-ngx. Non-fatal: the PDF has already
	// been generated and saved locally, so an unreachable/misconfigured
	// Paperless instance should not turn a successful generation into a
	// failed CLI run.
	if *paperlessFlag {
		plPath := *paperlessConfigPath
		if plPath == "" {
			plPath = filepath.Join(paperlessSearchDir, "paperless.yaml")
		} else if absP, absErr := filepath.Abs(plPath); absErr == nil {
			plPath = absP
		}
		plCfg, overridePath, plErr := paperless.LoadWithLocalOverride(plPath)
		if plErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not load paperless config (%s): %v\n", plPath, plErr)
		} else {
			if overridePath != "" {
				fmt.Printf("Using local override:  %s\n", overridePath)
			}
			title := strings.TrimSuffix(filepath.Base(out), filepath.Ext(out))
			if err := paperless.Upload(plCfg, out, title); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: paperless upload failed: %v\n", err)
			} else {
				fmt.Println("Uploaded to Paperless.")
			}
		}
	}

	docLabel := "Invoice"
	if docType == config.DocQuote {
		docLabel = "Quote"
	}
	fmt.Printf("%s created:  %s\n", docLabel, out)
	if *zugferdFlag {
		fmt.Println("  (with Factur-X/ZUGFeRD e-invoice, BASIC profile)")
	}
	if !usedFpdf {
		fmt.Println("  (rendered via HTML template + Chrome)")
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
	case "version", "-v", "--version":
		fmt.Printf("invoice %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
	case "init":
		handleInit(os.Args[2:])
	case "quote", "angebot":
		handleGenerate(config.DocQuote, os.Args[2:])
	default:
		handleGenerate(config.DocInvoice, os.Args[1:])
	}
}
