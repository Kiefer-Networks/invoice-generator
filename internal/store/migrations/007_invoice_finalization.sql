ALTER TABLE invoices ADD COLUMN frozen_snapshot TEXT;
ALTER TABLE invoices ADD COLUMN finalization_key TEXT;
CREATE UNIQUE INDEX invoices_finalization_key_idx ON invoices(finalization_key);
ALTER TABLE invoices ADD COLUMN invoice_sequence INTEGER CHECK (invoice_sequence IS NULL OR (typeof(invoice_sequence)='integer' AND invoice_sequence>0));
ALTER TABLE invoices ADD COLUMN paid_at TEXT;
CREATE TABLE invoice_finalization_keys (
 key TEXT PRIMARY KEY, invoice_id TEXT NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
 draft_version INTEGER NOT NULL CHECK(draft_version>0), company_snapshot TEXT NOT NULL
);
CREATE TRIGGER invoice_final_content_guard BEFORE UPDATE OF id, correction_of_invoice_id, frozen_snapshot, finalization_key, invoice_sequence, finalized_at, created_at ON invoices
WHEN OLD.state <> 'draft'
BEGIN SELECT RAISE(ABORT,'finalized invoice content is immutable'); END;
CREATE TRIGGER invoice_final_delete_guard BEFORE DELETE ON invoices WHEN OLD.state <> 'draft'
BEGIN SELECT RAISE(ABORT,'finalized invoices cannot be deleted'); END;
CREATE TRIGGER invoice_transition_guard BEFORE UPDATE OF state ON invoices
WHEN NOT (NEW.state=OLD.state OR (OLD.state='draft' AND NEW.state='finalized') OR (OLD.state='finalized' AND NEW.state IN ('paid','overdue','cancelled')) OR (OLD.state='overdue' AND NEW.state IN ('paid','cancelled')))
BEGIN SELECT RAISE(ABORT,'invalid invoice transition'); END;
CREATE TRIGGER invoice_transition_metadata_guard BEFORE UPDATE ON invoices
WHEN (NEW.state='paid' AND (NEW.paid_at IS NULL OR NEW.paid_at='')) OR (NEW.state<>'paid' AND NEW.paid_at IS NOT NULL) OR (NEW.state='cancelled' AND trim(NEW.cancellation_reason)='') OR (NEW.state<>'cancelled' AND NEW.cancellation_reason<>'') OR (OLD.state='paid' AND NEW.paid_at IS NOT OLD.paid_at) OR (OLD.state='cancelled' AND NEW.cancellation_reason IS NOT OLD.cancellation_reason)
BEGIN SELECT RAISE(ABORT,'invalid invoice transition metadata'); END;
CREATE TRIGGER invoice_items_move_guard BEFORE UPDATE OF invoice_id ON invoice_items
WHEN (SELECT state FROM invoices WHERE id=NEW.invoice_id)<>'draft'
BEGIN SELECT RAISE(ABORT,'invoice items are immutable after finalization'); END;
CREATE TRIGGER correction_link_guard BEFORE INSERT ON invoices
WHEN NEW.correction_of_invoice_id IS NOT NULL AND (SELECT state FROM invoices WHERE id=NEW.correction_of_invoice_id) NOT IN ('finalized','overdue','paid','cancelled')
BEGIN SELECT RAISE(ABORT,'correction requires finalized original'); END;

CREATE TRIGGER invoice_replace_guard BEFORE INSERT ON invoices
WHEN EXISTS(SELECT 1 FROM invoices WHERE state<>'draft' AND (id=NEW.id OR number=NEW.number OR finalization_key=NEW.finalization_key))
BEGIN SELECT RAISE(ABORT,'finalized invoices cannot be replaced'); END;
CREATE TRIGGER invoice_item_replace_guard BEFORE INSERT ON invoice_items
WHEN EXISTS(SELECT 1 FROM invoice_items i JOIN invoices v ON v.id=i.invoice_id WHERE i.id=NEW.id AND v.state<>'draft')
BEGIN SELECT RAISE(ABORT,'finalized positions cannot be replaced'); END;
CREATE TRIGGER correction_link_update_guard BEFORE UPDATE OF correction_of_invoice_id ON invoices
WHEN NEW.correction_of_invoice_id IS NOT OLD.correction_of_invoice_id
BEGIN SELECT RAISE(ABORT,'correction linkage is immutable'); END;
