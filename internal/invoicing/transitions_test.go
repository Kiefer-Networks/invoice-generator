package invoicing

import (
	"context"
	"errors"
	"github.com/kiefer-networks/invoice-generator/internal/store"
	"reflect"
	"testing"
	"time"
)

func TestTransitionsAndCorrection(t *testing.T) {
	s := draftStore(t)
	ctx := context.Background()
	svc := NewFinalizationService(s)
	d := readyDraft(t, s, 1)
	if _, err := svc.MarkPaid(ctx, d.ID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("draft paid=%v", err)
	}
	if _, err := svc.CreateCorrection(ctx, d.ID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("draft correction=%v", err)
	}
	key, _ := svc.Prepare(ctx, d.ID, d.Version)
	f, err := svc.Finalize(ctx, d.ID, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Cancel(ctx, d.ID, "   "); !store.IsValidationError(err) {
		t.Fatalf("empty reason=%v", err)
	}
	if n, err := svc.MarkOverdue(ctx); err != nil || n != 0 {
		t.Fatalf("premature overdue=%d %v", n, err)
	}
	paid, err := svc.MarkPaid(ctx, d.ID)
	if err != nil || paid.State != "paid" || paid.PaidAt.IsZero() {
		t.Fatalf("paid=%+v %v", paid, err)
	}
	if _, err = svc.Cancel(ctx, d.ID, "reason"); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("paid cancel=%v", err)
	}
	if _, err = svc.MarkPaid(ctx, d.ID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("repaid=%v", err)
	}
	correction, err := svc.CreateCorrection(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if correction.ID == d.ID || correction.Number != "" || len(correction.Lines) != 1 || correction.Lines[0].ID == d.Lines[0].ID || correction.Lines[0].Title != d.Lines[0].Title {
		t.Fatalf("correction=%+v", correction)
	}
	var linked string
	s.DB().QueryRow(`SELECT correction_of_invoice_id FROM invoices WHERE id=?`, correction.ID).Scan(&linked)
	if linked != d.ID {
		t.Fatal("missing linkage")
	}
	current, err := svc.Get(ctx, d.ID)
	if err != nil || !reflect.DeepEqual(current.Snapshot, f.Snapshot) {
		t.Fatal("original changed")
	}
	key, _ = svc.Prepare(ctx, correction.ID, correction.Version)
	cf, err := svc.Finalize(ctx, correction.ID, key)
	if err != nil || cf.Sequence != 2 || cf.CorrectionOf != d.ID {
		t.Fatalf("correction final=%+v %v", cf, err)
	}
	cancelled, err := svc.Cancel(ctx, cf.ID, "Wrong amount")
	if err != nil || cancelled.State != "cancelled" || cancelled.CancellationReason != "Wrong amount" {
		t.Fatalf("cancel=%+v %v", cancelled, err)
	}
	if _, err = svc.MarkPaid(ctx, cf.ID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("cancel paid=%v", err)
	}
	var audits int
	s.DB().QueryRow(`SELECT count(*) FROM audit_events WHERE target_type='invoice'`).Scan(&audits)
	if audits != 5 {
		t.Fatalf("audit count=%d", audits)
	}
}
func TestTransitionRollbackAndOverdueSelection(t *testing.T) {
	s := draftStore(t)
	ctx := context.Background()
	svc := NewFinalizationService(s)
	d := readyDraft(t, s, 1)
	key, _ := svc.Prepare(ctx, d.ID, d.Version)
	_, err := svc.Finalize(ctx, d.ID, key)
	if err != nil {
		t.Fatal(err)
	}
	s.DB().Exec(`CREATE TRIGGER fail_transition BEFORE INSERT ON audit_events WHEN NEW.action='invoice.paid' BEGIN SELECT RAISE(ABORT,'injected'); END`)
	if _, err = svc.MarkPaid(ctx, d.ID); err == nil {
		t.Fatal("expected rollback")
	}
	f, _ := svc.Get(ctx, d.ID)
	if f.State != "finalized" || !f.PaidAt.IsZero() {
		t.Fatal("transition persisted despite audit failure")
	}
	// The repository cutoff is exercised with a deterministic future date. The public service uses the server clock.
	n, err := s.InvoiceRepository().MarkOverdue(ctx, time.Now().UTC().AddDate(0, 0, 15))
	if err != nil || n != 1 {
		t.Fatalf("overdue=%d %v", n, err)
	}
	n, err = s.InvoiceRepository().MarkOverdue(ctx, time.Now().UTC().AddDate(0, 0, 15))
	if err != nil || n != 0 {
		t.Fatalf("repeat overdue=%d %v", n, err)
	}
	s.DB().Exec(`DROP TRIGGER fail_transition`)
	if _, err = svc.MarkPaid(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
}
func TestFinalizeRejectsIncompleteDocumentAndRecomputesTotals(t *testing.T) {
	s := draftStore(t)
	ctx := context.Background()
	d := readyDraft(t, s, 1)
	svc := NewFinalizationService(s)
	c, _ := s.CompanyRepository().Get(ctx)
	c.AddressLine1 = ""
	s.CompanyRepository().Save(ctx, c.CompanyInput)
	key, _ := svc.Prepare(ctx, d.ID, d.Version)
	if _, err := svc.Finalize(ctx, d.ID, key); !store.IsValidationError(err) {
		t.Fatalf("incomplete=%v", err)
	}
	c.AddressLine1 = "Issuer Street 1"
	s.CompanyRepository().Save(ctx, c.CompanyInput)
	key, _ = svc.Prepare(ctx, d.ID, d.Version)
	s.DB().Exec(`UPDATE invoice_items SET net_total_minor=0,tax_total_minor=0,gross_total_minor=0 WHERE invoice_id=?`, d.ID)
	f, err := svc.Finalize(ctx, d.ID, key)
	if err != nil || f.Snapshot.Draft.Lines[0].GrossMinor != 119 {
		t.Fatalf("totals=%+v %v", f, err)
	}
}
