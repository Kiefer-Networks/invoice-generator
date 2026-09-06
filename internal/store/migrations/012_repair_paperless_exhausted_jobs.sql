-- Repair exhausted historical jobs without changing migration 011 or remote history.
UPDATE paperless_jobs
SET state='failed',lease_token='',lease_expires_at=0,
 last_error_code='lease_expired',last_error_summary='Delivery attempts exhausted; retry to reconcile.'
WHERE state IN ('queued','leased') AND attempts>=5;
-- Older schemas allowed jobs for documents that cannot be delivered. Keep their
-- history visible instead of leaving permanently unclaimable queued work.
UPDATE paperless_jobs
SET state='failed',lease_token='',lease_expires_at=0,
 last_error_code='document_invalid',last_error_summary='Document is not an immutable ready invoice PDF.'
WHERE state IN ('queued','leased') AND NOT EXISTS(
 SELECT 1 FROM documents d JOIN invoices i ON i.id=d.invoice_id
 WHERE d.id=paperless_jobs.document_id AND d.status='ready' AND d.kind='invoice_pdf'
 AND i.state<>'draft' AND i.frozen_snapshot IS NOT NULL
);
