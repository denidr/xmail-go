-- Adds attachment filename list caching to messages_cache: the email
-- body & metadata (subject, from, to, date, attachment list) is
-- stored/cached in SQLite. Stored as a JSON array of filenames (e.g.
-- '["invoice.pdf","logo.png"]'); NULL/empty means no attachments or
-- the protocol doesn't report them (POP3).
ALTER TABLE messages_cache ADD COLUMN attachments TEXT;
