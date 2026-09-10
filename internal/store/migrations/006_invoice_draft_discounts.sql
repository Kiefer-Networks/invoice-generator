ALTER TABLE invoice_items ADD COLUMN discount_basis_points INTEGER NOT NULL DEFAULT 0 CHECK (discount_basis_points >= 0 AND discount_basis_points <= 10000);

CREATE TRIGGER invoice_items_require_integer_discount_on_insert
BEFORE INSERT ON invoice_items
WHEN typeof(NEW.discount_basis_points) <> 'integer'
BEGIN
    SELECT RAISE(ABORT, 'invoice item discount must be an integer');
END;

CREATE TRIGGER invoice_items_require_integer_discount_on_update
BEFORE UPDATE OF discount_basis_points ON invoice_items
WHEN typeof(NEW.discount_basis_points) <> 'integer'
BEGIN
    SELECT RAISE(ABORT, 'invoice item discount must be an integer');
END;
