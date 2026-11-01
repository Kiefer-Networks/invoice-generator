// Package config defines the invoice-generator configuration schema and
// handles loading/merging YAML and TOML config files.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

// ============================================================
// Types – English field names and English YAML/TOML tags
// ============================================================

type Config struct {
	Logo       string    `yaml:"logo" toml:"logo"`
	Language   string    `yaml:"language" toml:"language"`
	Color      string    `yaml:"color" toml:"color"`
	Currency   string    `yaml:"currency" toml:"currency"`
	Company    Company   `yaml:"company" toml:"company"`
	Customer   Customer  `yaml:"customer" toml:"customer"`
	Invoice    InvInfo   `yaml:"invoice" toml:"invoice"`
	Items      []Item    `yaml:"items" toml:"items"`
	VAT        VATConfig `yaml:"vat" toml:"vat"`
	Notice     string    `yaml:"notice" toml:"notice"`
	Notes      string    `yaml:"notes" toml:"notes"`
	PayTerms   string    `yaml:"payment_terms" toml:"payment_terms"`
	PayMethod  string    `yaml:"payment_method" toml:"payment_method"`
	Font       FontCfg   `yaml:"font" toml:"font"`
	Formatting FmtCfg    `yaml:"format" toml:"format"`
}

type Company struct {
	Name      string   `yaml:"name" toml:"name"`
	Address   string   `yaml:"address" toml:"address"`
	ZIP       string   `yaml:"zip" toml:"zip"`
	City      string   `yaml:"city" toml:"city"`
	Country   string   `yaml:"country" toml:"country"`
	Phone     string   `yaml:"phone" toml:"phone"`
	Email     string   `yaml:"email" toml:"email"`
	Website   string   `yaml:"website" toml:"website"`
	TaxNumber string   `yaml:"tax_number" toml:"tax_number"`
	VatID     string   `yaml:"vat_id" toml:"vat_id"`
	CEO       string   `yaml:"ceo" toml:"ceo"`
	Court     string   `yaml:"court" toml:"court"`
	Bank      BankInfo `yaml:"bank" toml:"bank"`
}

type BankInfo struct {
	Name   string `yaml:"name" toml:"name"`
	BLZ    string `yaml:"bank_code" toml:"bank_code"`
	AcctNr string `yaml:"account_number" toml:"account_number"`
	Holder string `yaml:"account_holder" toml:"account_holder"`
	BIC    string `yaml:"bic" toml:"bic"`
	IBAN   string `yaml:"iban" toml:"iban"`
}

type Customer struct {
	Name        string `yaml:"name" toml:"name"`
	Contact     string `yaml:"contact" toml:"contact"`
	Email       string `yaml:"email" toml:"email"`
	Address     string `yaml:"address" toml:"address"`
	ZIP         string `yaml:"zip" toml:"zip"`
	City        string `yaml:"city" toml:"city"`
	Country     string `yaml:"country" toml:"country"`
	CountryName string `yaml:"country_name" toml:"country_name"`
	VatID       string `yaml:"vat_id" toml:"vat_id"`
}

type InvInfo struct {
	Number  DocNumber `yaml:"number" toml:"number"`
	Date    string    `yaml:"date" toml:"date"`
	DueDate string    `yaml:"due_date" toml:"due_date"`
	Status  string    `yaml:"status" toml:"status"`
	// ValidUntil is used for quotes ("Angebot") instead of DueDate — the
	// offer's expiry date rather than a payment due date.
	ValidUntil string `yaml:"valid_until" toml:"valid_until"`
}

// DocNumber is an invoice/quote number kept exactly as written. A plain
// `any` let YAML's implicit typing rewrite unquoted numbers ("0042" was
// decoded as octal 34, "2026.10" as 2026.1), silently changing the legal
// document number.
type DocNumber string

// UnmarshalYAML keeps the scalar's literal text instead of its resolved type.
func (n *DocNumber) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("invoice number must be a single value (line %d)", node.Line)
	}
	*n = DocNumber(node.Value)
	return nil
}

// UnmarshalTOML accepts both quoted strings and bare TOML integers.
func (n *DocNumber) UnmarshalTOML(v any) error {
	switch t := v.(type) {
	case string:
		*n = DocNumber(t)
	case int64:
		*n = DocNumber(strconv.FormatInt(t, 10))
	default:
		return fmt.Errorf("invoice number must be a string or integer, got %T", v)
	}
	return nil
}

// DocType distinguishes an invoice ("Rechnung") from a quote ("Angebot").
// A quote reuses the entire invoice pipeline but swaps a handful of
// labels/dates and is never eligible for ZUGFeRD/Factur-X e-invoice XML,
// which only applies to actual invoices (EN 16931).
type DocType string

const (
	DocInvoice DocType = "invoice"
	DocQuote   DocType = "quote"
)

type Item struct {
	Description string  `yaml:"description" toml:"description"`
	Details     string  `yaml:"details" toml:"details"`
	Quantity    float64 `yaml:"quantity" toml:"quantity"`
	Unit        string  `yaml:"unit" toml:"unit"`
	Price       float64 `yaml:"price" toml:"price"`
}

type FontCfg struct {
	Regular string `yaml:"regular" toml:"regular"`
	Bold    string `yaml:"bold" toml:"bold"`
}

type VATConfig struct {
	Liable bool    `yaml:"liable" toml:"liable"`
	Rate   float64 `yaml:"rate" toml:"rate"`
}

type FmtCfg struct {
	DateFmt        string `yaml:"date" toml:"date"`
	DecimalSep     string `yaml:"decimal_separator" toml:"decimal_separator"`
	ThousandSep    string `yaml:"thousand_separator" toml:"thousand_separator"`
	CurrencySymbol string `yaml:"currency_symbol" toml:"currency_symbol"`
	CurrencyBefore *bool  `yaml:"currency_before" toml:"currency_before"`
	CurrencySpace  *bool  `yaml:"currency_space" toml:"currency_space"`
}

// ============================================================
// Tax calculation
// ============================================================

// RoundCents rounds a monetary amount half away from zero to two decimals.
// The value is first printed with six decimals so that binary float error
// (1.005*100 == 100.49999999999999) cannot push a .5 boundary down.
func RoundCents(x float64) float64 {
	cents, _ := strconv.ParseFloat(strconv.FormatFloat(x*100, 'f', 6, 64), 64)
	r := math.Round(cents) / 100
	if r == 0 {
		return 0 // never -0, which would print as "-0.00"
	}
	return r
}

// LineTotal is the net amount of one line item, rounded to cents.
func LineTotal(it Item) float64 {
	return RoundCents(it.Quantity * it.Price)
}

// CalcNet sums line-item totals (quantity * price), rounding each line to
// cents individually before summing — matching standard invoice tax rules.
// The sum is rounded again so float drift never reaches the tax base.
func CalcNet(items []Item) float64 {
	total := 0.0
	for _, it := range items {
		total += LineTotal(it)
	}
	return RoundCents(total)
}

// VATApplies reports whether VAT is charged under the given VAT config.
func VATApplies(vat VATConfig) bool {
	return vat.Liable && vat.Rate > 0
}

// CalcTax computes the VAT amount for a net total under the given VAT config.
func CalcTax(net float64, vat VATConfig) float64 {
	if !VATApplies(vat) {
		return 0
	}
	return RoundCents(net * vat.Rate / 100)
}

// ============================================================
// Config loading
// ============================================================

// MaxFileSize bounds how large a config file may be before parsing.
// Defends against resource-exhaustion (e.g. YAML "billion laughs" style
// anchor expansion) from an unexpectedly huge or malicious input file.
const MaxFileSize = 5 << 20 // 5 MiB

// Load reads and parses a YAML or TOML config file, selecting the parser
// by file extension (falling back to trying both if the extension is
// unrecognized).
func Load(path string) (*Config, error) {
	cfg := &Config{}
	if err := decodeInto(cfg, path); err != nil {
		return nil, err
	}
	return cfg, nil
}

// decodeInto decodes the file at path on top of cfg. Both decoders only
// assign keys that are present in the file, so decoding several files in
// turn gives per-key precedence to the later file — including explicit
// false/0/"" values, which a "fill empty fields" merge cannot express.
// A relative logo path is resolved against the directory of the file that
// set it.
func decodeInto(cfg *Config, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Size() > MaxFileSize {
		return fmt.Errorf("config file too large (%d bytes, max %d)", info.Size(), MaxFileSize)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	prevLogo := cfg.Logo
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".yaml", ".yml":
		err = yaml.Unmarshal(data, cfg)
	case ".toml":
		err = toml.Unmarshal(data, cfg)
	default:
		// Decode into a copy so a failed YAML attempt can't leave partial
		// values behind before trying TOML.
		attempt := *cfg
		if yamlErr := yaml.Unmarshal(data, &attempt); yamlErr != nil {
			attempt = *cfg
			if tomlErr := toml.Unmarshal(data, &attempt); tomlErr != nil {
				return fmt.Errorf("could not parse file as YAML or TOML")
			}
		}
		*cfg = attempt
	}
	if err != nil {
		return err
	}
	if cfg.Logo != prevLogo && cfg.Logo != "" && !filepath.IsAbs(cfg.Logo) {
		cfg.Logo = filepath.Join(filepath.Dir(path), cfg.Logo)
	}
	return nil
}

// LocalOverridePath returns the conventional "local override" path for a
// config file: inserting ".local" before the extension, e.g.
// "company.yaml" -> "company.local.yaml". This lets a team commit a safe
// template config while each user keeps real, sensitive data (bank
// details, tax IDs, ...) in an untracked sibling file. Callers are
// expected to add "*.local.*" to .gitignore.
func LocalOverridePath(path string) string {
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	return base + ".local" + ext
}

// LoadWithLocalOverride loads the config at path, then — if a sibling
// "<name>.local.<ext>" file exists (see LocalOverridePath) — loads it too
// and lets its values take precedence over the base file wherever it sets
// them. This is the mechanism for keeping real company/bank data out of
// version control while still shipping a safe example config.
//
// It returns the merged config and, if an override was applied, the path
// to the override file that was used (otherwise an empty string).
func LoadWithLocalOverride(path string) (*Config, string, error) {
	cfg := &Config{}
	overridePath, err := LoadInto(cfg, path)
	if err != nil {
		return nil, "", err
	}
	return cfg, overridePath, nil
}

// LoadInto decodes the config at path, then its local override (if one
// exists), on top of cfg. Every key a later file sets wins; everything it
// leaves out keeps its earlier value. Used to layer company.yaml,
// company.local.yaml, invoice.yaml and invoice.local.yaml in that order.
// It returns the override path that was applied, or "".
func LoadInto(cfg *Config, path string) (string, error) {
	if err := decodeInto(cfg, path); err != nil {
		return "", err
	}
	overridePath := LocalOverridePath(path)
	if _, statErr := os.Stat(overridePath); statErr != nil {
		if errors.Is(statErr, fs.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("error checking local override %s: %w", overridePath, statErr)
	}
	if err := decodeInto(cfg, overridePath); err != nil {
		return "", fmt.Errorf("error loading local override %s: %w", overridePath, err)
	}
	return overridePath, nil
}

// ============================================================
// Font discovery
// ============================================================

var fontCandidates = []struct{ regular, bold string }{
	// Linux
	{"/usr/share/fonts/liberation-sans-fonts/LiberationSans-Regular.ttf", "/usr/share/fonts/liberation-sans-fonts/LiberationSans-Bold.ttf"},
	{"/usr/share/fonts/dejavu-sans-fonts/DejaVuSans.ttf", "/usr/share/fonts/dejavu-sans-fonts/DejaVuSans-Bold.ttf"},
	{"/usr/share/fonts/google-noto-sans-fonts/NotoSans-Regular.ttf", "/usr/share/fonts/google-noto-sans-fonts/NotoSans-Bold.ttf"},
	{"/usr/share/fonts/truetype/liberation/LiberationSans-Regular.ttf", "/usr/share/fonts/truetype/liberation/LiberationSans-Bold.ttf"},
	{"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf", "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf"},
	{"/usr/share/fonts/truetype/noto/NotoSans-Regular.ttf", "/usr/share/fonts/truetype/noto/NotoSans-Bold.ttf"},
	{"/usr/share/fonts/TTF/DejaVuSans.ttf", "/usr/share/fonts/TTF/DejaVuSans-Bold.ttf"},
	// macOS
	{"/Library/Fonts/Arial.ttf", "/Library/Fonts/Arial Bold.ttf"},
	{"/Library/Fonts/Supplemental/Arial.ttf", "/Library/Fonts/Supplemental/Arial Bold.ttf"},
	{"/System/Library/Fonts/Supplemental/Arial.ttf", "/System/Library/Fonts/Supplemental/Arial Bold.ttf"},
	{"/System/Library/Fonts/Supplemental/Helvetica.ttf", "/System/Library/Fonts/Supplemental/Helvetica Bold.ttf"},
	// Windows
	{`C:\Windows\Fonts\arial.ttf`, `C:\Windows\Fonts\arialbd.ttf`},
	{`C:\Windows\Fonts\calibri.ttf`, `C:\Windows\Fonts\calibrib.ttf`},
	{`C:\Windows\Fonts\segoeui.ttf`, `C:\Windows\Fonts\segoeuib.ttf`},
}

// FindFonts locates a regular/bold TTF font pair, checking (in order):
// explicit config, the INVOICE_FONT_REGULAR/INVOICE_FONT_BOLD env vars,
// and a list of common OS font locations (Linux, macOS, Windows).
func FindFonts(cfg *Config) (string, string, error) {
	if cfg.Font.Regular != "" && cfg.Font.Bold != "" {
		if _, err := os.Stat(cfg.Font.Regular); err != nil {
			return "", "", fmt.Errorf("font regular not found: %s", cfg.Font.Regular)
		}
		if _, err := os.Stat(cfg.Font.Bold); err != nil {
			return "", "", fmt.Errorf("font bold not found: %s", cfg.Font.Bold)
		}
		return cfg.Font.Regular, cfg.Font.Bold, nil
	}
	if envReg, envBold := os.Getenv("INVOICE_FONT_REGULAR"), os.Getenv("INVOICE_FONT_BOLD"); envReg != "" && envBold != "" {
		if _, err := os.Stat(envReg); err == nil {
			if _, err := os.Stat(envBold); err == nil {
				return envReg, envBold, nil
			}
		}
	}
	for _, c := range fontCandidates {
		if _, err := os.Stat(c.regular); err == nil {
			if _, err := os.Stat(c.bold); err == nil {
				return c.regular, c.bold, nil
			}
		}
	}
	return "", "", fmt.Errorf("no TTF fonts found – install e.g.: sudo dnf install liberation-sans-fonts (Linux), or set font.regular/font.bold in the config, or INVOICE_FONT_REGULAR/INVOICE_FONT_BOLD env vars")
}

// ============================================================
// Sanitizing
// ============================================================

// SanitizeFilenamePart strips characters that could escape the intended
// output directory (path separators, "..", NUL) from a value that is
// about to be interpolated into a generated filename. The invoice number
// comes from user-supplied config data, so it must not be trusted as
// path-safe.
func SanitizeFilenamePart(s string) string {
	s = strings.ReplaceAll(s, "\x00", "")
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, "\\", "-")
	s = strings.ReplaceAll(s, "..", "-")
	// Characters invalid in Windows filenames (harmless to also strip
	// them on Unix, where they're legal but awkward in filenames).
	for _, c := range []string{":", "*", "?", "\"", "<", ">", "|"} {
		s = strings.ReplaceAll(s, c, "")
	}
	s = strings.TrimSpace(s)
	if s == "" {
		s = "unnamed"
	}
	return s
}
