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

func catalogForm(number, title string) url.Values {
	return url.Values{"number": {number}, "kind": {"good"}, "title": {title}, "unit": {"piece"}, "unit_price": {"12,50"}, "tax_rate": {"19"}}
}

func TestCatalogRoutesRenderMasterDetailAndCRUD(t *testing.T) {
	t.Parallel()
	h, _ := customerApp(t, &fakeAuth{})
	list := customerRequest(t, h, http.MethodGet, "/catalog", nil, false)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "Goods and services") || !strings.Contains(list.Body.String(), "catalog-detail") || !strings.Contains(list.Body.String(), `aria-label="New catalog item"`) {
		t.Fatalf("list = %d %q", list.Code, list.Body.String())
	}
	newPage := customerRequest(t, h, http.MethodGet, "/catalog/new", nil, false)
	if newPage.Code != http.StatusOK || !strings.Contains(newPage.Body.String(), "New catalog item") {
		t.Fatalf("new = %d %q", newPage.Code, newPage.Body.String())
	}
	created := customerRequest(t, h, http.MethodPost, "/catalog/new", catalogForm("G-001", "Paper"), false)
	if created.Code != http.StatusSeeOther || !strings.HasPrefix(created.Header().Get("Location"), "/catalog/") {
		t.Fatalf("create = %d %q", created.Code, created.Header().Get("Location"))
	}
	location := created.Header().Get("Location")
	detail := customerRequest(t, h, http.MethodGet, location, nil, false)
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), "Paper") || !strings.Contains(detail.Body.String(), "Archive catalog item") {
		t.Fatalf("detail = %d %q", detail.Code, detail.Body.String())
	}
	edit := customerRequest(t, h, http.MethodGet, location+"/edit", nil, false)
	if edit.Code != http.StatusOK || !strings.Contains(edit.Body.String(), "Edit catalog item") {
		t.Fatalf("edit = %d %q", edit.Code, edit.Body.String())
	}
	update := catalogForm("G-001", "Paper updated")
	update.Set("version", "1")
	updated := customerRequest(t, h, http.MethodPost, location+"/edit", update, true)
	if updated.Code != http.StatusOK || updated.Header().Get("HX-Retarget") != "#catalog-detail" || !strings.Contains(updated.Body.String(), "Paper updated") {
		t.Fatalf("HTMX update = %d target=%q body=%q", updated.Code, updated.Header().Get("HX-Retarget"), updated.Body.String())
	}
	id := strings.TrimPrefix(location, "/catalog/")
	archive := customerRequest(t, h, http.MethodPost, "/catalog/"+id+"/archive", url.Values{"version": {"2"}}, false)
	if archive.Code != http.StatusSeeOther || archive.Header().Get("Location") != location {
		t.Fatalf("archive = %d %q", archive.Code, archive.Header().Get("Location"))
	}
	if restored := customerRequest(t, h, http.MethodPost, "/catalog/"+id+"/restore", url.Values{"version": {"3"}}, false); restored.Code != http.StatusSeeOther {
		t.Fatalf("restore=%d", restored.Code)
	}
}

func TestCatalogRoutesRejectInvalidDuplicateStaleAndKeepRawInput(t *testing.T) {
	t.Parallel()
	h, s := customerApp(t, &fakeAuth{})
	invalid := catalogForm("G-001", "Paper")
	invalid.Set("kind", "bundle")
	invalid.Set("unit_price", "-1,00")
	invalid.Set("tax_rate", "100,01")
	bad := customerRequest(t, h, http.MethodPost, "/catalog/new", invalid, false)
	for _, want := range []string{"<!doctype html>", "validation-summary", "bundle", "-1,00", "100,01"} {
		if bad.Code != http.StatusBadRequest || !strings.Contains(bad.Body.String(), want) {
			t.Fatalf("invalid missing %q: %d %q", want, bad.Code, bad.Body.String())
		}
	}
	created, err := s.CatalogRepository().Create(context.Background(), store.CatalogInput{Number: "G-001", Kind: "good", Title: "Paper", Unit: "piece", UnitPriceMinor: 1250, TaxRateBasisPoints: 1900})
	if err != nil {
		t.Fatal(err)
	}
	duplicate := customerRequest(t, h, http.MethodPost, "/catalog/new", catalogForm("G-001", "Other"), false)
	if duplicate.Code != http.StatusBadRequest || !strings.Contains(duplicate.Body.String(), "already exists") {
		t.Fatalf("duplicate=%d %q", duplicate.Code, duplicate.Body.String())
	}
	if _, err := s.CatalogRepository().Update(context.Background(), created.ID, created.Version, store.CatalogInput{Number: "G-001", Kind: "good", Title: "Current", Unit: "piece", UnitPriceMinor: 1250, TaxRateBasisPoints: 1900}); err != nil {
		t.Fatal(err)
	}
	stale := catalogForm("G-001", "Stale")
	stale.Set("version", strconv.Itoa(created.Version))
	conflict := customerRequest(t, h, http.MethodPost, "/catalog/"+created.ID+"/edit", stale, true)
	if conflict.Code != http.StatusConflict || conflict.Header().Get("HX-Retarget") != "#catalog-detail" || !strings.Contains(conflict.Body.String(), "changed") {
		t.Fatalf("conflict=%d target=%q body=%q", conflict.Code, conflict.Header().Get("HX-Retarget"), conflict.Body.String())
	}
}

func TestCatalogRoutesRejectNegativeSubUnitPrice(t *testing.T) {
	t.Parallel()
	h, _ := customerApp(t, &fakeAuth{})
	form := catalogForm("G-001", "Paper")
	form.Set("unit_price", "-0,01")
	response := customerRequest(t, h, http.MethodPost, "/catalog/new", form, false)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "-0,01") {
		t.Fatalf("negative sub-unit price = %d %q", response.Code, response.Body.String())
	}
}

func TestCatalogRoutesRequireCSRFAndAuthenticationAndPreserveFilters(t *testing.T) {
	t.Parallel()
	csrf := &fakeAuth{csrfErr: context.Canceled}
	h, _ := customerApp(t, csrf)
	if got := customerRequest(t, h, http.MethodPost, "/catalog/new", catalogForm("G-001", "Paper"), false); got.Code != http.StatusForbidden {
		t.Fatalf("csrf=%d", got.Code)
	}
	unauthenticated, _ := customerApp(t, &fakeAuth{authenticateErr: context.Canceled})
	if got := customerRequest(t, unauthenticated, http.MethodGet, "/catalog", nil, false); got.Code != http.StatusFound {
		t.Fatalf("auth=%d", got.Code)
	}
	active, s := customerApp(t, &fakeAuth{})
	ctx := context.Background()
	first, err := s.CatalogRepository().Create(ctx, store.CatalogInput{Number: "G-000", Kind: "service", Title: "A&B", Unit: "hour", UnitPriceMinor: 0, TaxRateBasisPoints: 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CatalogRepository().Archive(ctx, first.ID, first.Version); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 26; i++ {
		if _, err := s.CatalogRepository().Create(ctx, store.CatalogInput{Number: "G-" + strconv.Itoa(i), Kind: "good", Title: "A&B", Unit: "piece", UnitPriceMinor: 0, TaxRateBasisPoints: 0}); err != nil {
			t.Fatal(err)
		}
	}
	archived := customerRequest(t, active, http.MethodGet, "/catalog?state=archived", nil, false)
	if archived.Code != http.StatusOK || !strings.Contains(archived.Body.String(), "Restore catalog item") && !strings.Contains(archived.Body.String(), `value="archived" selected`) {
		t.Fatalf("archived=%d %q", archived.Code, archived.Body.String())
	}
	page := customerRequest(t, active, http.MethodGet, "/catalog?q=A%26B&state=active", nil, false)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "q=A%26B") || !strings.Contains(page.Body.String(), "state=active") || !strings.Contains(page.Body.String(), "cursor=") {
		t.Fatalf("filter state=%d %q", page.Code, page.Body.String())
	}
}
