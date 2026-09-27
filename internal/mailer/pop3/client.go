// Package pop3 implements mailer.Fetcher using github.com/knadh/go-pop3.
// POP3 has no folder concept, so folder is always ignored/"INBOX".
package pop3

import (
	"context"
	"fmt"
	"time"

	"github.com/emersion/go-message/mail"
	gopop3 "github.com/knadh/go-pop3"

	"xmail/internal/account"
	"xmail/internal/mailer"
)

const dialTimeout = 15 * time.Second

// Client fetches mail for one account's POP3 configuration.
type Client struct {
	cfg      account.ConnectionConfig
	username string
	password string
}

// New builds a Client for the given account POP3 config and credentials.
func New(cfg account.ConnectionConfig, username, password string) *Client {
	return &Client{cfg: cfg, username: username, password: password}
}

var _ mailer.Fetcher = (*Client)(nil)

// connect dials and authenticates. Mapping account.TLSMode:
//   - TLSModeTLS  -> implicit TLS (POP3S, e.g. port 995)
//   - TLSModeNone -> no TLS at all (must be explicitly configured)
//   - TLSModeStartTLS -> NOT supported by the underlying go-pop3 client
//     (it has no STARTTLS implementation); returns a clear error rather
//     than silently downgrading to plaintext or guessing implicit TLS.
func (c *Client) connect() (*gopop3.Conn, error) {
	if c.cfg.TLSMode == account.TLSModeStartTLS {
		return nil, fmt.Errorf("pop3: tls_mode \"starttls\" is not supported for POP3 (underlying library has no STARTTLS support) — use \"tls\" or \"none\"")
	}

	client := gopop3.New(gopop3.Opt{
		Host:        c.cfg.Host,
		Port:        c.cfg.Port,
		DialTimeout: dialTimeout,
		TLSEnabled:  c.cfg.TLSMode == account.TLSModeTLS,
	})
	conn, err := client.NewConn()
	if err != nil {
		return nil, fmt.Errorf("pop3: connect: %w", err)
	}
	if err := conn.Auth(c.username, c.password); err != nil {
		conn.Quit()
		return nil, fmt.Errorf("pop3: auth: %w", err)
	}
	return conn, nil
}

// TestConnection dials and authenticates, then disconnects.
func (c *Client) TestConnection(ctx context.Context) error {
	conn, err := c.connect()
	if err != nil {
		return err
	}
	return conn.Quit()
}

// Fetch returns up to limit messages (most recent first), skipping
// offset. Only headers are downloaded (via TOP, 0 body lines) — not
// the full message body (metadata only, matching the messages_cache
// schema). POP3 has no per-message read/unread flag, so IsRead is
// always reported true.
func (c *Client) Fetch(ctx context.Context, folder string, limit, offset int) ([]mailer.Message, error) {
	conn, err := c.connect()
	if err != nil {
		return nil, err
	}
	defer conn.Quit()

	uidls, err := conn.Uidl(0)
	if err != nil {
		return nil, fmt.Errorf("pop3: uidl: %w", err)
	}
	start, end, ok := mailer.WindowRange(len(uidls), limit, offset)
	if !ok {
		return nil, nil
	}

	out := make([]mailer.Message, 0, end-start+1)
	for id := end; id >= start; id-- {
		msg, err := c.fetchOne(conn, uidls, id)
		if err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	return out, nil
}

func (c *Client) fetchOne(conn *gopop3.Conn, uidls []gopop3.MessageID, id int) (mailer.Message, error) {
	entity, err := conn.Top(id, 0)
	if err != nil {
		return mailer.Message{}, fmt.Errorf("pop3: top %d: %w", id, err)
	}
	h := mail.Header{Header: entity.Header}

	m := mailer.Message{Folder: "INBOX", IsRead: true}
	if id-1 >= 0 && id-1 < len(uidls) {
		m.UID = uidls[id-1].UID
	}
	if subject, err := h.Subject(); err == nil {
		m.Subject = subject
	}
	if date, err := h.Date(); err == nil {
		m.Date = date.Format(time.RFC3339)
	}
	if from, err := h.AddressList("From"); err == nil && len(from) > 0 {
		m.From = from[0].Address
	}
	if to, err := h.AddressList("To"); err == nil && len(to) > 0 {
		m.To = to[0].Address
	}
	return m, nil
}
