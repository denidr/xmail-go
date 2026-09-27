package account

import (
	"context"
	"fmt"

	"xmail/internal/mailer"
)

// Protocol names as they appear in account connection configs and the
// REST/MCP "protocol" inputs.
const (
	ProtocolSMTP = "smtp"
	ProtocolIMAP = "imap"
	ProtocolPOP3 = "pop3"
)

// Protocol describes how to build one mailer protocol's implementation
// from an account's ConnectionConfig and credentials. A nil constructor
// means the protocol does not support that capability (SMTP has no
// Fetcher; POP3 has no Sender). Registered once per protocol from
// internal/app via Service.RegisterProtocol.
//
// This replaces the earlier one-field-per-protocol design (six factory
// fields + six setters + ConnTester): adding a protocol is now one
// registration, not a new field/setter/switch across Service and
// internal/app. See docs/adr/0001-unify-protocol-dispatch.md.
type Protocol struct {
	Sender  func(cfg ConnectionConfig, fromAddress, username, secret string) mailer.Sender
	Fetcher func(cfg ConnectionConfig, username, secret string) mailer.Fetcher
	// FolderLister is nil for a protocol with no folder concept (SMTP has
	// no mailbox at all; POP3 only has INBOX) — see Service.ListFolders.
	FolderLister func(cfg ConnectionConfig, username, secret string) mailer.FolderLister
}

// testConn is the TestConnection method shared by mailer.Sender and
// mailer.Fetcher, used here to test a protocol connection through
// whichever constructor a protocol registers — instead of a separate
// ConnTester factory seam for the same behaviour.
type testConn interface {
	TestConnection(ctx context.Context) error
}

// protocolFor returns the registered Protocol for name, or an error
// naming the wiring site.
func (s *Service) protocolFor(name string) (Protocol, error) {
	p, ok := s.protocols[name]
	if !ok {
		return Protocol{}, fmt.Errorf("account: protocol %q is not registered (see internal/app.wireMailer)", name)
	}
	return p, nil
}
