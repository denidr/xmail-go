// Package account defines the Account domain model and its persistence
// and orchestration logic. See PLAN.md §2-3.
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
