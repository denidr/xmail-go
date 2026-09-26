// Package mailer defines protocol-agnostic interfaces implemented by
// internal/mailer/{smtp,imap,pop3}. api and mcpserver depend only on
// these interfaces (via account.Service), never on a specific
// protocol package. See PLAN.md §1.
package mailer

import "context"

// Attachment is a file attached to an outgoing message.
type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

// OutgoingMessage is what Sender.Send accepts.
type OutgoingMessage struct {
	To          []string
	CC          []string
	BCC         []string
	Subject     string
	BodyText    string
	BodyHTML    string
	Attachments []Attachment
	// Headers are additional custom header lines (e.g. "X-Priority",
	// "Reply-To") applied on top of the standard ones. Standard headers
	// (From/To/Cc/Bcc/Subject/Content-Type/etc.) are always managed by
	// the Sender implementation and cannot be overridden here — see
	// PRD.MD §6.2 "custom headers dasar".
	Headers map[string]string
}

// Message is a fetched/cached email summary. JSON tags matter: this
// struct is serialized directly (no separate DTO) by both
// GET /accounts/{id}/messages (internal/api/messages_handler.go) and
// the fetch_emails MCP tool (internal/mcpserver/server.go) — keeping
// it snake_case here keeps both outputs consistent with every other
// endpoint's JSON shape (see internal/api/dto.go).
type Message struct {
	UID     string `json:"uid"`
	Folder  string `json:"folder"`
	Subject string `json:"subject"`
	From    string `json:"from"`
	To      string `json:"to"`
	Date    string `json:"date"`
	IsRead  bool   `json:"is_read"`
	// Attachments is the list of attachment filenames found in the
	// message's MIME structure — names only (see PRD.MD §6.3), never
	// downloaded/decoded. Empty/nil for POP3 (TOP doesn't expose body
	// structure) and for IMAP messages with no attachment parts.
	Attachments []string `json:"attachments,omitempty"`
}

// Sender delivers outgoing mail (implemented by mailer/smtp).
type Sender interface {
	Send(ctx context.Context, msg OutgoingMessage) error
	TestConnection(ctx context.Context) error
}

// Fetcher lists/reads mail from a mailbox (implemented by
// mailer/imap and mailer/pop3).
//
// Fetch MUST return messages newest-first, and each Message's Folder
// must match the folder it was fetched from — except for a folder-less
// protocol (POP3), which always reports INBOX and is handed INBOX by
// account.Service (see canonicalFolder). The order matters beyond
// display: account.MessageCache.Upsert records it in sort_rank, so an
// oldest-first implementation would silently reverse cached order. Both
// implementations have an integration test asserting this (see
// internal/mailer/imap and internal/mailer/pop3's TestIntegration_Fetch*).
type Fetcher interface {
	Fetch(ctx context.Context, folder string, limit, offset int) ([]Message, error)
	TestConnection(ctx context.Context) error
}

// Checker reports the unread count for a mailbox without a full fetch
// (implemented by mailer/imap; POP3 has no unseen-flag concept, see
// mailer/pop3). "New since last check" is not a protocol-level
// concept — account.Service.CheckNew computes it by diffing a Fetch
// against messages_cache (see PLAN.md §3 /accounts/{id}/check).
type Checker interface {
	Check(ctx context.Context, folder string) (unread int, err error)
}

// Marker marks a message as read (implemented by mailer/imap via the
// IMAP \Seen flag; POP3 has no per-message flag concept, see
// mailer/pop3 — POP3 accounts do not implement this interface).
type Marker interface {
	MarkRead(ctx context.Context, folder, uid string) error
}

// FetcherChecker combines Fetcher and Checker — implemented by
// protocols that support both a mailbox listing and an unread count
// (currently only IMAP).
type FetcherChecker interface {
	Fetcher
	Checker
}

// DefaultFolder returns folder unchanged, or "INBOX" if it's empty —
// the shared fallback used by account.Service and mailer/imap so the
// same "folder == \"\" -> INBOX" guard isn't reimplemented at every
// call site (it previously was, 6 times across those two files —
// see CODE_REVIEW.md "Duplicated Code").
func DefaultFolder(folder string) string {
	if folder == "" {
		return "INBOX"
	}
	return folder
}

// WindowRange computes the 1-based, inclusive [start, end] sequence
// range for a "most recent limit messages, skipping offset" fetch,
// given the total message count in a mailbox. ok is false when the
// window is empty (nothing to fetch) — callers must check it instead
// of using start/end, since a naive start>end computation with a
// non-positive limit can otherwise be mishandled by protocol-specific
// range types (see the internal/mailer/imap zero-limit regression
// test in PLAN.md Fase 3/8 notes). Shared by mailer/imap and
// mailer/pop3 so the windowing logic is defined exactly once.
func WindowRange(total, limit, offset int) (start, end int, ok bool) {
	end = total - offset
	if end <= 0 || limit <= 0 {
		return 0, 0, false
	}
	start = end - limit + 1
	if start < 1 {
		start = 1
	}
	return start, end, true
}
