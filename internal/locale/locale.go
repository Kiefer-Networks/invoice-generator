// Package locale provides language-specific labels and locale-aware
// number, currency, and date formatting for invoice rendering.
package locale

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Labels holds all translatable UI strings for the invoice PDF.
type Labels struct {
	InvoiceTitle   string
	InvoiceFrom    string
	InvoiceTo      string
	Details        string
	Description    string
	Quantity       string
	UnitPrice      string
	Amount         string
	Summary        string
	Subtotal       string
	Tax            string
	Total          string
	InvoiceNr      string
	InvoiceDate    string
	DueDate        string
	Status         string
	Notes          string
	PaymentTerms   string
	PaymentMethods string
	BankAccount    string
	TaxID          string
	TaxOverview    string
	Taxable        string
	TaxAmount      string
	Page           string

	// Quote (Angebot) specific — used instead of the invoice-title/number/
	// due-date labels above when rendering a quote rather than an invoice.
	QuoteTitle string
	QuoteNr    string
	ValidUntil string
}

// Locale defines formatting rules and labels for a language.
type Locale struct {
	Labels        Labels
	DateFormat    string
	DecimalSep    string
	ThousandSep   string
	CurrencyPos   string // "before" or "after"
	CurrencySpace bool
}

// Formatting carries locale formatting overrides taken from a config file.
// It mirrors the subset of config fields that resolveLocale-style code needs,
// letting this package stay decoupled from the config package's YAML/TOML tags.
type Formatting struct {
	DateFmt        string
	DecimalSep     string
	ThousandSep    string
	CurrencySymbol string
	CurrencyBefore *bool
	CurrencySpace  *bool
}

var locales = map[string]*Locale{
	"de": {
		Labels: Labels{
			InvoiceTitle: "Rechnung", InvoiceFrom: "VON", InvoiceTo: "RECHNUNG AN", Details: "RECHNUNGSDETAILS",
			Description: "BESCHREIBUNG", Quantity: "MENGE", UnitPrice: "EINZELPREIS", Amount: "BETRAG",
			Summary: "ZUSAMMENFASSUNG", Subtotal: "Zwischensumme", Tax: "Steuer", Total: "Gesamt",
			InvoiceNr: "Rechnung Nr.", InvoiceDate: "Rechnungsdatum", DueDate: "Fällig am", Status: "Status",
			Notes: "NOTIZEN", PaymentTerms: "ZAHLUNGSBEDINGUNGEN", PaymentMethods: "ZAHLUNGSMETHODEN",
			BankAccount: "BANKVERBINDUNG", TaxID: "USt-IdNr.", TaxOverview: "Steuerübersicht",
			Taxable: "Steuerpflichtig", TaxAmount: "Steuerbetrag", Page: "Seite",
			QuoteTitle: "Angebot", QuoteNr: "Angebot Nr.", ValidUntil: "Gültig bis",
		},
		DateFormat: "02.01.2006", DecimalSep: ",", ThousandSep: ".", CurrencyPos: "after", CurrencySpace: true,
	},
	"en": {
		Labels: Labels{
			InvoiceTitle: "Invoice", InvoiceFrom: "FROM", InvoiceTo: "BILL TO", Details: "LINE ITEMS",
			Description: "DESCRIPTION", Quantity: "QTY", UnitPrice: "UNIT PRICE", Amount: "AMOUNT",
			Summary: "SUMMARY", Subtotal: "Subtotal", Tax: "Tax", Total: "Total",
			InvoiceNr: "Invoice No.", InvoiceDate: "Invoice Date", DueDate: "Due Date", Status: "Status",
			Notes: "NOTES", PaymentTerms: "PAYMENT TERMS", PaymentMethods: "PAYMENT METHODS",
			BankAccount: "BANK ACCOUNT", TaxID: "Tax ID", TaxOverview: "Tax Overview",
			Taxable: "Taxable", TaxAmount: "Tax Amount", Page: "Page",
			QuoteTitle: "Quote", QuoteNr: "Quote No.", ValidUntil: "Valid Until",
		},
		DateFormat: "01/02/2006", DecimalSep: ".", ThousandSep: ",", CurrencyPos: "before", CurrencySpace: false,
	},
	"fr": {
		Labels: Labels{
			InvoiceTitle: "Facture", InvoiceFrom: "DE", InvoiceTo: "FACTURER À", Details: "DÉTAILS",
			Description: "DESCRIPTION", Quantity: "QTÉ", UnitPrice: "PRIX UNITAIRE", Amount: "MONTANT",
			Summary: "RÉSUMÉ", Subtotal: "Sous-total", Tax: "TVA", Total: "Total",
			InvoiceNr: "Facture n°", InvoiceDate: "Date de facture", DueDate: "Échéance", Status: "Statut",
			Notes: "REMARQUES", PaymentTerms: "CONDITIONS DE PAIEMENT", PaymentMethods: "MODES DE PAIEMENT",
			BankAccount: "COORDONNÉES BANCAIRES", TaxID: "N° TVA", TaxOverview: "Détail TVA",
			Taxable: "Base HT", TaxAmount: "Montant TVA", Page: "Page",
			QuoteTitle: "Devis", QuoteNr: "Devis n°", ValidUntil: "Valable jusqu'au",
		},
		DateFormat: "02/01/2006", DecimalSep: ",", ThousandSep: " ", CurrencyPos: "after", CurrencySpace: true,
	},
	"es": {
		Labels: Labels{
			InvoiceTitle: "Factura", InvoiceFrom: "DE", InvoiceTo: "FACTURAR A", Details: "DETALLE",
			Description: "DESCRIPCIÓN", Quantity: "CANTIDAD", UnitPrice: "PRECIO UNIT.", Amount: "IMPORTE",
			Summary: "RESUMEN", Subtotal: "Subtotal", Tax: "IVA", Total: "Total",
			InvoiceNr: "Factura #", InvoiceDate: "Fecha de factura", DueDate: "Vencimiento", Status: "Estado",
			Notes: "NOTAS", PaymentTerms: "CONDICIONES DE PAGO", PaymentMethods: "MÉTODOS DE PAGO",
			BankAccount: "CUENTA BANCARIA", TaxID: "NIF/CIF", TaxOverview: "Desglose de impuestos",
			Taxable: "Base imponible", TaxAmount: "Cuota", Page: "Página",
			QuoteTitle: "Presupuesto", QuoteNr: "Presupuesto #", ValidUntil: "Válido hasta",
		},
		DateFormat: "02/01/2006", DecimalSep: ",", ThousandSep: ".", CurrencyPos: "after", CurrencySpace: true,
	},
	"it": {
		Labels: Labels{
			InvoiceTitle: "Fattura", InvoiceFrom: "DA", InvoiceTo: "FATTURARE A", Details: "DETTAGLI",
			Description: "DESCRIZIONE", Quantity: "QTÀ", UnitPrice: "PREZZO UNIT.", Amount: "IMPORTO",
			Summary: "RIEPILOGO", Subtotal: "Subtotale", Tax: "IVA", Total: "Totale",
			InvoiceNr: "Fattura n.", InvoiceDate: "Data fattura", DueDate: "Scadenza", Status: "Stato",
			Notes: "NOTE", PaymentTerms: "CONDIZIONI DI PAGAMENTO", PaymentMethods: "METODI DI PAGAMENTO",
			BankAccount: "COORDINATE BANCARIE", TaxID: "P.IVA", TaxOverview: "Dettaglio IVA",
			Taxable: "Imponibile", TaxAmount: "Imposta", Page: "Pagina",
			QuoteTitle: "Preventivo", QuoteNr: "Preventivo n.", ValidUntil: "Valido fino al",
		},
		DateFormat: "02/01/2006", DecimalSep: ",", ThousandSep: ".", CurrencyPos: "after", CurrencySpace: true,
	},
	"nl": {
		Labels: Labels{
			InvoiceTitle: "Factuur", InvoiceFrom: "VAN", InvoiceTo: "FACTUREREN AAN", Details: "FACTUURDETAILS",
			Description: "OMSCHRIJVING", Quantity: "AANTAL", UnitPrice: "EENHEIDSPRIJS", Amount: "BEDRAG",
			Summary: "SAMENVATTING", Subtotal: "Subtotaal", Tax: "BTW", Total: "Totaal",
			InvoiceNr: "Factuur #", InvoiceDate: "Factuurdatum", DueDate: "Vervaldatum", Status: "Status",
			Notes: "OPMERKINGEN", PaymentTerms: "BETALINGSVOORWAARDEN", PaymentMethods: "BETAALMETHODEN",
			BankAccount: "BANKREKENING", TaxID: "BTW-nr.", TaxOverview: "BTW-overzicht",
			Taxable: "Belastbaar", TaxAmount: "BTW-bedrag", Page: "Pagina",
			QuoteTitle: "Offerte", QuoteNr: "Offerte #", ValidUntil: "Geldig tot",
		},
		DateFormat: "02-01-2006", DecimalSep: ",", ThousandSep: ".", CurrencyPos: "before", CurrencySpace: true,
	},
	"pt": {
		Labels: Labels{
			InvoiceTitle: "Fatura", InvoiceFrom: "DE", InvoiceTo: "FATURAR PARA", Details: "DETALHES",
			Description: "DESCRIÇÃO", Quantity: "QTD", UnitPrice: "PREÇO UNIT.", Amount: "VALOR",
			Summary: "RESUMO", Subtotal: "Subtotal", Tax: "IVA", Total: "Total",
			InvoiceNr: "Fatura #", InvoiceDate: "Data da fatura", DueDate: "Vencimento", Status: "Estado",
			Notes: "NOTAS", PaymentTerms: "CONDIÇÕES DE PAGAMENTO", PaymentMethods: "MÉTODOS DE PAGAMENTO",
			BankAccount: "DADOS BANCÁRIOS", TaxID: "NIF", TaxOverview: "Resumo IVA",
			Taxable: "Base tributável", TaxAmount: "Valor IVA", Page: "Página",
			QuoteTitle: "Orçamento", QuoteNr: "Orçamento #", ValidUntil: "Válido até",
		},
		DateFormat: "02/01/2006", DecimalSep: ",", ThousandSep: ".", CurrencyPos: "after", CurrencySpace: true,
	},
}

var currencySymbols = map[string]string{
	"EUR": "€", "USD": "$", "GBP": "£", "CHF": "CHF", "JPY": "¥", "CNY": "¥",
	"SEK": "kr", "NOK": "kr", "DKK": "kr", "PLN": "zł", "CZK": "Kč", "HUF": "Ft",
	"RON": "lei", "TRY": "₺", "RUB": "₽", "INR": "₹", "BRL": "R$", "AUD": "A$",
	"CAD": "C$", "NZD": "NZ$", "MXN": "MX$", "ZAR": "R", "KRW": "₩", "THB": "฿",
}

// SupportedLanguages returns the list of supported ISO 639-1 language codes.
func SupportedLanguages() []string {
	return []string{"de", "en", "fr", "es", "it", "nl", "pt"}
}

// Get returns the locale for a language code, falling back to German
// if the code is unknown.
func Get(code string) *Locale {
	if loc, ok := locales[code]; ok {
		return loc
	}
	return locales["de"]
}

// Resolve picks the base locale for cfg's language and applies any
// formatting overrides on top of it. The returned Locale is an
// independent copy — mutating it never affects the shared base locale.
func Resolve(language string, overrides Formatting) *Locale {
	code := language
	if code == "" {
		code = "de"
	}
	base := Get(code)
	loc := *base // clone

	if overrides.DateFmt != "" {
		loc.DateFormat = overrides.DateFmt
	}
	if overrides.DecimalSep != "" {
		loc.DecimalSep = overrides.DecimalSep
	}
	if overrides.ThousandSep != "" {
		loc.ThousandSep = overrides.ThousandSep
	}
	if overrides.CurrencyBefore != nil {
		if *overrides.CurrencyBefore {
			loc.CurrencyPos = "before"
		} else {
			loc.CurrencyPos = "after"
		}
	}
	if overrides.CurrencySpace != nil {
		loc.CurrencySpace = *overrides.CurrencySpace
	}

	return &loc
}

// RegisterCurrencySymbol overrides (or adds) the display symbol for a
// currency code, e.g. to support a project-specific formatting override.
func RegisterCurrencySymbol(code, symbol string) {
	if code == "" || symbol == "" {
		return
	}
	currencySymbols[strings.ToUpper(strings.TrimSpace(code))] = symbol
}

// CurrencySymbol returns the display symbol for a currency code, falling
// back to the code itself (or "€" if empty) when the symbol is unknown.
func CurrencySymbol(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if sym, ok := currencySymbols[code]; ok {
		return sym
	}
	if code != "" {
		return code
	}
	return "€"
}

// FormatNumber formats a float with locale-specific separators.
func (l *Locale) FormatNumber(f float64, decimals int) string {
	neg := f < 0
	if neg {
		f = -f
	}
	mult := math.Pow(10, float64(decimals))
	rounded := math.Round(f*mult) / mult
	s := fmt.Sprintf("%.*f", decimals, rounded)

	parts := strings.SplitN(s, ".", 2)
	intPart := parts[0]

	if l.ThousandSep != "" && len(intPart) > 3 {
		var buf strings.Builder
		for i := 0; i < len(intPart); i++ {
			if i > 0 && (len(intPart)-i)%3 == 0 {
				buf.WriteString(l.ThousandSep)
			}
			buf.WriteByte(intPart[i])
		}
		intPart = buf.String()
	}

	result := intPart
	if decimals > 0 && len(parts) > 1 {
		result += l.DecimalSep + parts[1]
	}
	if neg {
		result = "-" + result
	}
	return result
}

// FormatCurrency formats a monetary amount with symbol in the correct position.
func (l *Locale) FormatCurrency(f float64, currencyCode string) string {
	sym := CurrencySymbol(currencyCode)
	num := l.FormatNumber(f, 2)

	if l.CurrencyPos == "before" {
		if l.CurrencySpace {
			return sym + " " + num
		}
		return sym + num
	}
	if l.CurrencySpace {
		return num + " " + sym
	}
	return num + sym
}

// FormatQuantity formats a quantity with auto-detected decimal places.
func (l *Locale) FormatQuantity(f float64) string {
	if f == math.Floor(f) {
		return l.FormatNumber(f, 0)
	}
	s := strings.TrimRight(fmt.Sprintf("%.4f", f), "0")
	parts := strings.SplitN(s, ".", 2)
	decimals := 1
	if len(parts) == 2 {
		decimals = len(parts[1])
	}
	return l.FormatNumber(f, decimals)
}

// ParseDate tries several common date formats.
func ParseDate(input string) (time.Time, error) {
	formats := []string{
		"02.01.2006", "2.1.2006", "2006-01-02",
		"01/02/2006", "1/2/2006", "02-01-2006",
	}
	input = strings.TrimSpace(input)
	for _, f := range formats {
		if t, err := time.Parse(f, input); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unknown date format: %s", input)
}

// FormatDate parses and reformats a date string. Unparseable input is
// returned unchanged so that malformed dates don't crash rendering.
func (l *Locale) FormatDate(input string) string {
	t, err := ParseDate(input)
	if err != nil {
		return input
	}
	return t.Format(l.DateFormat)
}

// ParseColor converts a hex color string ("#RRGGBB" or "RRGGBB") to RGB
// components. Falls back to the default accent color (#5B9BD5) for any
// malformed input (wrong length or non-hex characters).
func ParseColor(hex string) (int, int, int) {
	const defR, defG, defB = 91, 155, 213 // default: #5B9BD5

	hex = strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(hex) != 6 || !isHex(hex) {
		return defR, defG, defB
	}
	r, err1 := strconv.ParseUint(hex[0:2], 16, 8)
	g, err2 := strconv.ParseUint(hex[2:4], 16, 8)
	b, err3 := strconv.ParseUint(hex[4:6], 16, 8)
	if err1 != nil || err2 != nil || err3 != nil {
		return defR, defG, defB
	}
	return int(r), int(g), int(b)
}

// isHex reports whether every byte in s is an ASCII hex digit.
func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// DarkenColor produces a slightly darker shade of a hex color, used for
// header gradient/accent effects.
func DarkenColor(hex string) string {
	r, g, b := ParseColor(hex)
	const f = 0.82
	return fmt.Sprintf("#%02x%02x%02x", int(float64(r)*f), int(float64(g)*f), int(float64(b)*f))
}
