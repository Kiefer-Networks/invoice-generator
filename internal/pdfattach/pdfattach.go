// Package pdfattach embeds electronic invoice data into an existing PDF.
package pdfattach

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// EmbedFacturX adds xml to pdfPath as the conventional factur-x.xml
// attachment while preserving the already-rendered PDF pages and layout.
func EmbedFacturX(pdfPath string, xml []byte) error {
	if len(xml) == 0 {
		return fmt.Errorf("cannot embed empty Factur-X XML")
	}
	tmpDir, err := os.MkdirTemp("", "invoice-factur-x-*")
	if err != nil {
		return fmt.Errorf("could not create Factur-X temp directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	xmlPath := filepath.Join(tmpDir, "factur-x.xml")
	if err := os.WriteFile(xmlPath, xml, 0600); err != nil {
		return fmt.Errorf("could not stage Factur-X XML: %w", err)
	}
	in, err := os.Open(pdfPath)
	if err != nil {
		return fmt.Errorf("could not open rendered PDF: %w", err)
	}
	conf := model.NewDefaultConfiguration()
	conf.Cmd = model.ADDATTACHMENTS
	ctx, err := api.ReadValidateAndOptimize(in, conf)
	closeErr := in.Close()
	if err != nil {
		return fmt.Errorf("could not read rendered PDF: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("could not close rendered PDF: %w", closeErr)
	}
	if err := ctx.LocateNameTree("EmbeddedFiles", true); err != nil {
		return fmt.Errorf("could not prepare PDF attachments: %w", err)
	}

	attachment := model.Attachment{
		Reader: bytes.NewReader(xml),
		ID:     filepath.Base(xmlPath),
		Desc:   "Factur-X / ZUGFeRD e-invoice (CII XML, BASIC profile)",
	}
	fileSpec, err := ctx.NewFileSpecDictForAttachment(attachment)
	if err != nil {
		return fmt.Errorf("could not create Factur-X attachment: %w", err)
	}
	embeddedRef, found := fileSpec.DictEntry("EF").Find("F")
	if !found {
		return fmt.Errorf("could not locate Factur-X embedded file stream")
	}
	embedded, _, err := ctx.DereferenceStreamDict(embeddedRef)
	if err != nil {
		return fmt.Errorf("could not update Factur-X embedded file stream: %w", err)
	}
	embedded.InsertName("Subtype", "text/xml")
	// Factur-X requires the invoice XML to be an associated alternative
	// representation of the visible PDF, not merely a generic attachment.
	fileSpec.InsertName("AFRelationship", "Alternative")
	ref, err := ctx.IndRefForNewObject(fileSpec)
	if err != nil {
		return fmt.Errorf("could not register Factur-X attachment: %w", err)
	}
	nameMap := model.NameMap{attachment.ID: []types.Dict{fileSpec}}
	if err := ctx.Names["EmbeddedFiles"].Add(ctx.XRefTable, attachment.ID, *ref, nameMap, []string{"F", "UF"}); err != nil {
		return fmt.Errorf("could not index Factur-X attachment: %w", err)
	}
	catalog, err := ctx.Catalog()
	if err != nil {
		return fmt.Errorf("could not read PDF catalog: %w", err)
	}
	catalog.Update("AF", types.Array{*ref})

	out, err := os.CreateTemp(filepath.Dir(pdfPath), ".invoice-zugferd-*.pdf")
	if err != nil {
		return fmt.Errorf("could not stage ZUGFeRD PDF: %w", err)
	}
	outPath := out.Name()
	keep := false
	defer func() {
		_ = out.Close()
		if !keep {
			_ = os.Remove(outPath)
		}
	}()
	if err := api.Write(ctx, out, conf); err != nil {
		return fmt.Errorf("could not write ZUGFeRD PDF: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("could not close ZUGFeRD PDF: %w", err)
	}
	if err := os.Rename(outPath, pdfPath); err != nil {
		return fmt.Errorf("could not replace rendered PDF with ZUGFeRD PDF: %w", err)
	}
	keep = true
	if err := os.Chmod(pdfPath, 0600); err != nil {
		return fmt.Errorf("could not restrict PDF permissions: %w", err)
	}
	return nil
}
