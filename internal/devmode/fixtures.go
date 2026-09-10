//go:build !production

package devmode

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/kiefer-networks/invoice-generator/internal/documents"
	"github.com/kiefer-networks/invoice-generator/internal/invoicing"
	"github.com/kiefer-networks/invoice-generator/internal/store"
	fixtures "github.com/kiefer-networks/invoice-generator/testdata/dev"
)

const fixtureDate = "2026-01-15T12:00:00Z"

// Seed inserts deterministic business records transactionally, then generates
// one genuine PDF for the failed-delivery example before workers start. The
// completion marker preserves edits and jobs on subsequent process restarts.
func Seed(ctx context.Context, s *store.Store) (err error) {
	var seq int
	var name, path string
	if e := s.DB().QueryRowContext(ctx, "PRAGMA database_list").Scan(&seq, &name, &path); e != nil {
		return e
	}
	root := filepath.Dir(path)
	if filepath.Base(path) != "development.sqlite" {
		return errors.New("fixtures require the isolated development database")
	}
	if e := ValidateRoot(root); e != nil {
		return e
	}
	if e := seedBusiness(ctx, s); e != nil {
		return e
	}
	var version int
	if e := s.DB().QueryRowContext(ctx, "SELECT version FROM development_fixture_version").Scan(&version); e != nil {
		return e
	}
	if version == 2 {
		return nil
	}
	storage, e := documents.NewStorage(filepath.Join(root, "documents"), 20<<20)
	if e != nil {
		return e
	}
	defer func() { err = errors.Join(err, storage.Close()) }()
	svc := documents.New(s, storage)
	doc, e := s.DocumentRepository().ForInvoice(ctx, "dev-invoice-finalized-0001")
	if e != nil {
		return e
	}
	if doc.Status != "ready" {
		// No worker runs before fixture initialization completes. Reclaim only
		// this initial job if a previous startup stopped during PDF generation.
		if _, e = s.DB().ExecContext(ctx, `UPDATE document_jobs SET state='queued',attempts=0,next_attempt_at=0,lease_token='',lease_expires_at=0 WHERE document_id=? AND state<>'completed'`, doc.ID); e != nil {
			return e
		}
		job, e := s.DocumentRepository().Claim(ctx, time.Now(), 5*time.Minute)
		if e != nil {
			return e
		}
		if job.InvoiceID != "dev-invoice-finalized-0001" {
			return errors.New("unexpected fixture document job")
		}
		if e = svc.Generate(ctx, job); e != nil {
			return e
		}
	}
	// Begin with a genuine immutable PDF and a terminal delivery failure. The
	// ordinary authenticated Retry control can deliver it to the local fake.
	if _, e = s.DB().ExecContext(ctx, `UPDATE paperless_jobs SET id='dev-paperless-job-0001',state='failed',attempts=5,next_attempt_at=0,last_error_code='request_failed',last_error_summary='Development fixture: simulated delivery failure. Retry to send to local Paperless.' WHERE document_id=?`, doc.ID); e != nil {
		return e
	}
	_, e = s.DB().ExecContext(ctx, "UPDATE development_fixture_version SET version=2")
	return e
}

func seedBusiness(ctx context.Context, s *store.Store) (err error) {
	tx, e := s.DB().BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, rollbackErr)
		}
	}()
	if _, e = tx.ExecContext(ctx, "PRAGMA defer_foreign_keys=ON"); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS development_fixture_version(version INTEGER PRIMARY KEY)"); e != nil {
		return e
	}
	var version int
	e = tx.QueryRowContext(ctx, "SELECT version FROM development_fixture_version").Scan(&version)
	if e == nil {
		return nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	var count int
	if e = tx.QueryRowContext(ctx, "SELECT (SELECT count(*) FROM companies)+(SELECT count(*) FROM invoices)+(SELECT count(*) FROM customers)").Scan(&count); e != nil {
		return e
	}
	if count != 0 {
		return errors.New("refusing to seed an existing non-fixture database")
	}
	var c store.CompanyInput
	var customers []store.CustomerInput
	var catalog []store.CatalogInput
	for name, dest := range map[string]any{"company.json": &c, "customers.json": &customers, "catalog.json": &catalog} {
		data, err := fixtures.Files.ReadFile(name)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(data, dest); err != nil {
			return err
		}
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO companies(id,legal_name,email,address_line1,postal_code,city,country,tax_number,brand_color,default_language,currency,payment_terms_days,invoice_prefix,standard_notes,next_invoice_sequence,created_at,updated_at) VALUES('dev-company',?,?,?,?,?,?,?,?,?,?,?,?,?,3,?,?)`, c.LegalName, c.Email, c.AddressLine1, c.PostalCode, c.City, c.Country, c.TaxNumber, c.BrandColor, c.DefaultLanguage, c.Currency, c.PaymentTermsDays, c.InvoicePrefix, c.StandardNotes, fixtureDate, fixtureDate); e != nil {
		return e
	}
	for i, c := range customers {
		if _, e = tx.ExecContext(ctx, `INSERT INTO customers(id,number,display_name,legal_name,email,address_line1,postal_code,city,country,preferred_language,currency,payment_terms_days,search_key,sort_key,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, fmt.Sprintf("dev-customer-%d", i+1), c.Number, c.DisplayName, c.LegalName, c.Email, c.AddressLine1, c.PostalCode, c.City, c.Country, c.PreferredLanguage, c.Currency, c.PaymentTermsDays, strings.ToLower(c.Number+" "+c.DisplayName), strings.ToLower(c.DisplayName), fixtureDate, fixtureDate); e != nil {
			return e
		}
	}
	for i, c := range catalog {
		if _, e = tx.ExecContext(ctx, `INSERT INTO catalog_items(id,number,kind,title,description,unit,net_unit_price_minor,tax_rate_scaled,search_key,sort_key,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, fmt.Sprintf("dev-catalog-%d", i+1), c.Number, c.Kind, c.Title, c.Description, c.Unit, c.UnitPriceMinor, c.TaxRateBasisPoints, strings.ToLower(c.Number+" "+c.Title), strings.ToLower(c.Title), fixtureDate, fixtureDate); e != nil {
			return e
		}
	}
	now, _ := time.Parse(time.RFC3339, fixtureDate)
	customerJSON, _ := json.Marshal(customers[0])
	companyJSON, _ := json.Marshal(c)
	for i, id := range []string{"dev-invoice-draft-0001", "dev-invoice-finalized-0001", "dev-invoice-finalized-0002"} {
		d := invoicing.Draft{ID: id, CustomerID: "dev-customer-1", Customer: customers[0], Currency: "EUR", Version: 1, ServiceDate: "2026-01-15", IssueDate: now, DueDate: now.AddDate(0, 0, 14), NetMinor: 10000, TaxMinor: 1900, GrossMinor: 11900, Lines: []invoicing.DraftLine{{ID: id + "-line", CatalogItemID: "dev-catalog-1", Title: catalog[0].Title, Description: catalog[0].Description, Unit: "hour", Position: 1, QuantityScaled: 10000, UnitPriceMinor: 10000, TaxRateBasisPoints: 1900, NetMinor: 10000, TaxMinor: 1900, GrossMinor: 11900}}}
		if _, e = tx.ExecContext(ctx, `INSERT INTO invoices(id,customer_id,state,currency,customer_snapshot,service_date,due_date,net_total_minor,tax_total_minor,gross_total_minor,created_at,updated_at) VALUES(?,'dev-customer-1','draft','EUR',?,?,?,10000,1900,11900,?,?)`, id, string(customerJSON), d.ServiceDate, d.DueDate.Format("2006-01-02"), fixtureDate, fixtureDate); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO invoice_items(id,invoice_id,catalog_item_id,position,title_snapshot,description_snapshot,unit_snapshot,quantity_scaled,net_unit_price_minor,tax_rate_scaled,net_total_minor,tax_total_minor,gross_total_minor,created_at,updated_at) VALUES(?,?,'dev-catalog-1',1,?,?,'hour',10000,10000,1900,10000,1900,11900,?,?)`, id+"-line", id, catalog[0].Title, catalog[0].Description, fixtureDate, fixtureDate); e != nil {
			return e
		}
		if i == 0 {
			continue
		}
		d.Number = fmt.Sprintf("DEV-2026-%04d", i)
		totals, err := invoicing.Calculate([]invoicing.Line{{QuantityScaled: 10000, UnitPriceMinor: 10000, TaxRateBasisPoints: 1900}})
		if err != nil {
			return err
		}
		d.TaxGroups = totals.TaxGroups
		snap := invoicing.Snapshot{Draft: d, Company: c, Language: "en", Kind: "invoice", Notes: c.StandardNotes, TaxGroups: totals.TaxGroups}
		raw, err := json.Marshal(snap)
		if err != nil {
			return err
		}
		if _, e = tx.ExecContext(ctx, `UPDATE invoices SET state='finalized',number=?,invoice_sequence=?,issue_date='2026-01-15',company_snapshot=?,payment_snapshot='{}',locale_snapshot='{}',tax_snapshot='{}',note_snapshot='{}',frozen_snapshot=?,finalized_at=? WHERE id=?`, d.Number, i, string(companyJSON), string(raw), fixtureDate, id); e != nil {
			return e
		}
		// The queue trigger creates random identifiers; pin them before any worker runs.
		if _, e = tx.ExecContext(ctx, `UPDATE document_jobs SET id=?,next_attempt_at=? WHERE document_id=(SELECT id FROM documents WHERE invoice_id=?)`, id+"-document-job", i-1, id); e != nil {
			return e
		}
		documentID := fmt.Sprintf("dev-document-finalized-%04d", i)
		if _, e = tx.ExecContext(ctx, `UPDATE document_jobs SET document_id=? WHERE id=?`, documentID, id+"-document-job"); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE documents SET id=?,storage_key=? WHERE invoice_id=?`, documentID, documentID, id); e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO development_fixture_version VALUES(1)"); e != nil {
		return e
	}
	return tx.Commit()
}
