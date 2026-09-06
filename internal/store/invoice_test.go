package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestDraftRepositoryLinesUseTransactionsAndVersions(t *testing.T) {
	t.Parallel()
	s := openMigratedStore(t)
	ctx := context.Background()
	customer, err := s.CustomerRepository().Create(ctx, validCustomer("C-001", "Acme"))
	if err != nil {
		t.Fatal(err)
	}
	draft, err := s.InvoiceRepository().CreateDraft(ctx, InvoiceDraftInput{CustomerID: customer.ID, Currency: "EUR", Customer: customer.CustomerInput, DueDate: time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if draft.Number != "" || draft.Version != 1 {
		t.Fatalf("draft=%#v", draft)
	}
	line := InvoiceLine{Title: "Advice", Unit: "hour", QuantityScaled: 10000, UnitPriceMinor: 100, TaxRateBasisPoints: 1900, NetMinor: 100, TaxMinor: 19, GrossMinor: 119}
	draft, err = s.InvoiceRepository().AddLine(ctx, draft.ID, draft.Version, line, InvoiceTotals{NetMinor: 100, TaxMinor: 19, GrossMinor: 119})
	if err != nil || len(draft.Lines) != 1 || draft.Version != 2 {
		t.Fatalf("add=%#v err=%v", draft, err)
	}
	if _, err = s.InvoiceRepository().RemoveLine(ctx, draft.ID, 1, draft.Lines[0].ID, InvoiceTotals{}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale=%v", err)
	}
	draft, err = s.InvoiceRepository().RemoveLine(ctx, draft.ID, draft.Version, draft.Lines[0].ID, InvoiceTotals{})
	if err != nil || len(draft.Lines) != 0 || draft.NetMinor != 0 {
		t.Fatalf("remove=%#v err=%v", draft, err)
	}
}

func TestDraftRepositoryRejectsDuplicateAndForeignReorderIDs(t *testing.T) {
	t.Parallel()
	s := openMigratedStore(t)
	ctx := context.Background()
	customer, err := s.CustomerRepository().Create(ctx, validCustomer("C-001", "Acme"))
	if err != nil {
		t.Fatal(err)
	}
	repo := s.InvoiceRepository()
	draft, err := repo.CreateDraft(ctx, InvoiceDraftInput{CustomerID: customer.ID, Currency: "EUR", Customer: customer.CustomerInput, DueDate: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		draft, err = repo.AddLine(ctx, draft.ID, draft.Version, InvoiceLine{Title: fmt.Sprintf("L%d", i), Unit: "piece", QuantityScaled: 10000, UnitPriceMinor: 100, NetMinor: 100, GrossMinor: 100}, InvoiceTotals{NetMinor: int64((i + 1) * 100), GrossMinor: int64((i + 1) * 100)})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, ids := range [][]string{{draft.Lines[0].ID, draft.Lines[0].ID}, {draft.Lines[0].ID, "foreign"}} {
		if _, err = repo.ReorderLines(ctx, draft.ID, draft.Version, ids, InvoiceTotals{NetMinor: 200, GrossMinor: 200}); !IsValidationError(err) {
			t.Fatalf("ids=%v err=%v", ids, err)
		}
	}
}
