package store

import (
	"context"
	"testing"
)

func rejectAuditInserts(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.DB().Exec(`CREATE TRIGGER reject_audit_insert BEFORE INSERT ON audit_events BEGIN SELECT RAISE(FAIL, 'audit unavailable'); END`); err != nil {
		t.Fatal(err)
	}
}

func mutationAudit(action, targetType string) AuditEvent {
	return AuditEvent{ActorSubject: "subject-admin", Action: action, TargetType: targetType, Result: "success", RequestID: "request-1"}
}

func TestCompanyAuditedSaveRollsBackWhenAuditInsertFails(t *testing.T) {
	t.Parallel()
	s := openMigratedStore(t)
	repo := s.CompanyRepository()
	ctx := context.Background()
	original, err := repo.Save(ctx, CompanyInput{LegalName: "Original", Country: "DE", Currency: "EUR", DefaultLanguage: "de", BrandColor: "#5B9BD5"})
	if err != nil {
		t.Fatal(err)
	}
	rejectAuditInserts(t, s)

	if _, err = repo.SaveAudited(ctx, CompanyInput{LegalName: "Changed", Country: "DE", Currency: "EUR", DefaultLanguage: "de", BrandColor: "#5B9BD5"}, mutationAudit("company.saved", "company")); err == nil {
		t.Fatal("SaveAudited() error = nil, want audit failure")
	}
	got, err := repo.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != original.ID || got.LegalName != original.LegalName {
		t.Fatalf("company changed despite audit failure: %#v", got)
	}
}

func TestCustomerAuditedMutationsRollBackWhenAuditInsertFails(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func(context.Context, *CustomerRepository, Customer) error
		assert func(*testing.T, *CustomerRepository, Customer)
	}{
		{
			name: "create",
			mutate: func(ctx context.Context, repo *CustomerRepository, _ Customer) error {
				_, err := repo.CreateAudited(ctx, validCustomer("C-NEW", "New"), mutationAudit("customer.created", "customer"))
				return err
			},
			assert: func(t *testing.T, repo *CustomerRepository, _ Customer) {
				page, err := repo.List(context.Background(), CustomerListOptions{Search: "C-NEW", IncludeArchived: true})
				if err != nil || len(page.Customers) != 0 {
					t.Fatalf("created customer survived rollback: %#v, %v", page, err)
				}
			},
		},
		{
			name: "update",
			mutate: func(ctx context.Context, repo *CustomerRepository, original Customer) error {
				_, err := repo.UpdateAudited(ctx, original.ID, original.Version, validCustomer(original.Number, "Changed"), mutationAudit("customer.updated", "customer"))
				return err
			},
			assert: func(t *testing.T, repo *CustomerRepository, original Customer) {
				got, err := repo.Get(context.Background(), original.ID)
				if err != nil || got.DisplayName != original.DisplayName || got.Version != original.Version {
					t.Fatalf("updated customer survived rollback: %#v, %v", got, err)
				}
			},
		},
		{
			name: "archive",
			mutate: func(ctx context.Context, repo *CustomerRepository, original Customer) error {
				_, err := repo.ArchiveAudited(ctx, original.ID, original.Version, mutationAudit("customer.archived", "customer"))
				return err
			},
			assert: func(t *testing.T, repo *CustomerRepository, original Customer) {
				got, err := repo.Get(context.Background(), original.ID)
				if err != nil || !got.Active || got.Version != original.Version {
					t.Fatalf("archived customer survived rollback: %#v, %v", got, err)
				}
			},
		},
		{
			name: "restore",
			mutate: func(ctx context.Context, repo *CustomerRepository, original Customer) error {
				archived, err := repo.Archive(ctx, original.ID, original.Version)
				if err != nil {
					return err
				}
				_, err = repo.RestoreAudited(ctx, archived.ID, archived.Version, mutationAudit("customer.restored", "customer"))
				return err
			},
			assert: func(t *testing.T, repo *CustomerRepository, original Customer) {
				got, err := repo.Get(context.Background(), original.ID)
				if err != nil || got.Active || got.Version != original.Version+1 {
					t.Fatalf("restored customer survived rollback: %#v, %v", got, err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openMigratedStore(t)
			repo := s.CustomerRepository()
			original, err := repo.Create(context.Background(), validCustomer("C-001", "Original"))
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "restore" {
				// The restore case performs its setup archive inside mutate before the
				// audited restore, so install the trigger immediately afterward there.
				archived, archiveErr := repo.Archive(context.Background(), original.ID, original.Version)
				if archiveErr != nil {
					t.Fatal(archiveErr)
				}
				original = archived
				tc.mutate = func(ctx context.Context, repo *CustomerRepository, archived Customer) error {
					_, restoreErr := repo.RestoreAudited(ctx, archived.ID, archived.Version, mutationAudit("customer.restored", "customer"))
					return restoreErr
				}
				tc.assert = func(t *testing.T, repo *CustomerRepository, archived Customer) {
					got, getErr := repo.Get(context.Background(), archived.ID)
					if getErr != nil || got.Active || got.Version != archived.Version {
						t.Fatalf("restored customer survived rollback: %#v, %v", got, getErr)
					}
				}
			}
			rejectAuditInserts(t, s)
			if err := tc.mutate(context.Background(), repo, original); err == nil {
				t.Fatal("audited mutation error = nil, want audit failure")
			}
			tc.assert(t, repo, original)
		})
	}
}

func TestCatalogAuditedMutationsRollBackWhenAuditInsertFails(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		setup  func(context.Context, *CatalogRepository, CatalogItem) (CatalogItem, error)
		mutate func(context.Context, *CatalogRepository, CatalogItem) error
		check  func(CatalogItem, CatalogItem) bool
	}{
		{"create", nil, func(ctx context.Context, repo *CatalogRepository, _ CatalogItem) error {
			_, err := repo.CreateAudited(ctx, validCatalog("G-NEW", "New"), mutationAudit("catalog.created", "catalog_item"))
			return err
		}, func(_, got CatalogItem) bool { return got.ID == "" }},
		{"update", nil, func(ctx context.Context, repo *CatalogRepository, item CatalogItem) error {
			_, err := repo.UpdateAudited(ctx, item.ID, item.Version, validCatalog(item.Number, "Changed"), mutationAudit("catalog.updated", "catalog_item"))
			return err
		}, func(want, got CatalogItem) bool { return got.Title == want.Title && got.Version == want.Version }},
		{"archive", nil, func(ctx context.Context, repo *CatalogRepository, item CatalogItem) error {
			_, err := repo.ArchiveAudited(ctx, item.ID, item.Version, mutationAudit("catalog.archived", "catalog_item"))
			return err
		}, func(want, got CatalogItem) bool { return got.Active && got.Version == want.Version }},
		{"restore", func(ctx context.Context, repo *CatalogRepository, item CatalogItem) (CatalogItem, error) {
			return repo.Archive(ctx, item.ID, item.Version)
		}, func(ctx context.Context, repo *CatalogRepository, item CatalogItem) error {
			_, err := repo.RestoreAudited(ctx, item.ID, item.Version, mutationAudit("catalog.restored", "catalog_item"))
			return err
		}, func(want, got CatalogItem) bool { return !got.Active && got.Version == want.Version }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openMigratedStore(t)
			repo := s.CatalogRepository()
			original, err := repo.Create(context.Background(), validCatalog("G-001", "Original"))
			if err != nil {
				t.Fatal(err)
			}
			if tc.setup != nil {
				original, err = tc.setup(context.Background(), repo, original)
				if err != nil {
					t.Fatal(err)
				}
			}
			rejectAuditInserts(t, s)
			if err := tc.mutate(context.Background(), repo, original); err == nil {
				t.Fatal("audited mutation error = nil, want audit failure")
			}
			var got CatalogItem
			if tc.name != "create" {
				got, err = repo.Get(context.Background(), original.ID)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				page, listErr := repo.List(context.Background(), CatalogListOptions{Search: "G-NEW", IncludeArchived: true})
				if listErr != nil {
					t.Fatal(listErr)
				}
				if len(page.Items) != 0 {
					got = page.Items[0]
				}
			}
			if !tc.check(original, got) {
				t.Fatalf("catalog mutation survived audit rollback: before=%#v after=%#v", original, got)
			}
		})
	}
}
