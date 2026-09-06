package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// CatalogInput is the editable catalog item data. Prices and tax rates remain
// integers (minor units and basis points) at this boundary.
type CatalogInput struct {
	Number, Kind, Title, Description, Unit string
	UnitPriceMinor, TaxRateBasisPoints     int
}

// CatalogItem is an active or archived goods or services record.
type CatalogItem struct {
	ID string
	CatalogInput
	Active               bool
	Version              int
	CreatedAt, UpdatedAt time.Time
}

type CatalogListOptions struct {
	Search, Cursor                string
	Limit                         int
	IncludeArchived, ArchivedOnly bool
}
type CatalogPage struct {
	Items      []CatalogItem
	NextCursor string
}
type CatalogRepository struct{ store *Store }

func (s *Store) CatalogRepository() *CatalogRepository { return &CatalogRepository{store: s} }

func (r *CatalogRepository) Create(ctx context.Context, input CatalogInput) (CatalogItem, error) {
	in, err := normalizeCatalog(input)
	if err != nil {
		return CatalogItem{}, err
	}
	id, err := newBusinessID()
	if err != nil {
		return CatalogItem{}, err
	}
	searchKey, sortKey := catalogKeys(in)
	_, err = r.store.db.ExecContext(ctx, `INSERT INTO catalog_items (id, number, kind, title, description, unit, net_unit_price_minor, tax_rate_scaled, active, version, search_key, sort_key) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, 1, ?, ?)`, id, in.Number, in.Kind, in.Title, in.Description, in.Unit, in.UnitPriceMinor, in.TaxRateBasisPoints, searchKey, sortKey)
	if err != nil {
		return CatalogItem{}, catalogDBError(err)
	}
	return r.Get(ctx, id)
}

func (r *CatalogRepository) Get(ctx context.Context, id string) (CatalogItem, error) {
	item, err := scanCatalog(r.store.db.QueryRowContext(ctx, `SELECT id, number, kind, title, description, unit, net_unit_price_minor, tax_rate_scaled, active, version, created_at, updated_at FROM catalog_items WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return CatalogItem{}, ErrNotFound
	}
	if err != nil {
		return CatalogItem{}, fmt.Errorf("get catalog item: %w", err)
	}
	return item, nil
}

func (r *CatalogRepository) List(ctx context.Context, options CatalogListOptions) (CatalogPage, error) {
	limit := options.Limit
	if limit <= 0 {
		limit = 25
	}
	if limit > 100 {
		limit = 100
	}
	cursor, err := decodeCatalogCursor(options.Cursor)
	if err != nil {
		return CatalogPage{}, fieldError("cursor", "is invalid")
	}
	where := make([]string, 0, 3)
	args := make([]any, 0, 8)
	if options.ArchivedOnly {
		where = append(where, "active=0")
	} else if !options.IncludeArchived {
		where = append(where, "active=1")
	}
	if search := strings.ToLower(clean(options.Search)); search != "" {
		where = append(where, "search_key LIKE ? ESCAPE '!'")
		args = append(args, "%"+escapeLike(search)+"%")
	}
	if cursor.ID != "" {
		where = append(where, "(sort_key > ? OR (sort_key = ? AND (number > ? OR (number = ? AND id > ?))))")
		args = append(args, cursor.Title, cursor.Title, cursor.Number, cursor.Number, cursor.ID)
	}
	query := `SELECT id, number, kind, title, description, unit, net_unit_price_minor, tax_rate_scaled, active, version, created_at, updated_at FROM catalog_items`
	if len(where) != 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY sort_key, number, id LIMIT ?"
	args = append(args, limit+1)
	rows, err := r.store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return CatalogPage{}, fmt.Errorf("list catalog: %w", err)
	}
	defer rows.Close()
	page := CatalogPage{}
	for rows.Next() {
		item, err := scanCatalog(rows)
		if err != nil {
			return CatalogPage{}, fmt.Errorf("scan catalog item: %w", err)
		}
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return CatalogPage{}, fmt.Errorf("list catalog: %w", err)
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = encodeCatalogCursor(catalogCursor{Title: catalogSortKey(last.Title), Number: last.Number, ID: last.ID})
	}
	return page, nil
}

func (r *CatalogRepository) Update(ctx context.Context, id string, version int, input CatalogInput) (CatalogItem, error) {
	in, err := normalizeCatalog(input)
	if err != nil {
		return CatalogItem{}, err
	}
	if version < 1 {
		return CatalogItem{}, fieldError("version", "is invalid")
	}
	searchKey, sortKey := catalogKeys(in)
	result, err := r.store.db.ExecContext(ctx, `UPDATE catalog_items SET number=?, kind=?, title=?, description=?, unit=?, net_unit_price_minor=?, tax_rate_scaled=?, search_key=?, sort_key=?, version=version+1, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=? AND version=?`, in.Number, in.Kind, in.Title, in.Description, in.Unit, in.UnitPriceMinor, in.TaxRateBasisPoints, searchKey, sortKey, id, version)
	if err != nil {
		return CatalogItem{}, catalogDBError(err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return CatalogItem{}, r.updateMissingOrConflict(ctx, id)
	}
	return r.Get(ctx, id)
}
func (r *CatalogRepository) Archive(ctx context.Context, id string, version int) (CatalogItem, error) {
	return r.setActive(ctx, id, version, false)
}
func (r *CatalogRepository) Restore(ctx context.Context, id string, version int) (CatalogItem, error) {
	return r.setActive(ctx, id, version, true)
}
func (r *CatalogRepository) setActive(ctx context.Context, id string, version int, active bool) (CatalogItem, error) {
	if version < 1 {
		return CatalogItem{}, fieldError("version", "is invalid")
	}
	value := 0
	if active {
		value = 1
	}
	result, err := r.store.db.ExecContext(ctx, `UPDATE catalog_items SET active=?, version=version+1, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=? AND version=?`, value, id, version)
	if err != nil {
		return CatalogItem{}, fmt.Errorf("change catalog state: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return CatalogItem{}, r.updateMissingOrConflict(ctx, id)
	}
	return r.Get(ctx, id)
}
func (r *CatalogRepository) updateMissingOrConflict(ctx context.Context, id string) error {
	_, err := r.Get(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return ErrNotFound
	}
	return ErrConflict
}

type catalogScanner interface{ Scan(...any) error }

func scanCatalog(scanner catalogScanner) (CatalogItem, error) {
	var item CatalogItem
	var active int
	var created, updated string
	err := scanner.Scan(&item.ID, &item.Number, &item.Kind, &item.Title, &item.Description, &item.Unit, &item.UnitPriceMinor, &item.TaxRateBasisPoints, &active, &item.Version, &created, &updated)
	if err == nil {
		item.Active = active == 1
		item.CreatedAt = parseBusinessTime(created)
		item.UpdatedAt = parseBusinessTime(updated)
	}
	return item, err
}

func normalizeCatalog(in CatalogInput) (CatalogInput, error) {
	in.Number, in.Title, in.Description, in.Unit = clean(in.Number), clean(in.Title), strings.TrimSpace(in.Description), clean(in.Unit)
	in.Kind = strings.ToLower(clean(in.Kind))
	if err := required(in.Number, "number", 64); err != nil {
		return in, err
	}
	if in.Kind != "good" && in.Kind != "service" {
		return in, fieldError("kind", "must be good or service")
	}
	if err := required(in.Title, "title", 200); err != nil {
		return in, err
	}
	if err := required(in.Unit, "unit", 64); err != nil {
		return in, err
	}
	if err := optional(in.Description, "description", 5000); err != nil {
		return in, err
	}
	if in.UnitPriceMinor < 0 {
		return in, fieldError("unit_price_minor", "must not be negative")
	}
	if in.TaxRateBasisPoints < 0 || in.TaxRateBasisPoints > 10000 {
		return in, fieldError("tax_rate_basis_points", "must be between 0 and 10000")
	}
	return in, nil
}
func catalogDBError(err error) error {
	if strings.Contains(strings.ToLower(err.Error()), "unique constraint failed: catalog_items.number") {
		return fmt.Errorf("catalog item number: %w", ErrDuplicate)
	}
	return fmt.Errorf("write catalog item: %w", err)
}
func catalogKeys(in CatalogInput) (string, string) {
	return strings.Join([]string{catalogSortKey(in.Number), catalogSortKey(in.Kind), catalogSortKey(in.Title), catalogSortKey(in.Description), catalogSortKey(in.Unit)}, "\x1f"), catalogSortKey(in.Title)
}
func catalogSortKey(value string) string { return strings.ToLower(strings.TrimSpace(value)) }

func backfillCatalogKeysOnConn(ctx context.Context, conn *sql.Conn) error {
	rows, err := conn.QueryContext(ctx, `SELECT id, number, kind, title, description, unit, search_key, sort_key FROM catalog_items`)
	if err != nil {
		return fmt.Errorf("read catalog keys: %w", err)
	}
	defer rows.Close()
	type row struct {
		id           string
		in           CatalogInput
		search, sort string
	}
	var records []row
	for rows.Next() {
		var item row
		if err := rows.Scan(&item.id, &item.in.Number, &item.in.Kind, &item.in.Title, &item.in.Description, &item.in.Unit, &item.search, &item.sort); err != nil {
			return fmt.Errorf("scan catalog keys: %w", err)
		}
		wantSearch, wantSort := catalogKeys(item.in)
		if item.search != wantSearch || item.sort != wantSort {
			item.search, item.sort = wantSearch, wantSort
			records = append(records, item)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read catalog keys: %w", err)
	}
	for _, item := range records {
		if _, err := conn.ExecContext(ctx, `UPDATE catalog_items SET search_key=?, sort_key=? WHERE id=?`, item.search, item.sort, item.id); err != nil {
			return fmt.Errorf("backfill catalog keys: %w", err)
		}
	}
	return nil
}

type catalogCursor struct{ Title, Number, ID string }

func encodeCatalogCursor(cursor catalogCursor) string {
	b, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(b)
}
func decodeCatalogCursor(value string) (catalogCursor, error) {
	if value == "" {
		return catalogCursor{}, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return catalogCursor{}, err
	}
	var cursor catalogCursor
	if err := json.Unmarshal(b, &cursor); err != nil || cursor.Title == "" || cursor.Number == "" || cursor.ID == "" {
		return catalogCursor{}, errors.New("invalid cursor")
	}
	return cursor, nil
}
