CREATE TABLE companies (
    id TEXT PRIMARY KEY CHECK (id <> ''),
    singleton INTEGER NOT NULL DEFAULT 1 UNIQUE CHECK (singleton = 1),
    legal_name TEXT NOT NULL,
    contact_name TEXT NOT NULL DEFAULT '',
    email TEXT NOT NULL DEFAULT '',
    phone TEXT NOT NULL DEFAULT '',
    address_line1 TEXT NOT NULL DEFAULT '',
    address_line2 TEXT NOT NULL DEFAULT '',
    postal_code TEXT NOT NULL DEFAULT '',
    city TEXT NOT NULL DEFAULT '',
    country TEXT NOT NULL DEFAULT '',
    tax_number TEXT NOT NULL DEFAULT '',
    vat_identifier TEXT NOT NULL DEFAULT '',
    bank_name TEXT NOT NULL DEFAULT '',
    iban TEXT NOT NULL DEFAULT '',
    bic TEXT NOT NULL DEFAULT '',
    logo_key TEXT NOT NULL DEFAULT '',
    brand_color TEXT NOT NULL DEFAULT '',
    default_language TEXT NOT NULL DEFAULT 'de',
    currency TEXT NOT NULL DEFAULT 'EUR',
    payment_terms_days INTEGER NOT NULL DEFAULT 14 CHECK (payment_terms_days >= 0),
    invoice_prefix TEXT NOT NULL DEFAULT '',
    next_invoice_sequence INTEGER NOT NULL DEFAULT 1 CHECK (next_invoice_sequence >= 1),
    standard_notes TEXT NOT NULL DEFAULT '',
    active INTEGER NOT NULL DEFAULT 1 CHECK (active = 1),
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE customers (
    id TEXT PRIMARY KEY CHECK (id <> ''),
    number TEXT NOT NULL UNIQUE CHECK (number <> ''),
    display_name TEXT NOT NULL CHECK (display_name <> ''),
    legal_name TEXT NOT NULL DEFAULT '',
    contact_name TEXT NOT NULL DEFAULT '',
    email TEXT NOT NULL DEFAULT '',
    address_line1 TEXT NOT NULL DEFAULT '',
    address_line2 TEXT NOT NULL DEFAULT '',
    postal_code TEXT NOT NULL DEFAULT '',
    city TEXT NOT NULL DEFAULT '',
    country TEXT NOT NULL DEFAULT '',
    vat_identifier TEXT NOT NULL DEFAULT '',
    preferred_language TEXT NOT NULL DEFAULT 'de',
    currency TEXT NOT NULL DEFAULT 'EUR',
    payment_terms_days INTEGER NOT NULL DEFAULT 14 CHECK (payment_terms_days >= 0),
    notes TEXT NOT NULL DEFAULT '',
    active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0, 1)),
    version INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX customers_active_number_idx ON customers(number) WHERE active = 1;
CREATE INDEX customers_active_display_name_idx ON customers(display_name) WHERE active = 1;

CREATE TABLE catalog_items (
    id TEXT PRIMARY KEY CHECK (id <> ''),
    number TEXT NOT NULL UNIQUE CHECK (number <> ''),
    kind TEXT NOT NULL CHECK (kind IN ('good', 'service')),
    title TEXT NOT NULL CHECK (title <> ''),
    description TEXT NOT NULL DEFAULT '',
    unit TEXT NOT NULL DEFAULT 'piece',
    net_unit_price_minor INTEGER NOT NULL CHECK (net_unit_price_minor >= 0),
    tax_rate_scaled INTEGER NOT NULL CHECK (tax_rate_scaled >= 0),
    active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0, 1)),
    version INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX catalog_items_active_number_idx ON catalog_items(number) WHERE active = 1;
CREATE INDEX catalog_items_active_title_idx ON catalog_items(title) WHERE active = 1;

CREATE TABLE invoices (
    id TEXT PRIMARY KEY CHECK (id <> ''),
    customer_id TEXT NOT NULL REFERENCES customers(id),
    correction_of_invoice_id TEXT REFERENCES invoices(id),
    state TEXT NOT NULL CHECK (state IN ('draft', 'finalized', 'paid', 'overdue', 'cancelled')),
    number TEXT UNIQUE,
    currency TEXT NOT NULL CHECK (currency <> ''),
    issue_date TEXT,
    due_date TEXT,
    cancellation_reason TEXT NOT NULL DEFAULT '',
    company_snapshot TEXT,
    customer_snapshot TEXT,
    payment_snapshot TEXT,
    locale_snapshot TEXT,
    tax_snapshot TEXT,
    note_snapshot TEXT,
    net_total_minor INTEGER NOT NULL DEFAULT 0 CHECK (net_total_minor >= 0),
    tax_total_minor INTEGER NOT NULL DEFAULT 0 CHECK (tax_total_minor >= 0),
    gross_total_minor INTEGER NOT NULL DEFAULT 0 CHECK (gross_total_minor >= 0),
    version INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    finalized_at TEXT,
    CHECK ((state = 'draft' AND number IS NULL) OR (state <> 'draft' AND number IS NOT NULL)),
    CHECK (state = 'draft' OR (company_snapshot IS NOT NULL AND customer_snapshot IS NOT NULL AND payment_snapshot IS NOT NULL AND locale_snapshot IS NOT NULL AND tax_snapshot IS NOT NULL AND note_snapshot IS NOT NULL))
);
CREATE INDEX invoices_customer_idx ON invoices(customer_id);
CREATE INDEX invoices_open_state_idx ON invoices(state) WHERE state IN ('draft', 'finalized', 'overdue');

CREATE TABLE invoice_items (
    id TEXT PRIMARY KEY CHECK (id <> ''),
    invoice_id TEXT NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
    catalog_item_id TEXT REFERENCES catalog_items(id),
    position INTEGER NOT NULL CHECK (position > 0),
    title_snapshot TEXT NOT NULL CHECK (title_snapshot <> ''),
    description_snapshot TEXT NOT NULL DEFAULT '',
    unit_snapshot TEXT NOT NULL DEFAULT 'piece',
    quantity_scaled INTEGER NOT NULL CHECK (quantity_scaled > 0),
    net_unit_price_minor INTEGER NOT NULL CHECK (net_unit_price_minor >= 0),
    tax_rate_scaled INTEGER NOT NULL CHECK (tax_rate_scaled >= 0),
    net_total_minor INTEGER NOT NULL DEFAULT 0 CHECK (net_total_minor >= 0),
    tax_total_minor INTEGER NOT NULL DEFAULT 0 CHECK (tax_total_minor >= 0),
    gross_total_minor INTEGER NOT NULL DEFAULT 0 CHECK (gross_total_minor >= 0),
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (invoice_id, position)
);

CREATE TRIGGER invoice_items_insert_only_for_drafts
BEFORE INSERT ON invoice_items
WHEN (SELECT state FROM invoices WHERE id = NEW.invoice_id) <> 'draft'
BEGIN
    SELECT RAISE(ABORT, 'invoice items are immutable after finalization');
END;

CREATE TRIGGER invoice_items_update_only_for_drafts
BEFORE UPDATE ON invoice_items
WHEN (SELECT state FROM invoices WHERE id = OLD.invoice_id) <> 'draft'
BEGIN
    SELECT RAISE(ABORT, 'invoice items are immutable after finalization');
END;

CREATE TRIGGER invoice_items_delete_only_for_drafts
BEFORE DELETE ON invoice_items
WHEN (SELECT state FROM invoices WHERE id = OLD.invoice_id) <> 'draft'
BEGIN
    SELECT RAISE(ABORT, 'invoice items are immutable after finalization');
END;

CREATE TRIGGER finalized_invoice_snapshots_are_immutable
BEFORE UPDATE OF number, customer_id, company_snapshot, customer_snapshot, payment_snapshot, locale_snapshot, tax_snapshot, note_snapshot, currency, issue_date, due_date, net_total_minor, tax_total_minor, gross_total_minor ON invoices
WHEN OLD.state <> 'draft'
BEGIN
    SELECT RAISE(ABORT, 'finalized invoice snapshots are immutable');
END;

CREATE TABLE documents (
    id TEXT PRIMARY KEY CHECK (id <> ''),
    invoice_id TEXT NOT NULL REFERENCES invoices(id),
    kind TEXT NOT NULL CHECK (kind IN ('invoice_pdf', 'preview_pdf')),
    storage_key TEXT NOT NULL UNIQUE CHECK (storage_key <> ''),
    media_type TEXT NOT NULL CHECK (media_type <> ''),
    size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0),
    checksum_sha256 TEXT NOT NULL CHECK (length(checksum_sha256) = 64),
    generator_version TEXT NOT NULL CHECK (generator_version <> ''),
    status TEXT NOT NULL CHECK (status IN ('pending', 'ready', 'failed')),
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (invoice_id, kind)
);
CREATE INDEX documents_invoice_status_idx ON documents(invoice_id, status);

CREATE TABLE paperless_jobs (
    id TEXT PRIMARY KEY CHECK (id <> ''),
    document_id TEXT NOT NULL UNIQUE REFERENCES documents(id),
    state TEXT NOT NULL CHECK (state IN ('queued', 'leased', 'completed', 'failed')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at TEXT NOT NULL,
    lease_expires_at TEXT,
    last_error_code TEXT NOT NULL DEFAULT '',
    last_error_summary TEXT NOT NULL DEFAULT '',
    remote_task_id TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX paperless_jobs_claim_idx ON paperless_jobs(next_attempt_at) WHERE state = 'queued';

CREATE TABLE oidc_users (
    id TEXT PRIMARY KEY CHECK (id <> ''),
    issuer TEXT NOT NULL CHECK (issuer <> ''),
    subject TEXT NOT NULL CHECK (subject <> ''),
    display_name TEXT NOT NULL DEFAULT '',
    email TEXT NOT NULL DEFAULT '',
    last_login_at TEXT,
    last_authorization_at TEXT,
    active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0, 1)),
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (issuer, subject)
);

CREATE TABLE sessions (
    id TEXT PRIMARY KEY CHECK (id <> ''),
    user_id TEXT NOT NULL REFERENCES oidc_users(id) ON DELETE CASCADE,
    token_hash BLOB NOT NULL UNIQUE,
    csrf_secret_hash BLOB NOT NULL,
    authorization_expires_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_seen_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX sessions_expiry_idx ON sessions(expires_at);

CREATE TABLE audit_events (
    id TEXT PRIMARY KEY CHECK (id <> ''),
    actor_subject TEXT NOT NULL DEFAULT '',
    action TEXT NOT NULL CHECK (action <> ''),
    target_type TEXT NOT NULL CHECK (target_type <> ''),
    target_id TEXT NOT NULL DEFAULT '',
    result TEXT NOT NULL CHECK (result IN ('success', 'failure')),
    request_id TEXT NOT NULL DEFAULT '',
    change_summary TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX audit_events_target_idx ON audit_events(target_type, target_id, created_at);
