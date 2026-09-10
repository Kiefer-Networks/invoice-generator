// Package web provides the authenticated HTTP application shell.
package web

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kiefer-networks/invoice-generator/internal/auth"
	"github.com/kiefer-networks/invoice-generator/internal/documents"
	"github.com/kiefer-networks/invoice-generator/internal/invoicing"
	"github.com/kiefer-networks/invoice-generator/internal/render"
	"github.com/kiefer-networks/invoice-generator/internal/store"
	"github.com/kiefer-networks/invoice-generator/internal/units"
)

const defaultBodyLimit int64 = 1 << 20

// Authenticator is the small Pocket ID boundary used by the HTTP server.
// *auth.Manager is the production implementation.
type Authenticator interface {
	Begin(returnTo string) (string, *http.Cookie, error)
	Callback(context.Context, *url.URL, *http.Cookie) (auth.SessionResult, error)
	Authenticate(context.Context, *http.Cookie) (auth.Principal, error)
	ValidateCSRF(context.Context, *http.Cookie, string) error
	CSRFToken(*http.Cookie) (string, error)
	Logout(context.Context, *http.Cookie) error
}

type sessionAdministrator interface {
	ListSessions(context.Context, *http.Cookie) ([]auth.Session, error)
	RevokeSession(context.Context, *http.Cookie, string, store.AuditEvent) error
	RevokeAllSessions(context.Context, *http.Cookie, store.AuditEvent) error
}

type Config struct {
	DevAssetsDir         string
	AllowedHosts         []string
	TrustedProxies       []netip.Prefix
	Development          bool
	BodyLimit            int64
	authRateLimit        int
	authRateWindow       time.Duration
	authRateClients      int
	expensiveConcurrency int
	now                  func() time.Time
}
type Dependencies struct {
	Documents     *documents.Service
	WakeDocuments func()
	Auth          Authenticator
	Store         *store.Store
	Config        Config
	Logger        *slog.Logger
}
type app struct {
	documents     *documents.Service
	wakeDocuments func()
	auth          Authenticator
	store         *store.Store
	config        Config
	logger        *slog.Logger
	templates     *template.Template
	static        http.Handler
	authLimiter   *clientRateLimiter
	expensive     chan struct{}
}

type pageData struct {
	Development                                                        bool
	AppJSURL                                                           string
	Document                                                           *store.Document
	PaperlessJob                                                       *store.PaperlessJob
	DocumentsEnabled                                                   bool
	Finalized                                                          *invoicing.FinalizedInvoice
	FinalizationKey, InvoiceConfirmation, InvoiceReason                string
	InvoiceState                                                       string
	FinalizationReady                                                  bool
	Nonce, CSRFToken, DisplayName, CSSURL, HTMXURL, InvoiceJSURL       string
	CompanyInput                                                       store.CompanyInput
	Customers                                                          store.CustomerPage
	Customer                                                           *store.Customer
	CustomerInput                                                      store.CustomerInput
	CustomerVersion                                                    int
	CustomerAction, CustomerTitle, Search                              string
	CustomerState, NextPageURL                                         string
	Catalog                                                            store.CatalogPage
	CatalogItem                                                        *store.CatalogItem
	CatalogInput                                                       store.CatalogInput
	CatalogVersion                                                     int
	CatalogAction, CatalogTitle, CatalogState                          string
	CatalogQuery, CatalogCurrency                                      string
	Invoices                                                           []store.InvoiceDraft
	Invoice                                                            *invoicing.Draft
	InvoicePreview                                                     *render.TplData
	InvoiceCustomers                                                   store.CustomerPage
	InvoiceCatalog                                                     store.CatalogPage
	InvoiceAction, InvoiceQuery                                        string
	InvoiceNextURL                                                     string
	InvoiceCustomerSearch, InvoiceCatalogSearch                        string
	InvoiceCustomerNextURL, InvoiceCatalogNextURL                      string
	InvoicePickerPath, InvoiceSelectedCustomer, InvoiceSelectedCatalog string
	InvoiceNavigation                                                  url.Values
	InvoiceServiceDateRaw, InvoiceManualRaw, InvoiceCatalogRaw         map[string]string
	Errors                                                             map[string]string
	Raw                                                                map[string]string
	Sessions                                                           []auth.Session
}

func New(deps Dependencies) (http.Handler, error) {
	if deps.Config.DevAssetsDir != "" {
		return reloadHandler(deps)
	}
	return newWithFiles(deps, embeddedFiles)
}

func newWithFiles(deps Dependencies, assets fs.FS) (http.Handler, error) {
	if deps.Auth == nil {
		return nil, errors.New("manager for Pocket ID is required")
	}
	if len(deps.Config.AllowedHosts) == 0 {
		return nil, errors.New("at least one allowed host is required")
	}
	if deps.Config.BodyLimit == 0 {
		deps.Config.BodyLimit = defaultBodyLimit
	}
	if deps.Config.BodyLimit != defaultBodyLimit {
		return nil, errors.New("body limit must be 1 MiB")
	}
	if deps.Config.authRateLimit <= 0 {
		deps.Config.authRateLimit = defaultAuthRateLimit
	}
	if deps.Config.authRateWindow <= 0 {
		deps.Config.authRateWindow = defaultAuthRateWindow
	}
	if deps.Config.authRateClients <= 0 {
		deps.Config.authRateClients = defaultAuthRateClients
	}
	if deps.Config.expensiveConcurrency <= 0 {
		deps.Config.expensiveConcurrency = defaultExpensiveConcurrent
	}
	if deps.Config.now == nil {
		deps.Config.now = time.Now
	}
	files, err := fs.Sub(assets, "static")
	if err != nil {
		return nil, err
	}
	t, err := template.New("pages").Funcs(templateFunctions()).ParseFS(assets, "templates/layout.html", "templates/login.html", "templates/company.html", "templates/customers.html", "templates/customer_detail.html", "templates/customer_form.html", "templates/catalog.html", "templates/catalog_detail.html", "templates/catalog_form.html", "templates/invoices.html", "templates/invoice_editor.html", "templates/invoice_items.html", "templates/invoice_review.html", "templates/invoice_detail.html", "templates/sessions.html")
	if err != nil {
		return nil, err
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	a := &app{documents: deps.Documents, wakeDocuments: deps.WakeDocuments, auth: deps.Auth, store: deps.Store, config: deps.Config, logger: logger, templates: t, static: http.StripPrefix("/assets/", http.FileServer(http.FS(files))), authLimiter: newClientRateLimiter(deps.Config.authRateLimit, deps.Config.authRateWindow, deps.Config.authRateClients, deps.Config.now), expensive: make(chan struct{}, deps.Config.expensiveConcurrency)}
	return a.chain(http.HandlerFunc(a.routes)), nil
}

func templateFunctions() template.FuncMap {
	return template.FuncMap{
		"developmentBanner": developmentBanner,
		"invoiceUnits":      func() string { return units.Help },
		"formValue": func(raw map[string]string, name, fallback string) string {
			if raw != nil {
				if value, ok := raw[name]; ok {
					return value
				}
			}
			return fallback
		},
		"hasError": func(errors map[string]string, field string) bool { _, ok := errors[field]; return ok },
		"errorID": func(errors map[string]string, field string) string {
			if _, ok := errors[field]; ok {
				return field + "-error"
			}
			return ""
		},
		"catalogMinor":    formatMinor,
		"invoiceMinor":    func(value int64) string { return formatMinor(int(value)) },
		"invoiceQuantity": formatInvoiceQuantity,
		"catalogTax":      formatTaxRate,
		"string":          func(value int) string { return strconv.Itoa(value) },
	}
}

func (a *app) routes(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/_health":
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "ok")
	case "/auth/login":
		a.login(w, r)
	case "/auth/signed-out":
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		a.renderTemplate(w, "login", pageData{Development: a.config.Development})
	case "/auth/callback":
		a.callback(w, r)
	case "/auth/logout":
		a.logout(w, r)
	case "/settings/company":
		a.company(w, r)
	case "/settings/sessions":
		a.sessions(w, r)
	case "/settings/sessions/revoke-all":
		a.revokeAllSessions(w, r)
	case "/customers":
		a.customers(w, r)
	case "/customers/new":
		a.customerNew(w, r)
	case "/catalog":
		a.catalog(w, r)
	case "/catalog/new":
		a.catalogNew(w, r)
	case "/invoices":
		a.invoices(w, r)
	case "/invoices/new":
		a.invoiceNew(w, r)
	default:
		if strings.HasPrefix(r.URL.Path, "/settings/sessions/") {
			a.sessionRoute(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			if r.Method != http.MethodGet {
				methodNotAllowed(w, http.MethodGet)
				return
			}
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			a.static.ServeHTTP(w, r)
			return
		}
		if r.URL.Path != "/" {
			if strings.HasPrefix(r.URL.Path, "/customers/") {
				a.customerRoute(w, r)
				return
			}
			if strings.HasPrefix(r.URL.Path, "/catalog/") {
				a.catalogRoute(w, r)
				return
			}
			if strings.HasPrefix(r.URL.Path, "/documents/") {
				a.documentRoute(w, r)
				return
			}
			if strings.HasPrefix(r.URL.Path, "/invoices/") {
				a.invoiceRoute(w, r)
				return
			}
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		a.home(w, r)
	}
}

func (a *app) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	destination, transaction, err := a.auth.Begin(r.URL.Query().Get("return_to"))
	if auditErr := a.audit(r, "", "auth.login", "authentication", "", auditResult(err)); auditErr != nil {
		a.logger.Error("persist authentication audit", "action", "auth.login")
		http.Error(w, "unable to start sign-in", http.StatusServiceUnavailable)
		return
	}
	if err != nil {
		http.Error(w, "unable to start sign-in", http.StatusServiceUnavailable)
		return
	}
	a.protectCookie(transaction, http.SameSiteLaxMode)
	http.SetCookie(w, transaction)
	http.Redirect(w, r, destination, http.StatusFound) // #nosec G710 -- Auth.Begin constructs the authorization URL from the validated provider endpoint; return_to is stored in the sealed transaction.
}
func (a *app) callback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	scheme := "https"
	if a.config.Development {
		scheme = "http"
	}
	if r.URL.Scheme != "" {
		scheme = r.URL.Scheme
	}
	callback := &url.URL{Scheme: scheme, Host: r.Host, Path: r.URL.Path, RawQuery: r.URL.RawQuery}
	result, err := a.auth.Callback(r.Context(), callback, cookie(r, auth.TransactionCookieName))
	if result.TransactionCookie != nil {
		a.protectCookie(result.TransactionCookie, http.SameSiteLaxMode)
		http.SetCookie(w, result.TransactionCookie)
	}
	if err != nil {
		if auditErr := a.audit(r, result.AuditSubject, "auth.callback", "authentication", "", "failure"); auditErr != nil {
			a.logger.Error("persist authentication audit", "action", "auth.callback")
			http.Error(w, "sign-in unavailable", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, "sign-in failed", http.StatusBadRequest)
		return
	}
	if result.SessionCookie == nil {
		if auditErr := a.audit(r, "", "auth.callback", "authentication", "", "failure"); auditErr != nil {
			a.logger.Error("persist authentication audit", "action", "auth.callback")
			http.Error(w, "sign-in unavailable", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, "sign-in failed", http.StatusBadRequest)
		return
	}
	if auditErr := a.audit(r, result.Principal.Subject, "auth.callback", "authentication", result.Principal.UserID, "success"); auditErr != nil {
		_ = a.auth.Logout(r.Context(), result.SessionCookie)
		a.logger.Error("persist authentication audit", "action", "auth.callback")
		http.Error(w, "sign-in unavailable", http.StatusServiceUnavailable)
		return
	}
	a.protectCookie(result.SessionCookie, http.SameSiteStrictMode)
	http.SetCookie(w, result.SessionCookie)
	http.Redirect(w, r, safeReturnTo(result.ReturnTo), http.StatusFound)
}
func (a *app) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	session := cookie(r, auth.SessionCookieName)
	err := a.auth.Logout(r.Context(), session)
	if auditErr := a.audit(r, "", "auth.logout", "authentication", "", auditResult(err)); auditErr != nil {
		a.logger.Error("persist authentication audit", "action", "auth.logout")
		http.Error(w, "unable to sign out", http.StatusInternalServerError)
		return
	}
	if err != nil {
		http.Error(w, "unable to sign out", http.StatusInternalServerError)
		return
	}
	deleted := &http.Cookie{Name: auth.SessionCookieNameForSecure(!a.config.Development), Value: "", Path: "/", MaxAge: -1} // #nosec G124 -- protectCookie applies Secure, HttpOnly and SameSite before SetCookie below.
	a.protectCookie(deleted, http.SameSiteStrictMode)
	http.SetCookie(w, deleted)
	// End the form redirect on this origin. A redirect through the external
	// provider is blocked by form-action 'self' in conforming browsers.
	http.Redirect(w, r, "/auth/signed-out", http.StatusSeeOther)
}
func (a *app) home(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFromContext(r.Context())
	session := cookie(r, auth.SessionCookieName)
	csrf, err := a.auth.CSRFToken(session)
	if err != nil {
		http.Error(w, "sign-in required", http.StatusUnauthorized)
		return
	}
	d := pageData{Development: a.config.Development, Nonce: nonceFromContext(r.Context()), CSRFToken: csrf, DisplayName: p.DisplayName, CSSURL: "/assets/app.css?v=" + assetVersion("app.css"), HTMXURL: "/assets/htmx.min.js?v=" + assetVersion("htmx.min.js")}
	d.AppJSURL = "/assets/app.js?v=" + assetVersion("app.js")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Header.Get("HX-Request") == "true" {
		if err := a.templates.ExecuteTemplate(w, "shell", d); err != nil {
			a.logger.Error("render fragment", "error", err)
		}
		return
	}
	if err := a.templates.ExecuteTemplate(w, "layout", d); err != nil {
		a.logger.Error("render page", "error", err)
	}
}
func (a *app) protectCookie(c *http.Cookie, sameSite http.SameSite) { // #nosec G124 -- Every caller supplies Lax or Strict; Secure is disabled only in validated loopback development mode.
	c.Path = "/"
	c.HttpOnly = true
	c.SameSite = sameSite
	c.Secure = !a.config.Development
}
func methodNotAllowed(w http.ResponseWriter, allowed string) {
	w.Header().Set("Allow", allowed)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
func cookie(r *http.Request, name string) *http.Cookie {
	c, _ := r.Cookie(name)
	if c != nil {
		return c
	}
	switch name {
	case auth.SessionCookieName:
		c, _ = r.Cookie(auth.DevelopmentSessionCookieName)
	case auth.TransactionCookieName:
		c, _ = r.Cookie(auth.DevelopmentTransactionCookieName)
	}
	return c
}
func safeReturnTo(value string) string {
	u, err := url.Parse(value)
	if err != nil || value == "" || u.IsAbs() || u.Host != "" || !strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") {
		return "/"
	}
	return u.RequestURI()
}
func randomNonce() string {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("random CSP nonce: %v", err))
	}
	return base64.RawStdEncoding.EncodeToString(b)
}
