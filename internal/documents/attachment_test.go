package documents

import (
	"bytes"
	"github.com/go-pdf/fpdf"
	"github.com/kiefer-networks/invoice-generator/internal/pdfattach"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateRejectsMalformedAttachmentMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.pdf")
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddPage()
	pdf.SetFont("Arial", "", 12)
	pdf.Cell(20, 10, "Invoice")
	if e := pdf.OutputFileAndClose(path); e != nil {
		t.Fatal(e)
	}
	xml := []byte("<invoice>exact</invoice>")
	if e := pdfattach.EmbedFacturX(path, xml); e != nil {
		t.Fatal(e)
	}
	original, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if e = validatePDF(original, xml); e != nil {
		t.Fatal(e)
	}
	for _, field := range []string{"relationship", "mime", "xml"} {
		t.Run(field, func(t *testing.T) {
			conf := model.NewDefaultConfiguration()
			ctx, e := api.ReadAndValidate(bytes.NewReader(original), conf)
			if e != nil {
				t.Fatal(e)
			}
			_, obj, e := ctx.SearchEmbeddedFilesNameTreeNodeByContent("factur-x.xml")
			if e != nil {
				t.Fatal(e)
			}
			spec, e := ctx.DereferenceDict(obj)
			if e != nil {
				t.Fatal(e)
			}
			expected := xml
			if field == "relationship" {
				spec.Update("AFRelationship", types.Name("Data"))
			} else if field == "mime" {
				ref, _ := spec.DictEntry("EF").Find("F")
				stream, _, e := ctx.DereferenceStreamDict(ref)
				if e != nil {
					t.Fatal(e)
				}
				stream.Update("Subtype", types.Name("application/octet-stream"))
			} else {
				expected = []byte("<invoice>changed</invoice>")
			}
			var altered bytes.Buffer
			if e = api.Write(ctx, &altered, conf); e != nil {
				t.Fatal(e)
			}
			if e = validatePDF(altered.Bytes(), expected); e == nil {
				t.Fatal("malformed attachment accepted")
			}
		})
	}
}
