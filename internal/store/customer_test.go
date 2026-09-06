package store

import (
	"context"
	"errors"
	"fmt"
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

func TestCustomerRepositoryUsesUnicodeNormalizedKeysForSearchAndCursor(t *testing.T) {
	t.Parallel()
	repo := openMigratedStore(t).CustomerRepository()
	ctx := context.Background()
	for _, in := range []CustomerInput{
		validCustomer("C-001", "Éclair Studio"),
		validCustomer("C-002", "École Conseil"),
		validCustomer("C-003", "Zulu GmbH"),
	} {
		if _, err := repo.Create(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	page, err := repo.List(ctx, CustomerListOptions{Search: "é", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Customers) != 1 || page.Customers[0].Number != "C-001" || page.NextCursor == "" {
		t.Fatalf("first Unicode page = %#v", page)
	}
	page, err = repo.List(ctx, CustomerListOptions{Search: "é", Limit: 1, Cursor: page.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Customers) != 1 || page.Customers[0].Number != "C-002" || page.NextCursor != "" {
		t.Fatalf("second Unicode page = %#v", page)
	}
}

func TestCustomerRepositoryCapsPageAndEscapesLiteralSearchCharacters(t *testing.T) {
	t.Parallel()
	repo := openMigratedStore(t).CustomerRepository()
	ctx := context.Background()
	for i := 0; i < 102; i++ {
		name := "Customer"
		if i == 0 {
			name = "100%_! literal"
		}
		if _, err := repo.Create(ctx, validCustomer(fmt.Sprintf("C-%03d", i), name)); err != nil {
			t.Fatal(err)
		}
	}
	page, err := repo.List(ctx, CustomerListOptions{Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Customers) != 100 || page.NextCursor == "" {
		t.Fatalf("capped page = %#v", page)
	}
	page, err = repo.List(ctx, CustomerListOptions{Search: "100%_! literal"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Customers) != 1 || page.Customers[0].Number != "C-000" {
		t.Fatalf("literal search = %#v", page)
	}
}

func TestCustomerRepositoryRejectsUnsupportedISOCodes(t *testing.T) {
	t.Parallel()
	repo := openMigratedStore(t).CustomerRepository()
	for _, change := range []struct {
		name  string
		input CustomerInput
	}{
		{"country", CustomerInput{Number: "C-001", DisplayName: "Acme", Country: "ZZ", PreferredLanguage: "de", Currency: "EUR", PaymentTermsDays: 14}},
		{"currency", CustomerInput{Number: "C-002", DisplayName: "Acme", Country: "DE", PreferredLanguage: "de", Currency: "ZZZ", PaymentTermsDays: 14}},
	} {
		t.Run(change.name, func(t *testing.T) {
			_, err := repo.Create(context.Background(), change.input)
			if !IsValidationError(err) {
				t.Fatalf("Create() error = %v, want validation error", err)
			}
		})
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
