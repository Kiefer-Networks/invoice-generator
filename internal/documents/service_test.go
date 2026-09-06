package documents

import (
	"context"
	"errors"
	"github.com/kiefer-networks/invoice-generator/internal/invoicing"
	"github.com/kiefer-networks/invoice-generator/internal/render"
	"github.com/kiefer-networks/invoice-generator/internal/store"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func serviceFixture(t *testing.T) (*Service, *store.Store, invoicing.Draft) {
	t.Helper()
	ctx := context.Background()
	db, e := store.Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	if e = db.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	_, e = db.CompanyRepository().Save(ctx, store.CompanyInput{LegalName: "Frozen Issuer", AddressLine1: "Main 1", PostalCode: "10115", City: "Berlin", Country: "DE", VATIdentifier: "DE123456789", TaxNumber: "12/34/567", Currency: "EUR", DefaultLanguage: "en", InvoicePrefix: "INV", BrandColor: "#123456"})
	if e != nil {
		t.Fatal(e)
	}
	c, e := db.CustomerRepository().Create(ctx, store.CustomerInput{Number: "C-1", DisplayName: "Buyer", LegalName: "Frozen Buyer GmbH", ContactName: "Person", Email: "buyer@example.com", AddressLine1: "Street 2", PostalCode: "10117", City: "Berlin", Country: "DE", Currency: "EUR", PreferredLanguage: "en", PaymentTermsDays: 14})
	if e != nil {
		t.Fatal(e)
	}
	ds := invoicing.NewDraftService(db)
	d, e := ds.Create(ctx, c.ID, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	d, e = ds.AddManualLine(ctx, d.ID, d.Version, invoicing.DraftLineInput{Title: "Work <script>alert(1)</script>", Description: "Frozen detail", Unit: "hour", QuantityScaled: 10000, UnitPriceMinor: 10000, DiscountBasisPoints: 1000, TaxRateBasisPoints: 1900})
	if e != nil {
		t.Fatal(e)
	}
	d, e = ds.SetServiceDate(ctx, d.ID, d.Version, "2026-08-31")
	if e != nil {
		t.Fatal(e)
	}
	st, e := NewStorage(t.TempDir(), 20<<20)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { st.Close() })
	return New(db, st), db, d
}
func TestPreviewDraftWithoutPersistence(t *testing.T) {
	s, db, d := serviceFixture(t)
	var seen *render.TplData
	s.render = func(ctx context.Context, p *render.TplData, path string) error {
		seen = p
		return errors.New("test renderer unavailable")
	}
	if _, e := s.Preview(context.Background(), d.ID); e == nil {
		t.Fatal("expected unavailable renderer")
	}
	if seen == nil || seen.InvNumber != "" || seen.Status != "DRAFT" || seen.GrossTotal != "107.10" {
		t.Fatal(seen)
	}
	var n int
	db.DB().QueryRow(`SELECT count(*) FROM documents`).Scan(&n)
	if n != 0 {
		t.Fatal(n)
	}
	db.DB().QueryRow(`SELECT count(*) FROM invoice_finalization_keys`).Scan(&n)
	if n != 0 {
		t.Fatal("preview persisted review", n)
	}
}
func TestGenerateFrozenChromeArtifact(t *testing.T) {
	if render.FindChrome() == "" {
		t.Fatal("Chrome is required for integration")
	}
	s, db, d := serviceFixture(t)
	ctx := context.Background()
	fs := invoicing.NewFinalizationService(db)
	key, e := fs.Prepare(ctx, d.ID, d.Version)
	if e != nil {
		t.Fatal(e)
	}
	f, e := fs.Finalize(ctx, d.ID, key)
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.DB().Exec(`UPDATE companies SET legal_name='Changed Issuer'`)
	if e != nil {
		t.Fatal(e)
	}
	job, e := db.DocumentRepository().Claim(ctx, time.Now().Add(time.Second), 5*time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Generate(ctx, job); e != nil {
		t.Fatal(e)
	}
	doc, e := db.DocumentRepository().Get(ctx, job.DocumentID)
	if e != nil || doc.Status != "ready" {
		t.Fatal(doc, e)
	}
	h, e := s.storage.Open(doc.StorageKey, doc.SHA256, doc.Size)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := io.ReadAll(h)
	h.Close()
	if e = validatePDF(b, nil); e != nil {
		t.Fatal(e)
	}
	if e = s.Generate(ctx, job); e != nil {
		t.Fatal("idempotent", e)
	}
	if _, e = db.DocumentRepository().Enqueue(ctx, f.ID); e != nil {
		t.Fatal("ready enqueue", e)
	}
	if os.Getenv("INVOICE_DOCUMENT_QA") != "" {
		path := filepath.Join("..", "..", "tmp", "pdfs")
		os.MkdirAll(path, 0700)
		os.WriteFile(filepath.Join(path, "frozen-invoice.pdf"), b, 0600)
	}
	html, e := render.SnapshotHTML(f.Snapshot.RenderData())
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(html, "<script>alert") || !strings.Contains(html, "Frozen Issuer") || strings.Contains(html, "Changed Issuer") {
		t.Fatal("unsafe or live HTML")
	}
}
