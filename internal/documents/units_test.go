package documents

import (
	"context"
	"encoding/json"
	"github.com/kiefer-networks/invoice-generator/internal/invoicing"
	"github.com/kiefer-networks/invoice-generator/internal/store"
	"strings"
	"testing"
	"time"
)

func TestGenerateHistoricalUnsupportedUnitFailsBeforeArtifact(t *testing.T) {
	s, db, d := serviceFixture(t)
	ctx := context.Background()
	company, e := db.CompanyRepository().Get(ctx)
	if e != nil {
		t.Fatal(e)
	}
	d.Number = "HIST-UNITS"
	d.IssueDate = time.Now().UTC()
	d.Lines[0].Unit = "fortnight"
	snap := invoicing.Snapshot{Draft: d, Company: company.CompanyInput, Kind: "invoice", Language: "en"}
	raw, e := json.Marshal(snap)
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.DB().Exec(`UPDATE invoices SET state='finalized',number=?,company_snapshot='{}',payment_snapshot='{}',locale_snapshot='{}',tax_snapshot='{}',note_snapshot='{}',frozen_snapshot=? WHERE id=?`, d.Number, string(raw), d.ID)
	if e != nil {
		t.Fatal(e)
	}
	j, e := db.DocumentRepository().Claim(ctx, time.Now().Add(time.Second), time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Generate(ctx, j); !store.IsValidationError(e) || !strings.Contains(e.Error(), "unsupported unit") {
		t.Fatalf("historical unit failure unclear: %v", e)
	}
	doc, e := db.DocumentRepository().Get(ctx, j.DocumentID)
	if e != nil || doc.Status == "ready" {
		t.Fatal(doc, e)
	}
	dir, e := s.storage.root.Open(".")
	if e != nil {
		t.Fatal(e)
	}
	defer dir.Close()
	entries, e := dir.ReadDir(-1)
	if e != nil || len(entries) != 0 {
		t.Fatal("partial artifact exists", entries, e)
	}
}
