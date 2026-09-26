-- Tracks how much of a mailbox's newest-first window messages_cache
-- actually covers, per (account, protocol, folder).
--
-- Fixes a real bug (see CODE_REVIEW.md round 5): Service.FetchMessages
-- served the cache whenever it held *any* row for the folder, so a
-- later, larger request was silently truncated to whatever the first
-- fetch happened to cache — e.g. ?limit=20 then ?limit=50 returned the
-- 20 cached rows without ever dialing the server again. The cache can
-- only answer a request when it knows it covers that window:
--   * coverage  = number of newest messages known to be cached
--                 contiguously from the top.
--   * exhausted = 1 once a fetch returned a short page (fewer rows than
--                 asked for), meaning the whole mailbox is cached and
--                 any window can be answered from the cache.
CREATE TABLE IF NOT EXISTS messages_cache_state (
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    protocol   TEXT NOT NULL,
    folder     TEXT NOT NULL,
    coverage   INTEGER NOT NULL DEFAULT 0,
    exhausted  INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (account_id, protocol, folder)
);
