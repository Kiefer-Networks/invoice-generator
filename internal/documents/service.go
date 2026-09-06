package documents

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/kiefer-networks/invoice-generator/internal/invoicing"
	"github.com/kiefer-networks/invoice-generator/internal/pdfattach"
	"github.com/kiefer-networks/invoice-generator/internal/render"
	"github.com/kiefer-networks/invoice-generator/internal/store"
	"github.com/kiefer-networks/invoice-generator/internal/zugferd"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"io"
	"os"
	"path/filepath"
	"time"
)

const GeneratorVersion = "chrome-snapshot-cii-d16b-1"

type Service struct {
	db      *store.Store
	storage *Storage
	render  func(context.Context, *render.TplData, string) error
	slots   chan struct{}
}

func New(db *store.Store, storage *Storage) *Service {
	return &Service{db: db, storage: storage, render: render.FromSnapshot, slots: make(chan struct{}, 2)}
}
func (s *Service) acquire(ctx context.Context) error {
	select {
	case s.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Service) Preview(ctx context.Context, id string) ([]byte, error) {
	d, e := invoicing.NewDraftService(s.db).Get(ctx, id)
	if e != nil {
		return nil, e
	}
	var snap invoicing.Snapshot
	if d.Number != "" {
		f, e := invoicing.NewFinalizationService(s.db).Get(ctx, id)
		if e != nil {
			return nil, e
		}
		snap = f.Snapshot
	} else {
		company, e := s.db.CompanyRepository().Get(ctx)
		if e != nil {
			return nil, e
		}
		snap = invoicing.Snapshot{Draft: d, Company: company.CompanyInput, Language: d.Customer.PreferredLanguage, Notes: company.StandardNotes, Kind: "invoice"}
	}
	p := snap.RenderData()
	if d.Number == "" {
		p.Title = "Invoice draft"
		p.Status = "DRAFT"
		p.StatusClass = "draft"
	}
	return s.renderPDF(ctx, p, nil)
}
func (s *Service) renderPDF(ctx context.Context, p *render.TplData, xml []byte) ([]byte, error) {
	if e := s.acquire(ctx); e != nil {
		return nil, e
	}
	defer func() { <-s.slots }()
	dir, e := os.MkdirTemp("", "invoice-document-*")
	if e != nil {
		return nil, e
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "invoice.pdf")
	if e = s.render(ctx, p, path); e != nil {
		return nil, e
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	info, e := os.Stat(path)
	if e != nil {
		return nil, e
	}
	if info.Size() <= 0 || info.Size() > s.storage.max {
		return nil, ErrIntegrity
	}
	if xml != nil {
		if e = pdfattach.EmbedFacturX(path, xml); e != nil {
			return nil, e
		}
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	data, e := io.ReadAll(io.LimitReader(f, s.storage.max+1))
	if e != nil {
		return nil, e
	}
	if int64(len(data)) > s.storage.max {
		return nil, ErrIntegrity
	}
	if e = validatePDF(data, xml); e != nil {
		return nil, e
	}
	return data, nil
}
func (s *Service) Generate(ctx context.Context, j store.DocumentJob) error {
	doc, e := s.db.DocumentRepository().Get(ctx, j.DocumentID)
	if e != nil {
		return e
	}
	if doc.InvoiceID != j.InvoiceID {
		return store.ErrConflict
	}
	if doc.Status == "ready" {
		return nil
	}
	f, e := invoicing.NewFinalizationService(s.db).Get(ctx, doc.InvoiceID)
	if e != nil {
		return e
	}
	xml, e := zugferd.GenerateSnapshot(f.Snapshot)
	if e != nil {
		return e
	}
	if e = zugferd.ValidateCII(ctx, xml); e != nil {
		return e
	}
	data, e := s.renderPDF(ctx, f.Snapshot.RenderData(), xml)
	if e != nil {
		return e
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	a, e := s.storage.Put(bytes.NewReader(data))
	if e != nil {
		return e
	}
	e = s.db.DocumentRepository().Complete(ctx, j, store.Document{StorageKey: a.Key, SHA256: a.SHA256, Size: a.Size, GeneratorVersion: GeneratorVersion})
	// A COMMIT error can have an uncertain outcome. Never delete a file here:
	// it may already be referenced by a committed ready document. Recover
	// reconciles old unreferenced artifacts after its grace period.
	return e
}
func (s *Service) OpenAuthorized(ctx context.Context, userID, id string) (*os.File, store.Document, error) {
	d, e := s.db.DocumentRepository().Authorized(ctx, userID, id)
	if e != nil {
		return nil, d, e
	}
	f, e := s.storage.Open(d.StorageKey, d.SHA256, d.Size)
	return f, d, e
}
func validatePDF(data, expectedXML []byte) error {
	conf := model.NewDefaultConfiguration()
	ctx, e := api.ReadAndValidate(bytes.NewReader(data), conf)
	if e != nil {
		return fmt.Errorf("invalid generated PDF: %w", e)
	}
	if ctx.PageCount < 1 || ctx.PageCount > 1000 {
		return errors.New("invalid document page count")
	}
	if expectedXML == nil {
		return nil
	}
	_, obj, e := ctx.SearchEmbeddedFilesNameTreeNodeByContent("factur-x.xml")
	if e != nil {
		return e
	}
	spec, e := ctx.DereferenceDict(obj)
	if e != nil {
		return e
	}
	if rel := spec.NameEntry("AFRelationship"); rel == nil || *rel != "Alternative" {
		return ErrIntegrity
	}
	ref, ok := spec.DictEntry("EF").Find("F")
	if !ok {
		return ErrIntegrity
	}
	stream, _, e := ctx.DereferenceStreamDict(ref)
	if e != nil {
		return e
	}
	if mime := stream.NameEntry("Subtype"); mime == nil || *mime != "text/xml" {
		return ErrIntegrity
	}
	if e = stream.Decode(); e != nil {
		return e
	}
	if !bytes.Equal(stream.Content, expectedXML) {
		return ErrIntegrity
	}
	cat, e := ctx.Catalog()
	if e != nil || len(cat.ArrayEntry("AF")) != 1 {
		return ErrIntegrity
	}
	return nil
}

// Recover is called before workers start. Committed files without a database
// record are never downloadable and become collectable after a 24-hour grace.
func (s *Service) Recover(ctx context.Context) error {
	rows, e := s.db.DB().QueryContext(ctx, `SELECT id FROM invoices WHERE state<>'draft' AND frozen_snapshot IS NOT NULL`)
	if e != nil {
		return e
	}
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, id := range ids {
		if _, e = s.db.DocumentRepository().Enqueue(ctx, id); e != nil {
			return e
		}
	}
	rows, e = s.db.DB().QueryContext(ctx, `SELECT storage_key FROM documents`)
	if e != nil {
		return e
	}
	refs := map[string]bool{}
	for rows.Next() {
		var key string
		if e = rows.Scan(&key); e != nil {
			rows.Close()
			return e
		}
		refs[key] = true
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	return s.storage.Sweep(time.Now().Add(-24*time.Hour), refs)
}
