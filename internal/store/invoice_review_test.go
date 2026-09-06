package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func populatedDraft(t *testing.T) (*Store, *InvoiceRepository, InvoiceDraft) {
	t.Helper()
	s := openMigratedStore(t)
	repo := s.InvoiceRepository()
	ctx := context.Background()
	c, e := s.CustomerRepository().Create(ctx, validCustomer("C", "Acme"))
	if e != nil {
		t.Fatal(e)
	}
	d, e := repo.CreateDraft(ctx, InvoiceDraftInput{CustomerID: c.ID, Currency: "EUR", Customer: c.CustomerInput, DueDate: time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)})
	if e != nil {
		t.Fatal(e)
	}
	for _, title := range []string{"A", "B", "C", "D"} {
		d, e = repo.AddLine(ctx, d.ID, d.Version, InvoiceLine{Title: title, Unit: "hour", QuantityScaled: 10000, UnitPriceMinor: 100, TaxRateBasisPoints: 1900, NetMinor: 100, TaxMinor: 19, GrossMinor: 119}, InvoiceTotals{NetMinor: d.NetMinor + 100, TaxMinor: d.TaxMinor + 19, GrossMinor: d.GrossMinor + 119})
		if e != nil {
			t.Fatal(e)
		}
	}
	return s, repo, d
}

func TestDraftRepositoryRemoveAfterReorderCompactsExactPositions(t *testing.T) {
	_, repo, d := populatedDraft(t)
	ctx := context.Background()
	ids := []string{d.Lines[3].ID, d.Lines[1].ID, d.Lines[0].ID, d.Lines[2].ID}
	d, e := repo.ReorderLines(ctx, d.ID, d.Version, ids, InvoiceTotals{NetMinor: 400, TaxMinor: 76, GrossMinor: 476})
	if e != nil {
		t.Fatal(e)
	}
	for i, id := range ids {
		if d.Lines[i].ID != id || d.Lines[i].Position != i+1 {
			t.Fatalf("reorder positions: %#v", d.Lines)
		}
	}
	d, e = repo.RemoveLine(ctx, d.ID, d.Version, ids[1], InvoiceTotals{NetMinor: 300, TaxMinor: 57, GrossMinor: 357})
	if e != nil {
		t.Fatal(e)
	}
	if len(d.Lines) != 3 || d.Version != 7 || d.NetMinor != 300 || d.TaxMinor != 57 || d.GrossMinor != 357 {
		t.Fatalf("remove totals/version: %#v", d)
	}
	for i, id := range []string{ids[0], ids[2], ids[3]} {
		if d.Lines[i].ID != id || d.Lines[i].Position != i+1 {
			t.Fatalf("compaction: %#v", d.Lines)
		}
	}
}

func TestDraftRepositoryPostBumpRollbackPreservesPopulatedDraft(t *testing.T) {
	for _, operation := range []string{"add", "update"} {
		t.Run(operation, func(t *testing.T) {
			_, repo, d := populatedDraft(t)
			injected := errors.New("injected after header bump")
			repo.afterBump = func() error { return injected }
			line := d.Lines[1]
			line.Title = "Changed"
			line.NetMinor = 200
			line.TaxMinor = 38
			line.GrossMinor = 238
			var e error
			if operation == "add" {
				_, e = repo.AddLine(context.Background(), d.ID, d.Version, line, InvoiceTotals{NetMinor: 600, TaxMinor: 114, GrossMinor: 714})
			} else {
				_, e = repo.UpdateLine(context.Background(), d.ID, d.Version, line, InvoiceTotals{NetMinor: 500, TaxMinor: 95, GrossMinor: 595})
			}
			if !errors.Is(e, injected) {
				t.Fatalf("hook not reached: %v", e)
			}
			got, e := repo.GetDraft(context.Background(), d.ID)
			if e != nil || !reflect.DeepEqual(got, d) {
				t.Fatalf("rollback lost header/positions: before=%#v after=%#v err=%v", d, got, e)
			}
		})
	}
}

func TestDraftRepositoryEveryFinalizedMutationIsRejected(t *testing.T) {
	s, repo, d := populatedDraft(t)
	ctx := context.Background()
	if _, e := s.DB().ExecContext(ctx, `UPDATE invoices SET state='finalized',number='I-1',company_snapshot='{}',payment_snapshot='{}',locale_snapshot='{}',tax_snapshot='{}',note_snapshot='{}' WHERE id=?`, d.ID); e != nil {
		t.Fatal(e)
	}
	before, e := repo.GetDraft(ctx, d.ID)
	if e != nil {
		t.Fatal(e)
	}
	totals := InvoiceTotals{NetMinor: 400, TaxMinor: 76, GrossMinor: 476}
	operations := map[string]func() (InvoiceDraft, error){
		"UpdateDraft": func() (InvoiceDraft, error) {
			return repo.UpdateDraft(ctx, d.ID, d.Version, InvoiceDraftInput{CustomerID: d.CustomerID, Currency: d.Currency, Customer: d.Customer, DueDate: d.DueDate}, totals)
		},
		"AddLine":    func() (InvoiceDraft, error) { return repo.AddLine(ctx, d.ID, d.Version, d.Lines[0], totals) },
		"UpdateLine": func() (InvoiceDraft, error) { return repo.UpdateLine(ctx, d.ID, d.Version, d.Lines[0], totals) },
		"RemoveLine": func() (InvoiceDraft, error) { return repo.RemoveLine(ctx, d.ID, d.Version, d.Lines[0].ID, totals) },
		"ReorderLines": func() (InvoiceDraft, error) {
			return repo.ReorderLines(ctx, d.ID, d.Version, []string{d.Lines[3].ID, d.Lines[2].ID, d.Lines[1].ID, d.Lines[0].ID}, totals)
		},
	}
	for name, mutation := range operations {
		t.Run(name, func(t *testing.T) {
			if _, e := mutation(); !errors.Is(e, ErrConflict) {
				t.Fatalf("finalized write accepted: %v", e)
			}
			got, e := repo.GetDraft(ctx, d.ID)
			if e != nil || !reflect.DeepEqual(got, before) {
				t.Fatalf("finalized changed: %#v %v", got, e)
			}
		})
	}
}

func TestDraftRepositoryUpdatesCustomerAndLineSnapshots(t *testing.T) {
	s, repo, d := populatedDraft(t)
	ctx := context.Background()
	c, e := s.CustomerRepository().Create(ctx, validCustomer("C2", "Replacement"))
	if e != nil {
		t.Fatal(e)
	}
	due := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	d, e = repo.UpdateDraft(ctx, d.ID, d.Version, InvoiceDraftInput{CustomerID: c.ID, Currency: "EUR", Customer: c.CustomerInput, DueDate: due}, InvoiceTotals{NetMinor: 400, TaxMinor: 76, GrossMinor: 476})
	if e != nil || d.Version != 6 || d.CustomerID != c.ID || d.Customer.DisplayName != "Replacement" || !d.DueDate.Equal(due) {
		t.Fatalf("customer update: %#v %v", d, e)
	}
	before := append([]InvoiceLine(nil), d.Lines...)
	line := d.Lines[1]
	line.Title = "Custom title"
	line.Description = "Custom detail"
	line.Unit = "day"
	line.QuantityScaled = 20000
	line.UnitPriceMinor = 200
	line.DiscountBasisPoints = 5000
	line.TaxRateBasisPoints = 700
	line.NetMinor = 200
	line.TaxMinor = 14
	line.GrossMinor = 214
	d, e = repo.UpdateLine(ctx, d.ID, d.Version, line, InvoiceTotals{NetMinor: 500, TaxMinor: 71, GrossMinor: 571})
	if e != nil || d.Version != 7 || d.NetMinor != 500 || d.TaxMinor != 71 || d.GrossMinor != 571 || !reflect.DeepEqual(d.Lines[1], line) {
		t.Fatalf("line update: %#v %v", d, e)
	}
	for _, i := range []int{0, 2, 3} {
		if !reflect.DeepEqual(d.Lines[i], before[i]) {
			t.Fatalf("unrelated line changed: %#v", d.Lines)
		}
	}
}
