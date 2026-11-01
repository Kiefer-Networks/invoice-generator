// Package pdfattach embeds electronic invoice data into an existing PDF.
package pdfattach

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// EmbedFacturX adds xml to pdfPath as the conventional factur-x.xml
// attachment while preserving the already-rendered PDF pages and layout.
func EmbedFacturX(ctx context.Context, pdfPath string, xml []byte) error {
	if len(xml) == 0 {
		return fmt.Errorf("cannot embed empty Factur-X XML")
	}
	in, err := os.Open(pdfPath)
	if err != nil {
		return fmt.Errorf("could not open rendered PDF: %w", err)
	}
	// Stateless: the user's global pdfcpu config.yml must neither change the
	// output nor crash the run (pdfcpu panics on config load errors).
	conf := model.NewStatelessConfiguration()
	conf.Cmd = model.ADDATTACHMENTS
	pdfCtx, err := api.ReadValidateAndOptimize(ctx, in, conf, nil)
	closeErr := in.Close()
	if err != nil {
		return fmt.Errorf("could not read rendered PDF: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("could not close rendered PDF: %w", closeErr)
	}
	if err := pdfCtx.LocateNameTree("EmbeddedFiles", true); err != nil {
		return fmt.Errorf("could not prepare PDF attachments: %w", err)
	}

	attachment := model.Attachment{
		Reader: bytes.NewReader(xml),
		ID:     "factur-x.xml",
		Desc:   "Factur-X / ZUGFeRD e-invoice (CII XML, BASIC profile)",
	}
	fileSpec, err := pdfCtx.NewFileSpecDictForAttachment(ctx, attachment)
	if err != nil {
		return fmt.Errorf("could not create Factur-X attachment: %w", err)
	}
	embeddedRef, found := fileSpec.DictEntry("EF").Find("F")
	if !found {
		return fmt.Errorf("could not locate Factur-X embedded file stream")
	}
	embedded, _, err := pdfCtx.DereferenceStreamDict(embeddedRef)
	if err != nil {
		return fmt.Errorf("could not update Factur-X embedded file stream: %w", err)
	}
	embedded.InsertName("Subtype", "text/xml")
	// Factur-X requires the invoice XML to be an associated alternative
	// representation of the visible PDF, not merely a generic attachment.
	fileSpec.InsertName("AFRelationship", "Alternative")
	ref, err := pdfCtx.IndRefForNewObject(fileSpec)
	if err != nil {
		return fmt.Errorf("could not register Factur-X attachment: %w", err)
	}
	nameMap := model.NameMap{attachment.ID: []types.Dict{fileSpec}}
	if err := pdfCtx.Names["EmbeddedFiles"].Add(ctx, pdfCtx.XRefTable, attachment.ID, *ref, nameMap, []string{"F", "UF"}); err != nil {
		return fmt.Errorf("could not index Factur-X attachment: %w", err)
	}
	catalog, err := pdfCtx.Catalog()
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
	if err := api.Write(ctx, pdfCtx, out, conf); err != nil {
		return fmt.Errorf("could not write ZUGFeRD PDF: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("could not close ZUGFeRD PDF: %w", err)
	}
	// os.CreateTemp creates the file with mode 0600, so the replaced PDF
	// keeps its PII-safe permissions.
	if err := os.Rename(outPath, pdfPath); err != nil {
		return fmt.Errorf("could not replace rendered PDF with ZUGFeRD PDF: %w", err)
	}
	keep = true
	return nil
}
