package documents

import (
	"bytes"
	"context"
	"fmt"
	"github.com/kiefer-networks/invoice-generator/internal/invoicing"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGenerateMultiPageMixedTaxCorrection(t *testing.T) {
	s, db, d := serviceFixture(t)
	ctx := context.Background()
	fs := invoicing.NewFinalizationService(db)
	key, e := fs.Prepare(ctx, d.ID, d.Version)
	if e != nil {
		t.Fatal(e)
	}
	original, e := fs.Finalize(ctx, d.ID, key)
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
	correction, e := fs.CreateCorrection(ctx, original.ID)
	if e != nil {
		t.Fatal(e)
	}
	ds := invoicing.NewDraftService(db)
	for i := 0; i < 36; i++ {
		unit, rate := "HUR", int64(1900)
		if i%2 == 0 {
			unit, rate = "piece", 700
		}
		correction, e = ds.AddManualLine(ctx, correction.ID, correction.Version, invoicing.DraftLineInput{Title: fmt.Sprintf("Corrected consulting phase %02d", i+1), Description: "Detailed service documentation and delivery review", Unit: unit, QuantityScaled: 20000, UnitPriceMinor: 12500, DiscountBasisPoints: 1000, TaxRateBasisPoints: rate})
		if e != nil {
			t.Fatal(e)
		}
	}
	key, e = fs.Prepare(ctx, correction.ID, correction.Version)
	if e != nil {
		t.Fatal(e)
	}
	f, e := fs.Finalize(ctx, correction.ID, key)
	if e != nil {
		t.Fatal(e)
	}
	if len(f.Snapshot.Draft.TaxGroups) != 2 || f.Snapshot.Kind != "correction" || f.Snapshot.Correction.OriginalNumber != original.Number {
		t.Fatal("snapshot correction metadata missing")
	}
	job, e = db.DocumentRepository().Claim(ctx, time.Now().Add(time.Second), 5*time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Generate(ctx, job); e != nil {
		t.Fatal(e)
	}
	doc, e := db.DocumentRepository().Get(ctx, job.DocumentID)
	if e != nil {
		t.Fatal(e)
	}
	file, e := s.storage.Open(doc.StorageKey, doc.SHA256, doc.Size)
	if e != nil {
		t.Fatal(e)
	}
	data, e := io.ReadAll(file)
	file.Close()
	if e != nil {
		t.Fatal(e)
	}
	pdf, e := api.ReadAndValidate(bytes.NewReader(data), model.NewDefaultConfiguration())
	if e != nil {
		t.Fatal(e)
	}
	if pdf.PageCount < 2 || pdf.PageCount > 6 {
		t.Fatal("unexpected page count", pdf.PageCount)
	}
	if os.Getenv("INVOICE_DOCUMENT_QA") != "" {
		dir := filepath.Join("..", "..", "tmp", "pdfs")
		if e = os.MkdirAll(dir, 0700); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(dir, "mixed-correction.pdf"), data, 0600); e != nil {
			t.Fatal(e)
		}
		t.Logf("QA correction has %d pages", pdf.PageCount)
	}
}
