package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kiefer-networks/invoice-generator/internal/invoicing"
	"github.com/kiefer-networks/invoice-generator/internal/store"
)

func finalWebDraft(t *testing.T, s *store.Store) invoicing.Draft {
	t.Helper()
	ctx := context.Background()
	_, err := s.CompanyRepository().Save(ctx, store.CompanyInput{LegalName: "Issuer", AddressLine1: "Issuer Road", PostalCode: "10115", City: "Berlin", Country: "DE", TaxNumber: "123", IBAN: "DE123", BankName: "Invoice Bank", StandardNotes: "Payment note", DefaultLanguage: "de", Currency: "EUR", BrandColor: "#123456", InvoicePrefix: "INV"})
	if err != nil {
		t.Fatal(err)
	}
	c := invoiceCustomerFixture(t, s, "FINAL", "Recipient")
	d, err := invoicing.NewDraftService(s).Create(ctx, c.ID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	d, err = invoicing.NewDraftService(s).AddManualLine(ctx, d.ID, d.Version, invoicing.DraftLineInput{Title: "Advice", Unit: "hour", QuantityScaled: 10000, UnitPriceMinor: 100, TaxRateBasisPoints: 1900})
	if err != nil {
		t.Fatal(err)
	}
	d, err = invoicing.NewDraftService(s).SetServiceDate(ctx, d.ID, d.Version, "2026-08-31")
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func reviewKey(t *testing.T, body string) string {
	t.Helper()
	m := regexp.MustCompile(`name="idempotency_key" value="([^"]+)"`).FindStringSubmatch(body)
	if len(m) != 2 {
		t.Fatalf("missing key in %s", body)
	}
	return m[1]
}
func TestInvoiceFinalizeReviewConfirmationAndFrozenDetail(t *testing.T) {
	h, s := customerApp(t, &fakeAuth{})
	request := invoiceHTTP(t, h)
	d := finalWebDraft(t, s)
	path := "/invoices/" + d.ID
	code, _, body := request("GET", path+"/review", nil, false)
	if code != 200 {
		t.Fatalf("review=%d %s", code, body)
	}
	for _, want := range []string{"Recipient", "1.19", "Tax breakdown", "Ready to finalize", "Confirm finalization", "DE123", "Payment note", `name="confirm"`, `name="csrf_token"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s", want)
		}
	}
	key := reviewKey(t, body)
	form := url.Values{"idempotency_key": {key}, "version": {fmt.Sprint(d.Version)}}
	code, _, _ = request("POST", path+"/finalize", form, false)
	if code != 400 {
		t.Fatalf("unconfirmed=%d", code)
	}
	form.Set("confirm", "yes")
	code, _, _ = request("POST", path+"/finalize?cursor=-1", form, true)
	if code != 400 {
		t.Fatalf("navigation=%d", code)
	}
	var number *string
	if err := s.DB().QueryRow(`SELECT number FROM invoices WHERE id=?`, d.ID).Scan(&number); err != nil {
		t.Error(err)
	}
	if number != nil {
		t.Fatal("invalid request wrote number")
	}
	code, head, body := request("POST", path+"/finalize", form, true)
	if code != 200 || head.Get("HX-Redirect") != path {
		t.Fatalf("finalize=%d %s %s", code, head, body)
	}
	code, head, _ = request("POST", path+"/finalize", form, false)
	if code != 303 || head.Get("Location") != path {
		t.Fatalf("retry=%d %s", code, head)
	}
	code, _, body = request("GET", path, nil, false)
	if code != 200 || !strings.Contains(body, "INV-") || strings.Contains(body, "Change customer") || !strings.Contains(body, "Mark paid") {
		t.Fatalf("detail=%d %s", code, body)
	}
	code, _, _ = request("GET", path+"/paid", nil, false)
	if code != 405 {
		t.Fatalf("unsafe get=%d", code)
	}
	code, _, body = request("GET", path+"/cancel", nil, true)
	if code != 200 || !strings.Contains(body, `name="reason"`) || !strings.Contains(body, "Confirm cancellation") {
		t.Fatalf("cancel review=%d %s", code, body)
	}
	code, _, body = request("POST", path+"/cancel", url.Values{"confirm": {"yes"}, "reason": {""}}, true)
	if code != 400 || !strings.Contains(body, `aria-live="assertive"`) {
		t.Fatalf("cancel validation=%d %s", code, body)
	}
	code, _, _ = request("POST", path+"/cancel", url.Values{"confirm": {"yes"}, "reason": {"Wrong amount"}}, false)
	if code != 303 {
		t.Fatal(code)
	}
	code, head, _ = request("POST", path+"/correction", url.Values{"confirm": {"yes"}}, false)
	if code != 303 || head.Get("Location") == path {
		t.Fatalf("correction=%d %s", code, head)
	}
}
func TestInvoiceFinalizationRoutesRequireAuthenticationCSRFAndMethods(t *testing.T) {
	for _, suffix := range []string{"review", "finalize", "paid", "cancel", "correction"} {
		h, _ := customerApp(t, &fakeAuth{authenticateErr: context.Canceled})
		if w := customerRequest(t, h, "GET", "/invoices/unknown/"+suffix, nil, false); w.Code != http.StatusFound {
			t.Fatalf("auth %s=%d", suffix, w.Code)
		}
		h, _ = customerApp(t, &fakeAuth{csrfErr: context.Canceled})
		if w := customerRequest(t, h, "POST", "/invoices/unknown/"+suffix, url.Values{"confirm": {"yes"}}, false); w.Code != 403 {
			t.Fatalf("CSRF %s=%d", suffix, w.Code)
		}
	}
}
func TestInvoiceFinalizedListAndAuditProvenance(t *testing.T) {
	h, s := customerApp(t, &fakeAuth{})
	d := finalWebDraft(t, s)
	request := invoiceHTTP(t, h)
	path := "/invoices/" + d.ID
	_, _, body := request("GET", path+"/review", nil, false)
	key := reviewKey(t, body)
	code, _, _ := request("POST", path+"/finalize", url.Values{"idempotency_key": {key}, "confirm": {"yes"}}, false)
	if code != 303 {
		t.Fatal(code)
	}
	code, _, body = request("GET", "/invoices?state=finalized", nil, false)
	if code != 200 || !strings.Contains(body, "INV-") {
		t.Fatalf("finalized list=%d %s", code, body)
	}
	var actor, correlation string
	if err := s.DB().QueryRow(`SELECT actor_subject,request_id FROM audit_events WHERE action='invoice.finalized'`).Scan(&actor, &correlation); err != nil || actor == "" || correlation == "" {
		t.Fatalf("audit actor=%q request=%q error=%v", actor, correlation, err)
	}
}
func TestInvoiceFinalizedNavigationAndServerControlledFields(t *testing.T) {
	h, s := customerApp(t, &fakeAuth{})
	d := finalWebDraft(t, s)
	request := invoiceHTTP(t, h)
	path := "/invoices/" + d.ID
	_, _, body := request("GET", path+"/review", nil, false)
	key := reviewKey(t, body)
	form := url.Values{"idempotency_key": {key}, "confirm": {"yes"}, "number": {"EVIL-1"}, "issue_date": {"1900-01-01"}, "due_date": {"1900-01-01"}, "state": {"paid"}, "gross_total_minor": {"0"}}
	code, _, _ := request("POST", path+"/finalize", form, false)
	if code != 303 {
		t.Fatal(code)
	}
	code, _, body = request("GET", path+"?state=finalized", nil, false)
	if code != 200 || strings.Contains(body, "EVIL-1") || strings.Contains(body, "1900-01-01") || !strings.Contains(body, "1.19") {
		t.Fatalf("detail=%d %s", code, body)
	}
	code, _, body = request("POST", path+"/paid?state=finalized", url.Values{"confirm": {"yes"}, "paid_at": {"1900-01-01"}}, false)
	if code != 303 {
		t.Fatalf("paid=%d %s", code, body)
	}
}
func TestInvoiceFinalizeRejectsDifferentKeyAfterCommit(t *testing.T) {
	h, s := customerApp(t, &fakeAuth{})
	d := finalWebDraft(t, s)
	request := invoiceHTTP(t, h)
	path := "/invoices/" + d.ID
	_, _, body := request("GET", path+"/review", nil, false)
	key := reviewKey(t, body)
	code, _, _ := request("POST", path+"/finalize", url.Values{"idempotency_key": {key}, "confirm": {"yes"}}, false)
	if code != 303 {
		t.Fatal(code)
	}
	code, _, _ = request("POST", path+"/finalize", url.Values{"idempotency_key": {"different"}, "confirm": {"yes"}}, false)
	if code != 409 {
		t.Fatalf("different key retry=%d", code)
	}
}
