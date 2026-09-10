package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/kiefer-networks/invoice-generator/internal/store"
)

func TestHTMXConfigurationAndInvalidCustomerResponseSwap(t *testing.T) {
	t.Parallel()
	h, _ := customerApp(t, &fakeAuth{})
	page := customerRequest(t, h, http.MethodGet, "/customers", nil, false)
	for _, want := range []string{`"responseHandling"`, `"code":"400","swap":true`, `"code":"409","swap":true`} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("HTMX response configuration lacks %q: %q", want, page.Body.String())
		}
	}
	invalid := customerForm("C-001", "Acme")
	invalid.Set("payment_terms_days", "not-a-number")
	response := customerRequest(t, h, http.MethodPost, "/customers/new", invalid, true)
	if response.Code != http.StatusBadRequest || response.Header().Get("HX-Retarget") != "#customer-detail" || !strings.Contains(response.Body.String(), "validation-summary") {
		t.Fatalf("HTMX validation response = %d target=%q body=%q", response.Code, response.Header().Get("HX-Retarget"), response.Body.String())
	}
}

func TestCustomerValidationFullPagePreservesRawFieldStateAndAssociatesErrors(t *testing.T) {
	t.Parallel()
	h, _ := customerApp(t, &fakeAuth{})
	invalid := customerForm("C-001", "Acme")
	invalid.Set("payment_terms_days", "not-a-number")
	invalid.Set("preferred_language", "fr")
	response := customerRequest(t, h, http.MethodPost, "/customers/new", invalid, false)
	for _, want := range []string{"<!doctype html>", "validation-summary", `value="not-a-number"`, "received: not-a-number", `<option value="fr" selected>fr</option>`, `aria-describedby="payment_terms_days-error"`, `id="payment_terms_days-error"`} {
		if !strings.Contains(response.Body.String(), want) {
			t.Fatalf("full validation response lacks %q: %q", want, response.Body.String())
		}
	}
}

func TestCompanyValidationPreservesUnsupportedLanguageAndAssociatesErrors(t *testing.T) {
	t.Parallel()
	h, _ := customerApp(t, &fakeAuth{})
	invalid := url.Values{"legal_name": {"Kiefer Networks GmbH"}, "country": {"DE"}, "currency": {"EUR"}, "default_language": {"fr"}, "brand_color": {"#5B9BD5"}, "payment_terms_days": {"not-a-number"}}
	response := customerRequest(t, h, http.MethodPost, "/settings/company", invalid, false)
	for _, want := range []string{"<!doctype html>", "validation-summary", `value="not-a-number"`, "received: not-a-number", `<option value="fr" selected>fr</option>`, `aria-describedby="payment_terms_days-error"`, `id="payment_terms_days-error"`} {
		if !strings.Contains(response.Body.String(), want) {
			t.Fatalf("company validation response lacks %q: %q", want, response.Body.String())
		}
	}
}

func TestFullCustomerRenderersPreservePaginatedListState(t *testing.T) {
	t.Parallel()
	h, s := customerApp(t, &fakeAuth{})
	ctx := context.Background()
	for i := 0; i < 26; i++ {
		if _, err := s.CustomerRepository().Create(ctx, store.CustomerInput{Number: fmt.Sprintf("C-%03d", i), DisplayName: "Needle Customer", Country: "DE", PreferredLanguage: "de", Currency: "EUR", PaymentTermsDays: 14}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.CustomerRepository().List(ctx, store.CustomerListOptions{Search: "Needle"})
	if err != nil {
		t.Fatal(err)
	}
	id := page.Customers[0].ID
	query := "?q=Needle&state=active"
	for _, target := range []string{"/customers/" + id + query, "/customers/new" + query, "/customers/" + id + "/edit" + query} {
		response := customerRequest(t, h, http.MethodGet, target, nil, false)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "More customers") || !strings.Contains(response.Body.String(), "q=Needle") || !strings.Contains(response.Body.String(), "state=active") {
			t.Fatalf("full renderer %s lost page state: %d %q", target, response.Code, response.Body.String())
		}
	}
	invalid := customerForm("C-new", "Needle Customer")
	invalid.Set("payment_terms_days", "not-a-number")
	validation := customerRequest(t, h, http.MethodPost, "/customers/new"+query, invalid, false)
	if validation.Code != http.StatusBadRequest || !strings.Contains(validation.Body.String(), "More customers") || !strings.Contains(validation.Body.String(), "q=Needle") {
		t.Fatalf("validation page lost list state: %d %q", validation.Code, validation.Body.String())
	}
	current, err := s.CustomerRepository().Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CustomerRepository().Update(ctx, id, current.Version, store.CustomerInput{Number: current.Number, DisplayName: "Needle Customer", Country: "DE", PreferredLanguage: "de", Currency: "EUR", PaymentTermsDays: 14}); err != nil {
		t.Fatal(err)
	}
	stale := customerForm(current.Number, "Needle Customer")
	stale.Set("version", fmt.Sprint(current.Version))
	conflict := customerRequest(t, h, http.MethodPost, "/customers/"+id+"/edit"+query, stale, false)
	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), "More customers") || !strings.Contains(conflict.Body.String(), "q=Needle") {
		t.Fatalf("conflict page lost list state: %d %q", conflict.Code, conflict.Body.String())
	}
}

func TestCustomerConflictUsesLayoutForPagesAndFragmentForHTMX(t *testing.T) {
	t.Parallel()
	h, s := customerApp(t, &fakeAuth{})
	c, err := s.CustomerRepository().Create(context.Background(), store.CustomerInput{Number: "C-001", DisplayName: "Acme", Country: "DE", PreferredLanguage: "de", Currency: "EUR", PaymentTermsDays: 14})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CustomerRepository().Update(context.Background(), c.ID, c.Version, store.CustomerInput{Number: "C-001", DisplayName: "Current", Country: "DE", PreferredLanguage: "de", Currency: "EUR", PaymentTermsDays: 14}); err != nil {
		t.Fatal(err)
	}
	stale := customerForm("C-001", "Stale")
	stale.Set("version", "1")
	page := customerRequest(t, h, http.MethodPost, "/customers/"+c.ID+"/edit", stale, false)
	if page.Code != http.StatusConflict || !strings.Contains(page.Body.String(), "<!doctype html>") || !strings.Contains(page.Body.String(), "validation-summary") {
		t.Fatalf("page conflict = %d %q", page.Code, page.Body.String())
	}
	fragment := customerRequest(t, h, http.MethodPost, "/customers/"+c.ID+"/edit", stale, true)
	if fragment.Code != http.StatusConflict || fragment.Header().Get("HX-Retarget") != "#customer-detail" || strings.Contains(fragment.Body.String(), "<!doctype html>") {
		t.Fatalf("HTMX conflict = %d target=%q body=%q", fragment.Code, fragment.Header().Get("HX-Retarget"), fragment.Body.String())
	}
}

func TestCustomersExposeArchivedFilterAndPreserveQueryInPagination(t *testing.T) {
	t.Parallel()
	h, s := customerApp(t, &fakeAuth{})
	ctx := context.Background()
	first, err := s.CustomerRepository().Create(ctx, store.CustomerInput{Number: "C-000", DisplayName: "A&B Customer", Country: "DE", PreferredLanguage: "de", Currency: "EUR", PaymentTermsDays: 14})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CustomerRepository().Archive(ctx, first.ID, first.Version); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 26; i++ {
		if _, err := s.CustomerRepository().Create(ctx, store.CustomerInput{Number: fmt.Sprintf("C-%03d", i), DisplayName: "A&B Customer", Country: "DE", PreferredLanguage: "de", Currency: "EUR", PaymentTermsDays: 14}); err != nil {
			t.Fatal(err)
		}
	}
	archived := customerRequest(t, h, http.MethodGet, "/customers?state=archived", nil, false)
	if archived.Code != http.StatusOK || !strings.Contains(archived.Body.String(), "A&amp;B Customer") || !strings.Contains(archived.Body.String(), `value="archived" selected`) || !strings.Contains(archived.Body.String(), "Archived") {
		t.Fatalf("archived filter = %d %q", archived.Code, archived.Body.String())
	}
	detail := customerRequest(t, h, http.MethodGet, "/customers/"+first.ID, nil, false)
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), "Restore customer") {
		t.Fatalf("archived restore flow = %d %q", detail.Code, detail.Body.String())
	}
	active := customerRequest(t, h, http.MethodGet, "/customers?q=A%26B+Customer&state=active", nil, false)
	if active.Code != http.StatusOK || !strings.Contains(active.Body.String(), "q=A%26B&#43;Customer") || !strings.Contains(active.Body.String(), "state=active") || !strings.Contains(active.Body.String(), "cursor=") {
		t.Fatalf("pagination does not preserve escaped query/filter: %d %q", active.Code, active.Body.String())
	}
}
