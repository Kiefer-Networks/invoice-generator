package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/kiefer-networks/invoice-generator/internal/store"
)

func (a *app) company(w http.ResponseWriter, r *http.Request) {
	if a.store == nil {
		http.Error(w, "company storage is unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		input := defaultCompanyInput()
		if company, err := a.store.CompanyRepository().Get(r.Context()); err == nil {
			input = company.CompanyInput
		} else if !errors.Is(err, store.ErrNotFound) {
			http.Error(w, "unable to load company", http.StatusInternalServerError)
			return
		}
		a.renderCompany(w, r, pageData{CompanyInput: input})
	case http.MethodPost:
		input, err := companyInputFromRequest(r)
		if err == nil {
			_, err = a.store.CompanyRepository().Save(r.Context(), input)
		}
		if err != nil {
			a.renderCompanyError(w, r, input, err)
			return
		}
		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("HX-Retarget", "#company-profile")
			a.renderTemplate(w, "companyForm", a.withPageData(r, pageData{CompanyInput: input}))
			return
		}
		http.Redirect(w, r, "/settings/company", http.StatusSeeOther)
	default:
		methodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func defaultCompanyInput() store.CompanyInput {
	return store.CompanyInput{Country: "DE", Currency: "EUR", DefaultLanguage: "de", BrandColor: "#5B9BD5", PaymentTermsDays: 14}
}
func companyInputFromRequest(r *http.Request) (store.CompanyInput, error) {
	terms, err := formInt(r, "payment_terms_days")
	return store.CompanyInput{LegalName: r.Form.Get("legal_name"), ContactName: r.Form.Get("contact_name"), Email: r.Form.Get("email"), Phone: r.Form.Get("phone"), AddressLine1: r.Form.Get("address_line1"), AddressLine2: r.Form.Get("address_line2"), PostalCode: r.Form.Get("postal_code"), City: r.Form.Get("city"), Country: r.Form.Get("country"), TaxNumber: r.Form.Get("tax_number"), VATIdentifier: r.Form.Get("vat_identifier"), BankName: r.Form.Get("bank_name"), IBAN: r.Form.Get("iban"), BIC: r.Form.Get("bic"), LogoKey: r.Form.Get("logo_key"), BrandColor: r.Form.Get("brand_color"), DefaultLanguage: r.Form.Get("default_language"), Currency: r.Form.Get("currency"), PaymentTermsDays: terms, InvoicePrefix: r.Form.Get("invoice_prefix"), StandardNotes: r.Form.Get("standard_notes")}, err
}
func formInt(r *http.Request, name string) (int, error) { return strconv.Atoi(r.Form.Get(name)) }
func (a *app) renderCompany(w http.ResponseWriter, r *http.Request, data pageData) {
	a.renderTemplate(w, "companyPage", a.withPageData(r, data))
}
func (a *app) renderCompanyError(w http.ResponseWriter, r *http.Request, input store.CompanyInput, err error) {
	w.WriteHeader(http.StatusBadRequest)
	a.renderTemplate(w, "companyPage", a.withPageData(r, pageData{CompanyInput: input, Errors: errorFields(err)}))
}
