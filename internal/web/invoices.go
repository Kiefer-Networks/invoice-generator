package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kiefer-networks/invoice-generator/internal/invoicing"
	"github.com/kiefer-networks/invoice-generator/internal/store"
)

func formatInvoiceQuantity(value int64) string {
	whole, fraction := value/10000, value%10000
	if fraction == 0 {
		return strconv.FormatInt(whole, 10)
	}
	return strconv.FormatInt(whole, 10) + "." + strings.TrimRight(fmt.Sprintf("%04d", fraction), "0")
}

func (a *app) invoiceService() *invoicing.DraftService { return invoicing.NewDraftService(a.store) }
func (a *app) invoices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if a.store == nil {
		http.Error(w, "invoice storage is unavailable", http.StatusServiceUnavailable)
		return
	}
	data, err := a.invoiceListData(r, pageData{})
	if err != nil {
		http.Error(w, "invalid invoice query", http.StatusBadRequest)
		return
	}
	data = a.withPageData(r, data)
	if isHTMX(r) {
		a.renderTemplate(w, "invoiceList", data)
		return
	}
	a.renderTemplate(w, "invoicesPage", data)
}
func (a *app) invoiceNew(w http.ResponseWriter, r *http.Request) {
	if a.store == nil {
		http.Error(w, "invoice storage is unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		data, err := a.invoicePickerData(r, pageData{InvoiceAction: "/invoices/new"})
		if err != nil {
			http.Error(w, "unable to load customers", http.StatusInternalServerError)
			return
		}
		data = a.withPageData(r, data)
		a.renderTemplate(w, "invoiceNew", data)
	case http.MethodPost:
		if err := a.validateInvoiceNavigation(r); err != nil {
			http.Error(w, "invalid invoice query", http.StatusBadRequest)
			return
		}
		draft, err := a.invoiceService().Create(r.Context(), r.Form.Get("customer_id"), time.Now().UTC())
		if err == nil {
			a.invoiceSaved(w, r, draft)
			return
		}
		a.renderInvoiceError(w, r, "", err)
	default:
		methodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}
func (a *app) invoiceRoute(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/invoices/"), "/")
	if len(parts) < 1 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		a.invoiceDetail(w, r, id)
		return
	}
	if len(parts) == 2 && parts[1] == "customer" {
		a.invoiceCustomer(w, r, id)
		return
	}
	if len(parts) == 2 && parts[1] == "preview" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		a.invoicePreview(w, r, id)
		return
	}
	if len(parts) == 3 && parts[1] == "items" {
		switch parts[2] {
		case "catalog":
			a.invoiceCatalogLine(w, r, id)
		case "manual":
			a.invoiceManualLine(w, r, id)
		case "reorder":
			a.invoiceReorder(w, r, id)
		default:
			http.NotFound(w, r)
		}
		return
	}
	if len(parts) == 4 && parts[1] == "items" && parts[3] == "remove" {
		a.invoiceRemoveLine(w, r, id, parts[2])
		return
	}
	if len(parts) == 4 && parts[1] == "items" && parts[3] == "edit" {
		a.invoiceEditLine(w, r, id, parts[2])
		return
	}
	http.NotFound(w, r)
}
func (a *app) invoiceDetail(w http.ResponseWriter, r *http.Request, id string) {
	draft, err := a.invoiceService().Get(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "unable to load invoice", 500)
		return
	}
	data, err := a.invoiceEditorData(r, pageData{Invoice: &draft})
	if err != nil {
		http.Error(w, "unable to load invoice", 500)
		return
	}
	if isHTMX(r) {
		a.renderTemplate(w, "invoiceEditor", data)
		return
	}
	a.renderTemplate(w, "invoicesPage", data)
}
func (a *app) invoicePreview(w http.ResponseWriter, r *http.Request, id string) {
	draft, err := a.invoiceService().Get(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "unable to load invoice", http.StatusInternalServerError)
		return
	}
	a.renderTemplate(w, "invoiceTotals", a.withPageData(r, pageData{Invoice: &draft}))
}
func (a *app) invoiceCustomer(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if err := a.validateInvoiceNavigation(r); err != nil {
		http.Error(w, "invalid invoice query", 400)
		return
	}
	version, err := formInt(r, "version")
	var draft invoicing.Draft
	if err == nil {
		draft, err = a.invoiceService().Update(r.Context(), id, version, r.Form.Get("customer_id"))
	}
	if err == nil {
		a.invoiceSaved(w, r, draft)
		return
	}
	a.renderInvoiceError(w, r, id, err)
}
func (a *app) invoiceCatalogLine(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if err := a.validateInvoiceNavigation(r); err != nil {
		http.Error(w, "invalid invoice query", 400)
		return
	}
	version, err := formInt(r, "version")
	quantity, qerr := parseDecimal(r.Form.Get("quantity"), 4, "quantity")
	if err == nil {
		err = qerr
	}
	var draft invoicing.Draft
	if err == nil {
		draft, err = a.invoiceService().AddCatalogItem(r.Context(), id, version, r.Form.Get("catalog_id"), int64(quantity))
	}
	if err == nil {
		a.invoiceSaved(w, r, draft)
		return
	}
	a.renderInvoiceError(w, r, id, err)
}
func (a *app) invoiceManualLine(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if err := a.validateInvoiceNavigation(r); err != nil {
		http.Error(w, "invalid invoice query", 400)
		return
	}
	version, err := formInt(r, "version")
	input, inputErr := invoiceLineInput(r)
	if err == nil {
		err = inputErr
	}
	var draft invoicing.Draft
	if err == nil {
		draft, err = a.invoiceService().AddManualLine(r.Context(), id, version, input)
	}
	if err == nil {
		a.invoiceSaved(w, r, draft)
		return
	}
	a.renderInvoiceError(w, r, id, err)
}
func (a *app) invoiceEditLine(w http.ResponseWriter, r *http.Request, id, lineID string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if err := a.validateInvoiceNavigation(r); err != nil {
		http.Error(w, "invalid invoice query", 400)
		return
	}
	version, err := formInt(r, "version")
	input, inputErr := invoiceLineInput(r)
	if err == nil {
		err = inputErr
	}
	var draft invoicing.Draft
	if err == nil {
		draft, err = a.invoiceService().UpdateLine(r.Context(), id, version, lineID, input)
	}
	if err == nil {
		a.invoiceSaved(w, r, draft)
		return
	}
	a.renderInvoiceError(w, r, id, err)
}
func (a *app) invoiceRemoveLine(w http.ResponseWriter, r *http.Request, id, lineID string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if err := a.validateInvoiceNavigation(r); err != nil {
		http.Error(w, "invalid invoice query", 400)
		return
	}
	version, err := formInt(r, "version")
	var draft invoicing.Draft
	if err == nil {
		draft, err = a.invoiceService().RemoveLine(r.Context(), id, version, lineID)
	}
	if err == nil {
		a.invoiceSaved(w, r, draft)
		return
	}
	a.renderInvoiceError(w, r, id, err)
}
func (a *app) invoiceReorder(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if err := a.validateInvoiceNavigation(r); err != nil {
		http.Error(w, "invalid invoice query", 400)
		return
	}
	version, err := formInt(r, "version")
	var draft invoicing.Draft
	if err == nil {
		draft, err = a.invoiceService().ReorderLines(r.Context(), id, version, r.Form["line_ids"])
	}
	if err == nil {
		a.invoiceSaved(w, r, draft)
		return
	}
	a.renderInvoiceError(w, r, id, err)
}
func invoiceLineInput(r *http.Request) (invoicing.DraftLineInput, error) {
	q, err := parseDecimal(r.Form.Get("quantity"), 4, "quantity")
	if err != nil {
		return invoicing.DraftLineInput{}, err
	}
	price, err := parseDecimal(r.Form.Get("unit_price"), 2, "unit_price")
	if err != nil {
		return invoicing.DraftLineInput{}, err
	}
	discountRaw := r.Form.Get("discount")
	if discountRaw == "" {
		discountRaw = "0"
	}
	discount, err := parseDecimal(discountRaw, 2, "discount")
	if err != nil {
		return invoicing.DraftLineInput{}, err
	}
	tax, err := parseDecimal(r.Form.Get("tax_rate"), 2, "tax_rate")
	if err != nil {
		return invoicing.DraftLineInput{}, err
	}
	return invoicing.DraftLineInput{Title: r.Form.Get("title"), Description: r.Form.Get("description"), Unit: r.Form.Get("unit"), QuantityScaled: int64(q), UnitPriceMinor: int64(price), DiscountBasisPoints: int64(discount), TaxRateBasisPoints: int64(tax)}, nil
}
func (a *app) invoiceSaved(w http.ResponseWriter, r *http.Request, draft invoicing.Draft) {
	data, err := a.invoiceEditorData(r, pageData{Invoice: &draft})
	if err != nil {
		http.Error(w, "unable to load invoice", 500)
		return
	}
	if isHTMX(r) {
		w.Header().Set("HX-Retarget", "#invoice-editor")
		w.Header().Set("HX-Push-Url", "/invoices/"+draft.ID)
		a.renderTemplate(w, "invoiceSaved", data)
		return
	}
	http.Redirect(w, r, "/invoices/"+draft.ID, http.StatusSeeOther)
}
func (a *app) renderInvoiceError(w http.ResponseWriter, r *http.Request, id string, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, store.ErrConflict) {
		status = http.StatusConflict
	}
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if isHTMX(r) {
		w.Header().Set("HX-Retarget", "#invoice-editor")
	}
	w.WriteHeader(status)
	if id == "" {
		data, e := a.invoicePickerData(r, pageData{Errors: errorFields(err), Raw: rawForm(r), InvoiceAction: "/invoices/new"})
		if e != nil {
			http.Error(w, "unable to load customers", 500)
			return
		}
		data = a.withPageData(r, data)
		a.renderTemplate(w, "invoiceNew", data)
		return
	}
	draft, e := a.invoiceService().Get(r.Context(), id)
	if e != nil {
		http.Error(w, "unable to load invoice", 500)
		return
	}
	data, e := a.invoiceEditorData(r, pageData{Invoice: &draft, Errors: errorFields(err), Raw: rawForm(r)})
	if e != nil {
		http.Error(w, "unable to load invoice", 500)
		return
	}
	if isHTMX(r) {
		a.renderTemplate(w, "invoiceEditor", data)
		return
	}
	a.renderTemplate(w, "invoicesPage", data)
}
func (a *app) invoiceListData(r *http.Request, data pageData) (pageData, error) {
	state := r.URL.Query().Get("state")
	if state != "" && state != "draft" {
		return data, errors.New("invalid state")
	}
	items, err := a.store.InvoiceRepository().ListDrafts(r.Context(), store.InvoiceListOptions{State: "draft"})
	if err != nil {
		return data, err
	}
	data.Invoices = items
	data.InvoiceQuery = invoiceQuerySuffix(r.URL.Query())
	return data, nil
}
func (a *app) invoicePickerData(r *http.Request, data pageData) (pageData, error) {
	customers, err := a.store.CustomerRepository().List(r.Context(), store.CustomerListOptions{Limit: 100})
	if err != nil {
		return data, err
	}
	catalog, err := a.store.CatalogRepository().List(r.Context(), store.CatalogListOptions{Limit: 100})
	if err != nil {
		return data, err
	}
	data.InvoiceCustomers = customers
	data.InvoiceCatalog = catalog
	return data, nil
}
func (a *app) invoiceEditorData(r *http.Request, data pageData) (pageData, error) {
	data, err := a.invoicePickerData(r, data)
	if err != nil {
		return data, err
	}
	data, err = a.invoiceListData(r, data)
	if err != nil {
		return data, err
	}
	return a.withPageData(r, data), nil
}
func (a *app) validateInvoiceNavigation(r *http.Request) error {
	_, err := a.invoiceListData(r, pageData{})
	return err
}
func invoiceQuerySuffix(q url.Values) string {
	if state := q.Get("state"); state == "draft" {
		return "?state=draft"
	}
	return ""
}
