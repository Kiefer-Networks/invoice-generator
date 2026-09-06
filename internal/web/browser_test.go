package web_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/kiefer-networks/invoice-generator/internal/auth"
	"github.com/kiefer-networks/invoice-generator/internal/devmode"
	"github.com/kiefer-networks/invoice-generator/internal/documents"
	"github.com/kiefer-networks/invoice-generator/internal/jobs"
	"github.com/kiefer-networks/invoice-generator/internal/paperless"
	"github.com/kiefer-networks/invoice-generator/internal/render"
	"github.com/kiefer-networks/invoice-generator/internal/store"
	"github.com/kiefer-networks/invoice-generator/internal/web"
)

func browserFixture(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	root := filepath.Join(t.TempDir(), "development")
	secrets, e := devmode.PrepareRoot(root)
	if e != nil {
		t.Fatal(e)
	}
	db, e := store.Open(ctx, filepath.Join(root, "development.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	if e = db.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	if e = devmode.Seed(ctx, db); e != nil {
		t.Fatal(e)
	}
	server := httptest.NewUnstartedServer(nil)
	server.Start()
	t.Cleanup(server.Close)
	provider, e := devmode.StartOIDC(server.URL + "/auth/callback")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(provider.Close)
	remote, e := devmode.StartPaperless("accepted")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(remote.Close)
	manager, e := auth.NewManager(ctx, db, auth.Config{IssuerURL: provider.URL, ClientID: devmode.ClientID, ClientSecretFile: secrets.Client, SessionKeyFile: secrets.Session, TransactionKeyFile: secrets.Transaction, RedirectURL: server.URL + "/auth/callback", RequiredGroup: "invoice-admins", Development: true})
	if e != nil {
		t.Fatal(e)
	}
	storage, e := documents.NewStorage(filepath.Join(root, "documents"), 20<<20)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { storage.Close() })
	svc := documents.New(db, storage)
	runner := jobs.New(db.DocumentRepository(), svc.Generate)
	if e = runner.Start(ctx); e != nil {
		t.Fatal(e)
	}
	worker := jobs.NewPaperlessWorker(db, storage, func() (*paperless.Client, error) {
		return paperless.NewClient(paperless.Config{URL: remote.URL, APIKey: devmode.PaperlessToken}, nil, true)
	})
	done := make(chan struct{})
	go func() { defer close(done); worker.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
		stop, close := context.WithTimeout(context.Background(), 10*time.Second)
		defer close()
		if e := runner.Stop(stop); e != nil {
			t.Error(e)
		}
	})
	handler, e := web.New(web.Dependencies{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Store: db, Auth: manager, Documents: svc, WakeDocuments: func() { runner.Wake(); worker.Wake() }, Config: web.Config{Development: true, AllowedHosts: []string{"127.0.0.1"}}})
	if e != nil {
		t.Fatal(e)
	}
	server.Config.Handler = handler
	return server, db
}

func TestBrowserWorkflow(t *testing.T) {
	chrome := render.FindChrome()
	if chrome == "" {
		t.Fatal("Chrome is required for browser workflow; set INVOICE_CHROME")
	}
	server, db := browserFixture(t)
	alloc, stop := chromedp.NewExecAllocator(context.Background(), append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(chrome), chromedp.NoSandbox)...)
	defer stop()
	ctx, close := chromedp.NewContext(alloc)
	defer close()
	ctx, cancel := context.WithTimeout(ctx, 180*time.Second)
	defer cancel()
	if e := chromedp.Run(ctx, chromedp.Navigate(server.URL), chromedp.WaitVisible(`//button[normalize-space(.)='Sign in without required group']`, chromedp.BySearch), chromedp.Click(`//button[normalize-space(.)='Sign in without required group']`, chromedp.BySearch), chromedp.WaitVisible(`//pre[contains(.,'sign-in failed')]`, chromedp.BySearch), chromedp.Navigate(server.URL), chromedp.WaitVisible(`//button[normalize-space(.)='Sign in as invoice admin']`, chromedp.BySearch), chromedp.Click(`//button[normalize-space(.)='Sign in as invoice admin']`, chromedp.BySearch), chromedp.WaitVisible(`//button[normalize-space(.)='Log out']`, chromedp.BySearch)); e != nil {
		t.Fatal(e)
	}
	var banner bool
	if e := chromedp.Run(ctx, chromedp.Evaluate(`!!document.querySelector('[data-testid="development-banner"]')`, &banner)); e != nil {
		t.Fatal(e)
	}
	if !banner {
		t.Fatal("persistent development banner unavailable after real OIDC login")
	}
	var createLink bool
	if e := chromedp.Run(ctx, chromedp.Evaluate(`Array.from(document.querySelectorAll('a')).some(a=>a.textContent==='Create invoice'&&a.getAttribute('href')==='/invoices/new')`, &createLink)); e != nil {
		t.Fatal(e)
	}
	if !createLink {
		t.Fatal("dashboard has no working create invoice link")
	}
	browserWorkflow(t, ctx, server.URL, db)
}

type browserUI struct {
	t   *testing.T
	ctx context.Context
}

func (u browserUI) run(label string, actions ...chromedp.Action) {
	u.t.Helper()
	ctx, cancel := context.WithTimeout(u.ctx, 15*time.Second)
	defer cancel()
	if e := chromedp.Run(ctx, actions...); e != nil {
		var body string
		_ = chromedp.Run(u.ctx, chromedp.Text("body", &body, chromedp.ByQuery))
		u.t.Fatalf("%s: %v\n%s", label, e, body)
	}
}
func (u browserUI) click(name string) {
	u.t.Helper()
	selector := fmt.Sprintf(`//*[self::a or self::button or self::summary][@aria-label=%q or normalize-space(.)=%q]`, name, name)
	u.run("find "+name, chromedp.WaitVisible(selector, chromedp.BySearch))
	raw, _ := json.Marshal(selector)
	var navigation bool
	u.run("control type", chromedp.Evaluate(`(()=>{const e=document.evaluate(`+string(raw)+`,document,null,XPathResult.FIRST_ORDERED_NODE_TYPE,null).singleNodeValue;return (e.tagName==='A'&&!e.hasAttribute('hx-get'))||(e.tagName==='BUTTON'&&e.form&&!e.form.hasAttribute('hx-post'))})()`, &navigation))
	if navigation {
		ctx, cancel := context.WithTimeout(u.ctx, 15*time.Second)
		defer cancel()
		if _, e := chromedp.RunResponse(ctx, chromedp.Click(selector, chromedp.BySearch)); e != nil {
			u.t.Fatalf("navigate %s: %v", name, e)
		}
	} else {
		u.run("click "+name, chromedp.Click(selector, chromedp.BySearch), chromedp.WaitNotPresent(`.htmx-request`, chromedp.ByQuery), chromedp.WaitNotPresent(`.htmx-settling`, chromedp.ByQuery))
	}
}
func (u browserUI) fill(label, value string) {
	u.t.Helper()
	selector := fmt.Sprintf(`//label[normalize-space(text())=%q]/*[self::input or self::textarea or self::select]`, label)
	u.run("fill "+label, chromedp.WaitVisible(selector, chromedp.BySearch), chromedp.SetValue(selector, value, chromedp.BySearch))
}
func (u browserUI) waitText(text string) {
	u.t.Helper()
	u.run("visible "+text, chromedp.WaitVisible(fmt.Sprintf(`//body//*[not(self::script) and contains(text(),%q)]`, text), chromedp.BySearch))
}

func browserWorkflow(t *testing.T, ctx context.Context, base string, db *store.Store) {
	ui := browserUI{t, ctx}
	ui.run("failed Paperless fixture", chromedp.Navigate(base+"/invoices/dev-invoice-finalized-0001"))
	ui.waitText("Paperless: failed")
	var previewStatus int
	ui.run("fixture PDF preview", chromedp.Evaluate(`(async()=>{const a=Array.from(document.querySelectorAll('a')).find(a=>a.textContent==='Preview PDF');return (await fetch(a.href)).status})()`, &previewStatus, func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
		return p.WithAwaitPromise(true).WithReturnByValue(true)
	}))
	if previewStatus != 200 {
		t.Fatalf("fixture preview status %d", previewStatus)
	}
	ui.click("Retry Paperless delivery")
	for i := 0; i < 30; i++ {
		var delivered bool
		ui.run("Paperless status", chromedp.Evaluate(`document.body.innerText.includes('Paperless: delivered')`, &delivered))
		if delivered {
			break
		}
		ui.run("refresh Paperless", chromedp.Reload())
		if i == 29 {
			t.Fatal("Paperless retry did not deliver")
		}
		time.Sleep(100 * time.Millisecond)
	}
	ui.click("Settings")
	ui.fill("Legal name", "Browser Example GmbH")
	ui.click("Save company profile")
	ui.click("Customers")
	ui.click("New customer")
	for _, field := range [][2]string{{"Customer number", "BROWSER-001"}, {"Display name", "Browser customer"}, {"Address line 1", "Example road 1"}, {"Postal code", "10115"}, {"City", "Berlin"}, {"Currency", "XXX"}} {
		ui.fill(field[0], field[1])
	}
	ui.click("Save customer")
	ui.waitText("must be EUR")
	var focused bool
	ui.run("validation summary focus", chromedp.Evaluate(`document.activeElement?.id==='validation-summary'`, &focused))
	if !focused {
		t.Fatal("validation summary did not receive keyboard focus after HTMX error")
	}
	ui.fill("Currency", "EUR")
	ui.click("Save customer")
	ui.waitText("Edit customer")
	var listed bool
	ui.run("new customer list entry", chromedp.Evaluate(`Array.from(document.querySelectorAll('.list-pane a')).some(a=>a.textContent.includes('Browser customer'))`, &listed))
	if !listed {
		t.Fatal("new customer absent from list after HTMX save")
	}
	ui.click("Edit customer")
	ui.fill("Display name", "Browser customer edited")
	ui.click("Save customer")
	ui.waitText("Browser customer edited")
	ui.click("Archive customer")
	ui.waitText("Restore customer")
	ui.click("Restore customer")
	ui.waitText("Archive customer")
	ui.click("Goods and services")
	ui.click("New catalog item")
	for _, field := range [][2]string{{"Item number", "BROWSER-S001"}, {"Kind", "service"}, {"Title", "Browser consulting"}, {"Unit", "hour"}, {"Net unit price", "125.00"}, {"Tax rate (%)", "19"}} {
		ui.fill(field[0], field[1])
	}
	ui.click("Save catalog item")
	ui.waitText("Edit catalog item")
	ui.click("Invoices")
	ui.click("New invoice")
	var customerID string
	if e := db.DB().QueryRow(`SELECT id FROM customers WHERE number='BROWSER-001'`).Scan(&customerID); e != nil {
		t.Fatal(e)
	}
	ui.fill("Customer", customerID)
	ui.click("Create draft")
	ui.waitText("Draft invoice")
	ui.fill("Service or delivery date", "2026-09-06")
	ui.click("Save service date")
	var catalogID string
	if e := db.DB().QueryRow(`SELECT id FROM catalog_items WHERE number='BROWSER-S001'`).Scan(&catalogID); e != nil {
		t.Fatal(e)
	}
	ui.fill("Catalog item", catalogID)
	ui.click("Add catalog item")
	ui.waitText("148.75")
	ui.click("Edit position")
	ui.run("edit quantity", chromedp.SetValue(`//details//label[normalize-space(text())='Quantity']/input`, "2", chromedp.BySearch))
	ui.click("Save position")
	ui.waitText("297.50")
	ui.run("preview totals", chromedp.WaitVisible(`//*[@aria-label='Draft document preview']//p[contains(.,'297.50')]`, chromedp.BySearch))
	ui.click("Review and finalize")
	ui.waitText("Ready to finalize")
	ui.waitText("Paperless delivery is queued when the PDF is ready.")
	ui.run("confirm review", chromedp.Click(`//label[contains(.,'I confirm the recipient')]/input`, chromedp.BySearch))
	ui.click("Confirm finalization")
	ui.waitText("Status: finalized")
	var invoiceURL string
	ui.run("invoice URL", chromedp.Location(&invoiceURL))
	for i := 0; i < 30; i++ {
		var ready bool
		ui.run("document state", chromedp.Evaluate(`document.body.innerText.includes('Download PDF with ZUGFeRD')`, &ready))
		if ready {
			break
		}
		ui.run("refresh document status", chromedp.Reload())
		if i == 29 {
			t.Fatal("document never became ready")
		}
		time.Sleep(200 * time.Millisecond)
	}
	var download string
	ui.run("download link", chromedp.AttributeValue(`//a[normalize-space(.)='Download PDF with ZUGFeRD']`, "href", &download, nil, chromedp.BySearch))
	var pdfResult struct {
		Status int
		Type   string
		Size   int
		Prefix string
	}
	raw, _ := json.Marshal(download)
	ui.run("download PDF", chromedp.Evaluate(`(async()=>{const r=await fetch(`+string(raw)+`);const b=new Uint8Array(await r.arrayBuffer());return {Status:r.status,Type:r.headers.get('content-type'),Size:b.length,Prefix:new TextDecoder().decode(b.slice(0,5))}})()`, &pdfResult, func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
		return p.WithAwaitPromise(true).WithReturnByValue(true)
	}))
	if pdfResult.Status != 200 || pdfResult.Type != "application/pdf" || pdfResult.Size < 1000 || pdfResult.Prefix != "%PDF-" {
		t.Fatalf("download failed: %+v", pdfResult)
	}
	ui.click("Mark paid")
	ui.waitText("Status: paid")
	ui.run("second invoice", chromedp.Navigate(base+"/invoices/dev-invoice-finalized-0002"))
	ui.click("Cancel invoice")
	ui.fill("Cancellation reason", "Browser cancellation check")
	ui.run("confirm cancellation", chromedp.Click(`//label[contains(.,'I confirm cancellation')]/input`, chromedp.BySearch))
	ui.click("Confirm cancellation")
	ui.waitText("Status: cancelled")
	ui.click("Create correction draft")
	ui.waitText("Correction of")
	ui.run("return to issued invoice", chromedp.Navigate(invoiceURL))
	ui.waitText("Status: paid")
	ui.run("phone viewport", chromedp.EmulateViewport(390, 844))
	var overflow bool
	ui.run("mobile containment", chromedp.Evaluate(`document.documentElement.scrollWidth>window.innerWidth`, &overflow))
	if overflow {
		t.Fatal("finalized invoice overflows phone viewport")
	}
	ui.run("desktop viewport", chromedp.EmulateViewport(1440, 900))
	ui.click("Settings")
	ui.run("invalid CSRF form", chromedp.Evaluate(`document.querySelector('form[action="/settings/company"] input[name="csrf_token"]').value='invalid-test-token'`, nil))
	ui.click("Save company profile")
	ui.waitText("Request rejected. Reload this page and try again.")
	ui.run("restore normal form", chromedp.Reload())
	ui.click("Log out")
	ui.waitText("Sign in required")
	var signedOutBanner bool
	ui.run("signed-out development banner", chromedp.Evaluate(`!!document.querySelector('[data-testid="development-banner"]')`, &signedOutBanner))
	if !signedOutBanner {
		t.Fatal("development banner missing after logout")
	}
	ui.click("Sign in with Pocket ID")
	ui.waitText("Development Pocket ID")
}
