package invoicing

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/kiefer-networks/invoice-generator/internal/store"
)

func TestDraftServiceCreatesSnapshotsAndDoesNotAllocateNumber(t *testing.T) {
	t.Parallel()
	s := draftStore(t)
	ctx := context.Background()
	customer, err := s.CustomerRepository().Create(ctx, store.CustomerInput{Number: "C-001", DisplayName: "Acme", Country: "DE", PreferredLanguage: "de", Currency: "EUR", PaymentTermsDays: 14})
	if err != nil {
		t.Fatal(err)
	}
	service := NewDraftService(s)
	draft, err := service.Create(ctx, customer.ID, time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if draft.Number != "" || draft.Customer.DisplayName != "Acme" || draft.Currency != "EUR" || !draft.DueDate.Equal(time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("draft = %#v", draft)
	}
	updated, err := s.CustomerRepository().Update(ctx, customer.ID, customer.Version, store.CustomerInput{Number: "C-001", DisplayName: "Changed", Country: "DE", PreferredLanguage: "de", Currency: "USD", PaymentTermsDays: 1})
	if err != nil {
		t.Fatal(err)
	}
	_ = updated
	found, err := service.Get(ctx, draft.ID)
	if err != nil || found.Customer.DisplayName != "Acme" || found.Currency != "EUR" {
		t.Fatalf("snapshot = %#v, %v", found, err)
	}
	if _, err = s.CustomerRepository().Archive(ctx, customer.ID, updated.Version); err != nil {
		t.Fatal(err)
	}
	found, err = service.Get(ctx, draft.ID)
	if err != nil || found.Customer.DisplayName != "Acme" {
		t.Fatalf("archived customer draft = %#v, %v", found, err)
	}
}

func TestDraftServiceCopiesCatalogAndUsesOptimisticVersions(t *testing.T) {
	t.Parallel()
	s := draftStore(t)
	ctx := context.Background()
	customer, err := s.CustomerRepository().Create(ctx, store.CustomerInput{Number: "C-001", DisplayName: "Acme", Country: "DE", PreferredLanguage: "de", Currency: "EUR", PaymentTermsDays: 14})
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.CatalogRepository().Create(ctx, store.CatalogInput{Number: "S-001", Kind: "service", Title: "Advice", Description: "First", Unit: "hour", UnitPriceMinor: 1250, TaxRateBasisPoints: 1900})
	if err != nil {
		t.Fatal(err)
	}
	service := NewDraftService(s)
	draft, err := service.Create(ctx, customer.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	draft, err = service.AddCatalogItem(ctx, draft.ID, draft.Version, item.ID, 20000)
	if err != nil {
		t.Fatal(err)
	}
	if len(draft.Lines) != 1 || draft.Lines[0].Title != "Advice" || draft.Lines[0].NetMinor != 2500 || draft.GrossMinor != 2975 {
		t.Fatalf("draft = %#v", draft)
	}
	if _, err = s.CatalogRepository().Update(ctx, item.ID, item.Version, store.CatalogInput{Number: item.Number, Kind: item.Kind, Title: "Changed", Unit: item.Unit, UnitPriceMinor: 1, TaxRateBasisPoints: 0}); err != nil {
		t.Fatal(err)
	}
	found, err := service.Get(ctx, draft.ID)
	if err != nil || found.Lines[0].Title != "Advice" {
		t.Fatalf("catalog snapshot = %#v, %v", found, err)
	}
	if _, err = service.RemoveLine(ctx, draft.ID, draft.Version-1, draft.Lines[0].ID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale error = %v", err)
	}
}

func TestDraftServiceRejectsArchivedCustomerAndInvalidLineWithoutWriting(t *testing.T) {
	t.Parallel()
	s := draftStore(t)
	ctx := context.Background()
	customer, err := s.CustomerRepository().Create(ctx, store.CustomerInput{Number: "C-001", DisplayName: "Acme", Country: "DE", PreferredLanguage: "de", Currency: "EUR", PaymentTermsDays: 14})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CustomerRepository().Archive(ctx, customer.ID, customer.Version); err != nil {
		t.Fatal(err)
	}
	service := NewDraftService(s)
	if _, err = service.Create(ctx, customer.ID, time.Now().UTC()); !store.IsValidationError(err) {
		t.Fatalf("archived customer = %v", err)
	}
}

func draftStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "invoices.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
