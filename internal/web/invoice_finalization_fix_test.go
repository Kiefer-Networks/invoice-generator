package web

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/kiefer-networks/invoice-generator/internal/invoicing"
)

func TestInvoiceServiceDateReviewAndCorrectionRendering(t *testing.T) {
	h, s := customerApp(t, &fakeAuth{})
	d := finalWebDraft(t, s)
	ctx := context.Background()
	request := invoiceHTTP(t, h)
	path := "/invoices/" + d.ID
	c, _ := s.CustomerRepository().Get(ctx, d.CustomerID)
	c.LegalName = "Buyer Legal GmbH"
	c.ContactName = "Pat Buyer"
	c.Email = "buyer@example.test"
	c.VATIdentifier = "DE123"
	c, err := s.CustomerRepository().Update(ctx, c.ID, c.Version, c.CustomerInput)
	if err != nil {
		t.Fatal(err)
	}
	d, err = invoicing.NewDraftService(s).Update(ctx, d.ID, d.Version, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	code, _, body := request("GET", path, nil, false)
	if code != 200 || !strings.Contains(body, `name="service_date"`) {
		t.Fatalf("date editor=%d %s", code, body)
	}
	code, _, body = request("POST", path+"/service-date", url.Values{"version": {fmt.Sprint(d.Version)}, "service_date": {"2026-02-30"}}, true)
	if code != 400 || !strings.Contains(body, "2026-02-30") || !strings.Contains(body, `aria-live="assertive"`) {
		t.Fatalf("date error=%d %s", code, body)
	}
	code, _, _ = request("POST", path+"/service-date", url.Values{"version": {fmt.Sprint(d.Version)}, "service_date": {"2026-08-30"}}, false)
	if code != 303 {
		t.Fatal(code)
	}
	code, _, body = request("GET", path+"/review", nil, false)
	for _, value := range []string{"Buyer Legal GmbH", "Recipient", "Pat Buyer", "buyer@example.test", "DE123", "2026-08-30"} {
		if code != 200 || !strings.Contains(body, value) {
			t.Errorf("review missing %s", value)
		}
	}
	key := reviewKey(t, body)
	code, _, _ = request("POST", path+"/finalize", url.Values{"confirm": {"yes"}, "idempotency_key": {key}}, false)
	if code != 303 {
		t.Fatal(code)
	}
	original, _ := invoicing.NewFinalizationService(s).Get(ctx, d.ID)
	code, header, _ := request("POST", path+"/correction", url.Values{"confirm": {"yes"}}, false)
	if code != 303 {
		t.Fatal(code)
	}
	cp := strings.Split(header.Get("Location"), "?")[0]
	_, _, body = request("GET", cp+"/review", nil, false)
	if !strings.Contains(body, original.Number) || !strings.Contains(body, "Correction") {
		t.Fatalf("correction review=%s", body)
	}
	key = reviewKey(t, body)
	code, _, _ = request("POST", cp+"/finalize", url.Values{"confirm": {"yes"}, "idempotency_key": {key}}, false)
	if code != 303 {
		t.Fatal(code)
	}
	_, _, body = request("GET", cp, nil, false)
	if !strings.Contains(body, original.Number) || !strings.Contains(body, "Correction") {
		t.Fatal("missing immutable correction identity")
	}
}
func TestInvoiceMissingServiceDateCannotFinalizeAndRouteIsSecured(t *testing.T) {
	h, s := customerApp(t, &fakeAuth{})
	d := finalWebDraft(t, s)
	request := invoiceHTTP(t, h)
	path := "/invoices/" + d.ID
	if _, err := s.DB().Exec(`UPDATE invoices SET service_date='' WHERE id=?`, d.ID); err != nil {
		t.Error(err)
	}
	code, _, body := request("GET", path+"/review", nil, false)
	if code != 200 || strings.Contains(body, `name="idempotency_key"`) || !strings.Contains(body, "service or delivery date") {
		t.Fatalf("incomplete review=%d %s", code, body)
	}
	code, _, _ = request("GET", path+"/service-date", nil, false)
	if code != 405 {
		t.Fatal(code)
	}
	secured, _ := customerApp(t, &fakeAuth{csrfErr: context.Canceled})
	if r := customerRequest(t, secured, "POST", path+"/service-date", url.Values{"service_date": {"2026-08-31"}, "version": {"1"}}, false); r.Code != 403 {
		t.Fatalf("CSRF=%d", r.Code)
	}
	unauth, _ := customerApp(t, &fakeAuth{authenticateErr: context.Canceled})
	if r := customerRequest(t, unauth, "POST", path+"/service-date", url.Values{}, false); r.Code != 302 {
		t.Fatalf("auth=%d", r.Code)
	}
}
