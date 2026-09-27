// Package smtp implements mailer.Sender using github.com/wneessen/go-mail.
package smtp

import (
	"bytes"
	"context"
	"fmt"
	"time"

	gomail "github.com/wneessen/go-mail"

	"xmail/internal/account"
	"xmail/internal/mailer"
)

const dialTimeout = 15 * time.Second

// Client sends mail for one account's SMTP configuration.
type Client struct {
	cfg         account.ConnectionConfig
	fromAddress string
	username    string
	password    string
}

// New builds a Client for the given account SMTP config and
// credentials. fromAddress is used as the message's "From" header
// (only needed for Send, not for TestConnection) — pass "" when the
// Client is only used to test-connection.
func New(cfg account.ConnectionConfig, fromAddress, username, password string) *Client {
	return &Client{cfg: cfg, fromAddress: fromAddress, username: username, password: password}
}

var _ mailer.Sender = (*Client)(nil)

// buildMailClient maps account.TLSMode to the go-mail TLS options:
//   - TLSModeTLS      -> implicit TLS (SMTPS, e.g. port 465)
//   - TLSModeStartTLS -> mandatory STARTTLS (fails if server doesn't support it)
//   - TLSModeNone     -> no TLS at all (must be explicitly configured)
func (c *Client) buildMailClient() (*gomail.Client, error) {
	opts := []gomail.Option{
		gomail.WithPort(c.cfg.Port),
		gomail.WithTimeout(dialTimeout),
	}
	if c.username != "" {
		// go-mail defaults SMTPAuthType to SMTPAuthNoAuth — WithUsername/
		// WithPassword alone do NOT make it actually send AUTH (verified:
		// without this, DialWithContext against a real mail server
		// returned success even with a wrong password, and Send would
		// have silently attempted to relay unauthenticated). AutoDiscover
		// picks the strongest mechanism the server advertises.
		opts = append(opts,
			gomail.WithUsername(c.username),
			gomail.WithPassword(c.password),
			gomail.WithSMTPAuth(gomail.SMTPAuthAutoDiscover),
		)
	}
	switch c.cfg.TLSMode {
	case account.TLSModeTLS:
		opts = append(opts, gomail.WithSSL())
	case account.TLSModeStartTLS:
		opts = append(opts, gomail.WithTLSPolicy(gomail.TLSMandatory))
	case account.TLSModeNone:
		opts = append(opts, gomail.WithTLSPolicy(gomail.NoTLS))
	default:
		return nil, fmt.Errorf("smtp: unknown tls_mode %q", c.cfg.TLSMode)
	}
	mc, err := gomail.NewClient(c.cfg.Host, opts...)
	if err != nil {
		return nil, fmt.Errorf("smtp: build client: %w", err)
	}
	return mc, nil
}

// TestConnection dials the SMTP server and authenticates (if
// credentials are set), without sending anything.
func (c *Client) TestConnection(ctx context.Context) error {
	mc, err := c.buildMailClient()
	if err != nil {
		return err
	}
	if err := mc.DialWithContext(ctx); err != nil {
		return fmt.Errorf("smtp: connect: %w", err)
	}
	return mc.Close()
}

// Send builds a MIME message from msg and delivers it via the
// account's SMTP server.
func (c *Client) Send(ctx context.Context, msg mailer.OutgoingMessage) error {
	if c.fromAddress == "" {
		return fmt.Errorf("smtp: no from address configured for this client")
	}
	mc, err := c.buildMailClient()
	if err != nil {
		return err
	}

	m := gomail.NewMsg()
	if err := m.From(c.fromAddress); err != nil {
		return fmt.Errorf("smtp: invalid from address %q: %w", c.fromAddress, err)
	}
	for _, to := range msg.To {
		if err := m.AddTo(to); err != nil {
			return fmt.Errorf("smtp: invalid to address %q: %w", to, err)
		}
	}
	for _, cc := range msg.CC {
		if err := m.AddCc(cc); err != nil {
			return fmt.Errorf("smtp: invalid cc address %q: %w", cc, err)
		}
	}
	for _, bcc := range msg.BCC {
		if err := m.AddBcc(bcc); err != nil {
			return fmt.Errorf("smtp: invalid bcc address %q: %w", bcc, err)
		}
	}
	m.Subject(msg.Subject)
	for name, value := range msg.Headers {
		// Preformatted: sent as-is under the given name, letting callers
		// set arbitrary custom headers (e.g. "X-Priority", "Reply-To")
		// without go-mail re-validating/re-encoding the value as it
		// would for its own predefined address/date/etc. headers.
		m.SetGenHeaderPreformatted(gomail.Header(name), value)
	}

	switch {
	case msg.BodyHTML != "" && msg.BodyText != "":
		m.SetBodyString(gomail.TypeTextHTML, msg.BodyHTML)
		m.AddAlternativeString(gomail.TypeTextPlain, msg.BodyText)
	case msg.BodyHTML != "":
		m.SetBodyString(gomail.TypeTextHTML, msg.BodyHTML)
	default:
		m.SetBodyString(gomail.TypeTextPlain, msg.BodyText)
	}

	for _, a := range msg.Attachments {
		if err := m.AttachReader(a.Filename, bytes.NewReader(a.Data)); err != nil {
			return fmt.Errorf("smtp: attach %q: %w", a.Filename, err)
		}
	}

	if err := mc.DialAndSendWithContext(ctx, m); err != nil {
		return fmt.Errorf("smtp: send: %w", err)
	}
	return nil
}
