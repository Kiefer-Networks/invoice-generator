package units

import (
	"testing"
)

func TestCodeCanonicalAliasesAndUnknown(t *testing.T) {
	for _, tc := range []struct{ input, want string }{{"hour", "HUR"}, {"hour(s)", "HUR"}, {"HUR", "HUR"}, {" hur ", "HUR"}, {"Stunde(n)", "HUR"}, {"day", "DAY"}, {"Tag", "DAY"}, {"piece", "C62"}, {"St\u00fcck", "C62"}, {"C62", "C62"}, {"kg", "KGM"}, {"KGM", "KGM"}, {"minute", "MIN"}, {"MIN", "MIN"}, {"week", "WEE"}, {"WEE", "WEE"}, {"month", "MON"}, {"MON", "MON"}, {"km", "KMT"}, {"KMT", "KMT"}, {"m", "MTR"}, {"MTR", "MTR"}, {"l", "LTR"}, {"LTR", "LTR"}, {"m2", "MTK"}, {"MTK", "MTK"}, {"m3", "MTQ"}, {"MTQ", "MTQ"}, {"kWh", "KWH"}, {"KWH", "KWH"}} {
		got, e := Code(tc.input)
		if e != nil || got != tc.want {
			t.Errorf("%q: got %q %v, want %s", tc.input, got, e, tc.want)
		}
	}
	for _, input := range []string{"", "unknown-x", "fortnight", "parsec", "HUR/hour", "<script>"} {
		if got, e := Code(input); e == nil || got != "" {
			t.Errorf("unknown %q silently mapped to %q", input, got)
		}
	}
}
