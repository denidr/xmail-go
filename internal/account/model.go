// Package account defines the Account domain model and its persistence
// and orchestration logic.
package account

import "time"

// TLSMode controls how a mailer connection negotiates TLS.
type TLSMode string

const (
	TLSModeTLS      TLSMode = "tls"      // implicit TLS (e.g. SMTPS 465, IMAPS 993)
	TLSModeStartTLS TLSMode = "starttls" // opportunistic TLS on a plaintext port
	TLSModeNone     TLSMode = "none"     // no TLS, must be explicitly opted into
)

// ConnectionConfig holds host/port/TLS settings for one protocol
// (SMTP, IMAP, or POP3) on an account.
type ConnectionConfig struct {
	Host    string
	Port    int
	TLSMode TLSMode
}

// Account represents one configured mailbox. Credentials are stored
// separately (encrypted) and are never embedded in this struct when
// returned to API/MCP callers.
type Account struct {
	ID        string
	Name      string
	Email     string
	Username  string
	SMTP      *ConnectionConfig
	IMAP      *ConnectionConfig
	POP3      *ConnectionConfig
	CreatedAt time.Time
	UpdatedAt time.Time
}

// CheckResult is the outcome of a Check (Service.CheckNew): the
// server-reported unread count, and how many of the most recent messages
// were not yet in the Message cache. Shared by the REST and MCP adapters
// — both serialize this one type — so the two can't drift on the
// response shape. The JSON tags mirror the wire format both endpoints
// already used.
type CheckResult struct {
	Unread int `json:"unread_count"`
	New    int `json:"new_count"`
}
