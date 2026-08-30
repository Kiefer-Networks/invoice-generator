// Package config defines the invoice-generator configuration schema and
// handles loading/merging YAML and TOML config files.
package config

import (
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
	Logo       string    `yaml:"logo" toml:"logo"`
	Language   string    `yaml:"sprache" toml:"sprache"`
	Color      string    `yaml:"farbe" toml:"farbe"`
	Currency   string    `yaml:"waehrung" toml:"waehrung"`
	Company    Company   `yaml:"firma" toml:"firma"`
	Customer   Customer  `yaml:"kunde" toml:"kunde"`
	Invoice    InvInfo   `yaml:"rechnung" toml:"rechnung"`
	Items      []Item    `yaml:"positionen" toml:"positionen"`
	VAT        VATConfig `yaml:"mwst" toml:"mwst"`
	Notice     string    `yaml:"hinweis" toml:"hinweis"`
	Notes      string    `yaml:"notizen" toml:"notizen"`
	PayTerms   string    `yaml:"zahlungsbedingungen" toml:"zahlungsbedingungen"`
	PayMethod  string    `yaml:"zahlungsmethode" toml:"zahlungsmethode"`
	Font       FontCfg   `yaml:"schrift" toml:"schrift"`
	Formatting FmtCfg    `yaml:"format" toml:"format"`
}

type Company struct {
	Name      string   `yaml:"name" toml:"name"`
	Address   string   `yaml:"adresse" toml:"adresse"`
	ZIP       string   `yaml:"plz" toml:"plz"`
	City      string   `yaml:"ort" toml:"ort"`
	Country   string   `yaml:"land" toml:"land"`
	Phone     string   `yaml:"telefon" toml:"telefon"`
	Email     string   `yaml:"email" toml:"email"`
	Website   string   `yaml:"website" toml:"website"`
	TaxNumber string   `yaml:"steuernummer" toml:"steuernummer"`
	VatID     string   `yaml:"ust_id" toml:"ust_id"`
	CEO       string   `yaml:"geschaeftsfuehrer" toml:"geschaeftsfuehrer"`
	Court     string   `yaml:"amtsgericht" toml:"amtsgericht"`
	Bank      BankInfo `yaml:"bank" toml:"bank"`
}

type BankInfo struct {
	Name   string `yaml:"name" toml:"name"`
	BLZ    string `yaml:"blz" toml:"blz"`
	AcctNr string `yaml:"kontonr" toml:"kontonr"`
	Holder string `yaml:"kontoinhaber" toml:"kontoinhaber"`
	BIC    string `yaml:"bic" toml:"bic"`
	IBAN   string `yaml:"iban" toml:"iban"`
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
	// ValidUntil is used for quotes ("Angebot") instead of DueDate — the
	// offer's expiry date rather than a payment due date.
	ValidUntil string `yaml:"gueltig_bis" toml:"gueltig_bis"`
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

// CalcNet sums line-item totals (quantity * price), rounding each line to
// cents individually before summing — matching standard invoice tax rules.
func CalcNet(items []Item) float64 {
	total := 0.0
	for _, it := range items {
		total += math.Round(it.Quantity*it.Price*100) / 100
	}
	return total
}

// CalcTax computes the VAT amount for a net total under the given VAT config.
func CalcTax(net float64, vat VATConfig) float64 {
	if !vat.Liable || vat.Rate <= 0 {
		return 0
	}
	return math.Round(net*vat.Rate) / 100
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
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > MaxFileSize {
		return nil, fmt.Errorf("config file too large (%d bytes, max %d)", info.Size(), MaxFileSize)
	}

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
	base, err := Load(path)
	if err != nil {
		return nil, "", err
	}

	overridePath := LocalOverridePath(path)
	if overridePath == path {
		return base, "", nil
	}
	if _, statErr := os.Stat(overridePath); statErr != nil {
		return base, "", nil
	}

	override, err := Load(overridePath)
	if err != nil {
		return nil, "", fmt.Errorf("error loading local override %s: %w", overridePath, err)
	}

	// override's values win; anything it leaves empty falls back to base.
	Merge(override, base)
	return override, overridePath, nil
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
	return "", "", fmt.Errorf("no TTF fonts found – install e.g.: sudo dnf install liberation-sans-fonts (Linux), or set schrift.normal/schrift.fett in the config, or INVOICE_FONT_REGULAR/INVOICE_FONT_BOLD env vars")
}

// ============================================================
// Merging & sanitizing
// ============================================================

// Merge fills any field left empty in cfg with the corresponding value
// from co. Used both for the -company overlay and for local config
// overrides (see LoadWithLocalOverride).
func Merge(cfg, co *Config) {
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
	s = strings.TrimSpace(s)
	if s == "" {
		s = "unnamed"
	}
	return s
}
