package web

import (
	"errors"
	"net/http"
	"strings"

	"github.com/kiefer-networks/invoice-generator/internal/store"
)

func (a *app) customers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if a.store == nil {
		http.Error(w, "customer storage is unavailable", http.StatusServiceUnavailable)
		return
	}
	page, err := a.store.CustomerRepository().List(r.Context(), store.CustomerListOptions{Search: r.URL.Query().Get("q"), Cursor: r.URL.Query().Get("cursor")})
	if err != nil {
		http.Error(w, "invalid customer query", http.StatusBadRequest)
		return
	}
	data := a.withPageData(r, pageData{Customers: page, Search: r.URL.Query().Get("q")})
	if r.Header.Get("HX-Request") == "true" {
		a.renderTemplate(w, "customerList", data)
		return
	}
	a.renderTemplate(w, "customersPage", data)
}

func (a *app) customerNew(w http.ResponseWriter, r *http.Request) {
	if a.store == nil {
		http.Error(w, "customer storage is unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		a.renderCustomerPage(w, r, pageData{CustomerInput: defaultCustomerInput(), CustomerAction: "/customers/new", CustomerTitle: "New customer"}, false)
	case http.MethodPost:
		input, err := customerInputFromRequest(r)
		if err == nil {
			var customer store.Customer
			customer, err = a.store.CustomerRepository().Create(r.Context(), input)
			if err == nil {
				a.customerSaved(w, r, customer)
				return
			}
		}
		a.renderCustomerError(w, r, pageData{CustomerInput: input, CustomerAction: "/customers/new", CustomerTitle: "New customer"}, err)
	default:
		methodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func (a *app) customerRoute(w http.ResponseWriter, r *http.Request) {
	if a.store == nil {
		http.Error(w, "customer storage is unavailable", http.StatusServiceUnavailable)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/customers/"), "/")
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
		a.customerDetail(w, r, id)
		return
	}
	switch parts[1] {
	case "edit":
		a.customerEdit(w, r, id)
	case "archive":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		a.customerState(w, r, id, false)
	case "restore":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		a.customerState(w, r, id, true)
	default:
		http.NotFound(w, r)
	}
}

func (a *app) customerDetail(w http.ResponseWriter, r *http.Request, id string) {
	c, err := a.store.CustomerRepository().Get(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "unable to load customer", http.StatusInternalServerError)
		return
	}
	data := a.withPageData(r, pageData{Customer: &c})
	if r.Header.Get("HX-Request") == "true" {
		a.renderTemplate(w, "customerDetail", data)
		return
	}
	page, err := a.store.CustomerRepository().List(r.Context(), store.CustomerListOptions{})
	if err != nil {
		http.Error(w, "unable to load customers", http.StatusInternalServerError)
		return
	}
	data.Customers = page
	a.renderTemplate(w, "customersPage", data)
}

func (a *app) customerEdit(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		c, err := a.store.CustomerRepository().Get(r.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "unable to load customer", http.StatusInternalServerError)
			return
		}
		a.renderCustomerPage(w, r, pageData{CustomerInput: c.CustomerInput, CustomerVersion: c.Version, CustomerAction: "/customers/" + id + "/edit", CustomerTitle: "Edit customer"}, false)
	case http.MethodPost:
		input, err := customerInputFromRequest(r)
		version, versionErr := formInt(r, "version")
		if err == nil {
			err = versionErr
		}
		if err == nil {
			var c store.Customer
			c, err = a.store.CustomerRepository().Update(r.Context(), id, version, input)
			if err == nil {
				a.customerSaved(w, r, c)
				return
			}
		}
		status := http.StatusBadRequest
		if errors.Is(err, store.ErrConflict) {
			status = http.StatusConflict
		}
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		a.renderCustomerPage(w, r, pageData{CustomerInput: input, CustomerVersion: version, CustomerAction: "/customers/" + id + "/edit", CustomerTitle: "Edit customer", Errors: errorFields(err)}, true)
	default:
		methodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func (a *app) customerState(w http.ResponseWriter, r *http.Request, id string, active bool) {
	version, err := formInt(r, "version")
	if err == nil {
		if active {
			var c store.Customer
			c, err = a.store.CustomerRepository().Restore(r.Context(), id, version)
			if err == nil {
				a.customerSaved(w, r, c)
				return
			}
		} else {
			var c store.Customer
			c, err = a.store.CustomerRepository().Archive(r.Context(), id, version)
			if err == nil {
				a.customerSaved(w, r, c)
				return
			}
		}
	}
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if errors.Is(err, store.ErrConflict) {
		http.Error(w, "customer changed; refresh and try again", http.StatusConflict)
		return
	}
	http.Error(w, "invalid customer request", http.StatusBadRequest)
}

func defaultCustomerInput() store.CustomerInput {
	return store.CustomerInput{Country: "DE", PreferredLanguage: "de", Currency: "EUR", PaymentTermsDays: 14}
}
func customerInputFromRequest(r *http.Request) (store.CustomerInput, error) {
	terms, err := formInt(r, "payment_terms_days")
	return store.CustomerInput{Number: r.Form.Get("number"), DisplayName: r.Form.Get("display_name"), LegalName: r.Form.Get("legal_name"), ContactName: r.Form.Get("contact_name"), Email: r.Form.Get("email"), AddressLine1: r.Form.Get("address_line1"), AddressLine2: r.Form.Get("address_line2"), PostalCode: r.Form.Get("postal_code"), City: r.Form.Get("city"), Country: r.Form.Get("country"), VATIdentifier: r.Form.Get("vat_identifier"), PreferredLanguage: r.Form.Get("preferred_language"), Currency: r.Form.Get("currency"), PaymentTermsDays: terms, Notes: r.Form.Get("notes")}, err
}
func (a *app) customerSaved(w http.ResponseWriter, r *http.Request, c store.Customer) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Retarget", "#customer-detail")
		w.Header().Set("HX-Push-Url", "/customers/"+c.ID)
		a.renderTemplate(w, "customerDetail", a.withPageData(r, pageData{Customer: &c}))
		return
	}
	http.Redirect(w, r, "/customers/"+c.ID, http.StatusSeeOther)
}
func (a *app) renderCustomerPage(w http.ResponseWriter, r *http.Request, data pageData, fragment bool) {
	data = a.withPageData(r, data)
	if fragment || r.Header.Get("HX-Request") == "true" {
		a.renderTemplate(w, "customerForm", data)
		return
	}
	page, err := a.store.CustomerRepository().List(r.Context(), store.CustomerListOptions{})
	if err != nil {
		http.Error(w, "unable to load customers", http.StatusInternalServerError)
		return
	}
	data.Customers = page
	a.renderTemplate(w, "customersPage", data)
}
func (a *app) renderCustomerError(w http.ResponseWriter, r *http.Request, data pageData, err error) {
	w.WriteHeader(http.StatusBadRequest)
	data.Errors = errorFields(err)
	a.renderCustomerPage(w, r, data, true)
}
func errorFields(err error) map[string]string {
	result := map[string]string{}
	var validation *store.ValidationError
	if errors.As(err, &validation) {
		result[validation.Field] = validation.Message
		return result
	}
	if errors.Is(err, store.ErrDuplicate) {
		result["number"] = "already exists"
		return result
	}
	if errors.Is(err, store.ErrConflict) {
		result["form"] = "customer changed; refresh and try again"
		return result
	}
	result["form"] = "invalid request"
	return result
}
func (a *app) withPageData(r *http.Request, data pageData) pageData {
	p, _ := principalFromContext(r.Context())
	csrf, _ := a.auth.CSRFToken(cookie(r, "invoice_session"))
	data.Nonce = nonceFromContext(r.Context())
	data.CSRFToken = csrf
	data.DisplayName = p.DisplayName
	data.CSSURL = "/assets/app.css?v=" + assetVersion("app.css")
	data.HTMXURL = "/assets/htmx.min.js?v=" + assetVersion("htmx.min.js")
	return data
}
func (a *app) renderTemplate(w http.ResponseWriter, name string, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.templates.ExecuteTemplate(w, name, data); err != nil {
		a.logger.Error("render page", "error", err)
	}
}
