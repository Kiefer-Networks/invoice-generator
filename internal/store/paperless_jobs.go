package store

import (
	"context"
	"database/sql"
	"errors"
	"math/rand/v2"
	"time"
)

type PaperlessJob struct {
	ID, DocumentID, InvoiceID, InvoiceNumber, Token, State, RemoteTaskID, ErrorCode, ErrorSummary string
	Attempts                                                                                      int
	RemoteDocumentID, NextAttemptAt                                                               int64
	UploadStarted                                                                                 bool
}
type PaperlessRepository struct{ store *Store }

func (s *Store) PaperlessRepository() *PaperlessRepository { return &PaperlessRepository{s} }

const paperlessColumns = `j.id,j.document_id,d.invoice_id,i.number,j.lease_token,j.state,j.remote_task_id,j.last_error_code,j.last_error_summary,j.attempts,j.remote_document_id,CAST(j.next_attempt_at AS INTEGER),j.upload_started`
const paperlessJoin = ` FROM paperless_jobs j JOIN documents d ON d.id=j.document_id JOIN invoices i ON i.id=d.invoice_id `

func scanPaperless(row interface{ Scan(...any) error }) (j PaperlessJob, e error) {
	e = row.Scan(&j.ID, &j.DocumentID, &j.InvoiceID, &j.InvoiceNumber, &j.Token, &j.State, &j.RemoteTaskID, &j.ErrorCode, &j.ErrorSummary, &j.Attempts, &j.RemoteDocumentID, &j.NextAttemptAt, &j.UploadStarted)
	if errors.Is(e, sql.ErrNoRows) {
		e = ErrNotFound
	}
	return
}
func (r *PaperlessRepository) ForDocument(ctx context.Context, id string) (PaperlessJob, error) {
	return scanPaperless(r.store.db.QueryRowContext(ctx, `SELECT `+paperlessColumns+paperlessJoin+`WHERE d.id=?`, id))
}
func (r *PaperlessRepository) Claim(ctx context.Context, now time.Time, lease time.Duration) (j PaperlessJob, err error) {
	err = r.store.InvoiceRepository().immediate(ctx, func(c *sql.Conn) error {
		_, e := c.ExecContext(ctx, `UPDATE paperless_jobs SET state='failed',lease_token='',lease_expires_at=0,last_error_code='lease_expired',last_error_summary='Delivery interrupted; retry to reconcile.' WHERE attempts>=5 AND (state='queued' OR (state='leased' AND CAST(lease_expires_at AS INTEGER)<=?))`, now.Unix())
		if e != nil {
			return e
		}
		j, e = scanPaperless(c.QueryRowContext(ctx, `SELECT `+paperlessColumns+paperlessJoin+`WHERE d.status='ready' AND d.kind='invoice_pdf' AND i.state<>'draft' AND i.frozen_snapshot IS NOT NULL AND j.attempts<5 AND ((j.state='queued' AND CAST(j.next_attempt_at AS INTEGER)<=?) OR (j.state='leased' AND CAST(j.lease_expires_at AS INTEGER)<=?)) ORDER BY CAST(j.next_attempt_at AS INTEGER),j.id LIMIT 1`, now.Unix(), now.Unix()))
		if errors.Is(e, ErrNotFound) {
			return nil
		}
		if e != nil {
			return e
		}
		j.Token, e = newBusinessID()
		if e != nil {
			return e
		}
		j.Attempts++
		j.State = "leased"
		_, e = c.ExecContext(ctx, `UPDATE paperless_jobs SET state='leased',attempts=?,lease_token=?,lease_expires_at=? WHERE id=?`, j.Attempts, j.Token, now.Add(lease).Unix(), j.ID)
		return e
	})
	if err == nil && j.ID == "" {
		err = ErrNotFound
	}
	return
}
func (r *PaperlessRepository) mutate(ctx context.Context, j PaperlessJob, set string, args ...any) error {
	args = append(args, j.ID, j.DocumentID, j.Token)
	res, e := r.store.db.ExecContext(ctx, `UPDATE paperless_jobs SET `+set+`,updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=? AND document_id=? AND state='leased' AND lease_token=? AND CAST(lease_expires_at AS INTEGER)>unixepoch()`, args...) // #nosec G202 -- Private callers supply literal SET clauses; all external values are bound parameters.
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	return nil
}

// Persist intent before any bytes are submitted. Never clear this marker after
// an ambiguous network outcome: reconciliation is safe; blind resubmission is not.
func (r *PaperlessRepository) BeginUpload(ctx context.Context, j PaperlessJob) error {
	res, e := r.store.db.ExecContext(ctx, `UPDATE paperless_jobs SET upload_started=1 WHERE id=? AND document_id=? AND state='leased' AND lease_token=? AND upload_started=0 AND CAST(lease_expires_at AS INTEGER)>unixepoch()`, j.ID, j.DocumentID, j.Token)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	return nil
}
func (r *PaperlessRepository) SaveTask(ctx context.Context, j PaperlessJob, id string) error {
	if len(id) == 0 || len(id) > 128 {
		return ErrConflict
	}
	return r.mutate(ctx, j, `remote_task_id=?`, id)
}
func (r *PaperlessRepository) Complete(ctx context.Context, j PaperlessJob, id int64) error {
	if id <= 0 {
		return ErrConflict
	}
	return r.mutate(ctx, j, `state='completed',remote_document_id=?,lease_token='',lease_expires_at=0,last_error_code='',last_error_summary=''`, id)
}
func PaperlessErrorSummary(code string) string {
	switch code {
	case "api_incompatible":
		return "Paperless API 10 is required. Upgrade Paperless or check API version negotiation."
	case "configuration_missing":
		return "Paperless URL or token unavailable. Configure delivery and retry."
	case "configuration_invalid":
		return "Paperless configuration or token file is invalid. Correct it and retry."
	case "delivery_uncertain":
		return "Upload outcome uncertain. Retry checks Paperless without uploading again."
	case "remote_pending":
		return "Paperless is still processing. Retry checks the existing task."
	case "remote_failed":
		return "Paperless processing failed. Inspect the remote task before retrying."
	case "document_invalid":
		return "Document integrity check failed."
	case "lease_expired":
		return "Delivery interrupted; retry to reconcile."
	default:
		return "Paperless request failed. Delivery will retry within the attempt limit."
	}
}
func (r *PaperlessRepository) Fail(ctx context.Context, j PaperlessJob, now time.Time, code string, maxAttempts int) error {
	switch code {
	case "api_incompatible", "configuration_missing", "configuration_invalid", "delivery_uncertain", "remote_pending", "remote_failed", "document_invalid", "lease_expired":
	default:
		code = "request_failed"
	}
	state := "queued"
	if j.Attempts >= min(max(maxAttempts, 1), 5) {
		state = "failed"
	}
	base := int64(30) << min(max(j.Attempts-1, 0), 6)
	delay := base + rand.Int64N(base/4+1) // #nosec G404 -- Retry jitter needs distribution, not secrecy; lease tokens use crypto/rand via newBusinessID.
	return r.mutate(ctx, j, `state=?,next_attempt_at=?,lease_token='',lease_expires_at=0,last_error_code=?,last_error_summary=?`, state, now.Unix()+delay, code, PaperlessErrorSummary(code))
}
func (r *PaperlessRepository) Retry(ctx context.Context, id string) error {
	return r.store.InvoiceRepository().immediate(ctx, func(c *sql.Conn) error {
		res, e := c.ExecContext(ctx, `UPDATE paperless_jobs SET state='queued',attempts=0,next_attempt_at=?,lease_token='',lease_expires_at=0,last_error_code='',last_error_summary='' WHERE document_id=? AND state='failed' AND EXISTS(SELECT 1 FROM documents d JOIN invoices i ON i.id=d.invoice_id WHERE d.id=paperless_jobs.document_id AND d.status='ready' AND d.kind='invoice_pdf' AND i.state<>'draft' AND i.frozen_snapshot IS NOT NULL)`, time.Now().Unix(), id)
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return ErrConflict
		}
		aid, e := newBusinessID()
		if e != nil {
			return e
		}
		identity, _ := ctx.Value(invoiceAuditKey{}).(invoiceAuditIdentity)
		_, e = c.ExecContext(ctx, `INSERT INTO audit_events(id,action,target_type,target_id,result,change_summary,actor_subject,request_id) SELECT ?,'paperless.retry','paperless_job',id,'success','Manual delivery retry',?,? FROM paperless_jobs WHERE document_id=?`, aid, identity.actor, identity.request, id)
		return e
	})
}

// RejectUpload resets intent only for a definitive pre-consumption rejection or
// a transport failure proven not to have sent any request bytes.
func (r *PaperlessRepository) RejectUpload(ctx context.Context, j PaperlessJob) error {
	return r.mutate(ctx, j, `upload_started=0`)
}
