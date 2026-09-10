package web

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/kiefer-networks/invoice-generator/internal/store"
)

// Exercise the real HTTP listener, middleware, SQLite repositories and templates.
func invoiceHTTP(t *testing.T, h http.Handler) func(string, string, url.Values, bool) (int, http.Header, string) {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	client := srv.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return func(method, path string, form url.Values, hx bool) (int, http.Header, string) {
		t.Helper()
		if form != nil {
			form.Set("csrf_token", "csrf")
		}
		req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		req.Host = "app.example.test"
		req.AddCookie(&http.Cookie{Name: "invoice_session", Value: "session"}) // #nosec G124 -- Request cookies carry only name/value; response-only security attributes are irrelevant to AddCookie.
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		if hx {
			req.Header.Set("HX-Request", "true")
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, res.Header, string(body)
	}
}
func invoiceCustomerFixture(t *testing.T, s *store.Store, number, name string) store.Customer {
	t.Helper()
	c, e := s.CustomerRepository().Create(context.Background(), store.CustomerInput{Number: number, DisplayName: name, AddressLine1: "Billing Street 7", PostalCode: "10115", City: "Berlin", Country: "DE", PreferredLanguage: "de", Currency: "EUR", PaymentTermsDays: 14})
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func invoiceFormHTML(t *testing.T, body, suffix string) string {
	t.Helper()
	for _, f := range regexp.MustCompile(`(?s)<form\b.*?</form>`).FindAllString(body, -1) {
		if strings.Contains(f, suffix) {
			return f
		}
	}
	t.Fatalf("missing form %s", suffix)
	return ""
}
func invoiceLink(t *testing.T, body, label string) string {
	t.Helper()
	m := regexp.MustCompile(`<a[^>]*href="([^"]+)"[^>]*>` + regexp.QuoteMeta(label) + `</a>`).FindStringSubmatch(body)
	if len(m) != 2 {
		t.Fatalf("missing link %s", label)
	}
	return html.UnescapeString(m[1])
}
func TestInvoiceEditorPickerSearchPaginationAndArchivedSelections(t *testing.T) {
	h, s := customerApp(t, &fakeAuth{})
	request := invoiceHTTP(t, h)
	ctx := context.Background()
	var last store.Customer
	var item store.CatalogItem
	for i := 0; i < 105; i++ {
		last = invoiceCustomerFixture(t, s, fmt.Sprintf("C%03d", i), fmt.Sprintf("Customer %03d", i))
		var e error
		item, e = s.CatalogRepository().Create(ctx, store.CatalogInput{Number: fmt.Sprintf("I%03d", i), Kind: "service", Title: fmt.Sprintf("Service %03d", i), Unit: "hour", UnitPriceMinor: 300, TaxRateBasisPoints: 1900})
		if e != nil {
			t.Fatal(e)
		}
	}
	_, _, body := request("GET", "/invoices/new?state=draft&q=Customer", nil, false)
	if !strings.Contains(body, "/assets/invoice.js?") {
		t.Error("new page must load preview event script before HX creation")
	}
	for _, v := range []string{`name="customer_q"`, `Search customers`, `More customers`} {
		if !strings.Contains(body, v) {
			t.Errorf("missing picker control %s", v)
		}
	}
	next := invoiceLink(t, body, "More customers")
	if !strings.Contains(next, "q=Customer") || !strings.Contains(next, "state=draft") {
		t.Error("picker pagination lost invoice navigation")
	}
	_, _, body = request("GET", next, nil, false)
	if !strings.Contains(body, last.ID) {
		t.Error("customer beyond first 100 inaccessible")
	}
	_, _, body = request("GET", "/invoices/new?customer_q=104", nil, true)
	if !strings.Contains(body, last.ID) || strings.Contains(body, ">Customer 000</option>") {
		t.Error("customer search not applied")
	}
	_, headers, _ := request("POST", "/invoices/new", invoiceForm(last.ID), false)
	path := headers.Get("Location")
	_, _, body = request("GET", path, nil, false)
	next = invoiceLink(t, body, "More catalog items")
	_, _, body = request("GET", next, nil, false)
	if !strings.Contains(body, item.ID) {
		t.Error("catalog beyond first 100 inaccessible")
	}
	_, _, body = request("GET", path+"?catalog_q=104", nil, true)
	if !strings.Contains(body, item.ID) || strings.Contains(body, ">Service 000</option>") {
		t.Error("catalog search not applied")
	}
	code, _, _ := request("POST", path+"/items/catalog", url.Values{"catalog_id": {item.ID}, "quantity": {"0.3333"}, "version": {"1"}}, true)
	if code != 200 {
		t.Fatal(code)
	}
	if _, e := s.CustomerRepository().Archive(ctx, last.ID, last.Version); e != nil {
		t.Fatal(e)
	}
	if _, e := s.CatalogRepository().Archive(ctx, item.ID, item.Version); e != nil {
		t.Fatal(e)
	}
	_, _, body = request("GET", path, nil, false)
	customerForm := invoiceFormHTML(t, body, "/customer")
	if !strings.Contains(customerForm, `value="`+last.ID+`" selected`) {
		t.Error("archived selected customer missing")
	}
	catalogForm := invoiceFormHTML(t, body, "/items/catalog")
	if !strings.Contains(catalogForm, item.ID) || !strings.Contains(catalogForm, "archived") {
		t.Error("archived catalog reference missing")
	}
}
func TestInvoiceEditorNavigationSurvivesLinksWritesAndInvalidMoves(t *testing.T) {
	h, s := customerApp(t, &fakeAuth{})
	request := invoiceHTTP(t, h)
	c := invoiceCustomerFixture(t, s, "C", "Acme")
	query := "?cursor=0&q=Acme&state=draft"
	_, headers, _ := request("POST", "/invoices/new"+query, invoiceForm(c.ID), false)
	location := headers.Get("Location")
	path := strings.Split(location, "?")[0]
	if location != path+query {
		t.Errorf("redirect lost query: %s", location)
	}
	for i := 1; i <= 2; i++ {
		code, head, _ := request("POST", path+"/items/manual"+query, url.Values{"title": {fmt.Sprint("Line", i)}, "unit": {"hour"}, "quantity": {"0.3333"}, "unit_price": {"3.00"}, "tax_rate": {"19"}, "version": {fmt.Sprint(i)}}, true)
		if code != 200 {
			t.Fatal(code)
		}
		if head.Get("HX-Push-Url") != path+query {
			t.Error("HX URL lost query")
		}
	}
	_, _, body := request("GET", path+query, nil, false)
	if !strings.Contains(body, `href="`+path+`?cursor=0&amp;q=Acme&amp;state=draft"`) {
		t.Error("opening link lost navigation")
	}
	for _, f := range regexp.MustCompile(`(?s)<form\b.*?</form>`).FindAllString(body, -1) {
		if strings.Contains(f, `method="post"`) && strings.Contains(f, "/invoices/") && !strings.Contains(f, `?cursor=0&amp;q=Acme&amp;state=draft`) {
			t.Errorf("action lost query: %s", f)
		}
	}
	d, e := s.InvoiceRepository().GetDraft(context.Background(), strings.TrimPrefix(path, "/invoices/"))
	if e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{"state=finalized", "cursor=broken", "customer_cursor=broken", "catalog_cursor=broken"} {
		code, _, _ := request("GET", path+"?"+bad, nil, false)
		if code != 400 {
			t.Errorf("invalid editor query %s=%d", bad, code)
		}
		code, _, _ = request("POST", path+"/items/"+d.Lines[1].ID+"/up?"+bad, url.Values{"version": {"3"}}, true)
		if code != 400 {
			t.Errorf("invalid move %s=%d", bad, code)
		}
		got, e := s.InvoiceRepository().GetDraft(context.Background(), d.ID)
		if e != nil || !reflect.DeepEqual(got, d) {
			t.Fatalf("invalid navigation mutated draft: %#v %v", got, e)
		}
	}
}

func TestInvoiceEditorHTTPListPagesAndMutationControls(t *testing.T) {
	h, s := customerApp(t, &fakeAuth{})
	request := invoiceHTTP(t, h)
	ctx := context.Background()
	c := invoiceCustomerFixture(t, s, "C", "Acme")
	for i := 0; i < 27; i++ {
		code, _, _ := request("POST", "/invoices/new", invoiceForm(c.ID), false)
		if code != 303 {
			t.Fatal(code)
		}
	}
	_, _, body := request("GET", "/invoices?q=Acme&state=draft", nil, false)
	next := invoiceLink(t, body, "More invoices")
	if next != "/invoices?cursor=25&q=Acme&state=draft" {
		t.Fatalf("next page=%s", next)
	}
	_, _, body = request("GET", next, nil, false)
	links := regexp.MustCompile(`href="(/invoices/[^"?]+\?cursor=25&amp;q=Acme&amp;state=draft)"`).FindAllStringSubmatch(body, -1)
	// One new-draft action and two remaining invoice links.
	if len(links) != 3 {
		t.Fatalf("page links=%v", links)
	}
	target := html.UnescapeString(links[1][1])
	u, e := url.Parse(target)
	if e != nil {
		t.Fatal(e)
	}
	path := u.Path
	query := "?" + u.RawQuery
	replacement := invoiceCustomerFixture(t, s, "R", "Replacement")
	code, head, body := request("POST", path+"/customer"+query, url.Values{"customer_id": {replacement.ID}, "version": {"1"}}, true)
	if code != 200 || head.Get("HX-Push-Url") != target || !strings.Contains(body, `id="invoice-list"`) || !strings.Contains(body, `hx-swap-oob="true"`) {
		t.Fatalf("customer response %d", code)
	}
	_, _, body = request("POST", path+"/items/manual"+query, url.Values{"title": {"Advice"}, "unit": {"hour"}, "quantity": {"0.3333"}, "unit_price": {"3.00"}, "tax_rate": {"19"}, "version": {"2"}}, true)
	d, e := s.InvoiceRepository().GetDraft(ctx, strings.TrimPrefix(path, "/invoices/"))
	if e != nil {
		t.Fatal(e)
	}
	if d.CustomerID != replacement.ID || d.Version != 3 || d.NetMinor != 100 || d.TaxMinor != 19 || d.GrossMinor != 119 || !strings.Contains(body, `value="0.3333"`) {
		t.Fatalf("customer/quantity update: %#v", d)
	}
	for _, action := range []string{"up", "down", "edit", "remove"} {
		f := invoiceFormHTML(t, body, "/items/"+d.Lines[0].ID+"/"+action)
		for _, attr := range []string{`hx-post="`, `hx-target="#invoice-editor"`, `hx-swap="innerHTML"`, `name="csrf_token"`, `name="version"`} {
			if !strings.Contains(f, attr) {
				t.Errorf("%s control missing %s", action, attr)
			}
		}
	}
	code, _, body = request("POST", path+"/items/"+d.Lines[0].ID+"/edit"+query, url.Values{"title": {"Edited advice"}, "description": {"New details"}, "unit": {"day"}, "quantity": {"1.25"}, "unit_price": {"4.00"}, "discount": {"20"}, "tax_rate": {"7"}, "version": {"3"}}, true)
	if code != 200 || !strings.Contains(body, "Edited advice") || !strings.Contains(body, "4.28") {
		t.Fatalf("edit response=%d %s", code, body)
	}
	d, e = s.InvoiceRepository().GetDraft(ctx, d.ID)
	if e != nil || d.Lines[0].QuantityScaled != 12500 || d.Lines[0].Unit != "day" || d.NetMinor != 400 || d.TaxMinor != 28 || d.GrossMinor != 428 {
		t.Fatalf("saved edit=%#v %v", d, e)
	}
	code, head, _ = request("POST", path+"/items/"+d.Lines[0].ID+"/remove"+query, url.Values{"version": {"4"}}, false)
	if code != 303 || head.Get("Location") != target {
		t.Fatal("remove lost navigation")
	}
	d, e = s.InvoiceRepository().GetDraft(ctx, d.ID)
	if e != nil || len(d.Lines) != 0 || d.Version != 5 || d.GrossMinor != 0 {
		t.Fatalf("remove=%#v %v", d, e)
	}
}
func TestInvoiceEditorErrorsStayInOriginatingFormAndKeepWrapper(t *testing.T) {
	h, s := customerApp(t, &fakeAuth{})
	request := invoiceHTTP(t, h)
	c := invoiceCustomerFixture(t, s, "C", "Archived")
	ctx := context.Background()
	if _, e := s.CustomerRepository().Archive(ctx, c.ID, c.Version); e != nil {
		t.Fatal(e)
	}
	for _, hx := range []bool{false, true} {
		code, head, body := request("POST", "/invoices/new", invoiceForm(c.ID), hx)
		if code != 400 || !strings.Contains(body, `value="`+c.ID+`" selected`) {
			t.Errorf("new error lost customer: %d %s", code, body)
		}
		if hx && (head.Get("HX-Reswap") != "innerHTML" || strings.Contains(body, `id="invoice-editor"`)) {
			t.Error("new error swap contract breaks editor wrapper")
		}
	}
	active := invoiceCustomerFixture(t, s, "A", "Active")
	_, headers, body := request("POST", "/invoices/new", invoiceForm(active.ID), true)
	if headers.Get("HX-Reswap") != "innerHTML" || strings.Contains(body, `id="invoice-editor"`) {
		t.Error("new success swap contract breaks editor wrapper")
	}
	path := headers.Get("HX-Push-Url")
	_, _, body = request("POST", path+"/items/manual", url.Values{"title": {"First"}, "unit": {"hour"}, "quantity": {"1"}, "unit_price": {"3.00"}, "tax_rate": {"19"}, "version": {"1"}}, true)
	if strings.Contains(body, "<details open") {
		t.Error("closed details emits boolean open attribute")
	}
	d, e := s.InvoiceRepository().GetDraft(ctx, strings.TrimPrefix(path, "/invoices/"))
	if e != nil {
		t.Fatal(e)
	}
	_, _, body = request("POST", path+"/items/"+d.Lines[0].ID+"/edit", url.Values{"line_id": {d.Lines[0].ID}, "title": {"Edited"}, "unit": {"hour"}, "quantity": {"broken"}, "unit_price": {"3.00"}, "tax_rate": {"19"}, "version": {"2"}}, true)
	if strings.Count(body, `value="broken"`) != 1 || !strings.Contains(body, "<details open") {
		t.Error("line error leaks to other forms or hides edited line")
	}
	item, e := s.CatalogRepository().Create(ctx, store.CatalogInput{Number: "I", Kind: "good", Title: "Item", Unit: "piece"})
	if e != nil {
		t.Fatal(e)
	}
	_, _, body = request("POST", path+"/items/catalog", url.Values{"catalog_id": {item.ID}, "quantity": {"bad-catalog"}, "version": {"2"}}, true)
	f := invoiceFormHTML(t, body, "/items/catalog")
	if !strings.Contains(f, `value="`+item.ID+`" selected`) || strings.Count(body, `value="bad-catalog"`) != 1 {
		t.Error("catalog error lost selection or leaked quantity")
	}
}
func TestInvoiceEditorPreviewInitiallyAdjacentAndDedicated(t *testing.T) {
	h, s := customerApp(t, &fakeAuth{})
	request := invoiceHTTP(t, h)
	c := invoiceCustomerFixture(t, s, "C", "Recipient")
	_, head, _ := request("POST", "/invoices/new", invoiceForm(c.ID), false)
	path := head.Get("Location")
	_, _, body := request("POST", path+"/items/manual", url.Values{"title": {"Consulting"}, "description": {"Detailed advice"}, "unit": {"hour"}, "quantity": {"0.3333"}, "unit_price": {"3.00"}, "discount": {"10"}, "tax_rate": {"19"}, "version": {"1"}}, true)
	if !strings.Contains(body, `hx-swap-oob="true"`) {
		t.Error("mutation missing OOB invoice list")
	}
	_, _, body = request("GET", path, nil, false)
	for _, want := range []string{`class="invoice-workspace"`, `hx-target="#invoice-document-preview"`, `hx-swap="outerHTML"`, `Issuer`, `Billing Street 7`, `10115`, `Berlin`, `Detailed advice`, `0.3333`, `3.00`, `10.00`, `19.00`, `0.90`, `0.17`, `1.07`} {
		if !strings.Contains(body, want) {
			t.Errorf("initial preview missing %q", want)
		}
	}
	if strings.Count(body, `id="invoice-document-preview"`) != 1 || strings.Count(body, `id="invoice-totals"`) != 1 {
		t.Error("duplicate or missing preview/totals ID")
	}
	_, _, preview := request("GET", path+"/preview", nil, true)
	if strings.Contains(preview, `id="invoice-totals"`) || !strings.Contains(preview, `id="invoice-document-preview"`) {
		t.Error("preview must replace its dedicated target")
	}
}
