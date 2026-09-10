package web

import (
	"strings"
	"testing"
)

func TestSupportedUnitHelpOnCatalogAndInvoice(t *testing.T) {
	h, db := customerApp(t, &fakeAuth{})
	d := finalWebDraft(t, db)
	for _, path := range []string{"/catalog/new", "/invoices/" + d.ID} {
		w := customerRequest(t, h, "GET", path, nil, false)
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
		for _, hint := range []string{"hour/HUR", "minute/MIN", "week/WEE"} {
			if !strings.Contains(w.Body.String(), hint) {
				t.Errorf("%s missing supported unit hint %s", path, hint)
			}
		}
	}
}
