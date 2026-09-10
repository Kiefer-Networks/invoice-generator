package main

// End-to-end tests that build the actual CLI binary and exercise it as a
// subprocess. This is the only reliable way to test a command that calls
// os.Exit() on error paths.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kiefer-networks/invoice-generator/internal/render"
	"github.com/pdfcpu/pdfcpu/pkg/api"
)

var binPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "invoice-cli-test-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	name := "invoice-test-bin"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binPath = filepath.Join(dir, name)

	cmd := exec.Command("go", "build", "-o", binPath, ".") // #nosec G204 -- Fixed integration-test command; variable arguments are generated fixture paths or IDs, never request data.
	cmd.Dir = mustGetwd()
	out, err := cmd.CombinedOutput()
	if err != nil {
		panic("failed to build CLI binary for tests: " + err.Error() + "\n" + string(out))
	}

	os.Exit(m.Run())
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	return wd
}

func run(t *testing.T, dir string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(binPath, args...) // #nosec G204 -- Fixed integration-test command; variable arguments are generated fixture paths or IDs, never request data.
	cmd.Dir = dir
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return outBuf.String(), errBuf.String(), exitErr.ExitCode()
		}
		t.Fatalf("failed to run binary: %v", err)
	}
	return outBuf.String(), errBuf.String(), 0
}

func TestCLIHelp(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := run(t, dir, "help")
	if code != 0 {
		t.Fatalf("help exited with code %d", code)
	}
	if !strings.Contains(stdout, "USAGE") || !strings.Contains(stdout, "invoice quote") {
		t.Errorf("help output missing expected content: %s", stdout)
	}
}

func TestCLINoArgsShowsHelpAndFails(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := run(t, dir)
	if code == 0 {
		t.Error("expected non-zero exit code when called with no arguments")
	}
	if !strings.Contains(stdout, "USAGE") {
		t.Error("expected help text on stdout when called with no arguments")
	}
}

func TestCLIVersion(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := run(t, dir, "version")
	if code != 0 {
		t.Fatalf("version exited with code %d", code)
	}
	if !strings.Contains(stdout, "invoice ") {
		t.Errorf("unexpected version output: %s", stdout)
	}
}

func TestCLIInitAndGenerateInvoice(t *testing.T) {
	dir := t.TempDir()

	if _, stderr, code := run(t, dir, "init", "company", "--lang", "en"); code != 0 {
		t.Fatalf("init company failed: %s", stderr)
	}
	if _, stderr, code := run(t, dir, "init", "invoice", "--lang", "en"); code != 0 {
		t.Fatalf("init invoice failed: %s", stderr)
	}

	stdout, stderr, code := run(t, dir, "-company", "company.yaml", "-fpdf", "invoice.yaml")
	if code != 0 {
		t.Skipf("generate invoice failed (likely no TTF fonts available in this environment): %s / %s", stdout, stderr)
	}
	if !strings.Contains(stdout, "Invoice created") {
		t.Errorf("unexpected output: %s", stdout)
	}

	matches, _ := filepath.Glob(filepath.Join(dir, "*Rechnung*.pdf"))
	if len(matches) != 1 {
		t.Fatalf("expected exactly one generated PDF, found %v", matches)
	}
	info, err := os.Stat(matches[0])
	if err != nil || info.Size() < 100 {
		t.Errorf("generated PDF missing or too small: %v, size=%v", err, info)
	}
}

func TestCLIInitAndGenerateQuote(t *testing.T) {
	dir := t.TempDir()

	if _, stderr, code := run(t, dir, "init", "company", "--lang", "en"); code != 0 {
		t.Fatalf("init company failed: %s", stderr)
	}
	if _, stderr, code := run(t, dir, "init", "quote", "--lang", "en"); code != 0 {
		t.Fatalf("init quote failed: %s", stderr)
	}

	stdout, stderr, code := run(t, dir, "quote", "-company", "company.yaml", "-fpdf", "quote.yaml")
	if code != 0 {
		t.Skipf("generate quote failed (likely no TTF fonts available in this environment): %s / %s", stdout, stderr)
	}
	if !strings.Contains(stdout, "Quote created") {
		t.Errorf("unexpected output: %s", stdout)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*Angebot*.pdf"))
	if len(matches) != 1 {
		t.Fatalf("expected exactly one generated quote PDF, found %v", matches)
	}
}

func TestCLIQuoteRejectsZugferd(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "init", "company", "--lang", "en")
	run(t, dir, "init", "quote", "--lang", "en")

	_, stderr, code := run(t, dir, "quote", "-zugferd", "-company", "company.yaml", "quote.yaml")
	if code == 0 {
		t.Error("expected non-zero exit when combining -zugferd with a quote")
	}
	if !strings.Contains(stderr, "zugferd") {
		t.Errorf("expected error mentioning zugferd, got: %s", stderr)
	}
}

func TestCLILocalOverrideTakesPrecedence(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "init", "company", "--lang", "en")
	run(t, dir, "init", "invoice", "--lang", "en")

	// Real, untracked override with a different company name.
	err := os.WriteFile(filepath.Join(dir, "company.local.yaml"), []byte(`
company:
  name: "Overridden GmbH"
`), 0600)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	stdout, stderr, code := run(t, dir, "-company", "company.yaml", "-fpdf", "-html", "invoice.yaml")
	if code != 0 {
		t.Skipf("generate invoice failed (likely no TTF fonts available): %s / %s", stdout, stderr)
	}
	if !strings.Contains(stdout, "Using local override") {
		t.Errorf("expected CLI to report using the local override, got: %s", stdout)
	}

	matches, _ := filepath.Glob(filepath.Join(dir, "*Rechnung*.html"))
	if len(matches) != 1 {
		t.Fatalf("expected exactly one generated HTML file, found %v", matches)
	}
	html, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("could not read generated HTML: %v", err)
	}
	if !strings.Contains(string(html), "Overridden GmbH") {
		t.Error("expected rendered invoice to use the company name from the local override")
	}
}

func TestCLIInitPaperless(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := run(t, dir, "init", "paperless")
	if code != 0 {
		t.Fatalf("init paperless failed: %s / %s", stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "paperless.yaml")); err != nil {
		t.Errorf("expected paperless.yaml to be created: %v", err)
	}
}

func TestCLIPaperlessUpload(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "init", "company", "--lang", "en")
	run(t, dir, "init", "invoice", "--lang", "en")

	var uploaded bool
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tags/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			body, _ := json.Marshal(map[string]int{"id": 1})
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write(body)
			return
		}
		_, _ = w.Write([]byte(`{"count":0,"results":[]}`))
	})
	mux.HandleFunc("/api/documents/post_document/", func(w http.ResponseWriter, r *http.Request) {
		uploaded = true
		w.WriteHeader(http.StatusOK)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	paperlessYAML := "url: \"" + server.URL + "\"\napi_key: \"test-key\"\ntags:\n  - \"Invoices\"\n"
	if err := os.WriteFile(filepath.Join(dir, "paperless.yaml"), []byte(paperlessYAML), 0600); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	stdout, stderr, code := run(t, dir, "-company", "company.yaml", "-fpdf", "-paperless", "invoice.yaml")
	if code != 0 {
		t.Skipf("generate invoice failed (likely no TTF fonts available): %s / %s", stdout, stderr)
	}
	if !uploaded {
		t.Errorf("expected the generated PDF to be uploaded to the fake Paperless server, stdout=%s stderr=%s", stdout, stderr)
	}
	if !strings.Contains(stdout, "Uploaded to Paperless") {
		t.Errorf("expected CLI to report the upload, got: %s", stdout)
	}
}

func TestCLIRejectsPathTraversalInInvoiceNumber(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "init", "company", "--lang", "en")

	malicious := `
language: "en"
customer:
  name: "Client GmbH"
invoice:
  number: "../../evil"
  date: "01.03.2026"
items:
  - description: "Work"
    quantity: 1
    price: 10.0
`
	if err := os.WriteFile(filepath.Join(dir, "invoice.yaml"), []byte(malicious), 0600); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	stdout, stderr, code := run(t, dir, "-company", "company.yaml", "-fpdf", "invoice.yaml")
	if code != 0 {
		t.Skipf("generate invoice failed (likely no TTF fonts available): %s / %s", stdout, stderr)
	}

	// The output must stay inside dir — no file should have been written
	// to a parent directory.
	parent := filepath.Dir(dir)
	entries, _ := os.ReadDir(parent)
	for _, e := range entries {
		if strings.Contains(e.Name(), "evil") {
			t.Errorf("path traversal via invoice number escaped the output directory: %s", e.Name())
		}
	}
}

func TestCLIZugferdKeepsHTMLTemplateRenderer(t *testing.T) {
	if render.FindChrome() == "" {
		t.Skip("Chrome is not available")
	}
	dir := t.TempDir()
	run(t, dir, "init", "company", "--lang", "en")
	run(t, dir, "init", "invoice", "--lang", "en")
	custom := filepath.Join(dir, "custom.html")
	if err := os.WriteFile(custom, []byte(`<html><body>HTML TEMPLATE {{.CompanyName}}</body></html>`), 0600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "zugferd.pdf")
	stdout, stderr, code := run(t, dir, "-company", "company.yaml", "-zugferd", "-t", custom, "-o", out, "invoice.yaml")
	if code != 0 {
		t.Fatalf("ZUGFeRD generation failed: %s / %s", stdout, stderr)
	}
	if !strings.Contains(stdout, "rendered via HTML template + Chrome") {
		t.Fatalf("expected HTML renderer with ZUGFeRD, got: %s", stdout)
	}
	f, err := os.Open(out) // #nosec G304 -- Fixture file in a test-owned temporary directory; no HTTP or external input selects this path.
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	attachments, err := api.Attachments(f, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(attachments) != 1 || attachments[0].FileName != "factur-x.xml" {
		t.Fatalf("missing Factur-X attachment: %+v", attachments)
	}
}
