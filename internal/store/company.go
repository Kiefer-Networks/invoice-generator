package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"
)

var (
	ErrNotFound   = errors.New("record not found")
	ErrConflict   = errors.New("record changed by another request")
	ErrDuplicate  = errors.New("duplicate record")
	ErrValidation = errors.New("invalid input")
	colorPattern  = regexp.MustCompile(`^#[0-9A-F]{6}$`)
)

// ValidationError identifies the submitted field without exposing database errors.
type ValidationError struct{ Field, Message string }

func (e *ValidationError) Error() string { return e.Field + ": " + e.Message }
func (e *ValidationError) Unwrap() error { return ErrValidation }
func IsValidationError(err error) bool   { return errors.Is(err, ErrValidation) }

// CompanyInput is the editable singleton profile.
type CompanyInput struct {
	LegalName, ContactName, Email, Phone                  string
	AddressLine1, AddressLine2, PostalCode, City, Country string
	TaxNumber, VATIdentifier                              string
	BankName, IBAN, BIC, LogoKey, BrandColor              string
	DefaultLanguage, Currency                             string
	PaymentTermsDays                                      int
	InvoicePrefix, StandardNotes                          string
}

// Company is the stored company profile.
type Company struct {
	ID string
	CompanyInput
	NextInvoiceSequence  int
	CreatedAt, UpdatedAt time.Time
}

// CompanyRepository owns the one active company profile.
type CompanyRepository struct{ store *Store }

func (s *Store) CompanyRepository() *CompanyRepository { return &CompanyRepository{store: s} }

func (r *CompanyRepository) Get(ctx context.Context) (Company, error) {
	row := r.store.db.QueryRowContext(ctx, `SELECT id, legal_name, contact_name, email, phone, address_line1, address_line2, postal_code, city, country, tax_number, vat_identifier, bank_name, iban, bic, logo_key, brand_color, default_language, currency, payment_terms_days, invoice_prefix, next_invoice_sequence, standard_notes, created_at, updated_at FROM companies WHERE singleton = 1 AND active = 1`)
	company, err := scanCompany(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Company{}, ErrNotFound
	}
	if err != nil {
		return Company{}, fmt.Errorf("get company: %w", err)
	}
	return company, nil
}

func (r *CompanyRepository) Save(ctx context.Context, input CompanyInput) (Company, error) {
	normalized, err := normalizeCompany(input)
	if err != nil {
		return Company{}, err
	}
	id, err := newBusinessID()
	if err != nil {
		return Company{}, err
	}
	_, err = r.store.db.ExecContext(ctx, `INSERT INTO companies (id, singleton, legal_name, contact_name, email, phone, address_line1, address_line2, postal_code, city, country, tax_number, vat_identifier, bank_name, iban, bic, logo_key, brand_color, default_language, currency, payment_terms_days, invoice_prefix, standard_notes) VALUES (?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(singleton) DO UPDATE SET legal_name=excluded.legal_name, contact_name=excluded.contact_name, email=excluded.email, phone=excluded.phone, address_line1=excluded.address_line1, address_line2=excluded.address_line2, postal_code=excluded.postal_code, city=excluded.city, country=excluded.country, tax_number=excluded.tax_number, vat_identifier=excluded.vat_identifier, bank_name=excluded.bank_name, iban=excluded.iban, bic=excluded.bic, logo_key=excluded.logo_key, brand_color=excluded.brand_color, default_language=excluded.default_language, currency=excluded.currency, payment_terms_days=excluded.payment_terms_days, invoice_prefix=excluded.invoice_prefix, standard_notes=excluded.standard_notes, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')`,
		id, normalized.LegalName, normalized.ContactName, normalized.Email, normalized.Phone, normalized.AddressLine1, normalized.AddressLine2, normalized.PostalCode, normalized.City, normalized.Country, normalized.TaxNumber, normalized.VATIdentifier, normalized.BankName, normalized.IBAN, normalized.BIC, normalized.LogoKey, normalized.BrandColor, normalized.DefaultLanguage, normalized.Currency, normalized.PaymentTermsDays, normalized.InvoicePrefix, normalized.StandardNotes)
	if err != nil {
		return Company{}, fmt.Errorf("save company: %w", err)
	}
	return r.Get(ctx)
}

func scanCompany(row *sql.Row) (Company, error) {
	var company Company
	var created, updated string
	err := row.Scan(&company.ID, &company.LegalName, &company.ContactName, &company.Email, &company.Phone, &company.AddressLine1, &company.AddressLine2, &company.PostalCode, &company.City, &company.Country, &company.TaxNumber, &company.VATIdentifier, &company.BankName, &company.IBAN, &company.BIC, &company.LogoKey, &company.BrandColor, &company.DefaultLanguage, &company.Currency, &company.PaymentTermsDays, &company.InvoicePrefix, &company.NextInvoiceSequence, &company.StandardNotes, &created, &updated)
	if err == nil {
		company.CreatedAt = parseBusinessTime(created)
		company.UpdatedAt = parseBusinessTime(updated)
	}
	return company, err
}

func normalizeCompany(in CompanyInput) (CompanyInput, error) {
	in.LegalName = clean(in.LegalName)
	in.ContactName, in.Phone = clean(in.ContactName), clean(in.Phone)
	in.AddressLine1, in.AddressLine2, in.PostalCode, in.City = clean(in.AddressLine1), clean(in.AddressLine2), clean(in.PostalCode), clean(in.City)
	in.TaxNumber, in.VATIdentifier, in.BankName, in.IBAN, in.BIC, in.LogoKey = clean(in.TaxNumber), clean(in.VATIdentifier), clean(in.BankName), clean(in.IBAN), clean(in.BIC), clean(in.LogoKey)
	in.InvoicePrefix, in.StandardNotes = clean(in.InvoicePrefix), strings.TrimSpace(in.StandardNotes)
	in.Email = strings.ToLower(clean(in.Email))
	in.Country, in.Currency = strings.ToUpper(clean(in.Country)), strings.ToUpper(clean(in.Currency))
	in.DefaultLanguage = strings.ToLower(clean(in.DefaultLanguage))
	in.BrandColor = strings.ToUpper(clean(in.BrandColor))
	if err := required(in.LegalName, "legal_name", 200); err != nil {
		return in, err
	}
	if err := optional(in.Email, "email", 254); err != nil {
		return in, err
	}
	if in.Email != "" {
		if _, err := mail.ParseAddress(in.Email); err != nil {
			return in, fieldError("email", "must be a valid address")
		}
	}
	if err := countryCode(in.Country); err != nil {
		return in, err
	}
	if err := currencyCode(in.Currency); err != nil {
		return in, err
	}
	if in.DefaultLanguage != "de" && in.DefaultLanguage != "en" {
		return in, fieldError("default_language", "must be de or en")
	}
	if !colorPattern.MatchString(in.BrandColor) {
		return in, fieldError("brand_color", "must be a #RRGGBB color")
	}
	if in.PaymentTermsDays < 0 || in.PaymentTermsDays > 365 {
		return in, fieldError("payment_terms_days", "must be between 0 and 365")
	}
	for _, value := range []struct {
		value, field string
		limit        int
	}{{in.ContactName, "contact_name", 200}, {in.Phone, "phone", 64}, {in.AddressLine1, "address_line1", 250}, {in.AddressLine2, "address_line2", 250}, {in.PostalCode, "postal_code", 32}, {in.City, "city", 100}, {in.TaxNumber, "tax_number", 64}, {in.VATIdentifier, "vat_identifier", 64}, {in.BankName, "bank_name", 200}, {in.IBAN, "iban", 64}, {in.BIC, "bic", 32}, {in.LogoKey, "logo_key", 255}, {in.InvoicePrefix, "invoice_prefix", 32}, {in.StandardNotes, "standard_notes", 5000}} {
		if err := optional(value.value, value.field, value.limit); err != nil {
			return in, err
		}
	}
	return in, nil
}

func clean(value string) string              { return strings.TrimSpace(value) }
func fieldError(field, message string) error { return &ValidationError{Field: field, Message: message} }
func required(value, field string, max int) error {
	if value == "" {
		return fieldError(field, "is required")
	}
	return optional(value, field, max)
}
func optional(value, field string, max int) error {
	if len(value) > max {
		return fieldError(field, fmt.Sprintf("must be at most %d characters", max))
	}
	return nil
}
func countryCode(value string) error {
	if len(value) != 2 || !supportedCountries[value] {
		return fieldError("country", "must be a supported ISO 3166-1 alpha-2 code")
	}
	return nil
}

func currencyCode(value string) error {
	if len(value) != 3 || !supportedCurrencies[value] {
		return fieldError("currency", "must be EUR, USD, GBP, or CHF (two decimal places)")
	}
	return nil
}

func codeSet(values string) map[string]bool {
	set := make(map[string]bool)
	for _, value := range strings.Fields(values) {
		set[value] = true
	}
	return set
}

var supportedCountries = codeSet(`AD AE AF AG AI AL AM AO AQ AR AS AT AU AW AX AZ BA BB BD BE BF BG BH BI BJ BL BM BN BO BQ BR BS BT BW BY BZ CA CC CD CF CG CH CI CK CL CM CN CO CR CU CV CW CX CY CZ DE DJ DK DM DO DZ EC EE EG EH ER ES ET FI FJ FK FM FO FR GA GB GD GE GF GG GH GI GL GM GN GP GQ GR GS GT GU GW GY HK HM HN HR HT HU ID IE IL IM IN IO IQ IR IS IT JE JM JO JP KE KG KH KI KM KN KP KR KW KY KZ LA LB LC LI LK LR LS LT LU LV LY MA MC MD ME MG MH MK ML MM MN MO MP MQ MR MS MT MU MV MW MX MY MZ NA NC NE NF NG NI NL NO NP NR NU NZ OM PA PE PF PG PH PK PL PM PN PR PS PT PW PY QA RE RO RS RU RW SA SB SC SD SE SG SH SI SJ SK SL SM SN SO SR SS ST SV SX SY SZ TC TD TF TG TH TJ TK TL TM TN TO TR TT TV TW TZ UA UG UM US UY UZ VA VC VE VG VI VN VU WF WS YE YT ZA ZM ZW`)

// These currencies have the two minor-unit decimals implemented end to end.
var supportedCurrencies = codeSet(`EUR USD GBP CHF`)

func newBusinessID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate identifier: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func parseBusinessTime(value string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, value)
	return t.UTC()
}
