package store

import (
	"context"
	"testing"
)

func TestSupportedUnitsAtCatalogAndDraftWrites(t *testing.T) {
	s, repo, d := populatedDraft(t)
	ctx := context.Background()
	for _, unit := range []string{"fortnight", "unknown-x"} {
		in := validCatalog("U-"+unit, "Unit")
		in.Unit = unit
		if _, e := s.CatalogRepository().Create(ctx, in); !IsValidationError(e) {
			t.Errorf("catalog accepted %s: %v", unit, e)
		}
		line := InvoiceLine{Title: "Work", Unit: unit, QuantityScaled: 10000, UnitPriceMinor: 100, NetMinor: 100, GrossMinor: 100}
		if _, e := repo.AddLine(ctx, d.ID, d.Version, line, InvoiceTotals{NetMinor: 100, GrossMinor: 100}); !IsValidationError(e) {
			t.Errorf("draft accepted %s: %v", unit, e)
		}
	}
}
func TestSupportedUnitsRejectCatalogAndDraftEdits(t *testing.T) {
	s, repo, d := populatedDraft(t)
	ctx := context.Background()
	item, e := s.CatalogRepository().Create(ctx, validCatalog("U-EDIT", "Unit"))
	if e != nil {
		t.Fatal(e)
	}
	invalid := item.CatalogInput
	invalid.Unit = "unknown-x"
	if _, e = s.CatalogRepository().Update(ctx, item.ID, item.Version, invalid); !IsValidationError(e) {
		t.Fatal("catalog edit accepted unknown unit", e)
	}
	line := d.Lines[0]
	line.Unit = "unknown-x"
	if _, e = repo.UpdateLine(ctx, d.ID, d.Version, line, InvoiceTotals{NetMinor: d.NetMinor, TaxMinor: d.TaxMinor, GrossMinor: d.GrossMinor}); !IsValidationError(e) {
		t.Fatal("draft edit accepted unknown unit", e)
	}
	unchanged, e := repo.GetDraft(ctx, d.ID)
	if e != nil || unchanged.Lines[0].Unit != d.Lines[0].Unit || unchanged.Version != d.Version {
		t.Fatal("rejected edit changed the draft", unchanged, e)
	}
}
