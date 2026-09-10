package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
)

// CustomerInput is the editable customer data.
type CustomerInput struct {
	Number, DisplayName, LegalName, ContactName, Email    string
	AddressLine1, AddressLine2, PostalCode, City, Country string
	VATIdentifier, PreferredLanguage, Currency            string
	PaymentTermsDays                                      int
	Notes                                                 string
}

// Customer is an active or archived customer record.
type Customer struct {
	ID string
	CustomerInput
	Active               bool
	Version              int
	CreatedAt, UpdatedAt time.Time
}

type CustomerListOptions struct {
	Search, Cursor                string
	Limit                         int
	IncludeArchived, ArchivedOnly bool
}
type CustomerPage struct {
	Customers  []Customer
	NextCursor string
}
type CustomerRepository struct{ store *Store }

func (s *Store) CustomerRepository() *CustomerRepository { return &CustomerRepository{store: s} }

func (r *CustomerRepository) Create(ctx context.Context, input CustomerInput) (Customer, error) {
	in, err := normalizeCustomer(input)
	if err != nil {
		return Customer{}, err
	}
	id, err := newBusinessID()
	if err != nil {
		return Customer{}, err
	}
	searchKey, sortKey := customerKeys(in)
	_, err = r.store.db.ExecContext(ctx, `INSERT INTO customers (id, number, display_name, legal_name, contact_name, email, address_line1, address_line2, postal_code, city, country, vat_identifier, preferred_language, currency, payment_terms_days, notes, active, version, search_key, sort_key) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, 1, ?, ?)`, id, in.Number, in.DisplayName, in.LegalName, in.ContactName, in.Email, in.AddressLine1, in.AddressLine2, in.PostalCode, in.City, in.Country, in.VATIdentifier, in.PreferredLanguage, in.Currency, in.PaymentTermsDays, in.Notes, searchKey, sortKey)
	if err != nil {
		return Customer{}, customerDBError(err)
	}
	return r.Get(ctx, id)
}

func (r *CustomerRepository) Get(ctx context.Context, id string) (Customer, error) {
	row := r.store.db.QueryRowContext(ctx, `SELECT id, number, display_name, legal_name, contact_name, email, address_line1, address_line2, postal_code, city, country, vat_identifier, preferred_language, currency, payment_terms_days, notes, active, version, created_at, updated_at FROM customers WHERE id = ?`, id)
	c, err := scanCustomer(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Customer{}, ErrNotFound
	}
	if err != nil {
		return Customer{}, fmt.Errorf("get customer: %w", err)
	}
	return c, nil
}

func (r *CustomerRepository) List(ctx context.Context, options CustomerListOptions) (CustomerPage, error) {
	limit := options.Limit
	if limit <= 0 {
		limit = 25
	}
	if limit > 100 {
		limit = 100
	}
	cursor, err := decodeCustomerCursor(options.Cursor)
	if err != nil {
		return CustomerPage{}, fieldError("cursor", "is invalid")
	}
	where := make([]string, 0, 3)
	args := make([]any, 0, 8)
	if options.ArchivedOnly {
		where = append(where, "active = 0")
	} else if !options.IncludeArchived {
		where = append(where, "active = 1")
	}
	if search := strings.ToLower(clean(options.Search)); search != "" {
		pattern := "%" + escapeLike(search) + "%"
		where = append(where, "search_key LIKE ? ESCAPE '!'")
		args = append(args, pattern)
	}
	if cursor.ID != "" {
		where = append(where, `(sort_key > ? OR (sort_key = ? AND (number > ? OR (number = ? AND id > ?))))`)
		args = append(args, cursor.Name, cursor.Name, cursor.Number, cursor.Number, cursor.ID)
	}
	query := `SELECT id, number, display_name, legal_name, contact_name, email, address_line1, address_line2, postal_code, city, country, vat_identifier, preferred_language, currency, payment_terms_days, notes, active, version, created_at, updated_at FROM customers`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ") // #nosec G202 -- Only literal predicates are joined; all search and cursor values use bound parameters.
	}
	query += " ORDER BY sort_key, number, id LIMIT ?"
	args = append(args, limit+1)
	rows, err := r.store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return CustomerPage{}, fmt.Errorf("list customers: %w", err)
	}
	defer func() { _ = rows.Close() }() // Read-only query; iteration errors are checked with rows.Err.
	page := CustomerPage{}
	for rows.Next() {
		c, err := scanCustomerRows(rows)
		if err != nil {
			return CustomerPage{}, fmt.Errorf("scan customer: %w", err)
		}
		page.Customers = append(page.Customers, c)
	}
	if err := rows.Err(); err != nil {
		return CustomerPage{}, fmt.Errorf("list customers: %w", err)
	}
	if len(page.Customers) > limit {
		page.Customers = page.Customers[:limit]
		page.NextCursor = encodeCustomerCursor(customerCursor{Name: customerSortKey(page.Customers[len(page.Customers)-1].DisplayName), Number: page.Customers[len(page.Customers)-1].Number, ID: page.Customers[len(page.Customers)-1].ID})
	}
	return page, nil
}

func (r *CustomerRepository) Update(ctx context.Context, id string, version int, input CustomerInput) (Customer, error) {
	in, err := normalizeCustomer(input)
	if err != nil {
		return Customer{}, err
	}
	if version < 1 {
		return Customer{}, fieldError("version", "is invalid")
	}
	searchKey, sortKey := customerKeys(in)
	result, err := r.store.db.ExecContext(ctx, `UPDATE customers SET number=?, display_name=?, legal_name=?, contact_name=?, email=?, address_line1=?, address_line2=?, postal_code=?, city=?, country=?, vat_identifier=?, preferred_language=?, currency=?, payment_terms_days=?, notes=?, search_key=?, sort_key=?, version=version+1, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=? AND version=?`, in.Number, in.DisplayName, in.LegalName, in.ContactName, in.Email, in.AddressLine1, in.AddressLine2, in.PostalCode, in.City, in.Country, in.VATIdentifier, in.PreferredLanguage, in.Currency, in.PaymentTermsDays, in.Notes, searchKey, sortKey, id, version)
	if err != nil {
		return Customer{}, customerDBError(err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return Customer{}, r.updateMissingOrConflict(ctx, id)
	}
	return r.Get(ctx, id)
}

func (r *CustomerRepository) Archive(ctx context.Context, id string, version int) (Customer, error) {
	return r.setActive(ctx, id, version, false)
}
func (r *CustomerRepository) Restore(ctx context.Context, id string, version int) (Customer, error) {
	return r.setActive(ctx, id, version, true)
}
func (r *CustomerRepository) setActive(ctx context.Context, id string, version int, active bool) (Customer, error) {
	if version < 1 {
		return Customer{}, fieldError("version", "is invalid")
	}
	value := 0
	if active {
		value = 1
	}
	result, err := r.store.db.ExecContext(ctx, `UPDATE customers SET active=?, version=version+1, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=? AND version=?`, value, id, version)
	if err != nil {
		return Customer{}, fmt.Errorf("change customer state: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return Customer{}, r.updateMissingOrConflict(ctx, id)
	}
	return r.Get(ctx, id)
}
func (r *CustomerRepository) updateMissingOrConflict(ctx context.Context, id string) error {
	_, err := r.Get(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return ErrNotFound
	}
	return ErrConflict
}

func scanCustomer(row *sql.Row) (Customer, error)       { return scanCustomerValues(row) }
func scanCustomerRows(rows *sql.Rows) (Customer, error) { return scanCustomerValues(rows) }

type customerScanner interface{ Scan(...any) error }

func scanCustomerValues(scanner customerScanner) (Customer, error) {
	var c Customer
	var active int
	var created, updated string
	err := scanner.Scan(&c.ID, &c.Number, &c.DisplayName, &c.LegalName, &c.ContactName, &c.Email, &c.AddressLine1, &c.AddressLine2, &c.PostalCode, &c.City, &c.Country, &c.VATIdentifier, &c.PreferredLanguage, &c.Currency, &c.PaymentTermsDays, &c.Notes, &active, &c.Version, &created, &updated)
	if err == nil {
		c.Active = active == 1
		c.CreatedAt = parseBusinessTime(created)
		c.UpdatedAt = parseBusinessTime(updated)
	}
	return c, err
}

func normalizeCustomer(in CustomerInput) (CustomerInput, error) {
	in.Number, in.DisplayName, in.LegalName, in.ContactName = clean(in.Number), clean(in.DisplayName), clean(in.LegalName), clean(in.ContactName)
	in.Email = strings.ToLower(clean(in.Email))
	in.AddressLine1, in.AddressLine2, in.PostalCode, in.City = clean(in.AddressLine1), clean(in.AddressLine2), clean(in.PostalCode), clean(in.City)
	in.Country, in.VATIdentifier, in.PreferredLanguage, in.Currency = strings.ToUpper(clean(in.Country)), clean(in.VATIdentifier), strings.ToLower(clean(in.PreferredLanguage)), strings.ToUpper(clean(in.Currency))
	in.Notes = strings.TrimSpace(in.Notes)
	if err := required(in.Number, "number", 64); err != nil {
		return in, err
	}
	if err := required(in.DisplayName, "display_name", 200); err != nil {
		return in, err
	}
	if err := optional(in.Email, "email", 254); err != nil {
		return in, err
	}
	if in.Email != "" {
		if _, err := mail.ParseAddress(in.Email); err != nil {
			return in, fieldError("email", "must be a valid address")
		}
	}
	if err := countryCode(in.Country); err != nil {
		return in, err
	}
	if err := currencyCode(in.Currency); err != nil {
		return in, err
	}
	if in.PreferredLanguage != "de" && in.PreferredLanguage != "en" {
		return in, fieldError("preferred_language", "must be de or en")
	}
	if in.PaymentTermsDays < 0 || in.PaymentTermsDays > 365 {
		return in, fieldError("payment_terms_days", "must be between 0 and 365")
	}
	for _, value := range []struct {
		value, field string
		limit        int
	}{{in.LegalName, "legal_name", 200}, {in.ContactName, "contact_name", 200}, {in.AddressLine1, "address_line1", 250}, {in.AddressLine2, "address_line2", 250}, {in.PostalCode, "postal_code", 32}, {in.City, "city", 100}, {in.VATIdentifier, "vat_identifier", 64}, {in.Notes, "notes", 5000}} {
		if err := optional(value.value, value.field, value.limit); err != nil {
			return in, err
		}
	}
	return in, nil
}
func customerDBError(err error) error {
	if strings.Contains(strings.ToLower(err.Error()), "unique constraint failed: customers.number") {
		return fmt.Errorf("customer number: %w", ErrDuplicate)
	}
	return fmt.Errorf("write customer: %w", err)
}
func escapeLike(value string) string {
	value = strings.ReplaceAll(value, "!", "!!")
	value = strings.ReplaceAll(value, "%", "!%")
	return strings.ReplaceAll(value, "_", "!_")
}

func customerKeys(in CustomerInput) (string, string) {
	return strings.Join([]string{customerSortKey(in.Number), customerSortKey(in.DisplayName), customerSortKey(in.LegalName), customerSortKey(in.ContactName), customerSortKey(in.Email), customerSortKey(in.VATIdentifier)}, "\x1f"), customerSortKey(in.DisplayName)
}

func customerSortKey(value string) string { return strings.ToLower(strings.TrimSpace(value)) }

func backfillCustomerKeysOnConn(ctx context.Context, conn *sql.Conn) error {
	rows, err := conn.QueryContext(ctx, `SELECT id, number, display_name, legal_name, contact_name, email, vat_identifier, search_key, sort_key FROM customers`)
	if err != nil {
		return fmt.Errorf("read customer keys: %w", err)
	}
	defer func() { _ = rows.Close() }() // Read-only query; iteration errors are checked with rows.Err.
	type row struct {
		id           string
		in           CustomerInput
		search, sort string
	}
	var records []row
	for rows.Next() {
		var item row
		if err := rows.Scan(&item.id, &item.in.Number, &item.in.DisplayName, &item.in.LegalName, &item.in.ContactName, &item.in.Email, &item.in.VATIdentifier, &item.search, &item.sort); err != nil {
			return fmt.Errorf("scan customer keys: %w", err)
		}
		wantSearch, wantSort := customerKeys(item.in)
		if item.search != wantSearch || item.sort != wantSort {
			item.search, item.sort = wantSearch, wantSort
			records = append(records, item)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read customer keys: %w", err)
	}
	for _, item := range records {
		if _, err := conn.ExecContext(ctx, `UPDATE customers SET search_key=?, sort_key=? WHERE id=?`, item.search, item.sort, item.id); err != nil {
			return fmt.Errorf("backfill customer keys: %w", err)
		}
	}
	return nil
}

type customerCursor struct{ Name, Number, ID string }

func encodeCustomerCursor(cursor customerCursor) string {
	b, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(b)
}
func decodeCustomerCursor(value string) (customerCursor, error) {
	if value == "" {
		return customerCursor{}, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return customerCursor{}, err
	}
	var cursor customerCursor
	if err := json.Unmarshal(b, &cursor); err != nil || cursor.Name == "" || cursor.Number == "" || cursor.ID == "" {
		return customerCursor{}, errors.New("invalid cursor")
	}
	return cursor, nil
}
