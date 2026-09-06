package store

import (
	"context"
	"errors"
	"testing"
)

func validCustomer(number, name string) CustomerInput {
	return CustomerInput{Number: number, DisplayName: name, Country: "de", PreferredLanguage: "DE", Currency: "eur", PaymentTermsDays: 14}
}

func TestCustomerRepositoryCreatesNormalizedOpaqueCustomer(t *testing.T) {
	t.Parallel()
	repo := openMigratedStore(t).CustomerRepository()
	got, err := repo.Create(context.Background(), CustomerInput{
		Number: " C-001 ", DisplayName: "  Acme GmbH ", LegalName: " Acme GmbH ", ContactName: " Ada Lovelace ",
		Email: " SALES@ACME.TEST ", Country: "de", PreferredLanguage: "DE", Currency: "eur", PaymentTermsDays: 14,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID == "" || got.ID == got.Number || got.Version != 1 || !got.Active {
		t.Fatalf("Create() = %#v", got)
	}
	if got.Number != "C-001" || got.DisplayName != "Acme GmbH" || got.Email != "sales@acme.test" || got.Country != "DE" || got.PreferredLanguage != "de" || got.Currency != "EUR" {
		t.Fatalf("Create did not normalize fields: %#v", got)
	}
}

func TestCustomerRepositoryRejectsDuplicateNumber(t *testing.T) {
	t.Parallel()
	repo := openMigratedStore(t).CustomerRepository()
	if _, err := repo.Create(context.Background(), validCustomer("C-001", "Acme")); err != nil {
		t.Fatal(err)
	}
	_, err := repo.Create(context.Background(), validCustomer("C-001", "Other"))
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("Create duplicate error = %v, want ErrDuplicate", err)
	}
}

func TestCustomerRepositoryListsSearchResultsInStableCursorOrder(t *testing.T) {
	t.Parallel()
	repo := openMigratedStore(t).CustomerRepository()
	ctx := context.Background()
	for _, in := range []CustomerInput{
		validCustomer("C-010", "Zulu GmbH"),
		validCustomer("C-002", "Acme Consulting"),
		validCustomer("C-001", "Acme Software"),
	} {
		if _, err := repo.Create(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	page, err := repo.List(ctx, CustomerListOptions{Search: "acme", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Customers) != 1 || page.Customers[0].Number != "C-002" || page.NextCursor == "" {
		t.Fatalf("first page = %#v", page)
	}
	page, err = repo.List(ctx, CustomerListOptions{Search: "acme", Limit: 1000, Cursor: page.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Customers) != 1 || page.Customers[0].Number != "C-001" || page.NextCursor != "" {
		t.Fatalf("second page = %#v", page)
	}
}

func TestCustomerRepositoryUpdatesWithOptimisticVersion(t *testing.T) {
	t.Parallel()
	repo := openMigratedStore(t).CustomerRepository()
	ctx := context.Background()
	created, err := repo.Create(ctx, validCustomer("C-001", "Acme"))
	if err != nil {
		t.Fatal(err)
	}
	updated, err := repo.Update(ctx, created.ID, created.Version, validCustomer("C-001", "Acme Updated"))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Version != 2 || updated.DisplayName != "Acme Updated" {
		t.Fatalf("Update() = %#v", updated)
	}
	_, err = repo.Update(ctx, created.ID, created.Version, validCustomer("C-001", "Stale"))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale Update error = %v, want ErrConflict", err)
	}
}

func TestCustomerRepositoryArchivesAndRestoresWithoutLosingInvoiceReference(t *testing.T) {
	t.Parallel()
	s := openMigratedStore(t)
	repo := s.CustomerRepository()
	ctx := context.Background()
	created, err := repo.Create(ctx, validCustomer("C-001", "Acme"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(ctx, `INSERT INTO invoices (id, customer_id, state, currency) VALUES (?, ?, 'draft', 'EUR')`, "invoice-1", created.ID); err != nil {
		t.Fatal(err)
	}
	archived, err := repo.Archive(ctx, created.ID, created.Version)
	if err != nil {
		t.Fatal(err)
	}
	if archived.Active || archived.Version != 2 {
		t.Fatalf("Archive() = %#v", archived)
	}
	var invoiceCustomer string
	if err := s.DB().QueryRowContext(ctx, `SELECT customer_id FROM invoices WHERE id = 'invoice-1'`).Scan(&invoiceCustomer); err != nil {
		t.Fatal(err)
	}
	if invoiceCustomer != created.ID {
		t.Fatalf("invoice reference changed to %q", invoiceCustomer)
	}
	restored, err := repo.Restore(ctx, created.ID, archived.Version)
	if err != nil {
		t.Fatal(err)
	}
	if !restored.Active || restored.Version != 3 {
		t.Fatalf("Restore() = %#v", restored)
	}
}
