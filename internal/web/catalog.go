package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/kiefer-networks/invoice-generator/internal/store"
)

func (a *app) catalog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if a.store == nil {
		http.Error(w, "catalog storage is unavailable", http.StatusServiceUnavailable)
		return
	}
	data, err := a.catalogListViewData(r, pageData{})
	if err != nil {
		http.Error(w, "invalid catalog query", http.StatusBadRequest)
		return
	}
	data = a.withPageData(r, data)
	if isHTMX(r) {
		a.renderTemplate(w, "catalogList", data)
		return
	}
	a.renderTemplate(w, "catalogPage", data)
}
func (a *app) catalogNew(w http.ResponseWriter, r *http.Request) {
	if a.store == nil {
		http.Error(w, "catalog storage is unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		a.renderCatalogPage(w, r, pageData{CatalogInput: defaultCatalogInput(), CatalogAction: "/catalog/new", CatalogTitle: "New catalog item"}, false)
	case http.MethodPost:
		input, err := catalogInputFromRequest(r)
		if err == nil {
			var item store.CatalogItem
			item, err = a.store.CatalogRepository().Create(r.Context(), input)
			if err == nil {
				a.catalogSaved(w, r, item)
				return
			}
		}
		a.renderCatalogError(w, r, pageData{CatalogInput: input, CatalogAction: "/catalog/new", CatalogTitle: "New catalog item", Raw: rawForm(r)}, err)
	default:
		methodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}
func (a *app) catalogRoute(w http.ResponseWriter, r *http.Request) {
	if a.store == nil {
		http.Error(w, "catalog storage is unavailable", http.StatusServiceUnavailable)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/catalog/"), "/")
	if len(parts) < 1 || parts[0] == "" || len(parts) > 2 {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		a.catalogDetail(w, r, id)
		return
	}
	switch parts[1] {
	case "edit":
		a.catalogEdit(w, r, id)
	case "archive":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		a.catalogState(w, r, id, false)
	case "restore":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		a.catalogState(w, r, id, true)
	default:
		http.NotFound(w, r)
	}
}
func (a *app) catalogDetail(w http.ResponseWriter, r *http.Request, id string) {
	item, err := a.store.CatalogRepository().Get(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "unable to load catalog item", http.StatusInternalServerError)
		return
	}
	data := a.withPageData(r, pageData{CatalogItem: &item})
	if isHTMX(r) {
		a.renderTemplate(w, "catalogDetail", data)
		return
	}
	data, err = a.catalogListViewData(r, data)
	if err != nil {
		http.Error(w, "unable to load catalog", http.StatusInternalServerError)
		return
	}
	a.renderTemplate(w, "catalogPage", data)
}
func (a *app) catalogEdit(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		item, err := a.store.CatalogRepository().Get(r.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "unable to load catalog item", http.StatusInternalServerError)
			return
		}
		a.renderCatalogPage(w, r, pageData{CatalogInput: item.CatalogInput, CatalogVersion: item.Version, CatalogAction: "/catalog/" + id + "/edit", CatalogTitle: "Edit catalog item"}, false)
	case http.MethodPost:
		input, err := catalogInputFromRequest(r)
		version, versionErr := formInt(r, "version")
		if err == nil {
			err = versionErr
		}
		if err == nil {
			var item store.CatalogItem
			item, err = a.store.CatalogRepository().Update(r.Context(), id, version, input)
			if err == nil {
				a.catalogSaved(w, r, item)
				return
			}
		}
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		status := http.StatusBadRequest
		if errors.Is(err, store.ErrConflict) {
			status = http.StatusConflict
		}
		if isHTMX(r) {
			w.Header().Set("HX-Retarget", "#catalog-detail")
		}
		w.WriteHeader(status)
		a.renderCatalogPage(w, r, pageData{CatalogInput: input, CatalogVersion: version, CatalogAction: "/catalog/" + id + "/edit", CatalogTitle: "Edit catalog item", Errors: errorFields(err), Raw: rawForm(r)}, isHTMX(r))
	default:
		methodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}
func (a *app) catalogState(w http.ResponseWriter, r *http.Request, id string, active bool) {
	version, err := formInt(r, "version")
	if err == nil {
		if active {
			var item store.CatalogItem
			item, err = a.store.CatalogRepository().Restore(r.Context(), id, version)
			if err == nil {
				a.catalogSaved(w, r, item)
				return
			}
		} else {
			var item store.CatalogItem
			item, err = a.store.CatalogRepository().Archive(r.Context(), id, version)
			if err == nil {
				a.catalogSaved(w, r, item)
				return
			}
		}
	}
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if errors.Is(err, store.ErrConflict) {
		http.Error(w, "catalog item changed; refresh and try again", http.StatusConflict)
		return
	}
	http.Error(w, "invalid catalog request", http.StatusBadRequest)
}
func defaultCatalogInput() store.CatalogInput {
	return store.CatalogInput{Kind: "good", Unit: "piece", TaxRateBasisPoints: 1900}
}
func catalogInputFromRequest(r *http.Request) (store.CatalogInput, error) {
	input := store.CatalogInput{Number: r.Form.Get("number"), Kind: r.Form.Get("kind"), Title: r.Form.Get("title"), Description: r.Form.Get("description"), Unit: r.Form.Get("unit")}
	price, err := parseDecimal(r.Form.Get("unit_price"), 2, "unit_price")
	if err != nil {
		return input, err
	}
	tax, err := parseDecimal(r.Form.Get("tax_rate"), 2, "tax_rate_basis_points")
	input.UnitPriceMinor, input.TaxRateBasisPoints = price, tax
	return input, err
}
func parseDecimal(raw string, decimals int, field string) (int, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, &store.ValidationError{Field: field, Message: "is required"}
	}
	if strings.Count(value, ",")+strings.Count(value, ".") > 1 {
		return 0, &store.ValidationError{Field: field, Message: "must be a decimal number; received: " + raw}
	}
	negative := strings.HasPrefix(value, "-")
	if negative {
		value = strings.TrimPrefix(value, "-")
	}
	value = strings.ReplaceAll(value, ",", ".")
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" || (len(parts) == 2 && len(parts[1]) > decimals) {
		return 0, &store.ValidationError{Field: field, Message: "must have at most " + strconv.Itoa(decimals) + " decimal places; received: " + raw}
	}
	whole, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, &store.ValidationError{Field: field, Message: "must be a decimal number; received: " + raw}
	}
	fractional := 0
	if len(parts) == 2 && parts[1] != "" {
		fractional, err = strconv.Atoi(parts[1])
		if err != nil {
			return 0, &store.ValidationError{Field: field, Message: "must be a decimal number; received: " + raw}
		}
		for len(parts[1]) < decimals {
			fractional *= 10
			parts[1] += "0"
		}
	}
	multiplier := 1
	for i := 0; i < decimals; i++ {
		multiplier *= 10
	}
	result := whole*multiplier + fractional
	if negative {
		return -result, nil
	}
	return result, nil
}
func (a *app) catalogSaved(w http.ResponseWriter, r *http.Request, item store.CatalogItem) {
	if isHTMX(r) {
		w.Header().Set("HX-Retarget", "#catalog-detail")
		w.Header().Set("HX-Push-Url", "/catalog/"+item.ID)
		a.renderTemplate(w, "catalogDetail", a.withPageData(r, pageData{CatalogItem: &item}))
		return
	}
	http.Redirect(w, r, "/catalog/"+item.ID, http.StatusSeeOther)
}
func (a *app) renderCatalogPage(w http.ResponseWriter, r *http.Request, data pageData, fragment bool) {
	data = a.withPageData(r, data)
	if fragment || isHTMX(r) {
		a.renderTemplate(w, "catalogForm", data)
		return
	}
	var err error
	data, err = a.catalogListViewData(r, data)
	if err != nil {
		http.Error(w, "unable to load catalog", http.StatusInternalServerError)
		return
	}
	a.renderTemplate(w, "catalogPage", data)
}
func (a *app) renderCatalogError(w http.ResponseWriter, r *http.Request, data pageData, err error) {
	if isHTMX(r) {
		w.Header().Set("HX-Retarget", "#catalog-detail")
	}
	w.WriteHeader(http.StatusBadRequest)
	data.Errors = errorFields(err)
	a.renderCatalogPage(w, r, data, isHTMX(r))
}
func catalogListOptions(query url.Values) (string, store.CatalogListOptions, error) {
	state := query.Get("state")
	if state == "" {
		state = "active"
	}
	options := store.CatalogListOptions{Search: query.Get("q"), Cursor: query.Get("cursor")}
	switch state {
	case "active":
	case "archived":
		options.ArchivedOnly = true
	case "all":
		options.IncludeArchived = true
	default:
		return "", options, errors.New("invalid state")
	}
	return state, options, nil
}
func catalogListURL(search, state, cursor string) string {
	values := url.Values{}
	if search != "" {
		values.Set("q", search)
	}
	if state == "" || state == "active" {
		values.Set("state", "active")
	} else {
		values.Set("state", state)
	}
	if cursor != "" {
		values.Set("cursor", cursor)
	}
	return "/catalog?" + values.Encode()
}
func (a *app) catalogListViewData(r *http.Request, data pageData) (pageData, error) {
	state, options, err := catalogListOptions(r.URL.Query())
	if err != nil {
		return data, err
	}
	page, err := a.store.CatalogRepository().List(r.Context(), options)
	if err != nil {
		return data, err
	}
	data.Catalog, data.Search, data.CatalogState, data.NextPageURL = page, options.Search, state, catalogListURL(options.Search, state, page.NextCursor)
	return data, nil
}
