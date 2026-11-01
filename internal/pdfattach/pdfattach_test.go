package pdfattach

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-pdf/fpdf"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

func TestEmbedFacturXPreservesPDFAndAddsNamedAttachment(t *testing.T) {
	dir := t.TempDir()
	pdfPath := filepath.Join(dir, "invoice.pdf")
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddPage()
	pdf.SetFont("Arial", "", 12)
	pdf.Cell(40, 10, "Invoice layout")
	if err := pdf.OutputFileAndClose(pdfPath); err != nil {
		t.Fatal(err)
	}

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
	ctx, err := api.ReadAndValidate(t.Context(), f, conf)
	if err != nil {
		t.Fatal(err)
	}
	attachments, err := ctx.ListAttachments(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(attachments) != 1 || attachments[0].FileName != "factur-x.xml" {
		t.Fatalf("unexpected attachments: %+v", attachments)
	}
	_, obj, err := ctx.SearchEmbeddedFilesNameTreeNodeByContent(t.Context(), "factur-x.xml")
	if err != nil {
		t.Fatal(err)
	}
	fileSpec, err := ctx.DereferenceDict(obj)
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
	embedded, _, err := ctx.DereferenceStreamDict(embeddedRef)
	if err != nil {
		t.Fatal(err)
	}
	if subtype := embedded.NameEntry("Subtype"); subtype == nil || *subtype != "text/xml" {
		t.Fatalf("embedded XML subtype = %v, want text/xml", subtype)
	}
	catalog, err := ctx.Catalog()
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
}

func writeTestPDF(t *testing.T, path string) {
	t.Helper()
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddPage()
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

func TestEmbedFacturXIgnoresUserPdfcpuConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AppData", filepath.Join(home, "AppData"))
	configDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	pdfcpuDir := filepath.Join(configDir, "pdfcpu")
	if err := os.MkdirAll(pdfcpuDir, 0700); err != nil {
		t.Fatal(err)
	}
	// pdfcpu panics when it loads an invalid config.yml.
	if err := os.WriteFile(filepath.Join(pdfcpuDir, "config.yml"), []byte("validationMode: [broken"), 0600); err != nil {
		t.Fatal(err)
	}
	pdfPath := filepath.Join(t.TempDir(), "invoice.pdf")
	writeTestPDF(t, pdfPath)
	if err := EmbedFacturX(t.Context(), pdfPath, []byte("<invoice/>")); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(pdfcpuDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("EmbedFacturX wrote into the user's pdfcpu config dir: %v", entries)
	}
}
