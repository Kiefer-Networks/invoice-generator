-- REPLACE can delete its rowid victim without DELETE triggers when recursive
-- triggers are OFF. Capture a protected victim before insertion and verify that
-- it still exists afterward; RAISE(ABORT) rolls back the complete statement.
-- Candidates are captured transactionally and reset before each INSERT.
-- No permanent physical-rowid mapping is kept (VACUUM can reassign rowids).
CREATE TABLE invoice_rowid_replacement_checks (
    entity_type TEXT PRIMARY KEY CHECK (entity_type IN ('invoice','item')),
    owner_id TEXT NOT NULL CHECK (owner_id<>'')
) WITHOUT ROWID;

-- In BEFORE INSERT, NEW.rowid may be -1 for automatic allocation. It is also
-- legal to explicitly request rowid -1. Capturing the possible victim and then
-- checking its existence avoids rejecting normal automatically assigned IDs.
CREATE TRIGGER invoice_rowid_capture BEFORE INSERT ON invoices
BEGIN
 DELETE FROM invoice_rowid_replacement_checks WHERE entity_type='invoice';
 INSERT INTO invoice_rowid_replacement_checks(entity_type,owner_id)
 SELECT 'invoice',id FROM invoices WHERE rowid=NEW.rowid AND state<>'draft';
END;
CREATE TRIGGER invoice_item_rowid_capture BEFORE INSERT ON invoice_items
BEGIN
 DELETE FROM invoice_rowid_replacement_checks WHERE entity_type='item';
 INSERT INTO invoice_rowid_replacement_checks(entity_type,owner_id)
 SELECT 'item',i.id FROM invoice_items i JOIN invoices v ON v.id=i.invoice_id
 WHERE i.rowid=NEW.rowid AND v.state<>'draft';
END;
CREATE TRIGGER invoice_rowid_insert_guard AFTER INSERT ON invoices
BEGIN
 SELECT RAISE(ABORT,'finalized invoice rowid cannot be displaced')
 WHERE EXISTS(SELECT 1 FROM invoice_rowid_replacement_checks c WHERE c.entity_type='invoice'
 AND NOT EXISTS(SELECT 1 FROM invoices v WHERE v.id=c.owner_id AND v.state<>'draft'));
 DELETE FROM invoice_rowid_replacement_checks WHERE entity_type='invoice';
END;
CREATE TRIGGER invoice_item_rowid_insert_guard AFTER INSERT ON invoice_items
BEGIN
 SELECT RAISE(ABORT,'finalized position rowid cannot be displaced')
 WHERE EXISTS(SELECT 1 FROM invoice_rowid_replacement_checks c WHERE c.entity_type='item'
 AND NOT EXISTS(SELECT 1 FROM invoice_items i JOIN invoices v ON v.id=i.invoice_id WHERE i.id=c.owner_id AND v.state<>'draft'));
 DELETE FROM invoice_rowid_replacement_checks WHERE entity_type='item';
END;

-- NEW.rowid reflects all three aliases (rowid, _rowid_, oid). Do not use UPDATE
-- OF: that would match only the alias written in the triggering statement.
CREATE TRIGGER invoice_rowid_update_guard BEFORE UPDATE ON invoices
WHEN NEW.rowid<>OLD.rowid AND (OLD.state<>'draft' OR EXISTS(
 SELECT 1 FROM invoices WHERE rowid=NEW.rowid AND state<>'draft'))
BEGIN SELECT RAISE(ABORT,'finalized invoice rowid cannot be displaced'); END;
CREATE TRIGGER invoice_item_rowid_update_guard BEFORE UPDATE ON invoice_items
WHEN NEW.rowid<>OLD.rowid AND EXISTS(
 SELECT 1 FROM invoice_items i JOIN invoices v ON v.id=i.invoice_id WHERE i.rowid=NEW.rowid AND v.state<>'draft')
BEGIN SELECT RAISE(ABORT,'finalized position rowid cannot be displaced'); END;
