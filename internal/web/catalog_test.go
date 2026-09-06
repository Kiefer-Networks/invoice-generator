package web

import (
	"context"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/kiefer-networks/invoice-generator/internal/store"
)

func TestCatalogDecimalParserAcceptsExactIntegerMinorUnitsAndRejectsUnsafeSyntax(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		raw  string
		want int
	}{
		{"0", 0}, {"12,50", 1250}, {"12.5", 1250}, {"100", 10000},
	} {
		got, err := parseDecimal(test.raw, 2, "unit_price")
		if err != nil || got != test.want {
			t.Fatalf("parseDecimal(%q) = %d, %v; want %d", test.raw, got, err, test.want)
		}
	}
	for _, raw := range []string{"-0,01", "+1", "--1", "1.-1", "1,2,3", "1.234", "١٢", "999999999999999999999999999999"} {
		if _, err := parseDecimal(raw, 2, "unit_price"); !store.IsValidationError(err) {
			t.Fatalf("parseDecimal(%q) = %v, want validation", raw, err)
		}
	}
	if strconv.IntSize == 64 {
		value := strconv.FormatInt(int64(math.MaxInt)/100, 10) + ".00"
		if _, err := parseDecimal(value, 2, "unit_price"); err != nil {
			t.Fatalf("parse max integer price: %v", err)
		}
		if _, err := parseDecimal(strconv.FormatInt(int64(math.MaxInt)/100+1, 10)+".00", 2, "unit_price"); !store.IsValidationError(err) {
			t.Fatalf("parse overflow = %v", err)
		}
	}
}

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
	detailPath, query, _ := strings.Cut(location, "?")
	detail := customerRequest(t, h, http.MethodGet, location, nil, false)
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), "Paper") || !strings.Contains(detail.Body.String(), "Archive catalog item") {
		t.Fatalf("detail = %d %q", detail.Code, detail.Body.String())
	}
	edit := customerRequest(t, h, http.MethodGet, detailPath+"/edit"+queryPrefix(query), nil, false)
	if edit.Code != http.StatusOK || !strings.Contains(edit.Body.String(), "Edit catalog item") {
		t.Fatalf("edit = %d %q", edit.Code, edit.Body.String())
	}
	update := catalogForm("G-001", "Paper updated")
	update.Set("version", "1")
	updated := customerRequest(t, h, http.MethodPost, detailPath+"/edit"+queryPrefix(query), update, true)
	if updated.Code != http.StatusOK || updated.Header().Get("HX-Retarget") != "#catalog-detail" || !strings.Contains(updated.Body.String(), "Paper updated") {
		t.Fatalf("HTMX update = %d target=%q body=%q", updated.Code, updated.Header().Get("HX-Retarget"), updated.Body.String())
	}
	id := strings.TrimPrefix(detailPath, "/catalog/")
	archive := customerRequest(t, h, http.MethodPost, "/catalog/"+id+"/archive"+queryPrefix(query), url.Values{"version": {"2"}}, false)
	if archive.Code != http.StatusSeeOther || archive.Header().Get("Location") != location {
		t.Fatalf("archive = %d %q", archive.Code, archive.Header().Get("Location"))
	}
	if restored := customerRequest(t, h, http.MethodPost, "/catalog/"+id+"/restore"+queryPrefix(query), url.Values{"version": {"3"}}, false); restored.Code != http.StatusSeeOther {
		t.Fatalf("restore=%d", restored.Code)
	}
}

func queryPrefix(value string) string {
	if value == "" {
		return ""
	}
	return "?" + value
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

func TestCatalogFormsFormatStoredValuesAndAssociateEveryValidationError(t *testing.T) {
	t.Parallel()
	h, s := customerApp(t, &fakeAuth{})
	newPage := customerRequest(t, h, http.MethodGet, "/catalog/new", nil, false)
	if newPage.Code != http.StatusOK || !strings.Contains(newPage.Body.String(), `id="tax_rate" name="tax_rate" inputmode="decimal" value="19"`) {
		t.Fatalf("new catalog defaults=%d %q", newPage.Code, newPage.Body.String())
	}
	item, err := s.CatalogRepository().Create(context.Background(), store.CatalogInput{Number: "G-001", Kind: "service", Title: "Consulting", Description: "Advice", Unit: "hour", UnitPriceMinor: 1250, TaxRateBasisPoints: 1900})
	if err != nil {
		t.Fatal(err)
	}
	edit := customerRequest(t, h, http.MethodGet, "/catalog/"+item.ID+"/edit", nil, false)
	for _, want := range []string{`value="12.50"`, `value="19"`} {
		if !strings.Contains(edit.Body.String(), want) {
			t.Fatalf("formatted catalog view lacks %q: %q", want, edit.Body.String())
		}
	}
	detail := customerRequest(t, h, http.MethodGet, "/catalog/"+item.ID, nil, false)
	for _, want := range []string{"EUR 12.50", "19%"} {
		if !strings.Contains(detail.Body.String(), want) {
			t.Fatalf("formatted detail lacks %q: %q", want, detail.Body.String())
		}
	}
	roundTrip := catalogForm(item.Number, item.Title)
	roundTrip.Set("kind", item.Kind)
	roundTrip.Set("unit", item.Unit)
	roundTrip.Set("unit_price", "12.50")
	roundTrip.Set("tax_rate", "19")
	roundTrip.Set("version", strconv.Itoa(item.Version))
	saved := customerRequest(t, h, http.MethodPost, "/catalog/"+item.ID+"/edit", roundTrip, false)
	if saved.Code != http.StatusSeeOther {
		t.Fatalf("unchanged financial update=%d", saved.Code)
	}
	persisted, err := s.CatalogRepository().Get(context.Background(), item.ID)
	if err != nil || persisted.UnitPriceMinor != 1250 || persisted.TaxRateBasisPoints != 1900 {
		t.Fatalf("financial round trip=%#v, %v", persisted, err)
	}
	form := catalogForm("G-002", "Bad")
	form.Set("description", strings.Repeat("x", 5001))
	bad := customerRequest(t, h, http.MethodPost, "/catalog/new", form, false)
	for _, want := range []string{`href="#description"`, `id="description"`, `aria-invalid="true"`, `aria-describedby="description-error"`, `id="description-error"`} {
		if bad.Code != http.StatusBadRequest || !strings.Contains(bad.Body.String(), want) {
			t.Fatalf("validation markup lacks %q: %d %q", want, bad.Code, bad.Body.String())
		}
	}
	tax := catalogForm("G-003", "Tax")
	tax.Set("tax_rate", "101")
	badTax := customerRequest(t, h, http.MethodPost, "/catalog/new", tax, false)
	for _, want := range []string{`href="#tax_rate"`, `id="tax_rate"`, `aria-describedby="tax_rate-error"`, `id="tax_rate-error"`} {
		if badTax.Code != http.StatusBadRequest || !strings.Contains(badTax.Body.String(), want) {
			t.Fatalf("tax validation markup lacks %q: %d %q", want, badTax.Code, badTax.Body.String())
		}
	}
}

func TestCatalogHTMXSavesRefreshListAndPreservesNavigationState(t *testing.T) {
	t.Parallel()
	h, s := customerApp(t, &fakeAuth{})
	query := "?q=A%26B&state=active&cursor=eyJUaXRsZSI6IkEiLCJOdW1iZXIiOiJHLTAwMCIsIklEIjoieCJ9"
	created := customerRequest(t, h, http.MethodPost, "/catalog/new"+query, catalogForm("G-001", "A&B first"), true)
	pushURL, err := url.Parse(created.Header().Get("HX-Push-Url"))
	if err != nil {
		t.Fatal(err)
	}
	if created.Code != http.StatusOK || !strings.Contains(created.Body.String(), `hx-swap-oob="true"`) || !strings.Contains(created.Body.String(), "A&amp;B first") || pushURL.Query().Get("q") != "A&B" || pushURL.Query().Get("state") != "active" || pushURL.Query().Get("cursor") == "" {
		t.Fatalf("HTMX create=%d push=%q body=%q", created.Code, created.Header().Get("HX-Push-Url"), created.Body.String())
	}
	page, err := s.CatalogRepository().List(context.Background(), store.CatalogListOptions{Search: "A&B"})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("item lookup=%#v %v", page, err)
	}
	item := page.Items[0]
	edit := customerRequest(t, h, http.MethodGet, "/catalog/"+item.ID+"/edit"+query, nil, false)
	if edit.Code != http.StatusOK || !strings.Contains(edit.Body.String(), "q=A%26B") || !strings.Contains(edit.Body.String(), "state=active") {
		t.Fatalf("edit state=%d %q", edit.Code, edit.Body.String())
	}
	update := catalogForm("G-001", "A&B renamed")
	update.Set("version", strconv.Itoa(item.Version))
	updated := customerRequest(t, h, http.MethodPost, "/catalog/"+item.ID+"/edit"+query, update, true)
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), `hx-swap-oob="true"`) || !strings.Contains(updated.Body.String(), "A&amp;B renamed") || !strings.Contains(updated.Header().Get("HX-Push-Url"), "q=A%26B") {
		t.Fatalf("HTMX update=%d push=%q body=%q", updated.Code, updated.Header().Get("HX-Push-Url"), updated.Body.String())
	}
	current, err := s.CatalogRepository().Get(context.Background(), item.ID)
	if err != nil {
		t.Fatal(err)
	}
	archived := customerRequest(t, h, http.MethodPost, "/catalog/"+item.ID+"/archive"+query, url.Values{"version": {strconv.Itoa(current.Version)}}, true)
	if archived.Code != http.StatusOK || !strings.Contains(archived.Body.String(), `hx-swap-oob="true"`) {
		t.Fatalf("HTMX archive=%d %q", archived.Code, archived.Body.String())
	}
	current, err = s.CatalogRepository().Get(context.Background(), item.ID)
	if err != nil {
		t.Fatal(err)
	}
	restored := customerRequest(t, h, http.MethodPost, "/catalog/"+item.ID+"/restore"+query, url.Values{"version": {strconv.Itoa(current.Version)}}, true)
	if restored.Code != http.StatusOK || !strings.Contains(restored.Body.String(), `hx-swap-oob="true"`) || !strings.Contains(restored.Body.String(), "A&amp;B renamed") {
		t.Fatalf("HTMX restore=%d %q", restored.Code, restored.Body.String())
	}
}

func TestCatalogFullPageActionsPreserveEncodedArchivedState(t *testing.T) {
	t.Parallel()
	h, s := customerApp(t, &fakeAuth{})
	item, err := s.CatalogRepository().Create(context.Background(), store.CatalogInput{Number: "G-001", Kind: "good", Title: "A&B", Unit: "piece", UnitPriceMinor: 0, TaxRateBasisPoints: 1900})
	if err != nil {
		t.Fatal(err)
	}
	archived, err := s.CatalogRepository().Archive(context.Background(), item.ID, item.Version)
	if err != nil {
		t.Fatal(err)
	}
	state := "?q=A%26B&state=archived"
	detail := customerRequest(t, h, http.MethodGet, "/catalog/"+item.ID+state, nil, false)
	for _, want := range []string{"q=A%26B", "state=archived", "/restore?"} {
		if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), want) {
			t.Fatalf("archived detail state lacks %q: %d %q", want, detail.Code, detail.Body.String())
		}
	}
	response := customerRequest(t, h, http.MethodPost, "/catalog/"+item.ID+"/restore"+state, url.Values{"version": {strconv.Itoa(archived.Version)}}, false)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("restore=%d", response.Code)
	}
	location, err := url.Parse(response.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if location.Query().Get("q") != "A&B" || location.Query().Get("state") != "archived" {
		t.Fatalf("restore lost state: %q", response.Header().Get("Location"))
	}
}

func TestCatalogMutationsRejectInvalidNavigationBeforeWriting(t *testing.T) {
	t.Parallel()
	for _, navigation := range []string{"?state=invalid", "?cursor=not-a-cursor"} {
		t.Run(navigation, func(t *testing.T) {
			h, s := customerApp(t, &fakeAuth{})
			ctx := context.Background()
			beforeCount := catalogCount(t, s)
			create := customerRequest(t, h, http.MethodPost, "/catalog/new"+navigation, catalogForm("G-new", "New"), false)
			if create.Code != http.StatusBadRequest || catalogCount(t, s) != beforeCount {
				t.Fatalf("create invalid state=%d count=%d", create.Code, catalogCount(t, s))
			}

			item, err := s.CatalogRepository().Create(ctx, store.CatalogInput{Number: "G-001", Kind: "good", Title: "Original", Unit: "piece", UnitPriceMinor: 1250, TaxRateBasisPoints: 1900})
			if err != nil {
				t.Fatal(err)
			}
			update := catalogForm(item.Number, "Changed")
			update.Set("version", strconv.Itoa(item.Version))
			response := customerRequest(t, h, http.MethodPost, "/catalog/"+item.ID+"/edit"+navigation, update, false)
			unchanged, err := s.CatalogRepository().Get(ctx, item.ID)
			if err != nil || response.Code != http.StatusBadRequest || unchanged.Title != "Original" || unchanged.Version != item.Version {
				t.Fatalf("update state=%d item=%#v err=%v", response.Code, unchanged, err)
			}

			archive := customerRequest(t, h, http.MethodPost, "/catalog/"+item.ID+"/archive"+navigation, url.Values{"version": {strconv.Itoa(item.Version)}}, false)
			unchanged, err = s.CatalogRepository().Get(ctx, item.ID)
			if err != nil || archive.Code != http.StatusBadRequest || !unchanged.Active || unchanged.Version != item.Version {
				t.Fatalf("archive state=%d item=%#v err=%v", archive.Code, unchanged, err)
			}

			archived, err := s.CatalogRepository().Archive(ctx, item.ID, item.Version)
			if err != nil {
				t.Fatal(err)
			}
			restore := customerRequest(t, h, http.MethodPost, "/catalog/"+item.ID+"/restore"+navigation, url.Values{"version": {strconv.Itoa(archived.Version)}}, false)
			unchanged, err = s.CatalogRepository().Get(ctx, item.ID)
			if err != nil || restore.Code != http.StatusBadRequest || unchanged.Active || unchanged.Version != archived.Version {
				t.Fatalf("restore state=%d item=%#v err=%v", restore.Code, unchanged, err)
			}
		})
	}
}

func catalogCount(t *testing.T, s *store.Store) int {
	t.Helper()
	var count int
	if err := s.DB().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM catalog_items`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
