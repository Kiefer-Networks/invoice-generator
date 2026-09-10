ALTER TABLE customers ADD COLUMN search_key TEXT NOT NULL DEFAULT '';
ALTER TABLE customers ADD COLUMN sort_key TEXT NOT NULL DEFAULT '';
CREATE INDEX customers_active_sort_key_idx ON customers(sort_key, number, id) WHERE active = 1;
