-- Adds attachment filename list caching to messages_cache, per
-- PRD.MD §6.3 "Body & metadata email (subject, from, to, date,
-- attachment list) disimpan/cache di SQLite". Stored as a JSON array
-- of filenames (e.g. '["invoice.pdf","logo.png"]'); NULL/empty means
-- no attachments or the protocol doesn't report them (POP3).
ALTER TABLE messages_cache ADD COLUMN attachments TEXT;
