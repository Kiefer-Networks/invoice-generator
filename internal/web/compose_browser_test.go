//go:build !production

package web_test

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/kiefer-networks/invoice-generator/internal/render"
	"github.com/kiefer-networks/invoice-generator/internal/store"
)

func composeBrowser(t *testing.T) (browserUI, string, func()) {
	t.Helper()
	base := os.Getenv("INVOICE_BROWSER_BASE_URL")
	if base == "" {
		t.Skip("external Compose server required")
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme != "http" || parsed.Host != "127.0.0.1:8080" || parsed.Path != "" {
		t.Fatal("Compose smoke requires exact local server URL")
	}
	allocator, stop := chromedp.NewExecAllocator(context.Background(), append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(render.FindChrome()))...)
	ctx, closeBrowser := chromedp.NewContext(allocator)
	ctx, cancel := context.WithTimeout(ctx, 180*time.Second)
	ui := browserUI{t, ctx}
	if err := chromedp.Run(ctx, chromedp.Navigate(base), chromedp.WaitVisible(`//button[normalize-space(.)='Sign in as invoice admin']`, chromedp.BySearch), chromedp.Click(`//button[normalize-space(.)='Sign in as invoice admin']`, chromedp.BySearch), chromedp.WaitVisible(`//button[normalize-space(.)='Log out']`, chromedp.BySearch)); err != nil {
		cancel()
		closeBrowser()
		stop()
		t.Fatal(err)
	}
	return ui, base, func() { cancel(); closeBrowser(); stop() }
}

// The drill copies synthetic application data into an isolated production-schema
// volume. Its dev-only reseeding marker is not part of any production migration.
func TestPrepareRecoveryFixture(t *testing.T) {
	if os.Getenv("INVOICE_RECOVERY_FIXTURE") != "1" {
		t.Skip("isolated recovery copy required")
	}
	ctx := context.Background()
	db, err := store.Open(ctx, "/data/database/invoice.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(ctx, "DROP TABLE development_fixture_version"); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.IntegrityCheck(ctx, "/data/database/invoice.sqlite", "/data/documents"); err != nil {
		t.Fatal(err)
	}
}

func TestComposeBrowserWorkflow(t *testing.T) {
	ui, _, closeBrowser := composeBrowser(t)
	defer closeBrowser()
	ui.click("Invoices")
	ui.click("New invoice")
	var customer string
	ui.run("actual persisted customer", chromedp.Evaluate(`Array.from(document.querySelectorAll('select option')).find(o=>o.textContent.includes('Example Studio')).value`, &customer))
	ui.fill("Customer", customer)
	ui.click("Create draft")
	ui.waitText("Draft invoice")
	ui.fill("Service or delivery date", time.Now().UTC().Format("2006-01-02"))
	ui.click("Save service date")
	var catalog string
	ui.run("actual persisted catalog", chromedp.Evaluate(`Array.from(document.querySelectorAll('select option')).find(o=>o.textContent.includes('Development consulting')).value`, &catalog))
	ui.fill("Catalog item", catalog)
	ui.click("Add catalog item")
	ui.waitText("119.00")
	ui.click("Review and finalize")
	ui.waitText("Ready to finalize")
	ui.run("confirm actual invoice", chromedp.Click(`//label[contains(.,'I confirm the recipient')]/input`, chromedp.BySearch))
	ui.click("Confirm finalization")
	ui.waitText("Status: finalized")
	waitComposeDelivery(ui)
	var invoiceURL string
	ui.run("actual invoice URL", chromedp.Location(&invoiceURL))
	if err := os.WriteFile("/development/compose-smoke-url", []byte(invoiceURL), 0600); err != nil {
		t.Fatal(err)
	}
	checkComposePDF(ui)
	ui.click("Log out")
	ui.waitText("Sign in required")
	t.Log("actual Compose startup, mounted SQLite, OIDC login, finalization, Chrome+CII PDF and Paperless delivery verified")
}

func TestComposeBrowserPersistence(t *testing.T) {
	ui, base, closeBrowser := composeBrowser(t)
	defer closeBrowser()
	data, err := os.ReadFile("/development/compose-smoke-url")
	if err != nil {
		t.Fatal(err)
	}
	invoiceURL := string(data)
	if !strings.HasPrefix(invoiceURL, base+"/invoices/") {
		t.Fatal("unexpected smoke record URL")
	}
	ui.run("restored actual invoice", chromedp.Navigate(invoiceURL))
	ui.waitText("Status: finalized")
	ui.waitText("Paperless: delivered")
	checkComposePDF(ui)
	t.Log("actual Compose process restart preserved the finalized invoice and durable PDF")
}

func waitComposeDelivery(ui browserUI) {
	for i := 0; i < 120; i++ {
		var delivered bool
		ui.run("actual delivery state", chromedp.Evaluate(`document.body.innerText.includes('Paperless: delivered') && document.body.innerText.includes('Download PDF with ZUGFeRD')`, &delivered))
		if delivered {
			return
		}
		time.Sleep(500 * time.Millisecond)
		ui.run("refresh actual delivery", chromedp.Reload())
	}
	ui.t.Fatal("actual Compose document/Paperless job did not finish")
}

func checkComposePDF(ui browserUI) {
	var download string
	ui.run("actual PDF link", chromedp.AttributeValue(`//a[normalize-space(.)='Download PDF with ZUGFeRD']`, "href", &download, nil, chromedp.BySearch))
	raw, _ := json.Marshal(download)
	var valid bool
	ui.run("actual durable PDF", chromedp.Evaluate(`(async()=>{const r=await fetch(`+string(raw)+`);const b=new Uint8Array(await r.arrayBuffer());return r.status===200&&r.headers.get('content-type')==='application/pdf'&&b.length>1000&&new TextDecoder().decode(b.slice(0,5))==='%PDF-'})()`, &valid, func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
		return p.WithAwaitPromise(true).WithReturnByValue(true)
	}))
	if !valid {
		ui.t.Fatal("actual Compose PDF download invalid")
	}
}
