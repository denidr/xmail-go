package account

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"xmail/internal/mailer"
)

// CacheKey identifies one mailbox's cached messages: an account, a
// Mailer protocol, and a Folder. It is the single key every
// messages_cache operation is scoped by, so a caller can't key the
// same rows differently across methods.
type CacheKey struct {
	AccountID string
	Protocol  string
	Folder    string
}

// Window is a Fetch window: the most recent Limit messages, skipping
// Offset — the same shape mailer.Fetcher.Fetch takes.
type Window struct {
	Limit  int
	Offset int
}

// MessageCache is the Message cache — the store's only reader and
// writer. It owns the Coverage/Exhausted state machine that decides
// whether a requested Window can be answered from cache or must be
// dialed, so callers ask a yes/no question (Get's served) instead of
// re-deriving the predicate themselves:
//
//   - Coverage  = newest messages known to be cached contiguously from
//     the top of the mailbox.
//   - Exhausted = the whole mailbox is cached, so any Window is
//     answerable without dialing.
//
// See migrations/0004_message_cache_state.sql for the bug this fixes.
type MessageCache struct {
	db *sql.DB
}

// NewMessageCache builds a MessageCache backed by db.
func NewMessageCache(db *sql.DB) *MessageCache {
	return &MessageCache{db: db}
}

// Get returns the cached messages for key's Window when the Message
// cache covers it. served is false when it doesn't — the caller must
// dial the mail server instead. On a served result, msgs is the
// window's rows, newest-first.
func (c *MessageCache) Get(ctx context.Context, key CacheKey, w Window) ([]mailer.Message, bool, error) {
	coverage, exhausted, err := c.state(ctx, key)
	if err != nil {
		return nil, false, err
	}
	if !exhausted && w.Offset+w.Limit > coverage {
		return nil, false, nil
	}
	msgs, err := c.list(ctx, key, w)
	if err != nil {
		return nil, false, err
	}
	// Coverage may claim a window the cache can't actually produce (e.g.
	// rows removed out from under it); fall back to dialing if so.
	if !exhausted && len(msgs) == 0 {
		return nil, false, nil
	}
	return msgs, true, nil
}

// Upsert writes msgs into the cache for key, inserting new rows and
// refreshing metadata (subject/flags/etc.) for ones already cached,
// keyed by (account_id, protocol, folder, uid).
//
// msgs is the newest-first page a Fetch returned for window (offset,
// limit). Each row's absolute position from the top of the mailbox
// (offset+i, 0 = newest) is recorded in sort_rank, and list orders by
// that alone — position, not fetch recency. A per-batch rank (or
// ordering by fetched_at) interleaves pages fetched at different times
// once the cache holds more than one batch, which showed as a served
// window in the wrong order, silently dropping rows: see
// migrations/0003_message_sort_rank.sql and 0005_message_cache_position.sql.
//
// A page written from the top of the mailbox (offset 0) supersedes
// everything below it: if its newest message differs from the cached
// newest, the mailbox changed at the top since the cache was built (new
// mail arrived, or mail was deleted), which shifts every cached row's
// position. The cached window and its Coverage/Exhausted are then
// discarded rather than left to mix stale positions into a served window
// (see TestService_FetchMessages_NewMailAtTopDoesNotServeStaleRows).
func (c *MessageCache) Upsert(ctx context.Context, key CacheKey, msgs []mailer.Message, offset int) error {
	if len(msgs) == 0 {
		return nil
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("account: begin tx: %w", err)
	}
	defer tx.Rollback()

	// The page's first message should sit at rank `offset`. If the cache
	// disagrees — a different message occupies that rank, or this message
	// is cached at a different rank — the mailbox shifted under the cache
	// (new mail arrived at the top, or mail was deleted), so every cached
	// row's position is stale: discard the rows and their
	// Coverage/Exhausted rather than let stale positions mix into a served
	// window (see TestService_FetchMessages_NewMailAtTop*).
	stale := false
	anchors, err := tx.QueryContext(ctx, `
		SELECT uid, sort_rank FROM messages_cache
		WHERE account_id = ? AND protocol = ? AND folder = ? AND (sort_rank = ? OR uid = ?)`,
		key.AccountID, key.Protocol, key.Folder, offset, msgs[0].UID)
	if err != nil {
		return fmt.Errorf("account: read cache anchors: %w", err)
	}
	for anchors.Next() {
		var uid string
		var rank int
		if err := anchors.Scan(&uid, &rank); err != nil {
			anchors.Close()
			return fmt.Errorf("account: scan cache anchor: %w", err)
		}
		if (rank == offset && uid != msgs[0].UID) || (uid == msgs[0].UID && rank != offset) {
			stale = true
		}
	}
	anchors.Close()
	if err := anchors.Err(); err != nil {
		return fmt.Errorf("account: read cache anchors: %w", err)
	}
	if stale {
		for _, q := range []string{
			`DELETE FROM messages_cache WHERE account_id = ? AND protocol = ? AND folder = ?`,
			`DELETE FROM messages_cache_state WHERE account_id = ? AND protocol = ? AND folder = ?`,
		} {
			if _, err := tx.ExecContext(ctx, q, key.AccountID, key.Protocol, key.Folder); err != nil {
				return fmt.Errorf("account: invalidate stale cache: %w", err)
			}
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO messages_cache (id, account_id, protocol, folder, uid, subject, from_addr, to_addr, date, is_read, fetched_at, attachments, sort_rank)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(account_id, protocol, folder, uid) DO UPDATE SET
			subject = excluded.subject,
			from_addr = excluded.from_addr,
			to_addr = excluded.to_addr,
			date = excluded.date,
			is_read = excluded.is_read,
			fetched_at = excluded.fetched_at,
			attachments = excluded.attachments,
			sort_rank = excluded.sort_rank`)
	if err != nil {
		return fmt.Errorf("account: prepare upsert: %w", err)
	}
	defer stmt.Close()

	for i, m := range msgs {
		isRead := 0
		if m.IsRead {
			isRead = 1
		}
		rank := offset + i
		if _, err := stmt.ExecContext(ctx, uuid.NewString(), key.AccountID, key.Protocol, key.Folder, m.UID, m.Subject, m.From, m.To, m.Date, isRead, now, encodeAttachments(m.Attachments), rank); err != nil {
			return fmt.Errorf("account: upsert message uid=%s: %w", m.UID, err)
		}
	}
	return tx.Commit()
}

// Record updates key's Coverage/Exhausted after a live fetch that
// returned `returned` messages for Window w.
//
// A fetch only proves something about the top of the mailbox when its
// window starts at or before the region already known to be contiguous
// (w.Offset <= existing coverage). A page fetched from deeper in the
// mailbox says nothing about the rows above it, so it is ignored here —
// otherwise it would inflate Coverage and let a later offset=0 window be
// served from a truncated cache, and a short page deep down would
// wrongly latch Exhausted (a real truncation bug on the REST `?offset=`
// path; see TestMessageCache_Record_OffsetDoesNotInflateCoverage).
//
// A short page means the mailbox ends here, so Exhausted latches. A full
// page means there is more mail than this window showed, so the mailbox
// is not fully cached — which un-latches a stale Exhausted once the
// mailbox has grown since it was set (CheckNew always dials, so it
// clears the latch; see the TestService_FetchMessages_ExhaustedCache*
// regression test). The one exception: a full page that doesn't reach
// past the end we already knew about proves nothing, so the latch stands.
func (c *MessageCache) Record(ctx context.Context, key CacheKey, w Window, returned int) error {
	coverage, wasExhausted, err := c.state(ctx, key)
	if err != nil {
		return err
	}
	if w.Offset > coverage {
		return nil
	}
	short := returned < w.Limit
	next := w.Offset + returned
	if !short {
		next = w.Offset + w.Limit
	}
	exhausted := short || (wasExhausted && next <= coverage)
	ex := 0
	if exhausted {
		ex = 1
	}
	_, err = c.db.ExecContext(ctx, `
		INSERT INTO messages_cache_state (account_id, protocol, folder, coverage, exhausted, updated_at)
		VALUES (?,?,?,?,?,?)
		ON CONFLICT(account_id, protocol, folder) DO UPDATE SET
			coverage = MAX(messages_cache_state.coverage, excluded.coverage),
			exhausted = excluded.exhausted,
			updated_at = excluded.updated_at`,
		key.AccountID, key.Protocol, key.Folder, next, ex, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("account: record fetch: %w", err)
	}
	return nil
}

// ExistingUIDs returns the set of Message UIDs already cached for key —
// used by Service.CheckNew to compute how many of a fresh fetch are
// genuinely new.
func (c *MessageCache) ExistingUIDs(ctx context.Context, key CacheKey) (map[string]bool, error) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT uid FROM messages_cache WHERE account_id = ? AND protocol = ? AND folder = ?`,
		key.AccountID, key.Protocol, key.Folder)
	if err != nil {
		return nil, fmt.Errorf("account: existing uids: %w", err)
	}
	defer rows.Close()

	out := make(map[string]bool)
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			return nil, fmt.Errorf("account: scan uid: %w", err)
		}
		out[uid] = true
	}
	return out, rows.Err()
}

// MarkRead sets is_read=1 for the cached row matching key+uid, if
// present. A cache miss (row not cached yet) is not an error — the
// protocol client itself is the source of truth for mark-read (see
// Service.MarkRead); this only keeps a subsequent cached Get in sync.
func (c *MessageCache) MarkRead(ctx context.Context, key CacheKey, uid string) error {
	_, err := c.db.ExecContext(ctx, `
		UPDATE messages_cache SET is_read = 1
		WHERE account_id = ? AND protocol = ? AND folder = ? AND uid = ?`,
		key.AccountID, key.Protocol, key.Folder, uid)
	if err != nil {
		return fmt.Errorf("account: mark message read: %w", err)
	}
	return nil
}

// state reads key's Coverage/Exhausted. A missing row means "nothing
// known" — coverage 0, exhausted false, which forces Get to dial.
func (c *MessageCache) state(ctx context.Context, key CacheKey) (coverage int, exhausted bool, err error) {
	row := c.db.QueryRowContext(ctx, `
		SELECT coverage, exhausted FROM messages_cache_state
		WHERE account_id = ? AND protocol = ? AND folder = ?`,
		key.AccountID, key.Protocol, key.Folder)
	var ex int
	switch err := row.Scan(&coverage, &ex); err {
	case nil:
		return coverage, ex != 0, nil
	case sql.ErrNoRows:
		return 0, false, nil
	default:
		return 0, false, fmt.Errorf("account: cache state: %w", err)
	}
}

// list returns the cached messages for key's Window, newest-first, by
// each row's absolute mailbox position (see Upsert) — regardless of
// whether the cache covers the window (Get is the coverage-aware reader;
// this is the raw storage read).
func (c *MessageCache) list(ctx context.Context, key CacheKey, w Window) ([]mailer.Message, error) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT folder, uid, subject, from_addr, to_addr, date, is_read, attachments
		FROM messages_cache
		WHERE account_id = ? AND protocol = ? AND folder = ?
		ORDER BY sort_rank ASC
		LIMIT ? OFFSET ?`,
		key.AccountID, key.Protocol, key.Folder, w.Limit, w.Offset)
	if err != nil {
		return nil, fmt.Errorf("account: list messages: %w", err)
	}
	defer rows.Close()

	var out []mailer.Message
	for rows.Next() {
		var m mailer.Message
		var isRead int
		var attachments *string
		if err := rows.Scan(&m.Folder, &m.UID, &m.Subject, &m.From, &m.To, &m.Date, &isRead, &attachments); err != nil {
			return nil, fmt.Errorf("account: scan message: %w", err)
		}
		m.IsRead = isRead != 0
		m.Attachments = decodeAttachments(attachments)
		out = append(out, m)
	}
	return out, rows.Err()
}

// encodeAttachments/decodeAttachments convert Message.Attachments to
// and from the JSON string stored in messages_cache.attachments (see
// migrations/0002_message_attachments.sql). nil/empty slice is stored
// as SQL NULL rather than "[]" or "null".
func encodeAttachments(names []string) any {
	if len(names) == 0 {
		return nil
	}
	b, err := json.Marshal(names)
	if err != nil {
		return nil
	}
	return string(b)
}

func decodeAttachments(raw *string) []string {
	if raw == nil || *raw == "" {
		return nil
	}
	var names []string
	if err := json.Unmarshal([]byte(*raw), &names); err != nil {
		return nil
	}
	return names
}
