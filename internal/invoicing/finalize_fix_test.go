package invoicing

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/kiefer-networks/invoice-generator/internal/store"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestFinalizeUpdateOrReplaceCannotDeleteHistoricalRows(t *testing.T) {
	for _, attack := range []string{"invoice", "line"} {
		t.Run(attack, func(t *testing.T) {
			s := draftStore(t)
			ctx := context.Background()
			old := readyDraft(t, s, 1)
			draft := readyDraft(t, s, 2)
			svc := NewFinalizationService(s)
			key, _ := svc.Prepare(ctx, old.ID, old.Version)
			f, err := svc.Finalize(ctx, old.ID, key)
			if err != nil {
				t.Fatal(err)
			}
			conn, err := s.DB().Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if _, err = conn.ExecContext(ctx, `PRAGMA recursive_triggers=OFF`); err != nil {
				t.Fatal(err)
			}
			if attack == "invoice" {
				_, err = conn.ExecContext(ctx, `UPDATE OR REPLACE invoices SET id=? WHERE id=?`, old.ID, draft.ID)
			} else {
				_, err = conn.ExecContext(ctx, `UPDATE OR REPLACE invoice_items SET id=? WHERE id=?`, old.Lines[0].ID, draft.Lines[0].ID)
			}
			if err == nil {
				t.Fatal("UPDATE OR REPLACE deleted historical data")
			}
			got, err := svc.Get(ctx, old.ID)
			if err != nil || !reflect.DeepEqual(got, f) {
				t.Fatalf("historical invoice changed: %v", err)
			}
			after, err := NewDraftService(s).Get(ctx, draft.ID)
			if err != nil || !reflect.DeepEqual(after, draft) {
				t.Fatalf("draft changed: %v", err)
			}
		})
	}
}
func TestFinalizeReviewAndServiceDateContract(t *testing.T) {
	s := draftStore(t)
	ctx := context.Background()
	d := readyDraft(t, s, 1)
	svc := NewFinalizationService(s)
	d, err := NewDraftService(s).SetServiceDate(ctx, d.ID, d.Version, "2026-08-31")
	if err != nil {
		t.Fatal(err)
	}
	review, err := svc.PrepareReview(ctx, d.ID, d.Version)
	if err != nil {
		t.Fatal(err)
	}
	if review.Snapshot.Company.LegalName != "Issuer" || review.Snapshot.Draft.ServiceDate != "2026-08-31" || review.Snapshot.Draft.IssueDate.IsZero() {
		t.Fatalf("review=%+v", review)
	}
	// Deterministic former TOCTOU interleaving: review returns A, save B, submit A.
	c, _ := s.CompanyRepository().Get(ctx)
	c.LegalName = "Changed after review"
	s.CompanyRepository().Save(ctx, c.CompanyInput)
	if _, err = svc.Finalize(ctx, d.ID, review.Key); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("unreviewed company accepted: %v", err)
	}
	if review.Snapshot.Company.LegalName != "Issuer" {
		t.Fatal("review data aliased mutable company")
	}
	review, err = svc.PrepareReview(ctx, d.ID, d.Version)
	if err != nil {
		t.Fatal(err)
	}
	f, err := svc.Finalize(ctx, d.ID, review.Key)
	if err != nil {
		t.Fatal(err)
	}
	if !f.Snapshot.Draft.IssueDate.Equal(review.Snapshot.Draft.IssueDate) || !f.Snapshot.Draft.DueDate.Equal(review.Snapshot.Draft.DueDate) || f.Snapshot.Draft.ServiceDate != "2026-08-31" {
		t.Fatal("reviewed dates changed")
	}
}
func TestFinalizeRequiresValidServiceDate(t *testing.T) {
	s := draftStore(t)
	ctx := context.Background()
	d := readyDraft(t, s, 1)
	d.ServiceDate = ""
	c, _ := s.CompanyRepository().Get(ctx)
	if err := ValidateFinalization(d, c.CompanyInput); !store.IsValidationError(err) {
		t.Fatalf("missing service date=%v", err)
	}
	for _, date := range []string{"2026-02-30", "yesterday", "2026-08-31T00:00:00Z"} {
		if _, err := NewDraftService(s).SetServiceDate(ctx, d.ID, d.Version, date); !store.IsValidationError(err) {
			t.Errorf("invalid date %s=%v", date, err)
		}
	}
}
func TestFinalizeRecipientCorrectionAndServiceDateAdapters(t *testing.T) {
	s := draftStore(t)
	ctx := context.Background()
	d := readyDraft(t, s, 1)
	customer, _ := s.CustomerRepository().Get(ctx, d.CustomerID)
	customer.LegalName = "Recipient Legal GmbH"
	customer.ContactName = "Pat Buyer"
	customer.Email = "buyer@example.test"
	customer.VATIdentifier = "DE12345"
	customer, err := s.CustomerRepository().Update(ctx, customer.ID, customer.Version, customer.CustomerInput)
	if err != nil {
		t.Fatal(err)
	}
	d, err = NewDraftService(s).Update(ctx, d.ID, d.Version, customer.ID)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewFinalizationService(s)
	key, _ := svc.Prepare(ctx, d.ID, d.Version)
	original, err := svc.Finalize(ctx, d.ID, key)
	if err != nil {
		t.Fatal(err)
	}
	correction, err := svc.CreateCorrection(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	key, _ = svc.Prepare(ctx, correction.ID, correction.Version)
	f, err := svc.Finalize(ctx, correction.ID, key)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(f.Snapshot)
	var snap Snapshot
	if err = json.Unmarshal(encoded, &snap); err != nil {
		t.Fatal(err)
	}
	if snap.Kind != "correction" || snap.Correction.OriginalID != original.ID || snap.Correction.OriginalNumber != original.Number {
		t.Fatalf("correction lost: %+v", snap)
	}
	p := snap.RenderData()
	if p.CustName != "Recipient Legal GmbH" || p.CustDisplayName != "Recipient" || p.CustContact != "Pat Buyer" || p.CustEmail != "buyer@example.test" || p.CustVATID != "DE12345" || p.CorrectionOfNumber != original.Number || p.ServiceDate == "" || !strings.Contains(p.Title, "Correction") {
		t.Fatalf("render=%+v", p)
	}
	cfg, err := snap.Config()
	if err != nil || cfg.Invoice.CorrectionOfNumber != original.Number || cfg.Invoice.CorrectionOf != original.ID || cfg.Invoice.Kind != "correction" || cfg.Invoice.ServiceDate == "" {
		t.Fatalf("config=%+v %v", cfg, err)
	}
}
func TestTransitionCancellationAndOverdueAuditRollback(t *testing.T) {
	for _, action := range []string{"cancelled", "overdue"} {
		t.Run(action, func(t *testing.T) {
			s := draftStore(t)
			ctx := context.Background()
			d := readyDraft(t, s, 1)
			svc := NewFinalizationService(s)
			key, _ := svc.Prepare(ctx, d.ID, d.Version)
			before, err := svc.Finalize(ctx, d.ID, key)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.DB().Exec(`CREATE TRIGGER fail_state_audit BEFORE INSERT ON audit_events WHEN NEW.action='invoice.` + action + `' BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
				t.Fatal(err)
			}
			if action == "cancelled" {
				_, err = svc.Cancel(ctx, d.ID, "Wrong amount")
			} else {
				_, err = s.InvoiceRepository().MarkOverdue(ctx, time.Now().AddDate(0, 0, 30))
			}
			if err == nil {
				t.Fatal("expected audit failure")
			}
			after, _ := svc.Get(ctx, d.ID)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("transition leaked despite rollback")
			}
		})
	}
}
func TestFinalizationServiceDateRemainsImmutable(t *testing.T) {
	s := draftStore(t)
	ctx := context.Background()
	d := readyDraft(t, s, 1)
	svc := NewFinalizationService(s)
	key, _ := svc.Prepare(ctx, d.ID, d.Version)
	before, err := svc.Finalize(ctx, d.ID, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = NewDraftService(s).SetServiceDate(ctx, d.ID, d.Version, "2026-09-01"); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("service-date mutation=%v", err)
	}
	if _, err = s.DB().Exec(`UPDATE invoices SET service_date='2026-09-01' WHERE id=?`, d.ID); err == nil {
		t.Fatal("SQL service date changed")
	}
	after, _ := svc.Get(ctx, d.ID)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("frozen date changed")
	}
}
