package web

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/kiefer-networks/invoice-generator/internal/store"
)

func invoiceForm(customerID string) url.Values { return url.Values{"customer_id": {customerID}} }

func TestInvoiceEditorCreatesAndEditsDraftWithServerTotals(t *testing.T) {
	t.Parallel()
	h, s := customerApp(t, &fakeAuth{})
	ctx := context.Background()
	customer, err := s.CustomerRepository().Create(ctx, store.CustomerInput{Number: "C-001", DisplayName: "Acme", Country: "DE", PreferredLanguage: "de", Currency: "EUR", PaymentTermsDays: 14})
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.CatalogRepository().Create(ctx, store.CatalogInput{Number: "S-001", Kind: "service", Title: "Advice", Unit: "hour", UnitPriceMinor: 1250, TaxRateBasisPoints: 1900})
	if err != nil {
		t.Fatal(err)
	}
	list := customerRequest(t, h, http.MethodGet, "/invoices", nil, false)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "Invoices") || !strings.Contains(list.Body.String(), `aria-label="New invoice"`) {
		t.Fatalf("list=%d %q", list.Code, list.Body.String())
	}
	created := customerRequest(t, h, http.MethodPost, "/invoices/new", invoiceForm(customer.ID), false)
	if created.Code != http.StatusSeeOther || !strings.HasPrefix(created.Header().Get("Location"), "/invoices/") {
		t.Fatalf("new=%d %q", created.Code, created.Header().Get("Location"))
	}
	path := created.Header().Get("Location")
	add := customerRequest(t, h, http.MethodPost, path+"/items/catalog", url.Values{"catalog_id": {item.ID}, "quantity": {"2"}, "version": {"1"}}, true)
	if add.Code != http.StatusOK || !strings.Contains(add.Body.String(), "Advice") || !strings.Contains(add.Body.String(), "29.75") || add.Header().Get("HX-Retarget") != "#invoice-editor" {
		t.Fatalf("add=%d target=%q body=%q", add.Code, add.Header().Get("HX-Retarget"), add.Body.String())
	}
	if !strings.Contains(add.Body.String(), "invoice-totals") {
		t.Fatalf("missing totals fragment: %q", add.Body.String())
	}
}

func TestInvoiceEditorPreservesInvalidInputAndRejectsStaleAndUnsafeNavigation(t *testing.T) {
	t.Parallel()
	h, s := customerApp(t, &fakeAuth{})
	ctx := context.Background()
	customer, err := s.CustomerRepository().Create(ctx, store.CustomerInput{Number: "C-001", DisplayName: "Acme", Country: "DE", PreferredLanguage: "de", Currency: "EUR", PaymentTermsDays: 14})
	if err != nil {
		t.Fatal(err)
	}
	created := customerRequest(t, h, http.MethodPost, "/invoices/new", invoiceForm(customer.ID), false)
	path := created.Header().Get("Location")
	bad := customerRequest(t, h, http.MethodPost, path+"/items/manual", url.Values{"title": {""}, "unit": {"piece"}, "quantity": {"x"}, "unit_price": {"1.00"}, "tax_rate": {"19"}, "version": {"1"}}, false)
	for _, want := range []string{"validation-summary", `aria-live="assertive"`, `tabindex="-1"`, `value="x"`} {
		if bad.Code != http.StatusBadRequest || !strings.Contains(bad.Body.String(), want) {
			t.Fatalf("bad missing %q: %d %q", want, bad.Code, bad.Body.String())
		}
	}
	unsafe := customerRequest(t, h, http.MethodPost, "/invoices/new?state=finalized", invoiceForm(customer.ID), false)
	if unsafe.Code != http.StatusBadRequest {
		t.Fatalf("unsafe=%d", unsafe.Code)
	}
	stale := customerRequest(t, h, http.MethodPost, path+"/items/manual", url.Values{"title": {"Manual"}, "unit": {"piece"}, "quantity": {"1"}, "unit_price": {"1.00"}, "tax_rate": {"19"}, "version": {"99"}}, true)
	if stale.Code != http.StatusConflict || stale.Header().Get("HX-Retarget") != "#invoice-editor" {
		t.Fatalf("stale=%d target=%q", stale.Code, stale.Header().Get("HX-Retarget"))
	}
}

func TestInvoiceEditorRequiresCSRFAndAuthentication(t *testing.T) {
	t.Parallel()
	csrf := &fakeAuth{csrfErr: context.Canceled}
	h, _ := customerApp(t, csrf)
	if got := customerRequest(t, h, http.MethodPost, "/invoices/new", invoiceForm("x"), false); got.Code != http.StatusForbidden {
		t.Fatalf("csrf=%d", got.Code)
	}
	unauth, _ := customerApp(t, &fakeAuth{authenticateErr: context.Canceled})
	if got := customerRequest(t, unauth, http.MethodGet, "/invoices", nil, false); got.Code != http.StatusFound {
		t.Fatalf("auth=%d", got.Code)
	}
}

func TestInvoiceEditorPreviewIsReadOnlyTotalsFragment(t *testing.T) {
	t.Parallel()
	h, s := customerApp(t, &fakeAuth{})
	customer, err := s.CustomerRepository().Create(context.Background(), store.CustomerInput{Number: "C-001", DisplayName: "Acme", Country: "DE", PreferredLanguage: "de", Currency: "EUR", PaymentTermsDays: 14})
	if err != nil {
		t.Fatal(err)
	}
	created := customerRequest(t, h, http.MethodPost, "/invoices/new", invoiceForm(customer.ID), false)
	preview := customerRequest(t, h, http.MethodGet, created.Header().Get("Location")+"/preview", nil, true)
	if preview.Code != http.StatusOK || !strings.Contains(preview.Body.String(), `id="invoice-totals"`) {
		t.Fatalf("preview=%d %q", preview.Code, preview.Body.String())
	}
}

func TestInvoiceEditorReordersLines(t *testing.T) {
	t.Parallel()
	h, s := customerApp(t, &fakeAuth{})
	ctx := context.Background()
	customer, err := s.CustomerRepository().Create(ctx, store.CustomerInput{Number: "C-001", DisplayName: "Acme", Country: "DE", PreferredLanguage: "de", Currency: "EUR", PaymentTermsDays: 14})
	if err != nil {
		t.Fatal(err)
	}
	created := customerRequest(t, h, http.MethodPost, "/invoices/new", invoiceForm(customer.ID), false)
	path := created.Header().Get("Location")
	for i := 0; i < 2; i++ {
		version := strconv.Itoa(i + 1)
		response := customerRequest(t, h, http.MethodPost, path+"/items/manual", url.Values{"title": {"Line " + strconv.Itoa(i)}, "unit": {"piece"}, "quantity": {"1"}, "unit_price": {"1.00"}, "tax_rate": {"19"}, "version": {version}}, false)
		if response.Code != http.StatusSeeOther {
			t.Fatalf("add=%d", response.Code)
		}
	}
	invoiceID := strings.TrimPrefix(path, "/invoices/")
	draft, err := s.InvoiceRepository().GetDraft(ctx, invoiceID)
	if err != nil {
		t.Fatal(err)
	}
	response := customerRequest(t, h, http.MethodPost, path+"/items/reorder", url.Values{"line_ids": {draft.Lines[1].ID, draft.Lines[0].ID}, "version": {"3"}}, false)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("reorder=%d body=%q", response.Code, response.Body.String())
	}
	draft, err = s.InvoiceRepository().GetDraft(ctx, invoiceID)
	if err != nil || draft.Lines[0].Title != "Line 1" {
		t.Fatalf("draft=%#v %v", draft, err)
	}
}

func TestInvoiceEditorMoveControlsReorderWithHTMX(t *testing.T) {
	t.Parallel()
	h, s := customerApp(t, &fakeAuth{})
	ctx := context.Background()
	c, err := s.CustomerRepository().Create(ctx, store.CustomerInput{Number: "C-001", DisplayName: "Acme", Country: "DE", PreferredLanguage: "de", Currency: "EUR", PaymentTermsDays: 14})
	if err != nil {
		t.Fatal(err)
	}
	created := customerRequest(t, h, http.MethodPost, "/invoices/new", invoiceForm(c.ID), false)
	path := created.Header().Get("Location")
	for i := 0; i < 2; i++ {
		response := customerRequest(t, h, http.MethodPost, path+"/items/manual", url.Values{"title": {"Line " + strconv.Itoa(i)}, "unit": {"piece"}, "quantity": {"1"}, "unit_price": {"1.00"}, "tax_rate": {"19"}, "version": {strconv.Itoa(i + 1)}}, false)
		if response.Code != http.StatusSeeOther {
			t.Fatal(response.Code)
		}
	}
	draft, err := s.InvoiceRepository().GetDraft(ctx, strings.TrimPrefix(path, "/invoices/"))
	if err != nil {
		t.Fatal(err)
	}
	response := customerRequest(t, h, http.MethodPost, path+"/items/"+draft.Lines[1].ID+"/up", url.Values{"version": {"3"}}, true)
	if response.Code != http.StatusOK || response.Header().Get("HX-Retarget") != "#invoice-editor" || !strings.Contains(response.Body.String(), `aria-label="Move Line 1 up"`) {
		t.Fatalf("move=%d %q", response.Code, response.Body.String())
	}
}
