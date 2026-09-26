package account

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"xmail/internal/mailer"
)

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

// ExistingUIDs returns the set of message UIDs already cached for
// (accountID, protocol, folder) — used to compute how many messages
// in a fresh Fetch are genuinely new (see Service.CheckNew).
func (r *Repository) ExistingUIDs(ctx context.Context, accountID, protocol, folder string) (map[string]bool, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT uid FROM messages_cache WHERE account_id = ? AND protocol = ? AND folder = ?`,
		accountID, protocol, folder)
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

// UpsertMessages writes msgs into messages_cache, inserting new rows
// and refreshing metadata (subject/flags/etc.) for ones already
// cached, keyed by (account_id, protocol, folder, uid).
//
// msgs is assumed to already be in the order it should be displayed in
// (mailer.Fetcher.Fetch returns newest-first) — that order is recorded
// explicitly in sort_rank (len(msgs)-i, so the first/newest element
// gets the highest rank), rather than relied upon implicitly via
// insertion order/rowid, which silently breaks: see
// migrations/0003_message_sort_rank.sql for the bug this replaced.
func (r *Repository) UpsertMessages(ctx context.Context, accountID, protocol string, msgs []mailer.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("account: begin tx: %w", err)
	}
	defer tx.Rollback()

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
		sortRank := len(msgs) - i
		if _, err := stmt.ExecContext(ctx, uuid.NewString(), accountID, protocol, m.Folder, m.UID, m.Subject, m.From, m.To, m.Date, isRead, now, encodeAttachments(m.Attachments), sortRank); err != nil {
			return fmt.Errorf("account: upsert message uid=%s: %w", m.UID, err)
		}
	}
	return tx.Commit()
}

// ListMessages returns cached messages for (accountID, protocol,
// folder), most recently fetched first.
func (r *Repository) ListMessages(ctx context.Context, accountID, protocol, folder string, limit, offset int) ([]mailer.Message, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT folder, uid, subject, from_addr, to_addr, date, is_read, attachments
		FROM messages_cache
		WHERE account_id = ? AND protocol = ? AND folder = ?
		ORDER BY fetched_at DESC, sort_rank DESC
		LIMIT ? OFFSET ?`,
		accountID, protocol, folder, limit, offset)
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

// MarkMessageRead sets is_read=1 for the cached row matching (account,
// protocol, folder, uid), if present. A cache miss (row not cached
// yet) is not an error — the protocol client itself is the source of
// truth for MarkRead (see Service.MarkRead); this only keeps a
// subsequent cached FetchMessages read in sync.
func (r *Repository) MarkMessageRead(ctx context.Context, accountID, protocol, folder, uid string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE messages_cache SET is_read = 1
		WHERE account_id = ? AND protocol = ? AND folder = ? AND uid = ?`,
		accountID, protocol, folder, uid)
	if err != nil {
		return fmt.Errorf("account: mark message read: %w", err)
	}
	return nil
}
