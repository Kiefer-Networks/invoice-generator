//go:build !production

package web_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
	"github.com/kiefer-networks/invoice-generator/internal/auth"
	"github.com/kiefer-networks/invoice-generator/internal/devmode"
	"github.com/kiefer-networks/invoice-generator/internal/documents"
	"github.com/kiefer-networks/invoice-generator/internal/jobs"
	"github.com/kiefer-networks/invoice-generator/internal/paperless"
	"github.com/kiefer-networks/invoice-generator/internal/render"
	"github.com/kiefer-networks/invoice-generator/internal/store"
	"github.com/kiefer-networks/invoice-generator/internal/web"
)

func browserFixture(t *testing.T) (*httptest.Server, *store.Store, *devmode.Paperless, *documents.Service) {
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
	t.Cleanup(func() { _ = db.Close() })
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
	remote, e := devmode.StartPaperless("reject-once")
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
	t.Cleanup(func() { _ = storage.Close() })
	svc := documents.New(db, storage)
	runner := jobs.New(db.DocumentRepository(), svc.Generate)
	worker := jobs.NewPaperlessWorker(db, storage, func() (*paperless.Client, error) {
		return paperless.NewClient(paperless.Config{URL: remote.URL, APIKey: devmode.PaperlessToken}, nil, true)
	})
	// Exercise the last automatic attempt without waiting through production backoff.
	// The real worker must classify the fake's definitive rejection before a user retries.
	if _, e = db.DB().ExecContext(ctx, `UPDATE paperless_jobs SET state='queued',attempts=4,next_attempt_at=0 WHERE document_id='dev-document-finalized-0001'`); e != nil {
		t.Fatal(e)
	}
	repo := db.PaperlessRepository()
	j, e := repo.Claim(ctx, time.Now(), 5*time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if e = worker.Process(ctx, j); e == nil || e.Error() != "request_failed" {
		t.Fatalf("reject-once classification: %v", e)
	}
	if e = repo.Fail(ctx, j, time.Now(), e.Error(), 5); e != nil {
		t.Fatal(e)
	}
	j, e = repo.ForDocument(ctx, j.DocumentID)
	if e != nil || j.State != "failed" || j.ErrorCode != "request_failed" || j.UploadStarted || j.RemoteTaskID != "" || j.RemoteDocumentID != 0 {
		t.Fatalf("definitive rejection was not safely retryable: %+v %v", j, e)
	}
	assertRemoteDocumentCount(t, remote, 0)
	t.Log("reject-once: real worker classified request_failed; upload intent cleared; terminal failed; zero remote invoice documents")
	if e = runner.Start(ctx); e != nil {
		t.Fatal(e)
	}
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
	return server, db, remote, svc
}

func assertRemoteDocumentCount(t *testing.T, remote *devmode.Paperless, want int) {
	t.Helper()
	title := paperless.Title("DEV-2026-0001", "dev-document-finalized-0001")
	req, e := http.NewRequest(http.MethodGet, remote.URL+"/api/documents/?title__iexact="+url.QueryEscape(title), nil)
	if e != nil {
		t.Fatal(e)
	}
	req.Header.Set("Authorization", "Token "+devmode.PaperlessToken)
	res, e := remote.Client().Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	var result struct{ Count int }
	if e = json.NewDecoder(res.Body).Decode(&result); e != nil || res.StatusCode != 200 || result.Count != want {
		t.Fatalf("remote invoice document count: got %d want %d, status %d, %v", result.Count, want, res.StatusCode, e)
	}
}

func TestBrowserWorkflow(t *testing.T) {
	t.Cleanup(func() {
		for _, name := range []string{"pids.current", "pids.events", "cpu.stat", "memory.events"} {
			if data, err := os.ReadFile("/sys/fs/cgroup/" + name); err == nil { // #nosec G304 -- Fixture file in a test-owned temporary directory; no HTTP or external input selects this path.
				t.Logf("cgroup %s: %s", name, data)
			}
		}
	})
	chrome := render.FindChrome()
	if chrome == "" {
		t.Fatal("Chrome is required for browser workflow; set INVOICE_CHROME")
	}
	server, db, remote, svc := browserFixture(t)
	options := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	options = append(options,
		chromedp.ExecPath(chrome),
		chromedp.DisableGPU,
		chromedp.Flag("disable-gpu-compositing", true),
		chromedp.Flag("use-gl", "disabled"),
		// The test container is already unprivileged and uses a dedicated
		// seccomp profile; Chromium's nested filter rejects a harmless syscall
		// while the PDF worker runs alongside the browser session.
		chromedp.Flag("disable-seccomp-filter-sandbox", true),
	)
	if os.Geteuid() == 0 {
		options = append(options, chromedp.NoSandbox)
	}
	alloc, stop := chromedp.NewExecAllocator(context.Background(), options...)
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
	if e := chromedp.Run(ctx, chromedp.Evaluate(`Array.from(document.querySelectorAll('a')).some(a=>a.getAttribute('aria-label')==='Create invoice'&&a.getAttribute('href')==='/invoices/new')`, &createLink)); e != nil {
		t.Fatal(e)
	}
	if !createLink {
		t.Fatal("dashboard has no working create invoice link")
	}
	browserWorkflow(t, ctx, server.URL, db, svc)
	assertRemoteDocumentCount(t, remote, 1)
	t.Log("protected manual retry delivered the rejected invoice exactly once")
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

func browserWorkflow(t *testing.T, ctx context.Context, base string, db *store.Store, svc *documents.Service) {
	ui := browserUI{t, ctx}
	ui.run("failed Paperless fixture", chromedp.Navigate(base+"/invoices/dev-invoice-finalized-0001"))
	ui.waitText("Paperless: failed")
	var retryURL string
	ui.run("retry form action", chromedp.AttributeValue(`//form[button[@aria-label='Retry Paperless delivery']]`, "action", &retryURL, nil, chromedp.BySearch))
	anonymous := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, e := anonymous.Post(base+retryURL, "application/x-www-form-urlencoded", nil)
	if e != nil {
		t.Fatal(e)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusSeeOther && res.StatusCode != http.StatusFound {
		t.Fatalf("anonymous retry was not redirected to authentication: %d", res.StatusCode)
	}
	ui.run("invalid retry CSRF", chromedp.Evaluate(`document.querySelector('form[action$="/paperless-retry"] input[name="csrf_token"]').value='invalid-test-token'`, nil))
	ui.click("Retry Paperless delivery")
	ui.waitText("Request rejected. Reload this page and try again.")
	j, e := db.PaperlessRepository().ForDocument(ctx, "dev-document-finalized-0001")
	if e != nil || j.State != "failed" || j.Attempts != 5 {
		t.Fatalf("protected retry mutated the job: %+v %v", j, e)
	}
	ui.run("return to retry form", chromedp.Navigate(base+"/invoices/dev-invoice-finalized-0001"))
	var previewStatus int
	ui.run("fixture PDF preview", chromedp.Evaluate(`(async()=>{const a=document.querySelector('a[aria-label="Preview PDF"]');return (await fetch(a.href)).status})()`, &previewStatus, func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
		return p.WithAwaitPromise(true).WithReturnByValue(true)
	}))
	if previewStatus != 200 {
		_, cause := svc.Preview(ctx, "dev-invoice-finalized-0001")
		t.Fatalf("fixture preview status %d; independent renderer: %v", previewStatus, cause)
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
	ui.run("disconnect before save", network.Enable(), network.SetBlockedURLs().WithURLPatterns([]*network.BlockPattern{{URLPattern: base + "/settings/company", Block: true}}))
	ui.click("Save company profile")
	ui.waitText("Connection unavailable. Your entries are still here; please try again.")
	var retained bool
	ui.run("failed request preserves inputs and focuses feedback", chromedp.Evaluate(`document.querySelector('input[name="legal_name"]').value==='Browser Example GmbH' && document.activeElement.id==='request-feedback'`, &retained))
	if !retained {
		t.Fatal("network failure lost inputs or accessible feedback focus")
	}
	ui.run("restore connection", network.SetBlockedURLs())
	ui.click("Save company profile")
	var staleFeedback bool
	ui.run("successful retry clears network error", chromedp.Evaluate(`!!document.getElementById('request-feedback')`, &staleFeedback))
	if staleFeedback {
		t.Fatal("successful HTMX retry leaves stale network error visible")
	}
	if company, e := db.CompanyRepository().Get(ctx); e != nil || company.LegalName != "Browser Example GmbH" {
		t.Fatalf("successful retry did not persist company input: %v", e)
	}
	t.Log("browser network failure retained form input and focused alert; successful HTMX retry removed stale feedback")
	ui.click("Customers")
	ui.click("New customer")
	ui.run("focus first customer field", chromedp.Focus(`input[name="number"]`, chromedp.ByQuery), chromedp.KeyEvent(kb.Tab))
	var nextField string
	ui.run("keyboard forward focus", chromedp.Evaluate(`document.activeElement.name`, &nextField))
	if nextField != "display_name" {
		t.Fatalf("Tab from customer number focused %q", nextField)
	}
	ui.run("keyboard reverse focus", chromedp.KeyEvent(kb.Tab, chromedp.KeyModifiers(input.ModifierShift)), chromedp.Evaluate(`document.activeElement.name`, &nextField))
	if nextField != "number" {
		t.Fatalf("Shift+Tab from display name focused %q", nextField)
	}
	t.Log("keyboard Tab and Shift+Tab traverse customer fields in order")
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
	if response, e := chromedp.RunResponse(ctx, chromedp.Evaluate(`document.querySelector('form[action="/customers/new"]').submit()`, nil)); e != nil || response.Status != http.StatusBadRequest {
		t.Fatalf("native form validation response: %+v %v", response, e)
	}
	ui.waitText("must be EUR")
	var fullPageFallback bool
	ui.run("full-page validation preserves fields and focuses summary", chromedp.Evaluate(`document.activeElement.id==='validation-summary' && document.querySelector('input[name="currency"]').value==='XXX' && document.querySelector('input[name="display_name"]').value==='Browser customer' && !!document.querySelector('aside[aria-label="Primary navigation"]')`, &fullPageFallback))
	if !fullPageFallback {
		t.Fatal("full-page validation lost page shell, submitted values, or keyboard focus")
	}
	t.Log("native full-page POST returned 400 with page shell, retained invalid values, and focused validation summary")
	ui.fill("Currency", "EUR")
	ui.click("Save customer")
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
	ui.click("Restore customer")
	ui.run("archive control restored", chromedp.WaitVisible(`//*[@aria-label='Archive customer']`, chromedp.BySearch))
	ui.click("Goods and services")
	ui.click("New catalog item")
	for _, field := range [][2]string{{"Item number", "BROWSER-S001"}, {"Kind", "service"}, {"Title", "Browser consulting"}, {"Unit", "hour"}, {"Net unit price", "125.00"}, {"Tax rate (%)", "19"}} {
		ui.fill(field[0], field[1])
	}
	ui.click("Save catalog item")
	ui.run("catalog edit control", chromedp.WaitVisible(`//*[@aria-label='Edit catalog item']`, chromedp.BySearch))
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
	for i := 0; i < 60; i++ {
		var ready bool
		ui.run("document state", chromedp.Evaluate(`!!document.querySelector('a[aria-label="Download PDF with ZUGFeRD"]')`, &ready))
		if ready {
			break
		}
		// A worker update can briefly interrupt navigation while the response is
		// being replaced. Keep polling the same invoice and tolerate that
		// transient reload error; the next state check remains authoritative.
		reloadCtx, cancelReload := context.WithTimeout(ui.ctx, 15*time.Second)
		_ = chromedp.Run(reloadCtx, chromedp.Reload())
		cancelReload()
		if i == 59 {
			t.Fatal("document never became ready")
		}
		time.Sleep(250 * time.Millisecond)
	}
	var download string
	ui.run("download link", chromedp.AttributeValue(`//a[@aria-label='Download PDF with ZUGFeRD']`, "href", &download, nil, chromedp.BySearch))
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
