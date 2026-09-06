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
		input := defaultCatalogInput()
		a.renderCatalogPage(w, r, pageData{CatalogInput: input, Raw: catalogDisplayRaw(input), CatalogAction: "/catalog/new" + catalogQuerySuffix(r.URL.Query()), CatalogTitle: "New catalog item"}, false)
	case http.MethodPost:
		if err := a.validateCatalogNavigation(r); err != nil {
			http.Error(w, "invalid catalog query", http.StatusBadRequest)
			return
		}
		input, err := catalogInputFromRequest(r)
		if err == nil {
			var item store.CatalogItem
			item, err = a.store.CatalogRepository().Create(r.Context(), input)
			if err == nil {
				a.catalogSaved(w, r, item)
				return
			}
		}
		a.renderCatalogError(w, r, pageData{CatalogInput: input, CatalogAction: "/catalog/new" + catalogQuerySuffix(r.URL.Query()), CatalogTitle: "New catalog item", Raw: rawForm(r)}, err)
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
	data := a.withCatalogData(r, pageData{CatalogItem: &item})
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
		a.renderCatalogPage(w, r, pageData{CatalogInput: item.CatalogInput, CatalogVersion: item.Version, CatalogAction: "/catalog/" + id + "/edit" + catalogQuerySuffix(r.URL.Query()), CatalogTitle: "Edit catalog item", Raw: catalogDisplayRaw(item.CatalogInput)}, false)
	case http.MethodPost:
		if err := a.validateCatalogNavigation(r); err != nil {
			http.Error(w, "invalid catalog query", http.StatusBadRequest)
			return
		}
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
		a.renderCatalogPage(w, r, pageData{CatalogInput: input, CatalogVersion: version, CatalogAction: "/catalog/" + id + "/edit" + catalogQuerySuffix(r.URL.Query()), CatalogTitle: "Edit catalog item", Errors: catalogErrorFields(err), Raw: rawForm(r)}, isHTMX(r))
	default:
		methodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}
func (a *app) catalogState(w http.ResponseWriter, r *http.Request, id string, active bool) {
	if err := a.validateCatalogNavigation(r); err != nil {
		http.Error(w, "invalid catalog query", http.StatusBadRequest)
		return
	}
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
func catalogDisplayRaw(input store.CatalogInput) map[string]string {
	return map[string]string{"unit_price": formatMinor(input.UnitPriceMinor), "tax_rate": formatTaxRate(input.TaxRateBasisPoints)}
}
func formatMinor(value int) string {
	sign := ""
	if value < 0 {
		sign, value = "-", -value
	}
	return sign + strconv.Itoa(value/100) + "." + fmtTwoDigits(value%100)
}
func formatTaxRate(value int) string {
	formatted := formatMinor(value)
	return strings.TrimRight(strings.TrimRight(formatted, "0"), ".")
}
func fmtTwoDigits(value int) string {
	if value < 10 {
		return "0" + strconv.Itoa(value)
	}
	return strconv.Itoa(value)
}
func (a *app) withCatalogData(r *http.Request, data pageData) pageData {
	data = a.withPageData(r, data)
	data.CatalogQuery = catalogQuerySuffix(r.URL.Query())
	data.CatalogCurrency = "EUR"
	if a.store != nil {
		if company, err := a.store.CompanyRepository().Get(r.Context()); err == nil && company.Currency != "" {
			data.CatalogCurrency = company.Currency
		}
	}
	return data
}
func catalogInputFromRequest(r *http.Request) (store.CatalogInput, error) {
	input := store.CatalogInput{Number: r.Form.Get("number"), Kind: r.Form.Get("kind"), Title: r.Form.Get("title"), Description: r.Form.Get("description"), Unit: r.Form.Get("unit")}
	price, err := parseDecimal(r.Form.Get("unit_price"), 2, "unit_price")
	if err != nil {
		return input, err
	}
	tax, err := parseDecimal(r.Form.Get("tax_rate"), 2, "tax_rate")
	input.UnitPriceMinor, input.TaxRateBasisPoints = price, tax
	return input, err
}
func parseDecimal(raw string, decimals int, field string) (int, error) {
	value := strings.TrimSpace(raw)
	invalid := func(message string) (int, error) {
		return 0, &store.ValidationError{Field: field, Message: message + "; received: " + raw}
	}
	if value == "" {
		return 0, &store.ValidationError{Field: field, Message: "is required"}
	}
	if len(value) > 32 {
		return invalid("must be a decimal number")
	}
	max := int(^uint(0) >> 1)
	result, fractionalDigits, wholeDigits := 0, 0, 0
	separator := false
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c == '.' || c == ',' {
			if separator || wholeDigits == 0 {
				return invalid("must be a decimal number")
			}
			separator = true
			continue
		}
		if c < '0' || c > '9' {
			return invalid("must be a decimal number")
		}
		digit := int(c - '0')
		if result > (max-digit)/10 {
			return invalid("is out of range")
		}
		result = result*10 + digit
		if separator {
			fractionalDigits++
			if fractionalDigits > decimals {
				return invalid("must have at most " + strconv.Itoa(decimals) + " decimal places")
			}
		} else {
			wholeDigits++
		}
	}
	if separator && fractionalDigits == 0 {
		return invalid("must be a decimal number")
	}
	for fractionalDigits < decimals {
		if result > max/10 {
			return invalid("is out of range")
		}
		result *= 10
		fractionalDigits++
	}
	return result, nil
}
func (a *app) catalogSaved(w http.ResponseWriter, r *http.Request, item store.CatalogItem) {
	data := a.withCatalogData(r, pageData{CatalogItem: &item})
	if isHTMX(r) {
		w.Header().Set("HX-Retarget", "#catalog-detail")
		w.Header().Set("HX-Push-Url", "/catalog/"+item.ID+data.CatalogQuery)
		if listed, err := a.catalogListViewData(r, data); err == nil {
			a.renderTemplate(w, "catalogSaved", listed)
		} else {
			a.renderTemplate(w, "catalogDetail", data)
		}
		return
	}
	http.Redirect(w, r, "/catalog/"+item.ID+data.CatalogQuery, http.StatusSeeOther)
}
func (a *app) renderCatalogPage(w http.ResponseWriter, r *http.Request, data pageData, fragment bool) {
	data = a.withCatalogData(r, data)
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
	data.Errors = catalogErrorFields(err)
	a.renderCatalogPage(w, r, data, isHTMX(r))
}
func catalogErrorFields(err error) map[string]string {
	fields := errorFields(err)
	if message, ok := fields["form"]; ok {
		delete(fields, "form")
		fields["catalog-form"] = message
	}
	return fields
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
	data.CatalogQuery = catalogQuerySuffix(r.URL.Query())
	return data, nil
}
func (a *app) validateCatalogNavigation(r *http.Request) error {
	_, err := a.catalogListViewData(r, pageData{})
	return err
}
func catalogQuerySuffix(query url.Values) string {
	values := url.Values{}
	if search := query.Get("q"); search != "" {
		values.Set("q", search)
	}
	state := query.Get("state")
	if state == "" {
		state = "active"
	}
	if state == "active" || state == "archived" || state == "all" {
		values.Set("state", state)
	}
	if cursor := query.Get("cursor"); cursor != "" {
		values.Set("cursor", cursor)
	}
	if len(values) == 0 {
		return ""
	}
	return "?" + values.Encode()
}
