package web

import (
	"errors"
	"github.com/kiefer-networks/invoice-generator/internal/invoicing"
	"github.com/kiefer-networks/invoice-generator/internal/store"
	"net/http"
)

func (a *app) invoiceFinalizationRoute(w http.ResponseWriter, r *http.Request, id, action string) {
	allowedGet := action == "review" || action == "cancel"
	if r.Method != http.MethodPost && !(r.Method == http.MethodGet && allowedGet) {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if action == "review" && r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if _, err := a.invoiceEditorData(r, pageData{}); err != nil {
		http.Error(w, "invalid invoice query", 400)
		return
	}
	principal, _ := principalFromContext(r.Context())
	requestID, _ := r.Context().Value(requestIDKey).(string)
	r = r.WithContext(store.WithInvoiceAudit(r.Context(), principal.Subject, requestID))
	svc := invoicing.NewFinalizationService(a.store)
	if action == "review" {
		a.renderInvoiceReview(w, r, id, nil)
		return
	}
	if r.Method == http.MethodGet {
		f, err := svc.Get(r.Context(), id)
		if err != nil {
			a.finalizationError(w, r, err)
			return
		}
		a.renderFinalInvoice(w, r, f, "cancel", nil)
		return
	}
	if r.Form.Get("confirm") != "yes" {
		a.finalizationError(w, r, &store.ValidationError{Field: "confirmation", Message: "explicit confirmation is required"})
		return
	}
	var err error
	switch action {
	case "finalize":
		_, err = svc.Finalize(r.Context(), id, r.Form.Get("idempotency_key"))
	case "paid":
		_, err = svc.MarkPaid(r.Context(), id)
	case "cancel":
		_, err = svc.Cancel(r.Context(), id, r.Form.Get("reason"))
	case "correction":
		var d invoicing.Draft
		d, err = svc.CreateCorrection(r.Context(), id)
		if err == nil {
			id = d.ID
			q := r.URL.Query()
			q.Set("state", "draft")
			r.URL.RawQuery = q.Encode()
		}
	}
	if err != nil {
		if action == "finalize" && (store.IsValidationError(err) || errors.Is(err, store.ErrConflict)) {
			a.renderInvoiceReview(w, r, id, err)
			return
		}
		if action == "cancel" && (store.IsValidationError(err) || errors.Is(err, store.ErrConflict)) {
			f, e := svc.Get(r.Context(), id)
			if e == nil {
				a.renderFinalInvoice(w, r, f, "cancel", err)
				return
			}
		}
		a.finalizationError(w, r, err)
		return
	}
	path := "/invoices/" + id + invoiceQuerySuffix(r.URL.Query())
	if isHTMX(r) {
		w.Header().Set("HX-Redirect", path)
		w.WriteHeader(200)
		return
	}
	http.Redirect(w, r, path, http.StatusSeeOther)
}
func (a *app) finalizationError(w http.ResponseWriter, r *http.Request, err error) {
	code := 500
	message := "unable to update invoice"
	if errors.Is(err, store.ErrLegacyInvoice) {
		code = 409
		message = "Historical invoice has no complete document snapshot. Its original records remain unchanged; document output is unavailable."
	}
	if errors.Is(err, store.ErrNotFound) {
		code = 404
		message = "invoice not found"
	}
	if errors.Is(err, store.ErrConflict) {
		code = 409
		message = "invoice changed; reload before continuing"
	}
	if store.IsValidationError(err) {
		code = 400
		message = err.Error()
	}
	http.Error(w, message, code)
}
func (a *app) renderInvoiceReview(w http.ResponseWriter, r *http.Request, id string, problem error) {
	d, err := a.invoiceService().Get(r.Context(), id)
	if err != nil {
		a.finalizationError(w, r, err)
		return
	}
	if d.Number != "" {
		if problem != nil {
			a.finalizationError(w, r, problem)
			return
		}
		http.Redirect(w, r, "/invoices/"+id, 303)
		return
	}
	review, err := invoicing.NewFinalizationService(a.store).PrepareReview(r.Context(), id, d.Version)
	if err != nil {
		a.finalizationError(w, r, err)
		return
	}
	d = review.Snapshot.Draft
	data := pageData{Invoice: &d, CompanyInput: review.Snapshot.Company, InvoicePreview: review.Snapshot.RenderData(), InvoiceQuery: invoiceQuerySuffix(r.URL.Query()), Errors: map[string]string{}}
	readiness := invoicing.ValidateFinalization(d, review.Snapshot.Company)
	data.FinalizationReady = readiness == nil
	if readiness != nil {
		data.Errors["readiness"] = readiness.Error()
	} else {
		data.FinalizationKey = review.Key
	}
	if problem != nil {
		if errors.Is(problem, store.ErrConflict) {
			data.Errors["submission"] = "Invoice changed; review the current data and confirm again."
		} else {
			data.Errors["submission"] = problem.Error()
		}
	}

	data = a.withPageData(r, data)
	if isHTMX(r) {
		w.Header().Set("HX-Retarget", "#main")
		w.Header().Set("HX-Reswap", "innerHTML")
	}
	if problem != nil {
		if errors.Is(problem, store.ErrConflict) {
			w.WriteHeader(409)
		} else {
			w.WriteHeader(400)
		}
	}
	if isHTMX(r) {
		a.renderTemplate(w, "invoiceReview", data)
	} else {
		a.renderTemplate(w, "invoiceReviewPage", data)
	}
}
func (a *app) renderFinalInvoice(w http.ResponseWriter, r *http.Request, f invoicing.FinalizedInvoice, confirmation string, problem error) {
	if _, err := a.invoiceEditorData(r, pageData{}); err != nil {
		http.Error(w, "invalid invoice query", 400)
		return
	}
	data := a.withPageData(r, pageData{Finalized: &f, Invoice: &f.Snapshot.Draft, InvoicePreview: f.Snapshot.RenderData(), InvoiceConfirmation: confirmation, InvoiceReason: r.Form.Get("reason"), InvoiceQuery: invoiceQuerySuffix(r.URL.Query())})
	if problem != nil {
		data.Errors = map[string]string{"reason": problem.Error()}
	}
	if isHTMX(r) {
		w.Header().Set("HX-Retarget", "#main")
		w.Header().Set("HX-Reswap", "innerHTML")
	}
	if problem != nil {
		if errors.Is(problem, store.ErrConflict) {
			w.WriteHeader(409)
		} else {
			w.WriteHeader(400)
		}
	}
	if isHTMX(r) {
		a.renderTemplate(w, "invoiceFinalDetail", data)
	} else {
		a.renderTemplate(w, "invoiceFinalPage", data)
	}
}
