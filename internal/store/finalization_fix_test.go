package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestFinalizationSQLUpdateReplaceCollision(t *testing.T) {
	for _, attack := range []string{"invoice", "line"} {
		t.Run(attack, func(t *testing.T) {
			s, repo, d := populatedDraft(t)
			ctx := context.Background()
			other, err := repo.CreateDraft(ctx, InvoiceDraftInput{CustomerID: d.CustomerID, Customer: d.Customer, Currency: "EUR", DueDate: time.Now()})
			if err != nil {
				t.Fatal(err)
			}
			other, err = repo.AddLine(ctx, other.ID, other.Version, InvoiceLine{Title: "Other", Unit: "h", QuantityScaled: 10000, UnitPriceMinor: 1}, InvoiceTotals{})
			if err != nil {
				t.Fatal(err)
			}
			if attack == "invoice" {
				if _, err = s.DB().Exec(`DELETE FROM invoice_items WHERE invoice_id=?`, d.ID); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = s.DB().Exec(`UPDATE invoices SET state='finalized',number='HIST-1',company_snapshot='{}',payment_snapshot='{}',locale_snapshot='{}',tax_snapshot='{}',note_snapshot='{}' WHERE id=?`, d.ID); err != nil {
				t.Fatal(err)
			}
			before, _ := repo.GetDraft(ctx, d.ID)
			conn, err := s.DB().Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			conn.ExecContext(ctx, `PRAGMA recursive_triggers=OFF`)
			if attack == "invoice" {
				_, err = conn.ExecContext(ctx, `UPDATE OR REPLACE invoices SET id=? WHERE id=?`, d.ID, other.ID)
			} else {
				_, err = conn.ExecContext(ctx, `UPDATE OR REPLACE invoice_items SET id=? WHERE id=?`, d.Lines[0].ID, other.Lines[0].ID)
			}
			if err == nil {
				t.Fatal("UPDATE OR REPLACE accepted")
			}
			after, err := repo.GetDraft(ctx, d.ID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("historical content changed")
			}
		})
	}
}
func TestFinalizationCurrencyAllowlist(t *testing.T) {
	for _, currency := range []string{"JPY", "BHD", "KWD", "TND", "CLF"} {
		t.Run(currency, func(t *testing.T) {
			s := openMigratedStore(t)
			ctx := context.Background()
			c := CompanyInput{LegalName: "Issuer", Country: "DE", Currency: "EUR", DefaultLanguage: "de", BrandColor: "#123456"}
			c.Currency = currency
			if _, err := s.CompanyRepository().Save(ctx, c); !IsValidationError(err) {
				t.Errorf("company %s=%v", currency, err)
			}
			customer := validCustomer("C", "Buyer")
			customer.Currency = currency
			if _, err := s.CustomerRepository().Create(ctx, customer); !IsValidationError(err) {
				t.Errorf("customer %s=%v", currency, err)
			}
		})
	}
}

func TestFinalizationUpgradePreservesLegacyFinalizedAndUnsupportedCurrency(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	s.ensureMigrationTable(ctx)
	ms, _ := embeddedMigrations()
	for _, m := range ms[:6] {
		if err = s.applyMigration(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	_, err = s.DB().Exec(`INSERT INTO customers(id,number,display_name,country,currency) VALUES('c','C','Buyer','DE','JPY'); INSERT INTO invoices(id,customer_id,state,currency,number,company_snapshot,customer_snapshot,payment_snapshot,locale_snapshot,tax_snapshot,note_snapshot) VALUES('old','c','finalized','JPY','OLD-1','{}','{}','{}','{}','{}','{}')`)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, m := range ms[:6] {
		var checksum string
		s.DB().QueryRow(`SELECT checksum FROM schema_migrations WHERE version=?`, m.version).Scan(&checksum)
		if checksum != m.checksum {
			t.Fatal("old checksum changed")
		}
	}
	var currency, number string
	if err = s.DB().QueryRow(`SELECT currency,number FROM invoices WHERE id='old'`).Scan(&currency, &number); err != nil || currency != "JPY" || number != "OLD-1" {
		t.Fatal("legacy financial meaning changed")
	}
	if _, err = s.InvoiceRepository().GetFinalized(ctx, "old"); err != ErrLegacyInvoice {
		t.Fatalf("legacy snapshot status=%v", err)
	}
}

func TestFinalizationReviewIsOneTransactionAndBindsMidnight(t *testing.T) {
	s, repo, d := populatedDraft(t)
	ctx := context.Background()
	_, err := s.CompanyRepository().Save(ctx, CompanyInput{LegalName: "Reviewed company", Country: "DE", Currency: "EUR", DefaultLanguage: "de", BrandColor: "#123456", InvoicePrefix: "INV"})
	if err != nil {
		t.Fatal(err)
	}
	// A second writer must not enter between company read and key persistence.
	repo.afterReviewRead = func() {
		conn, e := s.DB().Conn(ctx)
		if e != nil {
			t.Fatal(e)
		}
		defer conn.Close()
		conn.ExecContext(ctx, `PRAGMA busy_timeout=0`)
		_, e = conn.ExecContext(ctx, `UPDATE companies SET legal_name='Unreviewed company'`)
		if e == nil || !strings.Contains(e.Error(), "locked") {
			t.Fatalf("review read did not hold transaction: %v", e)
		}
		conn.ExecContext(ctx, `PRAGMA busy_timeout=5000`)
	}
	beforeMidnight := time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC)
	review, err := repo.prepareReview(ctx, d.ID, d.Version, beforeMidnight)
	if err != nil {
		t.Fatal(err)
	}
	f, err := repo.Finalize(ctx, d.ID, review.Key, func(d InvoiceDraft, c Company, n string, at time.Time) (string, InvoiceTotals, []InvoiceLine, error) {
		if !at.Equal(beforeMidnight) || c.LegalName != review.Company.LegalName {
			t.Fatal("reviewed date or company changed")
		}
		return `{"Language":"de","TaxGroups":[],"Notes":""}`, InvoiceTotals{NetMinor: d.NetMinor, TaxMinor: d.TaxMinor, GrossMinor: d.GrossMinor}, d.Lines, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if f.Number != "INV-2025-1" {
		t.Fatalf("midnight changed numbering year: %s", f.Number)
	}
	var issued, finalized string
	var due sql.NullString
	s.DB().QueryRow(`SELECT issue_date,due_date,finalized_at FROM invoices WHERE id=?`, d.ID).Scan(&issued, &due, &finalized)
	if issued != beforeMidnight.Format(time.RFC3339Nano) || due.String != review.Draft.DueDate.Format(time.RFC3339Nano) || !strings.HasPrefix(finalized, time.Now().UTC().Format("2006-01-02")) {
		t.Fatalf("dates=%s %s %s", issued, due.String, finalized)
	}
}
func TestFinalizationLegacyCompanyCannotCreateUnsupportedCatalogPrices(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "currency.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	s.ensureMigrationTable(ctx)
	ms, _ := embeddedMigrations()
	for _, m := range ms[:7] {
		if err = s.applyMigration(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.DB().Exec(`INSERT INTO companies(id,legal_name,currency) VALUES('issuer','Issuer','JPY'); INSERT INTO catalog_items(id,number,kind,title,net_unit_price_minor,tax_rate_scaled) VALUES('legacy-item','I','good','Old price',123,0)`); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CatalogRepository().Create(ctx, CatalogInput{Number: "NEW", Kind: "good", Title: "New", Unit: "piece", UnitPriceMinor: 123}); !IsValidationError(err) {
		t.Fatalf("unsupported catalog create=%v", err)
	}
	if _, err = s.CatalogRepository().Update(ctx, "legacy-item", 1, CatalogInput{Number: "I", Kind: "good", Title: "Old price", Unit: "piece", UnitPriceMinor: 456}); !IsValidationError(err) {
		t.Fatalf("unsupported catalog update=%v", err)
	}
	var price int
	s.DB().QueryRow(`SELECT net_unit_price_minor FROM catalog_items WHERE id='legacy-item'`).Scan(&price)
	if price != 123 {
		t.Fatal("historical catalog amount reinterpreted")
	}
	if _, err = s.DB().Exec(`INSERT INTO invoices(id,customer_id,state,currency) VALUES('new','missing','draft','JPY')`); err == nil || !strings.Contains(err.Error(), "unsupported currency exponent") {
		t.Fatalf("SQL invoice currency=%v", err)
	}
}
func TestFinalization008Preserves007SnapshotAndInvalidatesUndatedKeys(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "v7.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	s.ensureMigrationTable(ctx)
	ms, _ := embeddedMigrations()
	for _, m := range ms[:7] {
		if err = s.applyMigration(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	_, err = s.DB().Exec(`INSERT INTO companies(id,legal_name,currency) VALUES('issuer','Issuer','EUR'); INSERT INTO customers(id,number,display_name,country,currency) VALUES('c','C','Buyer','DE','EUR'); INSERT INTO invoices(id,customer_id,state,currency,number,company_snapshot,customer_snapshot,payment_snapshot,locale_snapshot,tax_snapshot,note_snapshot,frozen_snapshot,invoice_sequence) VALUES('old','c','finalized','EUR','INV-1','{}','{}','{}','{}','{}','{}','{"historical":"unchanged"}',1); INSERT INTO invoices(id,customer_id,state,currency,customer_snapshot) VALUES('draft','c','draft','EUR','{}'); INSERT INTO invoice_finalization_keys(key,invoice_id,draft_version,company_snapshot) VALUES('undated','draft',1,'{}')`)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	f, err := s.InvoiceRepository().GetFinalized(ctx, "old")
	if err != nil || f.Snapshot != `{"historical":"unchanged"}` {
		t.Fatalf("old snapshot changed: %+v %v", f, err)
	}
	for _, m := range ms[:7] {
		var checksum string
		s.DB().QueryRow(`SELECT checksum FROM schema_migrations WHERE version=?`, m.version).Scan(&checksum)
		if checksum != m.checksum {
			t.Fatal("earlier checksum changed")
		}
	}
	// An old key without a reviewed date is invalid; no date is silently invented.
	company, _ := s.CompanyRepository().Get(ctx)
	encoded, _ := json.Marshal(company.CompanyInput)
	s.DB().Exec(`UPDATE invoice_finalization_keys SET company_snapshot=? WHERE key='undated'`, string(encoded))
	_, err = s.InvoiceRepository().Finalize(ctx, "draft", "undated", func(InvoiceDraft, Company, string, time.Time) (string, InvoiceTotals, []InvoiceLine, error) {
		t.Fatal("undated key reached allocation")
		return "", InvoiceTotals{}, nil, nil
	})
	if err != ErrConflict {
		t.Fatalf("old key=%v", err)
	}
}
