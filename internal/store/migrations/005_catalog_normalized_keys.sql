ALTER TABLE catalog_items ADD COLUMN search_key TEXT NOT NULL DEFAULT '';
ALTER TABLE catalog_items ADD COLUMN sort_key TEXT NOT NULL DEFAULT '';
CREATE INDEX catalog_items_active_sort_key_idx ON catalog_items(sort_key, number, id) WHERE active = 1;
