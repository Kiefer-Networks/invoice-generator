DROP TRIGGER invoice_items_update_only_for_drafts;

CREATE TRIGGER invoice_items_update_only_for_drafts
BEFORE UPDATE ON invoice_items
WHEN NEW.invoice_id <> OLD.invoice_id
  OR (SELECT state FROM invoices WHERE id = OLD.invoice_id) <> 'draft'
  OR (SELECT state FROM invoices WHERE id = NEW.invoice_id) <> 'draft'
BEGIN
    SELECT RAISE(ABORT, 'invoice items are immutable after finalization');
END;

CREATE TRIGGER catalog_items_require_integer_money_on_insert
BEFORE INSERT ON catalog_items
WHEN typeof(NEW.net_unit_price_minor) <> 'integer'
  OR typeof(NEW.tax_rate_scaled) <> 'integer'
BEGIN
    SELECT RAISE(ABORT, 'catalog money and tax values must be integers');
END;

CREATE TRIGGER catalog_items_require_integer_money_on_update
BEFORE UPDATE OF net_unit_price_minor, tax_rate_scaled ON catalog_items
WHEN typeof(NEW.net_unit_price_minor) <> 'integer'
  OR typeof(NEW.tax_rate_scaled) <> 'integer'
BEGIN
    SELECT RAISE(ABORT, 'catalog money and tax values must be integers');
END;

CREATE TRIGGER invoices_require_integer_money_on_insert
BEFORE INSERT ON invoices
WHEN typeof(NEW.net_total_minor) <> 'integer'
  OR typeof(NEW.tax_total_minor) <> 'integer'
  OR typeof(NEW.gross_total_minor) <> 'integer'
BEGIN
    SELECT RAISE(ABORT, 'invoice money values must be integers');
END;

CREATE TRIGGER invoices_require_integer_money_on_update
BEFORE UPDATE OF net_total_minor, tax_total_minor, gross_total_minor ON invoices
WHEN typeof(NEW.net_total_minor) <> 'integer'
  OR typeof(NEW.tax_total_minor) <> 'integer'
  OR typeof(NEW.gross_total_minor) <> 'integer'
BEGIN
    SELECT RAISE(ABORT, 'invoice money values must be integers');
END;

CREATE TRIGGER invoice_items_require_integer_values_on_insert
BEFORE INSERT ON invoice_items
WHEN typeof(NEW.quantity_scaled) <> 'integer'
  OR typeof(NEW.net_unit_price_minor) <> 'integer'
  OR typeof(NEW.tax_rate_scaled) <> 'integer'
  OR typeof(NEW.net_total_minor) <> 'integer'
  OR typeof(NEW.tax_total_minor) <> 'integer'
  OR typeof(NEW.gross_total_minor) <> 'integer'
BEGIN
    SELECT RAISE(ABORT, 'invoice item quantity, money, and tax values must be integers');
END;

CREATE TRIGGER invoice_items_require_integer_values_on_update
BEFORE UPDATE OF quantity_scaled, net_unit_price_minor, tax_rate_scaled, net_total_minor, tax_total_minor, gross_total_minor ON invoice_items
WHEN typeof(NEW.quantity_scaled) <> 'integer'
  OR typeof(NEW.net_unit_price_minor) <> 'integer'
  OR typeof(NEW.tax_rate_scaled) <> 'integer'
  OR typeof(NEW.net_total_minor) <> 'integer'
  OR typeof(NEW.tax_total_minor) <> 'integer'
  OR typeof(NEW.gross_total_minor) <> 'integer'
BEGIN
    SELECT RAISE(ABORT, 'invoice item quantity, money, and tax values must be integers');
END;
