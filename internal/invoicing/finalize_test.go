package invoicing

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/kiefer-networks/invoice-generator/internal/store"
)

func readyDraft(t *testing.T, s *store.Store, n int) Draft {
	t.Helper()
	ctx := context.Background()
	if _, err := s.CompanyRepository().Save(ctx, store.CompanyInput{LegalName: "Issuer", AddressLine1: "Issuer Street 1", PostalCode: "10115", City: "Berlin", Country: "DE", TaxNumber: "123", IBAN: "DE123", DefaultLanguage: "de", Currency: "EUR", BrandColor: "#123456", InvoicePrefix: "INV", StandardNotes: "Thank you"}); err != nil {
		t.Fatal(err)
	}
	c, err := s.CustomerRepository().Create(ctx, store.CustomerInput{Number: fmt.Sprint(n), DisplayName: "Recipient", AddressLine1: "Buyer Street 2", PostalCode: "10117", City: "Berlin", Country: "DE", PreferredLanguage: "en", Currency: "EUR", PaymentTermsDays: 14})
	if err != nil {
		t.Fatal(err)
	}
	d, err := NewDraftService(s).Create(ctx, c.ID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	d, err = NewDraftService(s).AddManualLine(ctx, d.ID, d.Version, DraftLineInput{Title: "Advice", Description: "Detailed work", Unit: "hour", QuantityScaled: 3333, UnitPriceMinor: 300, TaxRateBasisPoints: 1900})
	if err != nil {
		t.Fatal(err)
	}
	d, err = NewDraftService(s).SetServiceDate(ctx, d.ID, d.Version, "2026-08-31")
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func TestFinalizeSnapshotIdempotencyAndImmutability(t *testing.T) {
	s := draftStore(t)
	ctx := context.Background()
	d := readyDraft(t, s, 1)
	svc := NewFinalizationService(s)
	key, err := svc.Prepare(ctx, d.ID, d.Version)
	if err != nil {
		t.Fatal(err)
	}
	f, err := svc.Finalize(ctx, d.ID, key)
	if err != nil {
		t.Fatal(err)
	}
	if f.Number != fmt.Sprintf("INV-%d-1", time.Now().UTC().Year()) || f.State != "finalized" || f.Snapshot.Draft.GrossMinor != 119 || f.Snapshot.Company.StandardNotes != "Thank you" {
		t.Fatalf("finalized=%+v", f)
	}
	again, err := svc.Finalize(ctx, d.ID, key)
	if err != nil || !reflect.DeepEqual(f, again) {
		t.Fatalf("retry=%+v %v", again, err)
	}
	if _, err = svc.Finalize(ctx, d.ID, "another-key"); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("key reuse=%v", err)
	}
	if _, err = NewDraftService(s).AddManualLine(ctx, d.ID, d.Version, DraftLineInput{Title: "bad", Unit: "h", QuantityScaled: 10000}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("mutation=%v", err)
	}
	for _, q := range []string{`UPDATE invoices SET state='draft',number=NULL WHERE id=?`, `UPDATE invoices SET correction_of_invoice_id=id WHERE id=?`, `UPDATE invoices SET finalized_at='tampered' WHERE id=?`, `DELETE FROM invoices WHERE id=?`, `UPDATE invoice_items SET title_snapshot='tampered' WHERE invoice_id=?`, `DELETE FROM invoice_items WHERE invoice_id=?`} {
		if _, err = s.DB().ExecContext(ctx, q, d.ID); err == nil {
			t.Fatalf("allowed %s", q)
		}
	}
	before := f.Snapshot
	company, _ := s.CompanyRepository().Get(ctx)
	company.LegalName = "Changed"
	if _, err = s.CompanyRepository().Save(ctx, company.CompanyInput); err != nil {
		t.Fatal(err)
	}
	f, err = svc.Get(ctx, d.ID)
	if err != nil || !reflect.DeepEqual(before, f.Snapshot) {
		t.Fatalf("snapshot changed %v", err)
	}
	cfg, err := f.Snapshot.Config()
	if err != nil || cfg.Company.Name != "Issuer" || cfg.Customer.Name != "Recipient" || cfg.Invoice.Number != f.Number {
		t.Fatalf("config=%+v %v", cfg, err)
	}
}
func TestFinalizeValidationStaleAndRollback(t *testing.T) {
	s := draftStore(t)
	ctx := context.Background()
	d := readyDraft(t, s, 1)
	svc := NewFinalizationService(s)
	key, _ := svc.Prepare(ctx, d.ID, d.Version)
	d, err := NewDraftService(s).AddManualLine(ctx, d.ID, d.Version, DraftLineInput{Title: "Other", Unit: "h", QuantityScaled: 10000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Finalize(ctx, d.ID, key); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale=%v", err)
	}
	key, _ = svc.Prepare(ctx, d.ID, d.Version)
	if _, err = s.DB().Exec(`CREATE TRIGGER fail_audit BEFORE INSERT ON audit_events WHEN NEW.action='invoice.finalized' BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Finalize(ctx, d.ID, key); err == nil {
		t.Fatal("expected rollback")
	}
	c, _ := s.CompanyRepository().Get(ctx)
	got, _ := NewDraftService(s).Get(ctx, d.ID)
	if c.NextInvoiceSequence != 1 || got.Number != "" || got.Version != d.Version {
		t.Fatal("failed finalization consumed number or changed draft")
	}
	if _, err := s.DB().Exec(`DROP TRIGGER fail_audit`); err != nil {
		t.Error(err)
	}
	if _, err = svc.Finalize(ctx, d.ID, key); err != nil {
		t.Fatal(err)
	}
}
func TestConcurrentFinalization(t *testing.T) {
	s := draftStore(t)
	ctx := context.Background()
	svc := NewFinalizationService(s)
	ds := make([]Draft, 32)
	keys := make([]string, 32)
	for i := range ds {
		ds[i] = readyDraft(t, s, i)
		var err error
		keys[i], err = svc.Prepare(ctx, ds[i].ID, ds[i].Version)
		if err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	seqs := make(chan int, 32)
	errs := make(chan error, 32)
	for i := range ds {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			f, err := svc.Finalize(ctx, ds[i].ID, keys[i])
			if err != nil {
				errs <- err
				return
			}
			seqs <- f.Sequence
		}(i)
	}
	wg.Wait()
	close(seqs)
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	var seq []int
	for n := range seqs {
		seq = append(seq, n)
	}
	sort.Ints(seq)
	if len(seq) != 32 {
		t.Fatalf("numbers=%v", seq)
	}
	for i, n := range seq {
		if n != i+1 {
			t.Fatalf("numbers=%v", seq)
		}
	}
}
func TestFinalizeRejectsReplacementAndDestinationMoves(t *testing.T) {
	s := draftStore(t)
	ctx := context.Background()
	svc := NewFinalizationService(s)
	d := readyDraft(t, s, 1)
	other := readyDraft(t, s, 2)
	key, _ := svc.Prepare(ctx, d.ID, d.Version)
	if _, err := svc.Finalize(ctx, d.ID, key); err != nil {
		t.Fatal(err)
	}
	statements := []struct {
		q    string
		args []any
	}{
		{`INSERT OR REPLACE INTO invoices(id,customer_id,state,currency) VALUES(?,?,'draft','EUR')`, []any{d.ID, d.CustomerID}},
		{`INSERT OR REPLACE INTO invoice_items(id,invoice_id,position,title_snapshot,quantity_scaled,net_unit_price_minor,tax_rate_scaled) VALUES(?,?,2,'tampered',10000,1,0)`, []any{d.Lines[0].ID, other.ID}},
		{`UPDATE invoice_items SET invoice_id=?,position=2 WHERE id=?`, []any{d.ID, other.Lines[0].ID}},
	}
	for _, v := range statements {
		if _, err := s.DB().ExecContext(ctx, v.q, v.args...); err == nil {
			t.Errorf("allowed %s", v.q)
		}
	}
}
func TestFinalizeReviewRejectsChangedCompany(t *testing.T) {
	s := draftStore(t)
	d := readyDraft(t, s, 1)
	svc := NewFinalizationService(s)
	ctx := context.Background()
	key, _ := svc.Prepare(ctx, d.ID, d.Version)
	c, _ := s.CompanyRepository().Get(ctx)
	c.LegalName = "Unreviewed issuer"
	if _, err := s.CompanyRepository().Save(ctx, c.CompanyInput); err != nil {
		t.Error(err)
	}
	if _, err := svc.Finalize(ctx, d.ID, key); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("unreviewed company=%v", err)
	}
}
func TestFinalizeRenderDataIncludesFrozenPaymentAndNumber(t *testing.T) {
	s := draftStore(t)
	d := readyDraft(t, s, 1)
	svc := NewFinalizationService(s)
	ctx := context.Background()
	key, _ := svc.Prepare(ctx, d.ID, d.Version)
	f, err := svc.Finalize(ctx, d.ID, key)
	if err != nil {
		t.Fatal(err)
	}
	p := f.Snapshot.RenderData()
	if p.InvNumber != f.Number || p.BankIBAN != "DE123" || !p.HasBank {
		t.Fatalf("render=%+v", p)
	}
}
func TestFinalizeValidatesSupportedPartyFields(t *testing.T) {
	s := draftStore(t)
	d := readyDraft(t, s, 1)
	c, _ := s.CompanyRepository().Get(context.Background())
	for _, change := range []func(*Draft, *store.CompanyInput){func(d *Draft, c *store.CompanyInput) { d.Customer.Country = "XX" }, func(d *Draft, c *store.CompanyInput) { d.Customer.PreferredLanguage = "xx" }, func(d *Draft, c *store.CompanyInput) { c.DefaultLanguage = "xx" }, func(d *Draft, c *store.CompanyInput) { c.Country = "XX" }} {
		copyD, copyC := d, c.CompanyInput
		change(&copyD, &copyC)
		if err := ValidateFinalization(copyD, copyC); !store.IsValidationError(err) {
			t.Errorf("unsupported party fields accepted: %v", err)
		}
	}
}
func TestFinalizeConfigRejectsLegacyAggregateTaxRoundingDrift(t *testing.T) {
	d := Draft{Currency: "EUR", NetMinor: 6, TaxMinor: 2, GrossMinor: 8, Lines: []DraftLine{{Title: "A", Unit: "piece", QuantityScaled: 10000, UnitPriceMinor: 3, TaxRateBasisPoints: 1900}, {Title: "B", Unit: "piece", QuantityScaled: 10000, UnitPriceMinor: 3, TaxRateBasisPoints: 1900}}}
	if _, err := (Snapshot{Draft: d}).Config(); err == nil {
		t.Fatal("legacy aggregate tax would silently drop one cent")
	}
}
func TestConcurrentFinalizationSameDraftIsIdempotent(t *testing.T) {
	s := draftStore(t)
	ctx := context.Background()
	d := readyDraft(t, s, 1)
	svc := NewFinalizationService(s)
	key, _ := svc.Prepare(ctx, d.ID, d.Version)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f, err := svc.Finalize(ctx, d.ID, key)
			if err != nil || f.Sequence != 1 {
				t.Errorf("retry=%+v %v", f, err)
			}
		}()
	}
	wg.Wait()
	var n int
	if err := s.DB().QueryRow(`SELECT count(*) FROM audit_events WHERE action='invoice.finalized'`).Scan(&n); err != nil {
		t.Error(err)
	}
	if n != 1 {
		t.Fatalf("audit duplicates=%d", n)
	}
}
func TestCorrectionRollbackPreservesOriginalAndNoDraft(t *testing.T) {
	s := draftStore(t)
	ctx := context.Background()
	d := readyDraft(t, s, 1)
	svc := NewFinalizationService(s)
	key, _ := svc.Prepare(ctx, d.ID, d.Version)
	f, err := svc.Finalize(ctx, d.ID, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`CREATE TRIGGER fail_correction BEFORE INSERT ON audit_events WHEN NEW.action='invoice.correction_created' BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Error(err)
	}
	if _, err = svc.CreateCorrection(ctx, d.ID); err == nil {
		t.Fatal("expected correction rollback")
	}
	var count int
	if err := s.DB().QueryRow(`SELECT count(*) FROM invoices`).Scan(&count); err != nil {
		t.Error(err)
	}
	now, _ := svc.Get(ctx, d.ID)
	if count != 1 || !reflect.DeepEqual(now, f) {
		t.Fatal("correction rollback leaked changes")
	}
}
