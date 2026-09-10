package pdfattach

import (
	"os"
	"path/filepath"
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
	if err := EmbedFacturX(pdfPath, xml); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(pdfPath) // #nosec G304 -- Fixture file in a test-owned temporary directory; no HTTP or external input selects this path.
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	attachments, err := api.Attachments(f, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(attachments) != 1 || attachments[0].FileName != "factur-x.xml" {
		t.Fatalf("unexpected attachments: %+v", attachments)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	conf := model.NewDefaultConfiguration()
	ctx, err := api.ReadAndValidate(f, conf)
	if err != nil {
		t.Fatal(err)
	}
	_, obj, err := ctx.SearchEmbeddedFilesNameTreeNodeByContent("factur-x.xml")
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
	if err := api.ExtractAttachmentsFile(pdfPath, extractDir, nil, nil); err != nil {
		t.Fatal(err)
	}
	gotXML, err := os.ReadFile(filepath.Join(extractDir, "factur-x.xml")) // #nosec G304 -- Fixture file in a test-owned temporary directory; no HTTP or external input selects this path.
	if err != nil {
		t.Fatal(err)
	}
	if string(gotXML) != string(xml) {
		t.Fatalf("attachment content = %q, want %q", gotXML, xml)
	}
	data, err := os.ReadFile(pdfPath) // #nosec G304 -- Fixture file in a test-owned temporary directory; no HTTP or external input selects this path.
	if err != nil {
		t.Fatal(err)
	}
	if string(data[:5]) != "%PDF-" {
		t.Fatal("embedding corrupted the PDF")
	}
}

func TestEmbedFacturXRejectsEmptyXML(t *testing.T) {
	if err := EmbedFacturX(filepath.Join(t.TempDir(), "missing.pdf"), nil); err == nil {
		t.Fatal("expected empty XML to be rejected")
	}
}
