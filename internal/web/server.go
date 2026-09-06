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
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/kiefer-networks/invoice-generator/internal/auth"
	"github.com/kiefer-networks/invoice-generator/internal/invoicing"
	"github.com/kiefer-networks/invoice-generator/internal/render"
	"github.com/kiefer-networks/invoice-generator/internal/store"
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

type Config struct {
	AllowedHosts   []string
	TrustedProxies []netip.Prefix
	Development    bool
	BodyLimit      int64
}
type Dependencies struct {
	Auth   Authenticator
	Store  *store.Store
	Config Config
	Logger *slog.Logger
}
type app struct {
	auth      Authenticator
	store     *store.Store
	config    Config
	logger    *slog.Logger
	templates *template.Template
	static    http.Handler
}

type pageData struct {
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
	InvoiceManualRaw, InvoiceCatalogRaw                                map[string]string
	Errors                                                             map[string]string
	Raw                                                                map[string]string
}

func New(deps Dependencies) (http.Handler, error) {
	if deps.Auth == nil {
		return nil, errors.New("Pocket ID manager is required")
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
	files, err := staticFS()
	if err != nil {
		return nil, err
	}
	t, err := template.New("pages").Funcs(templateFunctions()).ParseFS(embeddedFiles, "templates/layout.html", "templates/login.html", "templates/company.html", "templates/customers.html", "templates/customer_detail.html", "templates/customer_form.html", "templates/catalog.html", "templates/catalog_detail.html", "templates/catalog_form.html", "templates/invoices.html", "templates/invoice_editor.html", "templates/invoice_items.html")
	if err != nil {
		return nil, err
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	a := &app{auth: deps.Auth, store: deps.Store, config: deps.Config, logger: logger, templates: t, static: http.StripPrefix("/assets/", http.FileServer(http.FS(files)))}
	return a.chain(http.HandlerFunc(a.routes)), nil
}

func templateFunctions() template.FuncMap {
	return template.FuncMap{
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
	case "/auth/callback":
		a.callback(w, r)
	case "/auth/logout":
		a.logout(w, r)
	case "/settings/company":
		a.company(w, r)
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
	if err != nil {
		http.Error(w, "unable to start sign-in", http.StatusServiceUnavailable)
		return
	}
	a.protectCookie(transaction, http.SameSiteLaxMode)
	http.SetCookie(w, transaction)
	http.Redirect(w, r, destination, http.StatusFound)
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
	result, err := a.auth.Callback(r.Context(), callback, cookie(r, "invoice_oidc_transaction"))
	if result.TransactionCookie != nil {
		a.protectCookie(result.TransactionCookie, http.SameSiteLaxMode)
		http.SetCookie(w, result.TransactionCookie)
	}
	if err != nil {
		http.Error(w, "sign-in failed", http.StatusBadRequest)
		return
	}
	if result.SessionCookie == nil {
		http.Error(w, "sign-in failed", http.StatusBadRequest)
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
	session := cookie(r, "invoice_session")
	if err := a.auth.Logout(r.Context(), session); err != nil {
		http.Error(w, "unable to sign out", http.StatusInternalServerError)
		return
	}
	deleted := &http.Cookie{Name: "invoice_session", Value: "", Path: "/", MaxAge: -1}
	a.protectCookie(deleted, http.SameSiteStrictMode)
	http.SetCookie(w, deleted)
	http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
}
func (a *app) home(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFromContext(r.Context())
	session := cookie(r, "invoice_session")
	csrf, err := a.auth.CSRFToken(session)
	if err != nil {
		http.Error(w, "sign-in required", http.StatusUnauthorized)
		return
	}
	d := pageData{Nonce: nonceFromContext(r.Context()), CSRFToken: csrf, DisplayName: p.DisplayName, CSSURL: "/assets/app.css?v=" + assetVersion("app.css"), HTMXURL: "/assets/htmx.min.js?v=" + assetVersion("htmx.min.js")}
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
func (a *app) protectCookie(c *http.Cookie, sameSite http.SameSite) {
	c.Path = "/"
	c.HttpOnly = true
	c.SameSite = sameSite
	c.Secure = !a.config.Development
}
func methodNotAllowed(w http.ResponseWriter, allowed string) {
	w.Header().Set("Allow", allowed)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
func cookie(r *http.Request, name string) *http.Cookie { c, _ := r.Cookie(name); return c }
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
