// Package imap implements mailer.Fetcher and mailer.Checker using
// github.com/emersion/go-imap/v2. See PLAN.md Fase 3.
package imap

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	imapv2 "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"xmail/internal/account"
	"xmail/internal/mailer"
)

// Client fetches/checks mail for one account's IMAP configuration.
type Client struct {
	cfg      account.ConnectionConfig
	username string
	password string
}

// New builds a Client for the given account IMAP config and credentials.
func New(cfg account.ConnectionConfig, username, password string) *Client {
	return &Client{cfg: cfg, username: username, password: password}
}

var (
	_ mailer.Fetcher        = (*Client)(nil)
	_ mailer.Checker        = (*Client)(nil)
	_ mailer.Marker         = (*Client)(nil)
	_ mailer.FetcherChecker = (*Client)(nil)
)

// dial connects and logs in, mapping account.TLSMode to the matching
// go-imap dial function:
//   - TLSModeTLS      -> implicit TLS (IMAPS, e.g. port 993)
//   - TLSModeStartTLS -> plaintext connect + mandatory STARTTLS
//   - TLSModeNone     -> no TLS at all (must be explicitly configured, see PRD.MD §8)
func (c *Client) dial(ctx context.Context) (*imapclient.Client, error) {
	addr := fmt.Sprintf("%s:%d", c.cfg.Host, c.cfg.Port)
	options := &imapclient.Options{}

	var cl *imapclient.Client
	var err error
	switch c.cfg.TLSMode {
	case account.TLSModeTLS:
		cl, err = imapclient.DialTLS(addr, options)
	case account.TLSModeStartTLS:
		cl, err = imapclient.DialStartTLS(addr, options)
	case account.TLSModeNone:
		cl, err = imapclient.DialInsecure(addr, options)
	default:
		return nil, fmt.Errorf("imap: unknown tls_mode %q", c.cfg.TLSMode)
	}
	if err != nil {
		return nil, fmt.Errorf("imap: connect: %w", err)
	}

	if err := cl.Login(c.username, c.password).Wait(); err != nil {
		cl.Close()
		return nil, fmt.Errorf("imap: login: %w", err)
	}
	return cl, nil
}

// TestConnection dials, logs in, and logs out again.
func (c *Client) TestConnection(ctx context.Context) error {
	cl, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer cl.Close()
	if err := cl.Logout().Wait(); err != nil {
		return fmt.Errorf("imap: logout: %w", err)
	}
	return nil
}

// Fetch selects folder and returns up to limit messages (most recent
// first), skipping offset. Uses the IMAP ENVELOPE + FLAGS + UID +
// BODYSTRUCTURE fetch items — message bodies themselves are not
// downloaded (metadata + attachment filenames only, matching PLAN.md
// §2 messages_cache schema and PRD.MD §6.3).
func (c *Client) Fetch(ctx context.Context, folder string, limit, offset int) ([]mailer.Message, error) {
	folder = mailer.DefaultFolder(folder)
	cl, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer closeClient(cl)

	selected, err := cl.Select(folder, nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("imap: select %q: %w", folder, err)
	}
	if selected.NumMessages == 0 {
		return nil, nil
	}

	// Most recent messages first: sequence numbers count from 1 (oldest)
	// to NumMessages (newest), so we walk backwards from the end.
	start, end, ok := mailer.WindowRange(int(selected.NumMessages), limit, offset)
	if !ok {
		return nil, nil
	}

	var seqSet imapv2.SeqSet
	seqSet.AddRange(uint32(start), uint32(end))

	fetchOptions := &imapv2.FetchOptions{
		Envelope: true,
		Flags:    true,
		UID:      true,
		// Extended: true requests BODYSTRUCTURE (not plain BODY) — the
		// extended data (Content-Disposition, incl. the attachment
		// filename param) is only present with this set, per go-imap/v2
		// docs. Without it, BodyStructureSinglePart.Filename() silently
		// returns "" for every attachment (caught by
		// TestIntegration_Fetch_Attachments).
		BodyStructure: &imapv2.FetchItemBodyStructure{Extended: true},
	}
	fetchCmd := cl.Fetch(seqSet, fetchOptions)
	buffers, err := fetchCmd.Collect()
	if err != nil {
		return nil, fmt.Errorf("imap: fetch: %w", err)
	}

	// Sort newest-first by UID. UIDs are assigned in arrival order and
	// only ever increase, so descending UID == newest first. The server's
	// response order for the requested sequence range isn't guaranteed,
	// so sort explicitly rather than assume it (the previous version only
	// reversed the slice while claiming to "sort by UID" — see
	// CODE_REVIEW.md round 5).
	sort.Slice(buffers, func(i, j int) bool { return buffers[i].UID > buffers[j].UID })

	out := make([]mailer.Message, 0, len(buffers))
	for _, buf := range buffers {
		out = append(out, toMessage(folder, buf))
	}
	return out, nil
}

// Check returns the number of unseen (unread) messages in folder via
// the IMAP STATUS command, without selecting the mailbox.
func (c *Client) Check(ctx context.Context, folder string) (unread int, err error) {
	folder = mailer.DefaultFolder(folder)
	cl, err := c.dial(ctx)
	if err != nil {
		return 0, err
	}
	defer closeClient(cl)

	data, err := cl.Status(folder, &imapv2.StatusOptions{NumUnseen: true}).Wait()
	if err != nil {
		return 0, fmt.Errorf("imap: status %q: %w", folder, err)
	}
	if data.NumUnseen == nil {
		return 0, nil
	}
	return int(*data.NumUnseen), nil
}

// MarkRead sets the \Seen flag on the message identified by uid in
// folder (see PRD.MD §6.3 "mark as read"). POP3 has no equivalent —
// mailer/pop3.Client intentionally does not implement mailer.Marker.
func (c *Client) MarkRead(ctx context.Context, folder, uid string) error {
	folder = mailer.DefaultFolder(folder)
	n, err := strconv.ParseUint(uid, 10, 32)
	if err != nil {
		return fmt.Errorf("imap: invalid uid %q: %w", uid, err)
	}

	cl, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer closeClient(cl)

	if _, err := cl.Select(folder, nil).Wait(); err != nil {
		return fmt.Errorf("imap: select %q: %w", folder, err)
	}

	uidSet := imapv2.UIDSetNum(imapv2.UID(n))
	storeCmd := cl.Store(uidSet, &imapv2.StoreFlags{
		Op:    imapv2.StoreFlagsAdd,
		Flags: []imapv2.Flag{imapv2.FlagSeen},
	}, nil)
	if _, err := storeCmd.Collect(); err != nil {
		return fmt.Errorf("imap: store \\Seen on uid %s: %w", uid, err)
	}
	return nil
}

// closeClient logs out (waiting for the server's OK so the session
// ends cleanly) and then closes the underlying connection, ignoring
// errors — used from defer where there is nothing more useful to do
// with a cleanup failure.
func closeClient(cl *imapclient.Client) {
	cl.Logout().Wait()
	cl.Close()
}

func toMessage(folder string, buf *imapclient.FetchMessageBuffer) mailer.Message {
	m := mailer.Message{
		UID:    fmt.Sprintf("%d", buf.UID),
		Folder: folder,
	}
	for _, f := range buf.Flags {
		if f == imapv2.FlagSeen {
			m.IsRead = true
			break
		}
	}
	if buf.Envelope != nil {
		m.Subject = buf.Envelope.Subject
		m.Date = buf.Envelope.Date.Format(time.RFC3339)
		if len(buf.Envelope.From) > 0 {
			m.From = buf.Envelope.From[0].Addr()
		}
		if len(buf.Envelope.To) > 0 {
			m.To = buf.Envelope.To[0].Addr()
		}
	}
	if buf.BodyStructure != nil {
		m.Attachments = attachmentNames(buf.BodyStructure)
	}
	return m
}

// attachmentNames walks a message's MIME body structure and collects
// the filenames of any part that looks like an attachment: it has a
// non-empty filename and either an explicit "attachment" Content-
// Disposition or is not the top-level part (a lone single-part body
// with a filename but no multipart wrapper is unusual and treated as
// an attachment too, matching common MUA behavior).
func attachmentNames(bs imapv2.BodyStructure) []string {
	var names []string
	bs.Walk(func(path []int, part imapv2.BodyStructure) bool {
		sp, ok := part.(*imapv2.BodyStructureSinglePart)
		if !ok {
			return true
		}
		name := sp.Filename()
		if name == "" {
			return true
		}
		if disp := sp.Disposition(); disp != nil && disp.Value != "" && disp.Value != "attachment" {
			// Explicit non-attachment disposition (e.g. "inline") with a
			// filename is typically an inline image referenced by the
			// HTML body, not a user-facing attachment.
			return true
		}
		names = append(names, name)
		return true
	})
	return names
}
