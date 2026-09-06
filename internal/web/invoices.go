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
		data, err := a.invoiceEditorData(r, pageData{InvoiceAction: "/invoices/new"})
		if err != nil {
			http.Error(w, "invalid invoice query", http.StatusBadRequest)
			return
		}
		data = a.withPageData(r, data)
		if isHTMX(r) {
			a.renderTemplate(w, "invoiceNewForm", data)
		} else {
			a.renderTemplate(w, "invoiceNew", data)
		}
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
	if len(parts) == 2 && parts[1] == "service-date" {
		a.invoiceServiceDate(w, r, id)
		return
	}
	if len(parts) == 2 {
		switch parts[1] {
		case "review", "finalize", "paid", "cancel", "correction":
			a.invoiceFinalizationRoute(w, r, id, parts[1])
			return
		}
	}
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
	if len(parts) == 4 && parts[1] == "items" && (parts[3] == "up" || parts[3] == "down") {
		a.invoiceMoveLine(w, r, id, parts[2], parts[3] == "up")
		return
	}
	http.NotFound(w, r)
}
func (a *app) invoiceMoveLine(w http.ResponseWriter, r *http.Request, id, lineID string, up bool) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if err := a.validateInvoiceNavigation(r); err != nil {
		http.Error(w, "invalid invoice query", 400)
		return
	}
	version, err := formInt(r, "version")
	draft, getErr := a.invoiceService().Get(r.Context(), id)
	if err == nil {
		err = getErr
	}
	if err == nil {
		ids, index := make([]string, len(draft.Lines)), -1
		for i, line := range draft.Lines {
			ids[i] = line.ID
			if line.ID == lineID {
				index = i
			}
		}
		if index < 0 {
			err = store.ErrNotFound
		} else {
			target := index + 1
			if up {
				target = index - 1
			}
			if target >= 0 && target < len(ids) {
				ids[index], ids[target] = ids[target], ids[index]
			}
			draft, err = a.invoiceService().ReorderLines(r.Context(), id, version, ids)
		}
	}
	if err == nil {
		a.invoiceSaved(w, r, draft)
		return
	}
	a.renderInvoiceError(w, r, id, err)
}
func (a *app) invoiceDetail(w http.ResponseWriter, r *http.Request, id string) {
	if f, err := invoicing.NewFinalizationService(a.store).Get(r.Context(), id); err == nil {
		a.renderFinalInvoice(w, r, f, "", nil)
		return
	} else if errors.Is(err, store.ErrLegacyInvoice) {
		a.finalizationError(w, r, err)
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		http.Error(w, "unable to load invoice", 500)
		return
	}
	draft, err := a.invoiceService().Get(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "unable to load invoice", 500)
		return
	}
	if err := a.validateInvoiceNavigation(r); err != nil {
		http.Error(w, "invalid invoice query", 400)
		return
	}
	data, err := a.invoiceEditorData(r, pageData{Invoice: &draft})
	if err != nil {
		http.Error(w, "invalid invoice query", http.StatusBadRequest)
		return
	}
	if isHTMX(r) {
		a.renderTemplate(w, "invoiceEditor", data)
		return
	}
	a.renderTemplate(w, "invoicesPage", data)
}
func (a *app) invoicePreview(w http.ResponseWriter, r *http.Request, id string) {
	if f, err := invoicing.NewFinalizationService(a.store).Get(r.Context(), id); err == nil {
		a.renderFinalInvoice(w, r, f, "", nil)
		return
	} else if errors.Is(err, store.ErrLegacyInvoice) {
		a.finalizationError(w, r, err)
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		http.Error(w, "unable to load invoice", 500)
		return
	}
	draft, err := a.invoiceService().Get(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "unable to load invoice", http.StatusInternalServerError)
		return
	}
	company, err := a.store.CompanyRepository().Get(r.Context())
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		http.Error(w, "unable to load company", 500)
		return
	}
	preview := invoicing.PreviewData(draft, company.CompanyInput)
	a.renderTemplate(w, "invoiceDocumentPreview", a.withPageData(r, pageData{Invoice: &draft, InvoicePreview: preview}))
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
	if err := a.validateInvoiceNavigation(r); err != nil {
		http.Error(w, "invalid invoice query", 400)
		return
	}
	data, err := a.invoiceEditorData(r, pageData{Invoice: &draft})
	if err != nil {
		http.Error(w, "unable to load invoice", 500)
		return
	}
	if isHTMX(r) {
		w.Header().Set("HX-Retarget", "#invoice-editor")
		w.Header().Set("HX-Reswap", "innerHTML")
		w.Header().Set("HX-Push-Url", "/invoices/"+draft.ID+data.InvoiceQuery)
		a.renderTemplate(w, "invoiceSaved", data)
		return
	}
	http.Redirect(w, r, "/invoices/"+draft.ID+data.InvoiceQuery, http.StatusSeeOther)
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
		w.Header().Set("HX-Reswap", "innerHTML")
	}
	w.WriteHeader(status)
	if id == "" {
		data, e := a.invoiceEditorData(r, pageData{Errors: errorFields(err), Raw: rawForm(r), InvoiceAction: "/invoices/new"})
		if e != nil {
			http.Error(w, "unable to load customers", 500)
			return
		}
		data = a.withPageData(r, data)
		if isHTMX(r) {
			a.renderTemplate(w, "invoiceNewForm", data)
		} else {
			a.renderTemplate(w, "invoiceNew", data)
		}
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
	if state != "" && state != "draft" && state != "finalized" && state != "paid" && state != "overdue" && state != "cancelled" {
		return data, errors.New("invalid state")
	}
	page, err := a.store.InvoiceRepository().ListDraftPage(r.Context(), store.InvoiceListOptions{State: state, Search: r.URL.Query().Get("q"), Cursor: r.URL.Query().Get("cursor")})
	if err != nil {
		return data, err
	}
	data.Invoices = page.Drafts
	data.InvoiceState = state
	if data.InvoiceState == "" {
		data.InvoiceState = "draft"
	}
	data.Search = r.URL.Query().Get("q")
	if page.NextCursor != "" {
		values := r.URL.Query()
		values.Set("cursor", page.NextCursor)
		data.InvoiceNextURL = "/invoices" + invoiceQuerySuffix(values)
	}
	data.InvoiceQuery = invoiceQuerySuffix(r.URL.Query())
	return data, nil
}
func (a *app) invoicePickerData(r *http.Request, data pageData) (pageData, error) {
	q := r.URL.Query()
	customers, err := a.store.CustomerRepository().List(r.Context(), store.CustomerListOptions{Limit: 100, Search: q.Get("customer_q"), Cursor: q.Get("customer_cursor")})
	if err != nil {
		return data, err
	}
	catalog, err := a.store.CatalogRepository().List(r.Context(), store.CatalogListOptions{Limit: 100, Search: q.Get("catalog_q"), Cursor: q.Get("catalog_cursor")})
	if err != nil {
		return data, err
	}
	data.InvoiceCustomers = customers
	data.InvoiceCatalog = catalog
	data.InvoicePickerPath = "/invoices/new"
	if data.Invoice != nil {
		data.InvoicePickerPath = "/invoices/" + data.Invoice.ID
		data.InvoiceSelectedCustomer = data.Invoice.CustomerID
	}
	if data.Raw != nil {
		switch {
		case strings.HasSuffix(r.URL.Path, "/service-date"):
			data.InvoiceServiceDateRaw = data.Raw
		case strings.HasSuffix(r.URL.Path, "/new"), strings.HasSuffix(r.URL.Path, "/customer"):
			data.InvoiceSelectedCustomer = data.Raw["customer_id"]
		case strings.HasSuffix(r.URL.Path, "/items/catalog"):
			data.InvoiceCatalogRaw = data.Raw
			data.InvoiceSelectedCatalog = data.Raw["catalog_id"]
		case strings.HasSuffix(r.URL.Path, "/items/manual"):
			data.InvoiceManualRaw = data.Raw
		}
		if strings.HasSuffix(r.URL.Path, "/edit") {
			parts := strings.Split(r.URL.Path, "/")
			data.Raw["line_id"] = parts[len(parts)-2]
		} else {
			data.Raw = nil
		}
	}
	if data.InvoiceSelectedCustomer != "" {
		found := false
		for _, c := range customers.Customers {
			if c.ID == data.InvoiceSelectedCustomer {
				found = true
			}
		}
		if !found {
			c, e := a.store.CustomerRepository().Get(r.Context(), data.InvoiceSelectedCustomer)
			if e != nil {
				return data, e
			}
			data.InvoiceCustomers.Customers = append(data.InvoiceCustomers.Customers, c)
		}
	}
	references := []string{data.InvoiceSelectedCatalog}
	if data.Invoice != nil {
		for _, line := range data.Invoice.Lines {
			references = append(references, line.CatalogItemID)
		}
	}
	for _, id := range references {
		if id == "" {
			continue
		}
		found := false
		for _, item := range data.InvoiceCatalog.Items {
			if item.ID == id {
				found = true
			}
		}
		if !found {
			item, e := a.store.CatalogRepository().Get(r.Context(), id)
			if e != nil {
				return data, e
			}
			data.InvoiceCatalog.Items = append(data.InvoiceCatalog.Items, item)
		}
	}
	data.InvoiceCustomerSearch = q.Get("customer_q")
	data.InvoiceCatalogSearch = q.Get("catalog_q")
	if customers.NextCursor != "" {
		next := r.URL.Query()
		next.Set("customer_cursor", customers.NextCursor)
		data.InvoiceCustomerNextURL = data.InvoicePickerPath + invoiceQuerySuffix(next)
	}
	if catalog.NextCursor != "" {
		next := r.URL.Query()
		next.Set("catalog_cursor", catalog.NextCursor)
		data.InvoiceCatalogNextURL = data.InvoicePickerPath + invoiceQuerySuffix(next)
	}
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
	data.InvoiceNavigation = r.URL.Query()
	if data.Invoice != nil {
		company, e := a.store.CompanyRepository().Get(r.Context())
		if e != nil && !errors.Is(e, store.ErrNotFound) {
			return data, e
		}
		data.InvoicePreview = invoicing.PreviewData(*data.Invoice, company.CompanyInput)
	}
	return a.withPageData(r, data), nil
}
func (a *app) validateInvoiceNavigation(r *http.Request) error {
	if state := r.URL.Query().Get("state"); state != "" && state != "draft" {
		return errors.New("invalid draft state")
	}
	_, err := a.invoiceEditorData(r, pageData{})
	return err
}
func invoiceQuerySuffix(q url.Values) string {
	values := url.Values{}
	for _, key := range []string{"state", "q", "cursor", "customer_q", "customer_cursor", "catalog_q", "catalog_cursor"} {
		if value := q.Get(key); value != "" {
			values.Set(key, value)
		}
	}
	if len(values) == 0 {
		return ""
	}
	return "?" + values.Encode()
}

func (a *app) invoiceServiceDate(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if err := a.validateInvoiceNavigation(r); err != nil {
		http.Error(w, "invalid invoice query", 400)
		return
	}
	version, err := formInt(r, "version")
	var d invoicing.Draft
	if err == nil {
		d, err = a.invoiceService().SetServiceDate(r.Context(), id, version, r.Form.Get("service_date"))
	}
	if err != nil {
		a.renderInvoiceError(w, r, id, err)
		return
	}
	a.invoiceSaved(w, r, d)
}
