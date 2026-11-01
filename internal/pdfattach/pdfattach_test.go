package pdfattach

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/go-pdf/fpdf"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// TestMain points the user config dir at a broken pdfcpu config.yml for the
// whole test binary. pdfcpu caches its default configuration per process, so
// only a process-wide setup reliably catches a call that falls back to it:
// such a call panics on the broken file instead of passing on a cached copy.
func TestMain(m *testing.M) {
	os.Exit(runWithBrokenPdfcpuConfig(m))
}

func runWithBrokenPdfcpuConfig(m *testing.M) int {
	home, err := os.MkdirTemp("", "pdfattach-home-*")
	if err != nil {
		panic(err)
	}
	defer func() { _ = os.RemoveAll(home) }()
	for key, dir := range map[string]string{
		"HOME":            home,
		"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"AppData":         filepath.Join(home, "AppData"),
	} {
		if err := os.Setenv(key, dir); err != nil {
			panic(err)
		}
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		panic(err)
	}
	pdfcpuDir := filepath.Join(configDir, "pdfcpu")
	if err := os.MkdirAll(pdfcpuDir, 0700); err != nil {
		panic(err)
	}
	if err := os.WriteFile(filepath.Join(pdfcpuDir, "config.yml"), []byte("validationMode: [broken"), 0600); err != nil {
		panic(err)
	}
	code := m.Run()
	entries, err := os.ReadDir(pdfcpuDir)
	if err != nil {
		panic(err)
	}
	if len(entries) != 1 {
		fmt.Fprintf(os.Stderr, "tests wrote into the user's pdfcpu config dir: %v\n", entries)
		return 1
	}
	return code
}

func TestEmbedFacturXPreservesPDFAndAddsNamedAttachment(t *testing.T) {
	dir := t.TempDir()
	pdfPath := filepath.Join(dir, "invoice.pdf")
	writeTestPDF(t, pdfPath)

	xml := []byte(`<?xml version="1.0"?><invoice>2026-0007</invoice>`)
	if err := EmbedFacturX(t.Context(), pdfPath, xml); err != nil {
		t.Fatal(err)
	}

	// Stateless, so the test neither reads nor creates the developer's
	// pdfcpu config directory.
	conf := model.NewStatelessConfiguration()
	f, err := os.Open(pdfPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	pdfCtx, err := api.ReadAndValidate(t.Context(), f, conf)
	if err != nil {
		t.Fatal(err)
	}
	attachments, err := pdfCtx.ListAttachments(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(attachments) != 1 || attachments[0].FileName != "factur-x.xml" {
		t.Fatalf("unexpected attachments: %+v", attachments)
	}
	_, obj, err := pdfCtx.SearchEmbeddedFilesNameTreeNodeByContent(t.Context(), "factur-x.xml")
	if err != nil {
		t.Fatal(err)
	}
	fileSpec, err := pdfCtx.DereferenceDict(obj)
	if err != nil {
		t.Fatal(err)
	}
	if rel := fileSpec.NameEntry("AFRelationship"); rel == nil || *rel != "Alternative" {
		t.Fatalf("AFRelationship = %v, want Alternative", rel)
	}
	embeddedRef, found := fileSpec.DictEntry("EF").Find("F")
	if !found {
		t.Fatal("embedded file stream is missing")
	}
	embedded, _, err := pdfCtx.DereferenceStreamDict(embeddedRef)
	if err != nil {
		t.Fatal(err)
	}
	if subtype := embedded.NameEntry("Subtype"); subtype == nil || *subtype != "text/xml" {
		t.Fatalf("embedded XML subtype = %v, want text/xml", subtype)
	}
	catalog, err := pdfCtx.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	if af := catalog.ArrayEntry("AF"); len(af) != 1 {
		t.Fatalf("catalog AF array = %+v, want one attachment", af)
	}
	extractDir := filepath.Join(dir, "attachments")
	if err := os.Mkdir(extractDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := api.ExtractAttachmentsFile(t.Context(), pdfPath, extractDir, nil, conf); err != nil {
		t.Fatal(err)
	}
	gotXML, err := os.ReadFile(filepath.Join(extractDir, "factur-x.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotXML) != string(xml) {
		t.Fatalf("attachment content = %q, want %q", gotXML, xml)
	}
	data, err := os.ReadFile(pdfPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data[:5]) != "%PDF-" {
		t.Fatal("embedding corrupted the PDF")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(pdfPath)
		if err != nil {
			t.Fatal(err)
		}
		// The replaced PDF holds customer data; os.CreateTemp's 0600 must survive.
		if perm := info.Mode().Perm(); perm != 0600 {
			t.Fatalf("embedded PDF has mode %o, want 600", perm)
		}
	}
}

func writeTestPDF(t *testing.T, path string) {
	t.Helper()
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddPage()
	pdf.SetFont("Arial", "", 12)
	pdf.Cell(40, 10, "Invoice layout")
	if err := pdf.OutputFileAndClose(path); err != nil {
		t.Fatal(err)
	}
}

func TestEmbedFacturXRejectsEmptyXML(t *testing.T) {
	// A valid PDF, so only the empty-XML guard can make this fail.
	pdfPath := filepath.Join(t.TempDir(), "invoice.pdf")
	writeTestPDF(t, pdfPath)
	err := EmbedFacturX(t.Context(), pdfPath, nil)
	if err == nil || !strings.Contains(err.Error(), "empty Factur-X XML") {
		t.Fatalf("expected empty XML to be rejected, got %v", err)
	}
}

func TestEmbedFacturXHonoursCancelledContext(t *testing.T) {
	pdfPath := filepath.Join(t.TempDir(), "invoice.pdf")
	writeTestPDF(t, pdfPath)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := EmbedFacturX(ctx, pdfPath, []byte("<invoice/>")); err == nil {
		t.Fatal("expected a cancelled context to stop embedding")
	}
}
