package invoicing

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/kiefer-networks/invoice-generator/internal/config"
	"github.com/kiefer-networks/invoice-generator/internal/render"
	"github.com/kiefer-networks/invoice-generator/internal/store"
	"math"
	"strings"
	"time"
)

// Snapshot is a detached value decoded from immutable persisted JSON. Exact
// totals and per-line tax/discount data are authoritative for document output.
type CorrectionReference struct{ OriginalID, OriginalNumber string }
type Snapshot struct {
	Kind            string
	Correction      CorrectionReference
	Draft           Draft
	Company         store.CompanyInput
	Language, Notes string
	TaxGroups       []TaxGroup
}
type FinalizedInvoice struct {
	ID, Number, State, CorrectionOf, CancellationReason string
	Sequence                                            int
	PaidAt                                              time.Time
	Snapshot                                            Snapshot
}
type FinalizationService struct{ store *store.Store }

func NewFinalizationService(s *store.Store) *FinalizationService {
	return &FinalizationService{store: s}
}

type Review struct {
	Key      string
	Snapshot Snapshot
}

func (s *FinalizationService) PrepareReview(ctx context.Context, id string, version int) (Review, error) {
	p, err := s.store.InvoiceRepository().PrepareReview(ctx, id, version)
	if err != nil {
		return Review{}, err
	}
	d, err := toDraft(p.Draft)
	if err != nil {
		return Review{}, err
	}
	return Review{Key: p.Key, Snapshot: invoiceSnapshot(d, p.Company.CompanyInput)}, nil
}
func (s *FinalizationService) Prepare(ctx context.Context, id string, version int) (string, error) {
	r, err := s.PrepareReview(ctx, id, version)
	return r.Key, err
}
func invoiceSnapshot(d Draft, c store.CompanyInput) Snapshot {
	language := d.Customer.PreferredLanguage
	if language == "" {
		language = c.DefaultLanguage
	}
	out := Snapshot{Draft: d, Company: c, Language: language, Notes: c.StandardNotes, TaxGroups: d.TaxGroups, Kind: "invoice"}
	if d.CorrectionOf != "" {
		out.Kind = "correction"
		out.Correction = CorrectionReference{OriginalID: d.CorrectionOf, OriginalNumber: d.CorrectionOfNumber}
	}
	return out
}
func (s *FinalizationService) Get(ctx context.Context, id string) (FinalizedInvoice, error) {
	f, err := s.store.InvoiceRepository().GetFinalized(ctx, id)
	if err != nil {
		return FinalizedInvoice{}, err
	}
	return decodeFinalized(f)
}
func decodeFinalized(f store.FrozenInvoice) (FinalizedInvoice, error) {
	out := FinalizedInvoice{ID: f.ID, Number: f.Number, State: f.State, CorrectionOf: f.CorrectionOf, Sequence: f.Sequence, CancellationReason: f.CancellationReason, PaidAt: f.PaidAt}
	err := json.Unmarshal([]byte(f.Snapshot), &out.Snapshot)
	return out, err
}
func (s *FinalizationService) Finalize(ctx context.Context, id, key string) (FinalizedInvoice, error) {
	f, err := s.store.InvoiceRepository().Finalize(ctx, id, key, func(d store.InvoiceDraft, c store.Company, number string, now time.Time) (string, store.InvoiceTotals, []store.InvoiceLine, error) {
		draft, err := toDraft(d)
		if err != nil {
			return "", store.InvoiceTotals{}, nil, err
		}
		if err = ValidateFinalization(draft, c.CompanyInput); err != nil {
			return "", store.InvoiceTotals{}, nil, err
		}
		totals, err := Calculate(linesForDraft(draft.Lines))
		if err != nil {
			return "", store.InvoiceTotals{}, nil, err
		}
		draft.Number = number
		draft.IssueDate = now
		draft.DueDate = time.Date(now.Year(), now.Month(), now.Day()+draft.Customer.PaymentTermsDays, 0, 0, 0, 0, time.UTC)
		for i := range draft.Lines {
			draft.Lines[i].NetMinor = totals.Lines[i].NetMinor
			draft.Lines[i].TaxMinor = totals.Lines[i].TaxMinor
			draft.Lines[i].GrossMinor = totals.Lines[i].GrossMinor
			d.Lines[i] = toStoreLine(draft.Lines[i])
		}
		snapshot := invoiceSnapshot(draft, c.CompanyInput)
		encoded, err := json.Marshal(snapshot)
		return string(encoded), store.InvoiceTotals{NetMinor: totals.NetMinor, TaxMinor: totals.TaxMinor, GrossMinor: totals.GrossMinor}, d.Lines, err
	})
	if err != nil {
		return FinalizedInvoice{}, err
	}
	return decodeFinalized(f)
}
func ValidateFinalization(d Draft, c store.CompanyInput) error {
	date, err := time.Parse("2006-01-02", d.ServiceDate)
	if err != nil || date.Format("2006-01-02") != d.ServiceDate {
		return &store.ValidationError{Field: "service_date", Message: "a valid service or delivery date is required before finalization"}
	}
	if err := store.ValidateInvoiceParties(c, d.Customer); err != nil {
		return err
	}
	for _, v := range []struct{ field, value string }{{"company.legal_name", c.LegalName}, {"company.address", c.AddressLine1}, {"company.postal_code", c.PostalCode}, {"company.city", c.City}, {"company.country", c.Country}, {"company.invoice_prefix", c.InvoicePrefix}, {"customer.name", d.Customer.DisplayName}, {"customer.address", d.Customer.AddressLine1}, {"customer.postal_code", d.Customer.PostalCode}, {"customer.city", d.Customer.City}, {"customer.country", d.Customer.Country}} {
		if strings.TrimSpace(v.value) == "" {
			return &store.ValidationError{Field: v.field, Message: "is required before finalization"}
		}
	}
	if c.TaxNumber == "" && c.VATIdentifier == "" {
		return &store.ValidationError{Field: "company.tax_number", Message: "a tax number or VAT identifier is required"}
	}
	if d.Currency != c.Currency || d.Currency != d.Customer.Currency {
		return &store.ValidationError{Field: "currency", Message: "must match company and customer"}
	}
	if d.Customer.PaymentTermsDays < 0 || d.Customer.PaymentTermsDays > 365 {
		return &store.ValidationError{Field: "payment_terms", Message: "must be between 0 and 365 days"}
	}
	if len(d.Lines) == 0 {
		return &store.ValidationError{Field: "lines", Message: "at least one position is required"}
	}
	for _, l := range d.Lines {
		if strings.TrimSpace(l.Title) == "" || strings.TrimSpace(l.Unit) == "" {
			return &store.ValidationError{Field: "lines", Message: "title and unit are required"}
		}
	}
	_, err = Calculate(linesForDraft(d.Lines))
	return err
}

// RenderData preserves exact integer monetary formatting, including mixed VAT
// and discounts which the legacy Config calculator cannot represent.
func (s Snapshot) RenderData() *render.TplData {
	p := PreviewData(s.Draft, s.Company)
	p.InvNumber = s.Draft.Number
	p.ServiceDate = s.Draft.ServiceDate
	p.CustDisplayName = s.Draft.Customer.DisplayName
	if s.Draft.Customer.LegalName != "" {
		p.CustName = s.Draft.Customer.LegalName
	}
	p.CustContact = s.Draft.Customer.ContactName
	p.CustEmail = s.Draft.Customer.Email
	p.CustVATID = s.Draft.Customer.VATIdentifier
	p.DocumentKind = s.Kind
	p.CorrectionOf = s.Correction.OriginalID
	p.CorrectionOfNumber = s.Correction.OriginalNumber
	p.Notes = s.Notes
	p.CompanyContact = s.Company.ContactName
	p.TaxID = s.Company.VATIdentifier
	if p.TaxID == "" {
		p.TaxID = s.Company.TaxNumber
	}
	p.HasPay = true
	p.PayTerms = fmt.Sprintf("%d days", s.Draft.Customer.PaymentTermsDays)
	p.HasBank = s.Company.IBAN != ""
	p.BankName = s.Company.BankName
	p.BankIBAN = s.Company.IBAN
	p.BankBIC = s.Company.BIC
	p.Title = "Invoice"
	if s.Kind == "correction" {
		p.Title = "Correction invoice"
	}
	p.Status = "FINALIZED"
	p.StatusClass = "finalized"
	return p
}

// Config adapts the legacy boundary only when it can faithfully represent the
// invoice. Document generation must use RenderData for mixed tax/discount data.
func (s Snapshot) Config() (config.Config, error) {
	c := config.Config{Language: s.Language, Color: s.Company.BrandColor, Logo: s.Company.LogoKey, Currency: s.Draft.Currency, Notes: s.Notes, PayTerms: fmt.Sprintf("%d days", s.Draft.Customer.PaymentTermsDays)}
	c.Company = config.Company{Name: s.Company.LegalName, Address: strings.TrimSpace(s.Company.AddressLine1 + "\n" + s.Company.AddressLine2), ZIP: s.Company.PostalCode, City: s.Company.City, Country: s.Company.Country, Phone: s.Company.Phone, Email: s.Company.Email, TaxNumber: s.Company.TaxNumber, VatID: s.Company.VATIdentifier, Bank: config.BankInfo{Name: s.Company.BankName, IBAN: s.Company.IBAN, BIC: s.Company.BIC}}
	name := s.Draft.Customer.LegalName
	if name == "" {
		name = s.Draft.Customer.DisplayName
	}
	c.Customer = config.Customer{DisplayName: s.Draft.Customer.DisplayName, Name: name, Contact: s.Draft.Customer.ContactName, Email: s.Draft.Customer.Email, Address: strings.TrimSpace(s.Draft.Customer.AddressLine1 + "\n" + s.Draft.Customer.AddressLine2), ZIP: s.Draft.Customer.PostalCode, City: s.Draft.Customer.City, Country: s.Draft.Customer.Country, VatID: s.Draft.Customer.VATIdentifier}
	c.Invoice = config.InvInfo{ServiceDate: s.Draft.ServiceDate, Kind: s.Kind, CorrectionOf: s.Correction.OriginalID, CorrectionOfNumber: s.Correction.OriginalNumber, Number: s.Draft.Number, Date: s.Draft.IssueDate.Format("2006-01-02"), DueDate: s.Draft.DueDate.Format("2006-01-02"), Status: "finalized"}
	var rate int64
	if len(s.Draft.Lines) > 0 {
		rate = s.Draft.Lines[0].TaxRateBasisPoints
	}
	for _, l := range s.Draft.Lines {
		if l.DiscountBasisPoints != 0 || l.TaxRateBasisPoints != rate || l.UnitPriceMinor > 1<<53 || l.QuantityScaled > 1<<53 {
			return config.Config{}, fmt.Errorf("invoice requires exact snapshot rendering")
		}
		c.Items = append(c.Items, config.Item{Description: l.Title, Details: l.Description, Quantity: float64(l.QuantityScaled) / 10000, Unit: l.Unit, Price: float64(l.UnitPriceMinor) / 100})
	}
	c.VAT = config.VATConfig{Liable: rate > 0, Rate: float64(rate) / 100}
	if s.Draft.NetMinor > 1<<53 || s.Draft.TaxMinor > 1<<53 || s.Draft.GrossMinor > 1<<53 || int64(math.Round(config.CalcNet(c.Items)*100)) != s.Draft.NetMinor || int64(math.Round(config.CalcTax(config.CalcNet(c.Items), c.VAT)*100)) != s.Draft.TaxMinor {
		return config.Config{}, fmt.Errorf("invoice requires exact snapshot rendering")
	}
	return c, nil
}
