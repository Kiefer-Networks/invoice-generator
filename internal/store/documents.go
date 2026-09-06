package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

type Document struct {
	ID, InvoiceID, StorageKey, MediaType, SHA256, GeneratorVersion, Status string
	Size                                                                   int64
}
type DocumentJob struct {
	ID, DocumentID, InvoiceID, Token string
	Attempts                         int
}
type DocumentRepository struct{ store *Store }

func (s *Store) DocumentRepository() *DocumentRepository { return &DocumentRepository{s} }

const documentColumns = `d.id,d.invoice_id,d.storage_key,d.media_type,d.checksum_sha256,d.generator_version,d.status,d.size_bytes`

func scanDocument(row interface{ Scan(...any) error }) (d Document, e error) {
	e = row.Scan(&d.ID, &d.InvoiceID, &d.StorageKey, &d.MediaType, &d.SHA256, &d.GeneratorVersion, &d.Status, &d.Size)
	if errors.Is(e, sql.ErrNoRows) {
		e = ErrNotFound
	}
	return
}
func (r *DocumentRepository) Get(ctx context.Context, id string) (Document, error) {
	return scanDocument(r.store.db.QueryRowContext(ctx, `SELECT `+documentColumns+` FROM documents d WHERE d.id=?`, id))
}
func (r *DocumentRepository) ForInvoice(ctx context.Context, id string) (Document, error) {
	return scanDocument(r.store.db.QueryRowContext(ctx, `SELECT `+documentColumns+` FROM documents d WHERE d.invoice_id=? AND d.kind='invoice_pdf'`, id))
}

// Every active Pocket ID administrator shares this singleton company's invoices.
// Membership/session freshness is checked by auth middleware; this second boundary
// requires the persisted active identity and a finalized invoice association.
func (r *DocumentRepository) Authorized(ctx context.Context, userID, id string) (Document, error) {
	return scanDocument(r.store.db.QueryRowContext(ctx, `SELECT `+documentColumns+` FROM documents d JOIN invoices i ON i.id=d.invoice_id WHERE d.id=? AND d.status='ready' AND i.state<>'draft' AND EXISTS(SELECT 1 FROM oidc_users WHERE id=? AND active=1)`, id, userID))
}
func (r *DocumentRepository) Enqueue(ctx context.Context, id string) (out Document, err error) {
	err = r.store.InvoiceRepository().immediate(ctx, func(c *sql.Conn) error {
		if _, e := readFrozen(ctx, c, id); e != nil {
			return e
		}
		existing, e := scanDocument(c.QueryRowContext(ctx, `SELECT `+documentColumns+` FROM documents d WHERE invoice_id=? AND kind='invoice_pdf'`, id))
		if e == nil {
			out = existing
			if existing.Status == "ready" {
				return nil
			}
			jid, e := newBusinessID()
			if e != nil {
				return e
			}
			_, e = c.ExecContext(ctx, `INSERT INTO document_jobs(id,document_id,state,next_attempt_at) VALUES(?,?,'queued',?) ON CONFLICT(document_id) DO NOTHING`, jid, out.ID, time.Now().Unix())
			return e
		}
		if !errors.Is(e, ErrNotFound) {
			return e
		}
		did, e := newBusinessID()
		if e != nil {
			return e
		}
		jid, e := newBusinessID()
		if e != nil {
			return e
		}
		_, e = c.ExecContext(ctx, `INSERT INTO documents(id,invoice_id,kind,storage_key,media_type,size_bytes,checksum_sha256,generator_version,status) VALUES(?,?,'invoice_pdf',?,'application/pdf',0,?,'pending','pending') ON CONFLICT(invoice_id,kind) DO NOTHING`, did, id, did, strings.Repeat("0", 64))
		if e != nil {
			return e
		}
		out, e = scanDocument(c.QueryRowContext(ctx, `SELECT `+documentColumns+` FROM documents d WHERE invoice_id=? AND kind='invoice_pdf'`, id))
		if e != nil {
			return e
		}
		_, e = c.ExecContext(ctx, `INSERT INTO document_jobs(id,document_id,state,next_attempt_at) VALUES(?,?,'queued',?) ON CONFLICT(document_id) DO NOTHING`, jid, out.ID, time.Now().Unix())
		return e
	})
	return
}
func (r *DocumentRepository) Claim(ctx context.Context, now time.Time, lease time.Duration) (j DocumentJob, err error) {
	err = r.store.InvoiceRepository().immediate(ctx, func(c *sql.Conn) error {
		_, e := c.ExecContext(ctx, `UPDATE documents SET status='failed' WHERE status<>'ready' AND id IN (SELECT document_id FROM document_jobs WHERE attempts>=5 AND state='leased' AND lease_expires_at<=?)`, now.Unix())
		if e != nil {
			return e
		}
		_, e = c.ExecContext(ctx, `UPDATE document_jobs SET state='failed',lease_token='',lease_expires_at=0,last_error_code='lease_expired' WHERE attempts>=5 AND state='leased' AND lease_expires_at<=?`, now.Unix())
		if e != nil {
			return e
		}
		e = c.QueryRowContext(ctx, `SELECT j.id,j.document_id,d.invoice_id,j.attempts FROM document_jobs j JOIN documents d ON d.id=j.document_id WHERE d.status<>'ready' AND ((j.state='queued' AND j.next_attempt_at<=?) OR (j.state='leased' AND j.lease_expires_at<=?)) ORDER BY j.next_attempt_at,j.id LIMIT 1`, now.Unix(), now.Unix()).Scan(&j.ID, &j.DocumentID, &j.InvoiceID, &j.Attempts)
		if errors.Is(e, sql.ErrNoRows) {
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
		_, e = c.ExecContext(ctx, `UPDATE document_jobs SET state='leased',attempts=?,lease_token=?,lease_expires_at=? WHERE id=?`, j.Attempts, j.Token, now.Add(lease).Unix(), j.ID)
		return e
	})
	if err == nil && j.ID == "" {
		err = ErrNotFound
	}
	return
}
func (r *DocumentRepository) Complete(ctx context.Context, j DocumentJob, a Document) error {
	if len(a.StorageKey) != 52 || len(a.SHA256) != 64 || a.Size <= 0 || a.GeneratorVersion == "" {
		return ErrConflict
	}
	return r.store.InvoiceRepository().immediate(ctx, func(c *sql.Conn) error {
		res, e := c.ExecContext(ctx, `UPDATE document_jobs SET state='completed',lease_token='',lease_expires_at=0,last_error_code='' WHERE id=? AND document_id=? AND state='leased' AND lease_token=?`, j.ID, j.DocumentID, j.Token)
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return ErrConflict
		}
		res, e = c.ExecContext(ctx, `UPDATE documents SET storage_key=?,checksum_sha256=?,size_bytes=?,generator_version=?,status='ready',updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=? AND status<>'ready'`, a.StorageKey, a.SHA256, a.Size, a.GeneratorVersion, j.DocumentID)
		if e != nil {
			return e
		}
		n, _ = res.RowsAffected()
		if n != 1 {
			return ErrConflict
		}
		return nil
	})
}
func (r *DocumentRepository) Fail(ctx context.Context, j DocumentJob, now time.Time, code string, max int) error {
	if code != "cancelled" && code != "render_failed" && code != "validation_failed" {
		code = "generation_failed"
	}
	state := "queued"
	if j.Attempts >= max {
		state = "failed"
	}
	delay := time.Second * 30 * time.Duration(1<<min(j.Attempts-1, 6))
	return r.store.InvoiceRepository().immediate(ctx, func(c *sql.Conn) error {
		res, e := c.ExecContext(ctx, `UPDATE document_jobs SET state=?,next_attempt_at=?,lease_token='',lease_expires_at=0,last_error_code=? WHERE id=? AND document_id=? AND state='leased' AND lease_token=?`, state, now.Add(delay).Unix(), code, j.ID, j.DocumentID, j.Token)
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return ErrConflict
		}
		_, e = c.ExecContext(ctx, `UPDATE documents SET status='failed' WHERE id=? AND status<>'ready'`, j.DocumentID)
		return e
	})
}
func (r *DocumentRepository) Retry(ctx context.Context, id string) error {
	return r.store.InvoiceRepository().immediate(ctx, func(c *sql.Conn) error {
		res, e := c.ExecContext(ctx, `UPDATE document_jobs SET state='queued',attempts=0,next_attempt_at=?,last_error_code='' WHERE document_id=? AND state='failed'`, time.Now().Unix(), id)
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return ErrConflict
		}
		_, e = c.ExecContext(ctx, `UPDATE documents SET status='pending' WHERE id=? AND status='failed'`, id)
		return e
	})
}
