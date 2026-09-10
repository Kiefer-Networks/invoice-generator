package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/kiefer-networks/invoice-generator/internal/units"
)

// InvoiceLine is the editable snapshot of one draft position. Integer fields
// retain their scale at every persistence boundary.
type InvoiceLine struct {
	ID, CatalogItemID, Title, Description, Unit string
	Position                                    int
	QuantityScaled, UnitPriceMinor              int64
	DiscountBasisPoints, TaxRateBasisPoints     int64
	NetMinor, TaxMinor, GrossMinor              int64
}

type InvoiceDraft struct {
	ServiceDate, CorrectionOf, CorrectionOfNumber string
	ID, CustomerID, Number, State, Currency       string
	Customer                                      CustomerInput
	IssueDate, DueDate                            time.Time
	Version                                       int
	Lines                                         []InvoiceLine
	NetMinor, TaxMinor, GrossMinor                int64
}
type InvoiceDraftInput struct {
	ServiceDate          string
	CustomerID, Currency string
	Customer             CustomerInput
	DueDate              time.Time
}
type InvoiceListOptions struct {
	State, Search, Cursor string
	Limit                 int
}
type InvoicePage struct {
	Drafts     []InvoiceDraft
	NextCursor string
}
type InvoiceRepository struct {
	store           *Store
	afterBump       func() error
	afterReviewRead func()
}

func (s *Store) InvoiceRepository() *InvoiceRepository { return &InvoiceRepository{store: s} }

func (r *InvoiceRepository) CreateDraft(ctx context.Context, input InvoiceDraftInput) (InvoiceDraft, error) {
	if err := currencyCode(strings.ToUpper(strings.TrimSpace(input.Currency))); err != nil {
		return InvoiceDraft{}, err
	}
	if err := validServiceDate(input.ServiceDate, false); err != nil {
		return InvoiceDraft{}, err
	}
	if strings.TrimSpace(input.CustomerID) == "" || strings.TrimSpace(input.Currency) == "" || input.DueDate.IsZero() {
		return InvoiceDraft{}, fieldError("draft", "is incomplete")
	}
	id, err := newBusinessID()
	if err != nil {
		return InvoiceDraft{}, err
	}
	snapshot, err := json.Marshal(input.Customer)
	if err != nil {
		return InvoiceDraft{}, fmt.Errorf("encode customer snapshot: %w", err)
	}
	tx, err := r.store.db.BeginTx(ctx, nil)
	if err != nil {
		return InvoiceDraft{}, fmt.Errorf("begin invoice draft: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `INSERT INTO invoices (id, customer_id, state, currency, due_date, customer_snapshot, version, service_date) VALUES (?, ?, 'draft', ?, ?, ?, 1, ?)`, id, input.CustomerID, strings.ToUpper(strings.TrimSpace(input.Currency)), input.DueDate.UTC().Format(time.RFC3339Nano), string(snapshot), input.ServiceDate)
	if err != nil {
		return InvoiceDraft{}, fmt.Errorf("create invoice draft: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return InvoiceDraft{}, fmt.Errorf("commit invoice draft: %w", err)
	}
	return r.GetDraft(ctx, id)
}
func (r *InvoiceRepository) GetDraft(ctx context.Context, id string) (InvoiceDraft, error) {
	tx, err := r.store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return InvoiceDraft{}, fmt.Errorf("begin draft read: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	draft, err := readInvoice(ctx, tx, id)
	if err != nil {
		return InvoiceDraft{}, err
	}
	if err = tx.Commit(); err != nil {
		return InvoiceDraft{}, err
	}
	return draft, nil
}

type invoiceReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func readInvoice(ctx context.Context, tx invoiceReader, id string) (InvoiceDraft, error) {
	draft, err := scanInvoiceDraft(tx.QueryRowContext(ctx, `SELECT id, customer_id, number, state, currency, issue_date, due_date, customer_snapshot, version, net_total_minor, tax_total_minor, gross_total_minor, service_date, COALESCE(correction_of_invoice_id,''), COALESCE((SELECT original.number FROM invoices original WHERE original.id=invoices.correction_of_invoice_id),'') FROM invoices WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return InvoiceDraft{}, ErrNotFound
	}
	if err != nil {
		return InvoiceDraft{}, fmt.Errorf("get invoice draft: %w", err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, COALESCE(catalog_item_id,''), position, title_snapshot, description_snapshot, unit_snapshot, quantity_scaled, net_unit_price_minor, discount_basis_points, tax_rate_scaled, net_total_minor, tax_total_minor, gross_total_minor FROM invoice_items WHERE invoice_id=? ORDER BY position`, id)
	if err != nil {
		return InvoiceDraft{}, fmt.Errorf("list invoice lines: %w", err)
	}
	defer func() { _ = rows.Close() }() // Read-only query; rows.Err reports iteration errors.
	for rows.Next() {
		var line InvoiceLine
		if err := rows.Scan(&line.ID, &line.CatalogItemID, &line.Position, &line.Title, &line.Description, &line.Unit, &line.QuantityScaled, &line.UnitPriceMinor, &line.DiscountBasisPoints, &line.TaxRateBasisPoints, &line.NetMinor, &line.TaxMinor, &line.GrossMinor); err != nil {
			return InvoiceDraft{}, fmt.Errorf("scan invoice line: %w", err)
		}
		draft.Lines = append(draft.Lines, line)
	}
	if err := rows.Err(); err != nil {
		return InvoiceDraft{}, fmt.Errorf("list invoice lines: %w", err)
	}
	return draft, nil
}
func (r *InvoiceRepository) ListDrafts(ctx context.Context, options InvoiceListOptions) ([]InvoiceDraft, error) {
	page, err := r.ListDraftPage(ctx, options)
	return page.Drafts, err
}
func (r *InvoiceRepository) ListDraftPage(ctx context.Context, options InvoiceListOptions) (InvoicePage, error) {
	limit := options.Limit
	if limit <= 0 {
		limit = 25
	}
	if limit > 100 {
		limit = 100
	}
	state := options.State
	if state == "" {
		state = "draft"
	}
	if state != "draft" && state != "finalized" && state != "paid" && state != "overdue" && state != "cancelled" {
		return InvoicePage{}, fieldError("state", "is invalid")
	}
	offset := 0
	if options.Cursor != "" {
		var err error
		offset, err = strconv.Atoi(options.Cursor)
		if err != nil || offset < 0 {
			return InvoicePage{}, fieldError("cursor", "is invalid")
		}
	}
	where := "state=?"
	args := []any{state}
	if search := strings.ToLower(strings.TrimSpace(options.Search)); search != "" {
		where += " AND (lower(customer_snapshot) LIKE ? ESCAPE '!' OR lower(number) LIKE ? ESCAPE '!')"
		args = append(args, "%"+escapeLike(search)+"%", "%"+escapeLike(search)+"%")
	}
	args = append(args, limit+1, offset)
	rows, err := r.store.db.QueryContext(ctx, `SELECT id, customer_id, number, state, currency, issue_date, due_date, customer_snapshot, version, net_total_minor, tax_total_minor, gross_total_minor, service_date, COALESCE(correction_of_invoice_id,''), COALESCE((SELECT original.number FROM invoices original WHERE original.id=invoices.correction_of_invoice_id),'') FROM invoices WHERE `+where+` ORDER BY updated_at DESC,id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return InvoicePage{}, fmt.Errorf("list invoice drafts: %w", err)
	}
	defer func() { _ = rows.Close() }() // Read-only query; rows.Err reports iteration errors.
	var result []InvoiceDraft
	for rows.Next() {
		draft, err := scanInvoiceDraft(rows)
		if err != nil {
			return InvoicePage{}, err
		}
		result = append(result, draft)
	}
	if err := rows.Err(); err != nil {
		return InvoicePage{}, err
	}
	page := InvoicePage{Drafts: result}
	if len(page.Drafts) > limit {
		page.Drafts = page.Drafts[:limit]
		page.NextCursor = strconv.Itoa(offset + limit)
	}
	return page, nil
}
func (r *InvoiceRepository) UpdateDraft(ctx context.Context, id string, version int, input InvoiceDraftInput, totals InvoiceTotals) (InvoiceDraft, error) {
	if err := currencyCode(strings.ToUpper(strings.TrimSpace(input.Currency))); err != nil {
		return InvoiceDraft{}, err
	}
	if err := validServiceDate(input.ServiceDate, false); err != nil {
		return InvoiceDraft{}, err
	}
	if version < 1 || strings.TrimSpace(input.CustomerID) == "" || strings.TrimSpace(input.Currency) == "" || input.DueDate.IsZero() {
		return InvoiceDraft{}, fieldError("draft", "is invalid")
	}
	snapshot, err := json.Marshal(input.Customer)
	if err != nil {
		return InvoiceDraft{}, err
	}
	result, err := r.store.db.ExecContext(ctx, `UPDATE invoices SET customer_id=?, currency=?, due_date=?, service_date=?, customer_snapshot=?, net_total_minor=?, tax_total_minor=?, gross_total_minor=?, version=version+1, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=? AND version=? AND state='draft'`, input.CustomerID, strings.ToUpper(strings.TrimSpace(input.Currency)), input.DueDate.UTC().Format(time.RFC3339Nano), input.ServiceDate, string(snapshot), totals.NetMinor, totals.TaxMinor, totals.GrossMinor, id, version)
	if err != nil {
		return InvoiceDraft{}, fmt.Errorf("update invoice draft: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return InvoiceDraft{}, r.draftMissingOrConflict(ctx, id)
	}
	return r.GetDraft(ctx, id)
}

type InvoiceTotals struct{ NetMinor, TaxMinor, GrossMinor int64 }

func (r *InvoiceRepository) AddLine(ctx context.Context, id string, version int, line InvoiceLine, totals InvoiceTotals) (InvoiceDraft, error) {
	if err := validInvoiceLine(line); err != nil {
		return InvoiceDraft{}, err
	}
	tx, err := r.store.db.BeginTx(ctx, nil)
	if err != nil {
		return InvoiceDraft{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := bumpDraft(tx, ctx, id, version, totals); err != nil {
		return InvoiceDraft{}, err
	}
	if r.afterBump != nil {
		if err = r.afterBump(); err != nil {
			return InvoiceDraft{}, err
		}
	}
	lineID, err := newBusinessID()
	if err != nil {
		return InvoiceDraft{}, err
	}
	var position int
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position),0)+1 FROM invoice_items WHERE invoice_id=?`, id).Scan(&position); err != nil {
		return InvoiceDraft{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO invoice_items (id,invoice_id,catalog_item_id,position,title_snapshot,description_snapshot,unit_snapshot,quantity_scaled,net_unit_price_minor,discount_basis_points,tax_rate_scaled,net_total_minor,tax_total_minor,gross_total_minor) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, lineID, id, nullID(line.CatalogItemID), position, line.Title, line.Description, line.Unit, line.QuantityScaled, line.UnitPriceMinor, line.DiscountBasisPoints, line.TaxRateBasisPoints, line.NetMinor, line.TaxMinor, line.GrossMinor)
	if err != nil {
		return InvoiceDraft{}, fmt.Errorf("add invoice line: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return InvoiceDraft{}, err
	}
	return r.GetDraft(ctx, id)
}
func (r *InvoiceRepository) UpdateLine(ctx context.Context, id string, version int, line InvoiceLine, totals InvoiceTotals) (InvoiceDraft, error) {
	if line.ID == "" {
		return InvoiceDraft{}, fieldError("line", "is required")
	}
	if err := validInvoiceLine(line); err != nil {
		return InvoiceDraft{}, err
	}
	tx, err := r.store.db.BeginTx(ctx, nil)
	if err != nil {
		return InvoiceDraft{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = bumpDraft(tx, ctx, id, version, totals); err != nil {
		return InvoiceDraft{}, err
	}
	if r.afterBump != nil {
		if err = r.afterBump(); err != nil {
			return InvoiceDraft{}, err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE invoice_items SET title_snapshot=?,description_snapshot=?,unit_snapshot=?,quantity_scaled=?,net_unit_price_minor=?,discount_basis_points=?,tax_rate_scaled=?,net_total_minor=?,tax_total_minor=?,gross_total_minor=?,updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=? AND invoice_id=?`, line.Title, line.Description, line.Unit, line.QuantityScaled, line.UnitPriceMinor, line.DiscountBasisPoints, line.TaxRateBasisPoints, line.NetMinor, line.TaxMinor, line.GrossMinor, line.ID, id)
	if err != nil {
		return InvoiceDraft{}, err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return InvoiceDraft{}, ErrNotFound
	}
	if err = tx.Commit(); err != nil {
		return InvoiceDraft{}, err
	}
	return r.GetDraft(ctx, id)
}
func (r *InvoiceRepository) RemoveLine(ctx context.Context, id string, version int, lineID string, totals InvoiceTotals) (InvoiceDraft, error) {
	tx, err := r.store.db.BeginTx(ctx, nil)
	if err != nil {
		return InvoiceDraft{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = bumpDraft(tx, ctx, id, version, totals); err != nil {
		return InvoiceDraft{}, err
	}
	var removedPosition int
	if err := tx.QueryRowContext(ctx, `SELECT position FROM invoice_items WHERE id=? AND invoice_id=?`, lineID, id).Scan(&removedPosition); errors.Is(err, sql.ErrNoRows) {
		return InvoiceDraft{}, ErrNotFound
	} else if err != nil {
		return InvoiceDraft{}, err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM invoice_items WHERE id=? AND invoice_id=?`, lineID, id)
	if err != nil {
		return InvoiceDraft{}, err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return InvoiceDraft{}, ErrNotFound
	}
	var remaining int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM invoice_items WHERE invoice_id=?`, id).Scan(&remaining); err != nil {
		return InvoiceDraft{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE invoice_items SET position=position+? WHERE invoice_id=? AND position>?`, remaining+1, id, removedPosition); err != nil {
		return InvoiceDraft{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE invoice_items SET position=position-? WHERE invoice_id=? AND position>?`, remaining+2, id, removedPosition+remaining+1); err != nil {
		return InvoiceDraft{}, err
	}
	if err = tx.Commit(); err != nil {
		return InvoiceDraft{}, err
	}
	return r.GetDraft(ctx, id)
}
func (r *InvoiceRepository) ReorderLines(ctx context.Context, id string, version int, lineIDs []string, totals InvoiceTotals) (InvoiceDraft, error) {
	if len(lineIDs) == 0 {
		return InvoiceDraft{}, fieldError("lines", "are required")
	}
	tx, err := r.store.db.BeginTx(ctx, nil)
	if err != nil {
		return InvoiceDraft{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = bumpDraft(tx, ctx, id, version, totals); err != nil {
		return InvoiceDraft{}, err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM invoice_items WHERE invoice_id=?`, id).Scan(&count); err != nil {
		return InvoiceDraft{}, err
	}
	if count != len(lineIDs) {
		return InvoiceDraft{}, fieldError("lines", "do not match draft")
	}
	seen := make(map[string]struct{}, len(lineIDs))
	for _, lineID := range lineIDs {
		if lineID == "" {
			return InvoiceDraft{}, fieldError("lines", "do not match draft")
		}
		if _, ok := seen[lineID]; ok {
			return InvoiceDraft{}, fieldError("lines", "must not contain duplicates")
		}
		seen[lineID] = struct{}{}
	}
	for i, lineID := range lineIDs {
		result, err := tx.ExecContext(ctx, `UPDATE invoice_items SET position=? WHERE id=? AND invoice_id=?`, count+i+1, lineID, id)
		if err != nil {
			return InvoiceDraft{}, err
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return InvoiceDraft{}, fieldError("lines", "do not match draft")
		}
	}
	for i, lineID := range lineIDs {
		if _, err = tx.ExecContext(ctx, `UPDATE invoice_items SET position=? WHERE id=? AND invoice_id=?`, i+1, lineID, id); err != nil {
			return InvoiceDraft{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return InvoiceDraft{}, err
	}
	return r.GetDraft(ctx, id)
}
func bumpDraft(tx *sql.Tx, ctx context.Context, id string, version int, totals InvoiceTotals) error {
	if version < 1 {
		return fieldError("version", "is invalid")
	}
	result, err := tx.ExecContext(ctx, `UPDATE invoices SET net_total_minor=?,tax_total_minor=?,gross_total_minor=?,version=version+1,updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=? AND version=? AND state='draft'`, totals.NetMinor, totals.TaxMinor, totals.GrossMinor, id, version)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrConflict
	}
	return nil
}
func (r *InvoiceRepository) draftMissingOrConflict(ctx context.Context, id string) error {
	_, err := r.GetDraft(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return ErrNotFound
	}
	return ErrConflict
}
func validInvoiceLine(line InvoiceLine) error {
	if _, err := units.Code(line.Unit); err != nil {
		return fieldError("unit", err.Error())
	}
	if strings.TrimSpace(line.Title) == "" || strings.TrimSpace(line.Unit) == "" || line.QuantityScaled <= 0 || line.UnitPriceMinor < 0 || line.DiscountBasisPoints < 0 || line.DiscountBasisPoints > 10000 || line.TaxRateBasisPoints < 0 || line.TaxRateBasisPoints > 10000 || line.NetMinor < 0 || line.TaxMinor < 0 || line.GrossMinor < 0 {
		return fieldError("line", "is invalid")
	}
	return nil
}
func nullID(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

type invoiceScanner interface{ Scan(...any) error }

func scanInvoiceDraft(s invoiceScanner) (InvoiceDraft, error) {
	var d InvoiceDraft
	var issue, due, number sql.NullString
	var snapshot string
	err := s.Scan(&d.ID, &d.CustomerID, &number, &d.State, &d.Currency, &issue, &due, &snapshot, &d.Version, &d.NetMinor, &d.TaxMinor, &d.GrossMinor, &d.ServiceDate, &d.CorrectionOf, &d.CorrectionOfNumber)
	if err != nil {
		return d, err
	}
	if snapshot != "" {
		if err = json.Unmarshal([]byte(snapshot), &d.Customer); err != nil {
			return d, fmt.Errorf("decode customer snapshot: %w", err)
		}
	}
	d.Number = number.String
	if issue.Valid {
		d.IssueDate = parseBusinessTime(issue.String)
	}
	if due.Valid {
		d.DueDate = parseBusinessTime(due.String)
	}
	return d, nil
}

func validServiceDate(value string, required bool) error {
	if value == "" && !required {
		return nil
	}
	date, err := time.Parse("2006-01-02", value)
	if err != nil || date.Format("2006-01-02") != value {
		return fieldError("service_date", "must be a valid service or delivery date (YYYY-MM-DD)")
	}
	return nil
}
func (r *InvoiceRepository) SetServiceDate(ctx context.Context, id string, version int, date string) (InvoiceDraft, error) {
	if err := validServiceDate(date, true); err != nil {
		return InvoiceDraft{}, err
	}
	result, err := r.store.db.ExecContext(ctx, `UPDATE invoices SET service_date=?,version=version+1,updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=? AND version=? AND state='draft'`, date, id, version)
	if err != nil {
		return InvoiceDraft{}, err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return InvoiceDraft{}, ErrConflict
	}
	return r.GetDraft(ctx, id)
}
