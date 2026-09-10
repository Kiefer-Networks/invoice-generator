-- Preserve existing migration checksums and any historical remote task IDs.
ALTER TABLE paperless_jobs ADD COLUMN lease_token TEXT NOT NULL DEFAULT '';
ALTER TABLE paperless_jobs ADD COLUMN remote_document_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE paperless_jobs ADD COLUMN upload_started INTEGER NOT NULL DEFAULT 0 CHECK(upload_started IN (0,1));
UPDATE paperless_jobs SET upload_started=1 WHERE attempts>0 OR remote_task_id<>'' OR state='completed';
UPDATE paperless_jobs SET next_attempt_at=coalesce(unixepoch(next_attempt_at),unixepoch()),lease_expires_at=0,state=CASE WHEN state='leased' THEN 'queued' ELSE state END;
CREATE TRIGGER paperless_requires_ready BEFORE INSERT ON paperless_jobs
WHEN NOT EXISTS(SELECT 1 FROM documents d JOIN invoices i ON i.id=d.invoice_id WHERE d.id=NEW.document_id AND d.status='ready' AND d.kind='invoice_pdf' AND i.state<>'draft' AND i.frozen_snapshot IS NOT NULL)
BEGIN SELECT RAISE(ABORT,'Paperless requires immutable invoice document'); END;
CREATE TRIGGER document_queue_paperless AFTER UPDATE OF status ON documents WHEN NEW.status='ready' AND NEW.kind='invoice_pdf'
BEGIN
 INSERT INTO paperless_jobs(id,document_id,state,next_attempt_at)
 SELECT lower(hex(randomblob(32))),NEW.id,'queued',unixepoch() WHERE EXISTS(SELECT 1 FROM invoices WHERE id=NEW.invoice_id AND state<>'draft' AND frozen_snapshot IS NOT NULL)
 ON CONFLICT(document_id) DO NOTHING;
END;
CREATE TRIGGER document_insert_queue_paperless AFTER INSERT ON documents WHEN NEW.status='ready' AND NEW.kind='invoice_pdf'
BEGIN
 INSERT INTO paperless_jobs(id,document_id,state,next_attempt_at)
 SELECT lower(hex(randomblob(32))),NEW.id,'queued',unixepoch() WHERE EXISTS(SELECT 1 FROM invoices WHERE id=NEW.invoice_id AND state<>'draft' AND frozen_snapshot IS NOT NULL)
 ON CONFLICT(document_id) DO NOTHING;
END;
INSERT INTO paperless_jobs(id,document_id,state,next_attempt_at)
SELECT lower(hex(randomblob(32))),d.id,'queued',unixepoch() FROM documents d JOIN invoices i ON i.id=d.invoice_id
WHERE d.status='ready' AND d.kind='invoice_pdf' AND i.state<>'draft' AND i.frozen_snapshot IS NOT NULL
ON CONFLICT(document_id) DO NOTHING;

CREATE TRIGGER paperless_audit_queue AFTER INSERT ON paperless_jobs
BEGIN
 INSERT INTO audit_events(id,action,target_type,target_id,result,change_summary)
 VALUES(lower(hex(randomblob(32))),'paperless.queued','paperless_job',NEW.id,'success','Delivery queued');
END;
CREATE TRIGGER paperless_audit_state AFTER UPDATE OF state ON paperless_jobs WHEN OLD.state<>NEW.state
BEGIN
 INSERT INTO audit_events(id,action,target_type,target_id,result,change_summary)
 VALUES(lower(hex(randomblob(32))),'paperless.'||NEW.state,'paperless_job',NEW.id,CASE WHEN NEW.state='failed' THEN 'failure' ELSE 'success' END,'Delivery state changed');
END;
