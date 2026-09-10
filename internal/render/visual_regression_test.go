//go:build visual

package render

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/kiefer-networks/invoice-generator/internal/config"
)

// A change to CSS spacing, pagination, print margins, font metrics or the
// renderer's footer must fail this test before it can clip invoice amounts.
func TestVisualInvoice(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || os.Getenv("INVOICE_VISUAL_RUNTIME") != "alpine-3.24.1-chromium-152" {
		t.Fatal("run visual tests with bash scripts/test-visual.sh (pinned runtime required)")
	}
	if os.Geteuid() == 0 {
		t.Fatal("visual Chromium must run as nonroot")
	}
	version, err := exec.Command(FindChrome(), "--version").Output()
	if err != nil || !strings.HasPrefix(string(version), "Chromium 152.0.7977.82 ") {
		t.Fatal("unexpected visual Chromium version")
	}
	update := os.Getenv("INVOICE_UPDATE_VISUAL") == "1"
	if update && os.Getenv("CI") != "" {
		t.Fatal("golden updates are forbidden in CI")
	}
	dir := t.TempDir()
	data := visualInvoice()
	body, err := SnapshotHTML(data)
	if err != nil {
		t.Fatal("render synthetic HTML failed")
	}
	htmlPath := filepath.Join(dir, "invoice.html")
	if err = os.WriteFile(htmlPath, []byte(body), 0600); err != nil {
		t.Fatal("write synthetic HTML failed")
	}
	ctx, cancel := visualBrowser(t)
	defer cancel()
	var screenshot []byte
	var violations []string
	if err = chromedp.Run(ctx, chromedp.EmulateViewport(794, 1123),
		chromedp.Navigate("file://"+htmlPath),
		chromedp.Poll(`document.fonts.status === 'loaded'`, nil),
		chromedp.Evaluate(visualBoundsJS, &violations), chromedp.FullScreenshot(&screenshot, 100)); err != nil {
		t.Fatalf("capture synthetic HTML failed: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("HTML layout violations: %v", violations)
	}
	if err = os.WriteFile(filepath.Join(dir, "html.png"), screenshot, 0600); err != nil {
		t.Fatal("write HTML screenshot failed")
	}
	// Prove the DOM guard sees real browser overlap and clipping, even if a
	// future baseline update accidentally blesses a bad screenshot.
	for name, css := range map[string]string{
		"overlap":  `.sum-area { margin-top: -100px !important; }`,
		"clipping": `.d-title { height: 1px !important; overflow: hidden !important; }`,
	} {
		t.Run("reject_"+name, func(t *testing.T) {
			var detected []string
			if err := chromedp.Run(ctx,
				chromedp.Evaluate(`document.querySelector('#regression')?.remove(); var s=document.createElement('style'); s.id='regression'; s.textContent=`+fmt.Sprintf("%q", css)+`; document.head.append(s)`, nil),
				chromedp.Evaluate(visualBoundsJS, &detected)); err != nil {
				t.Fatal("layout mutation failed")
			}
			if len(detected) == 0 {
				t.Fatal("layout guard accepted injected " + name)
			}
		})
	}
	t.Run("reject_pixel_drift", func(t *testing.T) {
		var shifted []byte
		if err := chromedp.Run(ctx,
			chromedp.Evaluate(`document.querySelector('#regression')?.remove(); document.querySelector('.grand').style.transform='translateX(2px)'`, nil),
			chromedp.FullScreenshot(&shifted, 100)); err != nil {
			t.Fatal("layout drift mutation failed")
		}
		before, errBefore := png.Decode(bytes.NewReader(screenshot))
		after, errAfter := png.Decode(bytes.NewReader(shifted))
		if errBefore != nil || errAfter != nil {
			t.Fatal("decode layout mutation failed")
		}
		if visualDifference(before, after) == nil {
			t.Fatal("comparison accepted a two-pixel total bar shift")
		}
	})
	// Release HTML Chromium before starting the production PDF renderer; two
	// browser process trees can exhaust the container's bounded PID budget.
	cancel()
	pdfPath := filepath.Join(dir, "invoice.pdf")
	if err = FromSnapshot(context.Background(), data, pdfPath); err != nil {
		t.Fatal("render synthetic PDF failed")
	}
	if err = exec.Command("pdftoppm", "-r", "96", "-png", pdfPath, filepath.Join(dir, "pdf")).Run(); err != nil {
		t.Fatal("rasterize synthetic PDF failed")
	}
	checkVisualPDF(t, pdfPath, dir)
	if t.Failed() {
		t.Fatal("layout invariants failed; refusing baseline comparison or update")
	}
	actual, _ := filepath.Glob(filepath.Join(dir, "*.png"))
	goldens, _ := filepath.Glob("/golden/*.png")
	if len(actual) < 3 {
		t.Fatal("fixture must include HTML and multiple PDF pages")
	}
	if !update && len(actual) != len(goldens) {
		t.Fatalf("visual golden/page count: got %d images, want %d; review explicit golden update", len(actual), len(goldens))
	}
	for _, path := range actual {
		name := filepath.Base(path)
		goldenPath := filepath.Join("/golden", name)
		if update {
			b, e := os.ReadFile(path)
			if e != nil || os.WriteFile(goldenPath, b, 0644) != nil {
				t.Fatal("write golden failed: " + name)
			}
			continue
		}
		if err := visualDifference(readVisualPNG(t, goldenPath), readVisualPNG(t, path)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if update {
		// Deleting obsolete PNGs is confined to the explicit baseline directory.
		for _, old := range goldens {
			if _, err := os.Stat(filepath.Join(dir, filepath.Base(old))); os.IsNotExist(err) {
				if os.Remove(old) != nil {
					t.Fatal("remove obsolete golden failed")
				}
			}
		}
	}
}

func visualInvoice() *TplData {
	cfg := sampleConfig()
	d := PrepareTplData(cfg, resolveLoc(cfg), config.DocInvoice)
	d.Title, d.DocumentKind, d.InvNumber = "Correction invoice", "correction", "INV-2026-0042"
	d.CorrectionOfNumber, d.ServiceDate = "INV-2026-0031", "31.08.2026"
	d.InvDate, d.DueDate, d.Currency = "06.09.2026", "20.09.2026", "EUR"
	d.CompanyName, d.CompanyEmail, d.CompanyWebsite = "Example Services GmbH", "accounts@example.invalid", "https://example.invalid"
	d.CustName, d.CustContact, d.CustEmail = "Example Customer GmbH", "Erika Musterfrau", "erika@example.invalid"
	d.CustLines = []string{"Example Street 42", "80331 Munich", "Germany"}
	d.Rows = nil
	for i := 1; i <= 36; i++ {
		rate, unit := "19", "h"
		if i%2 == 0 {
			rate, unit = "7", "piece"
		}
		d.Rows = append(d.Rows, TplRow{Desc: fmt.Sprintf("Row%02d Consulting and delivery review", i), Det: "Detailed documentation, implementation and acceptance review.", Qty: "2", Unit: unit, Price: "125.00", Amt: "225.00", Discount: "10", TaxRate: rate})
	}
	d.TaxGroups = []TplTaxGroup{{Rate: "7", Net: "4,050.00", Tax: "283.50"}, {Rate: "19", Net: "4,050.00", Tax: "769.50"}}
	d.Subtotal, d.GrossTotal = "8,100.00", "9,153.00"
	d.Notes = "This correction replaces invoice INV-2026-0031. Please retain both documents."
	d.PayTerms = "Payable within 14 days. Please use INV-2026-0042 as the payment reference."
	return d
}

func visualBrowser(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	opts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	opts = append(opts, chromedp.ExecPath(FindChrome()), chromedp.DisableGPU, chromedp.CombinedOutput(os.Stderr))
	alloc, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, cancelBrowser := chromedp.NewContext(alloc)
	ctx, cancelTimeout := context.WithTimeout(ctx, time.Minute)
	return ctx, func() { cancelTimeout(); cancelBrowser(); cancelAlloc() }
}

const visualBoundsJS = `(() => {
 const errors=[]; const rect=e=>e.getBoundingClientRect();
 if(document.documentElement.scrollWidth>794) errors.push('horizontal page overflow');
 const sections=[...document.querySelector('.page').children];
 for(let i=1;i<sections.length;i++) if(rect(sections[i]).top<rect(sections[i-1]).bottom-1) errors.push('section overlap '+i);
 for(const e of document.querySelectorAll('td,th,.d-title,.d-sub,.m-row span,.sr span,.grand span')) {
   const b=rect(e);
   if(e.scrollWidth>e.clientWidth+1 || e.scrollHeight>e.clientHeight+1) errors.push('text overflow '+e.className);
   if(b.left<0 || b.right>794) errors.push('outside viewport');
 }
 for(const row of document.querySelectorAll('tr,.m-row,.sr,.grand')) {
   const cells=[...row.children];
   for(let i=1;i<cells.length;i++) if(rect(cells[i]).left<rect(cells[i-1]).right-1) errors.push('cell overlap');
 }
 return errors;
})()`

type visualPDFWord struct {
	Text string  `xml:",chardata"`
	XMin float64 `xml:"xMin,attr"`
	XMax float64 `xml:"xMax,attr"`
	YMin float64 `xml:"yMin,attr"`
	YMax float64 `xml:"yMax,attr"`
}

func checkVisualPDF(t *testing.T, pdfPath, dir string) {
	t.Helper()
	path := filepath.Join(dir, "bounds.html")
	if exec.Command("pdftotext", "-bbox", pdfPath, path).Run() != nil {
		t.Fatal("extract PDF geometry failed")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("read PDF geometry failed")
	}
	var doc struct {
		Pages []struct {
			Width  float64         `xml:"width,attr"`
			Height float64         `xml:"height,attr"`
			Words  []visualPDFWord `xml:"word"`
		} `xml:"body>doc>page"`
	}
	if xml.Unmarshal(b, &doc) != nil || len(doc.Pages) < 2 {
		t.Fatal("expected multipage PDF geometry")
	}
	rows := map[string]int{}
	for p, page := range doc.Pages {
		if math.Abs(page.Width-595.28) > 1 || math.Abs(page.Height-841.89) > 1 {
			t.Fatal("PDF is not A4")
		}
		footer := false
		for _, w := range page.Words {
			if strings.HasPrefix(w.Text, "Row") {
				rows[w.Text]++
			}
			if w.XMin < 20 || w.XMax > page.Width-20 || w.YMin < 35 || w.YMax > page.Height-8 {
				t.Errorf("PDF page %d: text crosses safe page margins", p+1)
			}
			if w.YMax > 765 && w.YMin < 775 {
				t.Errorf("PDF page %d: text enters footer separation", p+1)
			}
			if w.Text == "accounts@example.invalid" && w.YMin > 775 {
				footer = true
			}
		}
		if !footer {
			t.Errorf("PDF page %d: repeating footer missing", p+1)
		}
	}
	for i := 1; i <= 36; i++ {
		if rows[fmt.Sprintf("Row%02d", i)] != 1 {
			t.Errorf("PDF row %02d clipped, missing or duplicated", i)
		}
	}
}

func readVisualPNG(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal("missing visual image: " + filepath.Base(path))
	}
	defer f.Close()
	im, err := png.Decode(f)
	if err != nil {
		t.Fatal("invalid visual image: " + filepath.Base(path))
	}
	return im
}

func visualDifference(want, got image.Image) error {
	if want.Bounds() != got.Bounds() {
		return fmt.Errorf("image dimensions changed")
	}
	bounds := want.Bounds()
	total := 0
	for top := bounds.Min.Y; top < bounds.Max.Y; top += 32 {
		for left := bounds.Min.X; left < bounds.Max.X; left += 32 {
			changed, pixels := 0, 0
			for y := top; y < min(top+32, bounds.Max.Y); y++ {
				for x := left; x < min(left+32, bounds.Max.X); x++ {
					pixels++
					wr, wg, wb, wa := want.At(x, y).RGBA()
					gr, gg, gb, ga := got.At(x, y).RGBA()
					if max(absChannel(wr, gr), absChannel(wg, gg), absChannel(wb, gb), absChannel(wa, ga)) > 12*257 {
						changed++
					}
				}
			}
			if float64(changed)/float64(pixels) > 0.01 {
				return fmt.Errorf("layout changed near pixel (%d,%d): %.2f%% tile difference exceeds 1%%", left, top, 100*float64(changed)/float64(pixels))
			}
			total += changed
		}
	}
	if float64(total)/float64(bounds.Dx()*bounds.Dy()) > 0.001 {
		return fmt.Errorf("page difference exceeds 0.1%%")
	}
	return nil
}

func absChannel(a, b uint32) uint32 {
	if a > b {
		return a - b
	}
	return b - a
}

func TestVisualComparisonRejectsClippedAmount(t *testing.T) {
	want, got := image.NewRGBA(image.Rect(0, 0, 800, 1100)), image.NewRGBA(image.Rect(0, 0, 800, 1100))
	for y := 0; y < 1100; y++ {
		for x := 0; x < 800; x++ {
			want.Set(x, y, color.White)
			got.Set(x, y, color.White)
		}
	}
	for y := 300; y < 308; y++ {
		for x := 700; x < 710; x++ {
			want.Set(x, y, color.Black)
		}
	}
	if visualDifference(want, got) == nil {
		t.Fatal("comparison accepted a clipped amount (only 80 pixels on a whole page)")
	}
}

func TestVisualComparisonTolerance(t *testing.T) {
	want := image.NewRGBA(image.Rect(0, 0, 64, 64))
	got := image.NewRGBA(want.Bounds())
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			want.Set(x, y, color.RGBA{R: 240, G: 240, B: 240, A: 255})
			got.Set(x, y, color.RGBA{R: 250, G: 250, B: 250, A: 255})
		}
	}
	if err := visualDifference(want, got); err != nil {
		t.Fatal("small antialiasing difference was rejected")
	}
	if visualDifference(want, image.NewRGBA(image.Rect(0, 0, 65, 64))) == nil {
		t.Fatal("changed page dimensions were accepted")
	}
	// Six dispersed pixels pass each tile's allowance but exceed 0.1% globally.
	for _, p := range []image.Point{{0, 0}, {1, 1}, {33, 0}, {34, 1}, {0, 33}, {33, 33}} {
		got.Set(p.X, p.Y, color.Black)
	}
	if visualDifference(want, got) == nil {
		t.Fatal("distributed page drift was accepted")
	}
}
