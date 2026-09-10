package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func validCatalog(number, title string) CatalogInput {
	return CatalogInput{Number: number, Kind: "good", Title: title, Unit: " piece ", UnitPriceMinor: 0, TaxRateBasisPoints: 1900}
}

func TestCatalogRepositoryCreatesNormalizedGoodsAndServices(t *testing.T) {
	t.Parallel()
	repo := openMigratedStore(t).CatalogRepository()
	for _, input := range []CatalogInput{
		{Number: " G-001 ", Kind: " good ", Title: " Paper ", Unit: " piece ", UnitPriceMinor: 0, TaxRateBasisPoints: 0},
		{Number: "S-001", Kind: "service", Title: "Consulting", Unit: "hours", UnitPriceMinor: 12500, TaxRateBasisPoints: 10000},
	} {
		item, err := repo.Create(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		if item.ID == "" || item.ID == item.Number || !item.Active || item.Version != 1 {
			t.Fatalf("Create() = %#v", item)
		}
		if item.Number != "G-001" && item.Number != "S-001" {
			t.Fatalf("number was not normalized: %#v", item)
		}
	}
	item, err := repo.Get(context.Background(), mustCatalog(t, repo, validCatalog("G-002", "Pins")).ID)
	if err != nil {
		t.Fatal(err)
	}
	if item.Unit != "piece" {
		t.Fatalf("unit = %q, want piece", item.Unit)
	}
}

func TestCatalogRepositoryRejectsDuplicateAndInvalidValues(t *testing.T) {
	t.Parallel()
	repo := openMigratedStore(t).CatalogRepository()
	if _, err := repo.Create(context.Background(), validCatalog("G-001", "Paper")); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Create(context.Background(), validCatalog("G-001", "Other")); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate = %v", err)
	}
	for _, input := range []CatalogInput{
		{Number: "G-2", Kind: "bundle", Title: "Bad", Unit: "piece", TaxRateBasisPoints: 1900},
		{Number: "G-3", Kind: "good", Title: "Bad", Unit: "piece", UnitPriceMinor: -1, TaxRateBasisPoints: 1900},
		{Number: "G-4", Kind: "good", Title: "Bad", Unit: "piece", TaxRateBasisPoints: -1},
		{Number: "G-5", Kind: "good", Title: "Bad", Unit: "piece", TaxRateBasisPoints: 10001},
	} {
		if _, err := repo.Create(context.Background(), input); !IsValidationError(err) {
			t.Fatalf("invalid input error = %v", err)
		}
	}
}

func TestCatalogRepositorySearchPaginationAndVersions(t *testing.T) {
	t.Parallel()
	repo := openMigratedStore(t).CatalogRepository()
	ctx := context.Background()
	for _, input := range []CatalogInput{validCatalog("G-010", "Zulu"), validCatalog("G-002", "Éclair"), validCatalog("G-001", "École")} {
		if _, err := repo.Create(ctx, input); err != nil {
			t.Fatal(err)
		}
	}
	first, err := repo.List(ctx, CatalogListOptions{Search: "é", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.Items[0].Number != "G-002" || first.NextCursor == "" {
		t.Fatalf("first page = %#v", first)
	}
	second, err := repo.List(ctx, CatalogListOptions{Search: "é", Limit: 1, Cursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].Number != "G-001" || second.NextCursor != "" {
		t.Fatalf("second page = %#v", second)
	}
	updated, err := repo.Update(ctx, first.Items[0].ID, first.Items[0].Version, validCatalog("G-002", "Éclair updated"))
	if err != nil || updated.Version != 2 {
		t.Fatalf("update = %#v, %v", updated, err)
	}
	if _, err := repo.Update(ctx, updated.ID, 1, validCatalog("G-002", "stale")); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update = %v", err)
	}
}

func TestCatalogRepositoryArchivesAndRestoresHistoricalReference(t *testing.T) {
	t.Parallel()
	s := openMigratedStore(t)
	repo := s.CatalogRepository()
	ctx := context.Background()
	created := mustCatalog(t, repo, validCatalog("G-001", "Paper"))
	if _, err := s.DB().ExecContext(ctx, `INSERT INTO customers (id, number, display_name, country, preferred_language, currency, payment_terms_days) VALUES ('customer-1','C-1','Acme','DE','de','EUR',14)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(ctx, `INSERT INTO invoices (id, customer_id, state, currency) VALUES ('invoice-1','customer-1','draft','EUR')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(ctx, `INSERT INTO invoice_items (id, invoice_id, catalog_item_id, position, title_snapshot, quantity_scaled, net_unit_price_minor, tax_rate_scaled, net_total_minor, tax_total_minor, gross_total_minor) VALUES ('item-1','invoice-1',?,1,'Paper',1000,0,1900,0,0,0)`, created.ID); err != nil {
		t.Fatal(err)
	}
	archived, err := repo.Archive(ctx, created.ID, created.Version)
	if err != nil {
		t.Fatal(err)
	}
	if archived.Active || archived.Version != 2 {
		t.Fatalf("archive = %#v", archived)
	}
	var reference string
	if err := s.DB().QueryRowContext(ctx, `SELECT catalog_item_id FROM invoice_items WHERE id='item-1'`).Scan(&reference); err != nil || reference != created.ID {
		t.Fatalf("reference = %q, %v", reference, err)
	}
	restored, err := repo.Restore(ctx, archived.ID, archived.Version)
	if err != nil || !restored.Active || restored.Version != 3 {
		t.Fatalf("restore = %#v, %v", restored, err)
	}
}

func TestCatalogRepositoryCapsPageAndEscapesSearch(t *testing.T) {
	t.Parallel()
	repo := openMigratedStore(t).CatalogRepository()
	ctx := context.Background()
	for i := 0; i < 102; i++ {
		title := "Item"
		if i == 0 {
			title = "100%_! literal"
		}
		if _, err := repo.Create(ctx, validCatalog(fmt.Sprintf("G-%03d", i), title)); err != nil {
			t.Fatal(err)
		}
	}
	page, err := repo.List(ctx, CatalogListOptions{Limit: 1000})
	if err != nil || len(page.Items) != 100 || page.NextCursor == "" {
		t.Fatalf("page = %#v, %v", page, err)
	}
	page, err = repo.List(ctx, CatalogListOptions{Search: "100%_! literal"})
	if err != nil || len(page.Items) != 1 || page.Items[0].Number != "G-000" {
		t.Fatalf("literal search = %#v, %v", page, err)
	}
}

func mustCatalog(t *testing.T, repo *CatalogRepository, input CatalogInput) CatalogItem {
	t.Helper()
	item, err := repo.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	return item
}
