CREATE TABLE document_jobs (
 id TEXT PRIMARY KEY NOT NULL,
 document_id TEXT NOT NULL UNIQUE REFERENCES documents(id),
 state TEXT NOT NULL CHECK(state IN ('queued','leased','completed','failed')),
 attempts INTEGER NOT NULL DEFAULT 0 CHECK(typeof(attempts)='integer' AND attempts>=0),
 next_attempt_at INTEGER NOT NULL,
 lease_expires_at INTEGER NOT NULL DEFAULT 0,
 lease_token TEXT NOT NULL DEFAULT '',
 last_error_code TEXT NOT NULL DEFAULT ''
);
CREATE INDEX document_jobs_claim ON document_jobs(state,next_attempt_at,lease_expires_at);
CREATE TRIGGER document_ready_update BEFORE UPDATE ON documents WHEN OLD.status='ready'
BEGIN SELECT RAISE(ABORT,'ready documents are immutable'); END;
CREATE TRIGGER document_ready_delete BEFORE DELETE ON documents WHEN OLD.status='ready'
BEGIN SELECT RAISE(ABORT,'ready documents are immutable'); END;
CREATE TRIGGER document_ready_replace BEFORE INSERT ON documents
WHEN EXISTS(SELECT 1 FROM documents WHERE status='ready' AND (id=NEW.id OR storage_key=NEW.storage_key OR (invoice_id=NEW.invoice_id AND kind=NEW.kind) OR rowid=NEW.rowid))
BEGIN SELECT RAISE(ABORT,'ready documents cannot be replaced'); END;
CREATE TRIGGER document_ready_update_replace BEFORE UPDATE ON documents
WHEN EXISTS(SELECT 1 FROM documents WHERE status='ready' AND (id=NEW.id OR storage_key=NEW.storage_key OR (invoice_id=NEW.invoice_id AND kind=NEW.kind) OR rowid=NEW.rowid))
BEGIN SELECT RAISE(ABORT,'ready documents cannot be replaced'); END;
CREATE TRIGGER invoice_queue_document AFTER UPDATE OF state ON invoices
WHEN OLD.state='draft' AND NEW.state='finalized' AND NEW.frozen_snapshot IS NOT NULL
BEGIN
 INSERT INTO documents(id,invoice_id,kind,storage_key,media_type,size_bytes,checksum_sha256,generator_version,status)
 VALUES(lower(hex(randomblob(32))),NEW.id,'invoice_pdf',lower(hex(randomblob(32))),'application/pdf',0,printf('%064d',0),'pending','pending');
 INSERT INTO document_jobs(id,document_id,state,next_attempt_at)
 SELECT lower(hex(randomblob(32))),id,'queued',unixepoch() FROM documents WHERE invoice_id=NEW.id AND kind='invoice_pdf';
END;
