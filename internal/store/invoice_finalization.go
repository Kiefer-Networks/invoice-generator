package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// FrozenInvoice carries serialized immutable domain data plus normalized state.
type FrozenInvoice struct {
	ID, Number, State, CorrectionOf, Snapshot, CancellationReason string
	Sequence                                                      int
	PaidAt                                                        time.Time
}
type FreezeBuilder func(InvoiceDraft, Company, string, time.Time) (string, InvoiceTotals, []InvoiceLine, error)

// immediate pins one connection and acquires SQLite's write lock before reading.
func (r *InvoiceRepository) immediate(ctx context.Context, fn func(*sql.Conn) error) error {
	conn, err := r.store.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	if err = fn(conn); err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, "COMMIT")
	return err
}
func (r *InvoiceRepository) PrepareFinalization(ctx context.Context, id string, version int) (string, error) {
	key, err := newBusinessID()
	if err != nil {
		return "", err
	}
	company, err := r.store.CompanyRepository().Get(ctx)
	if err != nil {
		return "", err
	}
	companyJSON, _ := json.Marshal(company.CompanyInput)
	result, err := r.store.db.ExecContext(ctx, `INSERT INTO invoice_finalization_keys(key,invoice_id,draft_version,company_snapshot) SELECT ?,id,version,? FROM invoices WHERE id=? AND version=? AND state='draft'`, key, string(companyJSON), id, version)
	if err != nil {
		return "", err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return "", ErrConflict
	}
	return key, nil
}
func readFrozen(ctx context.Context, q invoiceReader, id string) (FrozenInvoice, error) {
	var f FrozenInvoice
	var paid sql.NullString
	err := q.QueryRowContext(ctx, `SELECT id,number,state,COALESCE(correction_of_invoice_id,''),frozen_snapshot,invoice_sequence,cancellation_reason,paid_at FROM invoices WHERE id=? AND frozen_snapshot IS NOT NULL`, id).Scan(&f.ID, &f.Number, &f.State, &f.CorrectionOf, &f.Snapshot, &f.Sequence, &f.CancellationReason, &paid)
	if errors.Is(err, sql.ErrNoRows) {
		return f, ErrNotFound
	}
	if paid.Valid {
		f.PaidAt = parseBusinessTime(paid.String)
	}
	return f, err
}
func (r *InvoiceRepository) GetFinalized(ctx context.Context, id string) (FrozenInvoice, error) {
	return readFrozen(ctx, r.store.db, id)
}
func (r *InvoiceRepository) Finalize(ctx context.Context, id, key string, build FreezeBuilder) (FrozenInvoice, error) {
	var out FrozenInvoice
	if strings.TrimSpace(key) == "" {
		return out, ErrConflict
	}
	err := r.immediate(ctx, func(conn *sql.Conn) error {
		var state string
		var existing sql.NullString
		err := conn.QueryRowContext(ctx, `SELECT state,finalization_key FROM invoices WHERE id=?`, id).Scan(&state, &existing)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if state != "draft" {
			if existing.String != key {
				return ErrConflict
			}
			out, err = readFrozen(ctx, conn, id)
			return err
		}
		var version int
		var reviewedCompany string
		if err = conn.QueryRowContext(ctx, `SELECT draft_version,company_snapshot FROM invoice_finalization_keys WHERE key=? AND invoice_id=?`, key, id).Scan(&version, &reviewedCompany); errors.Is(err, sql.ErrNoRows) {
			return ErrConflict
		} else if err != nil {
			return err
		}
		d, err := readInvoice(ctx, conn, id)
		if err != nil {
			return err
		}
		if d.Version != version {
			return ErrConflict
		}
		c, err := scanCompany(conn.QueryRowContext(ctx, `SELECT id, legal_name, contact_name, email, phone, address_line1, address_line2, postal_code, city, country, tax_number, vat_identifier, bank_name, iban, bic, logo_key, brand_color, default_language, currency, payment_terms_days, invoice_prefix, next_invoice_sequence, standard_notes, created_at, updated_at FROM companies WHERE singleton=1 AND active=1`))
		if errors.Is(err, sql.ErrNoRows) {
			return fieldError("company", "setup is required")
		}
		if err != nil {
			return err
		}
		companyJSON, _ := json.Marshal(c.CompanyInput)
		if string(companyJSON) != reviewedCompany {
			return ErrConflict
		}
		now := time.Now().UTC()
		number := fmt.Sprintf("%s-%d-%d", c.InvoicePrefix, now.Year(), c.NextInvoiceSequence)
		snapshot, totals, lines, err := build(d, c, number, now)
		if err != nil {
			return err
		}
		for _, l := range lines {
			if _, err = conn.ExecContext(ctx, `UPDATE invoice_items SET net_total_minor=?,tax_total_minor=?,gross_total_minor=? WHERE id=? AND invoice_id=?`, l.NetMinor, l.TaxMinor, l.GrossMinor, l.ID, id); err != nil {
				return err
			}
		}
		company, _ := json.Marshal(c.CompanyInput)
		customer, _ := json.Marshal(d.Customer)
		var frozen map[string]json.RawMessage
		if err = json.Unmarshal([]byte(snapshot), &frozen); err != nil {
			return err
		}
		due := time.Date(now.Year(), now.Month(), now.Day()+d.Customer.PaymentTermsDays, 0, 0, 0, 0, time.UTC)
		_, err = conn.ExecContext(ctx, `UPDATE invoices SET state='finalized',number=?,invoice_sequence=?,issue_date=?,due_date=?,company_snapshot=?,customer_snapshot=?,payment_snapshot=?,locale_snapshot=?,tax_snapshot=?,note_snapshot=?,frozen_snapshot=?,finalization_key=?,net_total_minor=?,tax_total_minor=?,gross_total_minor=?,finalized_at=?,version=version+1,updated_at=? WHERE id=? AND state='draft' AND version=?`, number, c.NextInvoiceSequence, now.Format(time.RFC3339Nano), due.Format(time.RFC3339Nano), string(company), string(customer), string(company), string(frozen["Language"]), string(frozen["TaxGroups"]), string(frozen["Notes"]), snapshot, key, totals.NetMinor, totals.TaxMinor, totals.GrossMinor, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), id, version)
		if err != nil {
			return err
		}
		if _, err = conn.ExecContext(ctx, `UPDATE companies SET next_invoice_sequence=next_invoice_sequence+1 WHERE id=?`, c.ID); err != nil {
			return err
		}
		if err = invoiceAudit(ctx, conn, id, "invoice.finalized", `{}`); err != nil {
			return err
		}
		out, err = readFrozen(ctx, conn, id)
		return err
	})
	return out, err
}

type invoiceAuditKey struct{}
type invoiceAuditIdentity struct{ actor, request string }

func WithInvoiceAudit(ctx context.Context, actor, request string) context.Context {
	return context.WithValue(ctx, invoiceAuditKey{}, invoiceAuditIdentity{actor, request})
}
func invoiceAudit(ctx context.Context, conn *sql.Conn, id, action, summary string) error {
	identity, _ := ctx.Value(invoiceAuditKey{}).(invoiceAuditIdentity)
	aid, err := newBusinessID()
	if err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, `INSERT INTO audit_events(id,action,target_type,target_id,result,change_summary,actor_subject,request_id) VALUES (?,?,'invoice',?,'success',?,?,?)`, aid, action, id, summary, identity.actor, identity.request)
	return err
}

func (r *InvoiceRepository) Transition(ctx context.Context, id, state, reason string) (FrozenInvoice, error) {
	var out FrozenInvoice
	reason = strings.TrimSpace(reason)
	if state != "paid" && state != "cancelled" {
		return out, fieldError("state", "is invalid")
	}
	if state == "cancelled" && (reason == "" || len(reason) > 2000) {
		return out, fieldError("reason", "must contain between 1 and 2000 characters")
	}
	err := r.immediate(ctx, func(conn *sql.Conn) error {
		var paid any
		if state == "paid" {
			paid = time.Now().UTC().Format(time.RFC3339Nano)
		}
		result, err := conn.ExecContext(ctx, `UPDATE invoices SET state=?,paid_at=?,cancellation_reason=?,version=version+1,updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=? AND state IN ('finalized','overdue')`, state, paid, reason, id)
		if err != nil {
			return err
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return ErrConflict
		}
		if err = invoiceAudit(ctx, conn, id, "invoice."+state, `{}`); err != nil {
			return err
		}
		out, err = readFrozen(ctx, conn, id)
		return err
	})
	return out, err
}
func (r *InvoiceRepository) MarkOverdue(ctx context.Context, now time.Time) (int, error) {
	count := 0
	err := r.immediate(ctx, func(conn *sql.Conn) error {
		rows, err := conn.QueryContext(ctx, `SELECT id FROM invoices WHERE state='finalized' AND substr(due_date,1,10)<? ORDER BY invoice_sequence`, now.UTC().Format("2006-01-02"))
		if err != nil {
			return err
		}
		var ids []string
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, id := range ids {
			if _, err = conn.ExecContext(ctx, `UPDATE invoices SET state='overdue',version=version+1,updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=?`, id); err != nil {
				return err
			}
			if err = invoiceAudit(ctx, conn, id, "invoice.overdue", `{}`); err != nil {
				return err
			}
		}
		count = len(ids)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}
func (r *InvoiceRepository) CreateCorrection(ctx context.Context, id string) (InvoiceDraft, error) {
	var out InvoiceDraft
	err := r.immediate(ctx, func(conn *sql.Conn) error {
		original, err := readInvoice(ctx, conn, id)
		if err != nil {
			return err
		}
		if original.State == "draft" {
			return ErrConflict
		}
		newID, err := newBusinessID()
		if err != nil {
			return err
		}
		// Copy the historical customer and lines, including archived catalog references.
		_, err = conn.ExecContext(ctx, `INSERT INTO invoices(id,customer_id,correction_of_invoice_id,state,currency,due_date,customer_snapshot,net_total_minor,tax_total_minor,gross_total_minor) SELECT ?,customer_id,id,'draft',currency,?,customer_snapshot,net_total_minor,tax_total_minor,gross_total_minor FROM invoices WHERE id=?`, newID, time.Now().UTC().AddDate(0, 0, original.Customer.PaymentTermsDays).Format(time.RFC3339Nano), id)
		if err != nil {
			return err
		}
		for _, l := range original.Lines {
			lid, err := newBusinessID()
			if err != nil {
				return err
			}
			_, err = conn.ExecContext(ctx, `INSERT INTO invoice_items(id,invoice_id,catalog_item_id,position,title_snapshot,description_snapshot,unit_snapshot,quantity_scaled,net_unit_price_minor,discount_basis_points,tax_rate_scaled,net_total_minor,tax_total_minor,gross_total_minor) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, lid, newID, nullID(l.CatalogItemID), l.Position, l.Title, l.Description, l.Unit, l.QuantityScaled, l.UnitPriceMinor, l.DiscountBasisPoints, l.TaxRateBasisPoints, l.NetMinor, l.TaxMinor, l.GrossMinor)
			if err != nil {
				return err
			}
		}
		summary, _ := json.Marshal(map[string]string{"original_invoice_id": id, "correction_invoice_id": newID})
		if err = invoiceAudit(ctx, conn, newID, "invoice.correction_created", string(summary)); err != nil {
			return err
		}
		out, err = readInvoice(ctx, conn, newID)
		return err
	})
	return out, err
}

// ValidateInvoiceParties also validates detached historical customer values.
func ValidateInvoiceParties(company CompanyInput, customer CustomerInput) error {
	if _, err := normalizeCompany(company); err != nil {
		return err
	}
	_, err := normalizeCustomer(customer)
	return err
}
