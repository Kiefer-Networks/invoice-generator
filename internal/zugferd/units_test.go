package zugferd

import (
	"strings"
	"testing"
)

func TestGenerateCanonicalUnitsMatchVisibleInvoice(t *testing.T) {
	for _, tc := range []struct{ unit, code string }{{"hour", "HUR"}, {"HUR", "HUR"}, {"day", "DAY"}, {"piece", "C62"}, {"minute", "MIN"}, {"week", "WEE"}} {
		s := exactFixture()
		s.Draft.Lines[0].Unit = tc.unit
		out, e := GenerateSnapshot(s)
		if e != nil {
			t.Fatal(e)
		}
		if !strings.Contains(string(out), `unitCode="`+tc.code+`">1.0000`) {
			t.Errorf("visible %s mapped incorrectly in XML", tc.unit)
		}
		if s.RenderData().Rows[0].Unit != tc.unit {
			t.Fatal("visible unit changed")
		}
	}
}
func TestGenerateLegacyRejectsUnknownUnit(t *testing.T) {
	for _, unit := range []string{"", "unknown-x", "fortnight"} {
		cfg := sampleConfig()
		cfg.Items[0].Unit = unit
		if _, e := GenerateCII(cfg); e == nil {
			t.Errorf("unknown unit %q generated a mislabeled invoice", unit)
		}
	}
}
