package invoicing

import (
	"github.com/kiefer-networks/invoice-generator/internal/store"
	"testing"
)

func TestFinalizationRejectsHistoricalUnsupportedUnit(t *testing.T) {
	s := draftStore(t)
	d := readyDraft(t, s, 1)
	company, e := s.CompanyRepository().Get(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	d.Lines[0].Unit = "fortnight"
	if e = ValidateFinalization(d, company.CompanyInput); !store.IsValidationError(e) {
		t.Fatal("unsupported historical unit was finalizable", e)
	}
}
