package jobs

import (
	"context"
	"errors"
	"time"

	"github.com/kiefer-networks/invoice-generator/internal/documents"
	"github.com/kiefer-networks/invoice-generator/internal/paperless"
	"github.com/kiefer-networks/invoice-generator/internal/store"
)

// PaperlessWorker has one active upload at a time. SQLite leases fence concurrent
// processes; all remote work finishes within one minute of a five-minute lease.
type PaperlessWorker struct {
	db      *store.Store
	storage *documents.Storage
	client  func() (*paperless.Client, error)
	wake    chan struct{}
}

func NewPaperlessWorker(db *store.Store, storage *documents.Storage, client func() (*paperless.Client, error)) *PaperlessWorker {
	return &PaperlessWorker{db: db, storage: storage, client: client, wake: make(chan struct{}, 1)}
}
func (w *PaperlessWorker) Wake() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}
func (w *PaperlessWorker) Run(ctx context.Context) {
	timer := time.NewTicker(30 * time.Second)
	defer timer.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		j, e := w.db.PaperlessRepository().Claim(ctx, time.Now(), 5*time.Minute)
		if e == nil {
			e = w.Process(ctx, j)
			if e != nil {
				cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_ = w.db.PaperlessRepository().Fail(cleanup, j, time.Now(), e.Error(), 5)
				cancel()
			}
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-w.wake:
		}
	}
}
func (w *PaperlessWorker) Process(ctx context.Context, j store.PaperlessJob) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	repo := w.db.PaperlessRepository()
	// Reload and fence even callers holding an older in-memory job.
	current, e := repo.ForDocument(ctx, j.DocumentID)
	if e != nil {
		return e
	}
	if current.ID != j.ID || current.Token != j.Token || current.State != "leased" {
		return store.ErrConflict
	}
	j = current
	d, e := w.db.DocumentRepository().Get(ctx, j.DocumentID)
	if e != nil || d.Status != "ready" {
		return errors.New("document_invalid")
	}
	client, e := w.client()
	if e != nil {
		return e
	}
	title := paperless.Title(j.InvoiceNumber, j.DocumentID)
	if j.RemoteTaskID != "" {
		id, e := client.Poll(ctx, j.RemoteTaskID)
		if e != nil {
			return e
		}
		return w.verifyDelivery(ctx, client, j, d, title, id)
	}
	id, e := client.FindDocument(ctx, title)
	if e != nil {
		return deliveryError(e, "request_failed")
	}
	if id > 0 {
		if e = client.VerifyDocument(ctx, id, title, d.SHA256, d.Size); e != nil {
			return deliveryError(e, "delivery_uncertain")
		}
		return repo.Complete(ctx, j, id)
	}
	if j.UploadStarted {
		return deliveryError(e, "delivery_uncertain")
	}
	f, e := w.storage.Open(d.StorageKey, d.SHA256, d.Size)
	if e != nil {
		return errors.New("document_invalid")
	}
	defer func() { _ = f.Close() }() // Read-only input; reads and validation report their own errors.
	tags, e := client.ResolveTags(ctx)
	if e != nil {
		return deliveryError(e, "request_failed")
	}
	if e = repo.BeginUpload(ctx, j); e != nil {
		return e
	}
	task, e := client.Submit(ctx, f, d.Size, title, tags)
	var failure *paperless.SubmissionError
	if errors.As(e, &failure) && (failure.Certainty == paperless.Rejected || failure.Certainty == paperless.NotSent) {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if e = repo.RejectUpload(cleanup, j); e != nil {
			return e
		}
		return deliveryError(failure, "request_failed")
	}
	if e != nil {
		return deliveryError(e, "delivery_uncertain")
	}
	if e = repo.SaveTask(ctx, j, task); e != nil {
		return e
	}
	id, e = client.Poll(ctx, task)
	if e != nil {
		return e
	}
	return w.verifyDelivery(ctx, client, j, d, title, id)
}

func (w *PaperlessWorker) verifyDelivery(ctx context.Context, client *paperless.Client, j store.PaperlessJob, d store.Document, title string, id int64) error {
	candidate, e := client.FindDocument(ctx, title)
	if e != nil || (candidate != 0 && candidate != id) {
		return deliveryError(e, "delivery_uncertain")
	}
	if e = client.VerifyDocument(ctx, id, title, d.SHA256, d.Size); e != nil {
		return deliveryError(e, "delivery_uncertain")
	}
	return w.db.PaperlessRepository().Complete(ctx, j, id)
}

func deliveryError(err error, fallback string) error {
	if errors.Is(err, paperless.ErrAPIIncompatible) {
		return paperless.ErrAPIIncompatible
	}
	return errors.New(fallback)
}
