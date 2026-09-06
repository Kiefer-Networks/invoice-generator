package store

import (
	"context"
	"testing"
)

func TestCompanyRepositorySaveUpsertsSingleton(t *testing.T) {
	t.Parallel()
	s := openMigratedStore(t)
	repo := s.CompanyRepository()
	ctx := context.Background()

	first, err := repo.Save(ctx, CompanyInput{
		LegalName: "  Kiefer Networks GmbH  ", Email: " INFO@EXAMPLE.TEST ", Country: "de",
		Currency: "eur", DefaultLanguage: "DE", BrandColor: "#5b9bd5", PaymentTermsDays: 14,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == "" || first.LegalName != "Kiefer Networks GmbH" || first.Email != "info@example.test" || first.Country != "DE" || first.Currency != "EUR" || first.DefaultLanguage != "de" {
		t.Fatalf("unexpected normalized company: %#v", first)
	}

	second, err := repo.Save(ctx, CompanyInput{
		LegalName: "Kiefer Networks SE", Country: "DE", Currency: "EUR", DefaultLanguage: "de", BrandColor: "#5B9BD5", PaymentTermsDays: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || second.LegalName != "Kiefer Networks SE" || second.PaymentTermsDays != 30 {
		t.Fatalf("Save did not replace singleton: first=%#v second=%#v", first, second)
	}

	got, err := repo.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != first.ID || got.LegalName != "Kiefer Networks SE" {
		t.Fatalf("Get() = %#v", got)
	}
}

func TestCompanyRepositoryRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	repo := openMigratedStore(t).CompanyRepository()
	_, err := repo.Save(context.Background(), CompanyInput{LegalName: "", Country: "DE", Currency: "EUR", DefaultLanguage: "de", BrandColor: "#5B9BD5"})
	if !IsValidationError(err) {
		t.Fatalf("Save() error = %v, want validation error", err)
	}
}
