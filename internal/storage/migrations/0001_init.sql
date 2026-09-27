-- Initial schema for xmail.

CREATE TABLE IF NOT EXISTS accounts (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    email           TEXT NOT NULL,
    smtp_host       TEXT,
    smtp_port       INTEGER,
    smtp_tls_mode   TEXT,
    imap_host       TEXT,
    imap_port       INTEGER,
    imap_tls_mode   TEXT,
    pop3_host       TEXT,
    pop3_port       INTEGER,
    pop3_tls_mode   TEXT,
    username        TEXT NOT NULL,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS credentials (
    account_id       TEXT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    encrypted_secret BLOB NOT NULL,
    nonce            BLOB NOT NULL
);

CREATE TABLE IF NOT EXISTS messages_cache (
    id          TEXT PRIMARY KEY,
    account_id  TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    protocol    TEXT NOT NULL,
    folder      TEXT,
    uid         TEXT NOT NULL,
    subject     TEXT,
    from_addr   TEXT,
    to_addr     TEXT,
    date        TEXT,
    is_read     INTEGER DEFAULT 0,
    fetched_at  TEXT NOT NULL,
    UNIQUE(account_id, protocol, folder, uid)
);

CREATE TABLE IF NOT EXISTS api_keys (
    id           TEXT PRIMARY KEY,
    key_hash     TEXT NOT NULL,
    label        TEXT,
    created_at   TEXT NOT NULL,
    last_used_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_messages_account ON messages_cache(account_id, folder);
