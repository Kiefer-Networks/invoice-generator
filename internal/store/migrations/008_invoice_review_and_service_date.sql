-- Do not change earlier migrations: already issued documents retain their bytes.
ALTER TABLE invoices ADD COLUMN service_date TEXT NOT NULL DEFAULT '';
ALTER TABLE invoice_finalization_keys ADD COLUMN reviewed_at TEXT NOT NULL DEFAULT '';
CREATE TRIGGER invoice_update_replace_guard BEFORE UPDATE OF id,number,finalization_key ON invoices
WHEN EXISTS(SELECT 1 FROM invoices WHERE id<>OLD.id AND state<>'draft' AND (id=NEW.id OR number=NEW.number OR finalization_key=NEW.finalization_key))
BEGIN SELECT RAISE(ABORT,'finalized invoices cannot be replaced'); END;
CREATE TRIGGER invoice_item_update_replace_guard BEFORE UPDATE OF id ON invoice_items
WHEN EXISTS(SELECT 1 FROM invoice_items i JOIN invoices v ON v.id=i.invoice_id WHERE i.id=NEW.id AND i.id<>OLD.id AND v.state<>'draft')
BEGIN SELECT RAISE(ABORT,'finalized positions cannot be replaced'); END;
CREATE TRIGGER invoice_service_date_guard BEFORE UPDATE OF service_date ON invoices WHEN OLD.state<>'draft'
BEGIN SELECT RAISE(ABORT,'finalized service date is immutable'); END;
-- The application's integer renderer implements two decimal places. Historical
-- unsupported values stay untouched; new/edited monetary data must be supported.
CREATE TRIGGER company_currency_insert BEFORE INSERT ON companies WHEN NEW.currency NOT IN ('EUR','USD','GBP','CHF')
BEGIN SELECT RAISE(ABORT,'unsupported currency exponent'); END;
CREATE TRIGGER company_currency_update BEFORE UPDATE OF currency ON companies WHEN NEW.currency NOT IN ('EUR','USD','GBP','CHF')
BEGIN SELECT RAISE(ABORT,'unsupported currency exponent'); END;
CREATE TRIGGER customer_currency_insert BEFORE INSERT ON customers WHEN NEW.currency NOT IN ('EUR','USD','GBP','CHF')
BEGIN SELECT RAISE(ABORT,'unsupported currency exponent'); END;
CREATE TRIGGER customer_currency_update BEFORE UPDATE OF currency ON customers WHEN NEW.currency NOT IN ('EUR','USD','GBP','CHF')
BEGIN SELECT RAISE(ABORT,'unsupported currency exponent'); END;
CREATE TRIGGER invoice_currency_insert BEFORE INSERT ON invoices WHEN NEW.currency NOT IN ('EUR','USD','GBP','CHF')
BEGIN SELECT RAISE(ABORT,'unsupported currency exponent'); END;
CREATE TRIGGER invoice_currency_update BEFORE UPDATE OF currency ON invoices WHEN NEW.currency NOT IN ('EUR','USD','GBP','CHF')
BEGIN SELECT RAISE(ABORT,'unsupported currency exponent'); END;
CREATE TRIGGER catalog_currency_insert BEFORE INSERT ON catalog_items
WHEN EXISTS(SELECT 1 FROM companies WHERE currency NOT IN ('EUR','USD','GBP','CHF'))
BEGIN SELECT RAISE(ABORT,'unsupported currency exponent'); END;
CREATE TRIGGER catalog_currency_price_update BEFORE UPDATE OF net_unit_price_minor ON catalog_items
WHEN EXISTS(SELECT 1 FROM companies WHERE currency NOT IN ('EUR','USD','GBP','CHF'))
BEGIN SELECT RAISE(ABORT,'unsupported currency exponent'); END;
