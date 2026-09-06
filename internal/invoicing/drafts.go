package invoicing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kiefer-networks/invoice-generator/internal/store"
)

// Draft is the editable invoice view. All totals are calculated server side.
type Draft struct {
	ID, CustomerID, Number, Currency string
	Customer                         store.CustomerInput
	DueDate                          time.Time
	Version                          int
	Lines                            []DraftLine
	NetMinor, TaxMinor, GrossMinor   int64
	TaxGroups                        []TaxGroup
}
type DraftLine struct {
	ID, CatalogItemID, Title, Description, Unit string
	Position                                    int
	QuantityScaled, UnitPriceMinor              int64
	DiscountBasisPoints, TaxRateBasisPoints     int64
	NetMinor, TaxMinor, GrossMinor              int64
}
type DraftLineInput struct {
	Title, Description, Unit                string
	QuantityScaled, UnitPriceMinor          int64
	DiscountBasisPoints, TaxRateBasisPoints int64
}
type DraftService struct{ store *store.Store }

func NewDraftService(s *store.Store) *DraftService { return &DraftService{store: s} }

func (s *DraftService) Create(ctx context.Context, customerID string, now time.Time) (Draft, error) {
	if s == nil || s.store == nil {
		return Draft{}, errors.New("draft storage is unavailable")
	}
	customer, err := s.store.CustomerRepository().Get(ctx, customerID)
	if err != nil {
		return Draft{}, err
	}
	if !customer.Active {
		return Draft{}, &store.ValidationError{Field: "customer", Message: "must be active"}
	}
	due := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day()+customer.PaymentTermsDays, 0, 0, 0, 0, time.UTC)
	draft, err := s.store.InvoiceRepository().CreateDraft(ctx, store.InvoiceDraftInput{CustomerID: customer.ID, Currency: customer.Currency, Customer: customer.CustomerInput, DueDate: due})
	if err != nil {
		return Draft{}, err
	}
	return toDraft(draft)
}
func (s *DraftService) Get(ctx context.Context, id string) (Draft, error) {
	if s == nil || s.store == nil {
		return Draft{}, errors.New("draft storage is unavailable")
	}
	d, err := s.store.InvoiceRepository().GetDraft(ctx, id)
	if err != nil {
		return Draft{}, err
	}
	return toDraft(d)
}

// Update changes a draft customer snapshot after selecting an active customer.
func (s *DraftService) Update(ctx context.Context, id string, version int, customerID string) (Draft, error) {
	current, err := s.Get(ctx, id)
	if err != nil {
		return Draft{}, err
	}
	customer, err := s.store.CustomerRepository().Get(ctx, customerID)
	if err != nil {
		return Draft{}, err
	}
	if !customer.Active {
		return Draft{}, &store.ValidationError{Field: "customer", Message: "must be active"}
	}
	if customer.Currency != current.Currency && len(current.Lines) > 0 {
		return Draft{}, &store.ValidationError{Field: "customer", Message: "currency must match existing positions"}
	}
	due := time.Date(time.Now().UTC().Year(), time.Now().UTC().Month(), time.Now().UTC().Day()+customer.PaymentTermsDays, 0, 0, 0, 0, time.UTC)
	totals, err := Calculate(linesForDraft(current.Lines))
	if err != nil {
		return Draft{}, err
	}
	updated, err := s.store.InvoiceRepository().UpdateDraft(ctx, id, version, store.InvoiceDraftInput{CustomerID: customer.ID, Currency: customer.Currency, Customer: customer.CustomerInput, DueDate: due}, store.InvoiceTotals{NetMinor: totals.NetMinor, TaxMinor: totals.TaxMinor, GrossMinor: totals.GrossMinor})
	if err != nil {
		return Draft{}, err
	}
	return toDraft(updated)
}
func (s *DraftService) AddCatalogItem(ctx context.Context, id string, version int, catalogID string, quantityScaled int64) (Draft, error) {
	item, err := s.store.CatalogRepository().Get(ctx, catalogID)
	if err != nil {
		return Draft{}, err
	}
	current, err := s.Get(ctx, id)
	if err != nil {
		return Draft{}, err
	}
	if !item.Active {
		return Draft{}, &store.ValidationError{Field: "catalog_item", Message: "must be active"}
	}
	company, companyErr := s.store.CompanyRepository().Get(ctx)
	if companyErr != nil && !errors.Is(companyErr, store.ErrNotFound) {
		return Draft{}, companyErr
	}
	if companyErr == nil && company.Currency != current.Currency {
		return Draft{}, &store.ValidationError{Field: "catalog_item", Message: "uses the company currency " + company.Currency + ", which does not match this draft"}
	}
	line := DraftLine{CatalogItemID: item.ID, Title: item.Title, Description: item.Description, Unit: item.Unit, QuantityScaled: quantityScaled, UnitPriceMinor: int64(item.UnitPriceMinor), TaxRateBasisPoints: int64(item.TaxRateBasisPoints)}
	lines := append(append([]DraftLine{}, current.Lines...), line)
	totals, err := Calculate(linesForDraft(lines))
	if err != nil {
		return Draft{}, err
	}
	lineTotal := totals.Lines[len(totals.Lines)-1]
	line.NetMinor, line.TaxMinor, line.GrossMinor = lineTotal.NetMinor, lineTotal.TaxMinor, lineTotal.GrossMinor
	updated, err := s.store.InvoiceRepository().AddLine(ctx, id, version, toStoreLine(line), store.InvoiceTotals{NetMinor: totals.NetMinor, TaxMinor: totals.TaxMinor, GrossMinor: totals.GrossMinor})
	if err != nil {
		return Draft{}, err
	}
	return toDraft(updated)
}
func (s *DraftService) AddManualLine(ctx context.Context, id string, version int, input DraftLineInput) (Draft, error) {
	current, err := s.Get(ctx, id)
	if err != nil {
		return Draft{}, err
	}
	line := DraftLine{Title: input.Title, Description: input.Description, Unit: input.Unit, QuantityScaled: input.QuantityScaled, UnitPriceMinor: input.UnitPriceMinor, DiscountBasisPoints: input.DiscountBasisPoints, TaxRateBasisPoints: input.TaxRateBasisPoints}
	lines := append(append([]DraftLine{}, current.Lines...), line)
	totals, err := Calculate(linesForDraft(lines))
	if err != nil {
		return Draft{}, err
	}
	line.NetMinor, line.TaxMinor, line.GrossMinor = totals.Lines[len(totals.Lines)-1].NetMinor, totals.Lines[len(totals.Lines)-1].TaxMinor, totals.Lines[len(totals.Lines)-1].GrossMinor
	updated, err := s.store.InvoiceRepository().AddLine(ctx, id, version, toStoreLine(line), store.InvoiceTotals{NetMinor: totals.NetMinor, TaxMinor: totals.TaxMinor, GrossMinor: totals.GrossMinor})
	if err != nil {
		return Draft{}, err
	}
	return toDraft(updated)
}
func (s *DraftService) UpdateLine(ctx context.Context, id string, version int, lineID string, input DraftLineInput) (Draft, error) {
	current, err := s.Get(ctx, id)
	if err != nil {
		return Draft{}, err
	}
	found := false
	for i := range current.Lines {
		if current.Lines[i].ID == lineID {
			current.Lines[i].Title = input.Title
			current.Lines[i].Description = input.Description
			current.Lines[i].Unit = input.Unit
			current.Lines[i].QuantityScaled = input.QuantityScaled
			current.Lines[i].UnitPriceMinor = input.UnitPriceMinor
			current.Lines[i].DiscountBasisPoints = input.DiscountBasisPoints
			current.Lines[i].TaxRateBasisPoints = input.TaxRateBasisPoints
			found = true
		}
	}
	if !found {
		return Draft{}, store.ErrNotFound
	}
	totals, err := Calculate(linesForDraft(current.Lines))
	if err != nil {
		return Draft{}, err
	}
	var line DraftLine
	for i := range current.Lines {
		current.Lines[i].NetMinor = totals.Lines[i].NetMinor
		current.Lines[i].TaxMinor = totals.Lines[i].TaxMinor
		current.Lines[i].GrossMinor = totals.Lines[i].GrossMinor
		if current.Lines[i].ID == lineID {
			line = current.Lines[i]
		}
	}
	updated, err := s.store.InvoiceRepository().UpdateLine(ctx, id, version, toStoreLine(line), store.InvoiceTotals{NetMinor: totals.NetMinor, TaxMinor: totals.TaxMinor, GrossMinor: totals.GrossMinor})
	if err != nil {
		return Draft{}, err
	}
	return toDraft(updated)
}
func (s *DraftService) RemoveLine(ctx context.Context, id string, version int, lineID string) (Draft, error) {
	current, err := s.Get(ctx, id)
	if err != nil {
		return Draft{}, err
	}
	var lines []DraftLine
	found := false
	for _, line := range current.Lines {
		if line.ID == lineID {
			found = true
			continue
		}
		lines = append(lines, line)
	}
	if !found {
		return Draft{}, store.ErrNotFound
	}
	totals, err := Calculate(linesForDraft(lines))
	if err != nil {
		return Draft{}, err
	}
	updated, err := s.store.InvoiceRepository().RemoveLine(ctx, id, version, lineID, store.InvoiceTotals{NetMinor: totals.NetMinor, TaxMinor: totals.TaxMinor, GrossMinor: totals.GrossMinor})
	if err != nil {
		return Draft{}, err
	}
	return toDraft(updated)
}
func (s *DraftService) ReorderLines(ctx context.Context, id string, version int, lineIDs []string) (Draft, error) {
	current, err := s.Get(ctx, id)
	if err != nil {
		return Draft{}, err
	}
	if len(lineIDs) != len(current.Lines) {
		return Draft{}, &store.ValidationError{Field: "lines", Message: "do not match draft"}
	}
	totals, err := Calculate(linesForDraft(current.Lines))
	if err != nil {
		return Draft{}, err
	}
	updated, err := s.store.InvoiceRepository().ReorderLines(ctx, id, version, lineIDs, store.InvoiceTotals{NetMinor: totals.NetMinor, TaxMinor: totals.TaxMinor, GrossMinor: totals.GrossMinor})
	if err != nil {
		return Draft{}, err
	}
	return toDraft(updated)
}
func linesForDraft(lines []DraftLine) []Line {
	result := make([]Line, len(lines))
	for i, line := range lines {
		result[i] = Line{QuantityScaled: line.QuantityScaled, UnitPriceMinor: line.UnitPriceMinor, DiscountBasisPoints: line.DiscountBasisPoints, TaxRateBasisPoints: line.TaxRateBasisPoints}
	}
	return result
}
func toStoreLine(line DraftLine) store.InvoiceLine {
	return store.InvoiceLine{ID: line.ID, CatalogItemID: line.CatalogItemID, Title: line.Title, Description: line.Description, Unit: line.Unit, Position: line.Position, QuantityScaled: line.QuantityScaled, UnitPriceMinor: line.UnitPriceMinor, DiscountBasisPoints: line.DiscountBasisPoints, TaxRateBasisPoints: line.TaxRateBasisPoints, NetMinor: line.NetMinor, TaxMinor: line.TaxMinor, GrossMinor: line.GrossMinor}
}
func toDraft(input store.InvoiceDraft) (Draft, error) {
	lines := make([]DraftLine, len(input.Lines))
	for i, line := range input.Lines {
		lines[i] = DraftLine{ID: line.ID, CatalogItemID: line.CatalogItemID, Title: line.Title, Description: line.Description, Unit: line.Unit, Position: line.Position, QuantityScaled: line.QuantityScaled, UnitPriceMinor: line.UnitPriceMinor, DiscountBasisPoints: line.DiscountBasisPoints, TaxRateBasisPoints: line.TaxRateBasisPoints, NetMinor: line.NetMinor, TaxMinor: line.TaxMinor, GrossMinor: line.GrossMinor}
	}
	totals, err := Calculate(linesForDraft(lines))
	if err != nil {
		return Draft{}, fmt.Errorf("calculate invoice draft: %w", err)
	}
	return Draft{ID: input.ID, CustomerID: input.CustomerID, Number: input.Number, Currency: input.Currency, Customer: input.Customer, DueDate: input.DueDate, Version: input.Version, Lines: lines, NetMinor: totals.NetMinor, TaxMinor: totals.TaxMinor, GrossMinor: totals.GrossMinor, TaxGroups: totals.TaxGroups}, nil
}
