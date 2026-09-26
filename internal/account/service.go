package account

import (
	"context"
	"errors"
	"fmt"

	"xmail/internal/mailer"
)

// ErrValidation is wrapped by Service methods when input fails
// validation (maps to HTTP 400 in internal/api).
var ErrValidation = errors.New("account: validation failed")

// DefaultProtocol and DefaultFetchLimit are the shared fallback values
// for the "protocol"/"limit" inputs to TestConnection, FetchMessages,
// and CheckNew. Defined once here — rather than as separate literals
// in internal/api and internal/mcpserver — so REST and MCP can never
// drift on what "unspecified" means (see CODE_REVIEW.md "Duplicated
// Code": defaults were previously reimplemented in 3 places).
const (
	DefaultProtocol   = "imap"
	DefaultFetchLimit = 20
)

// connConfigForProtocol returns the account's ConnectionConfig for
// protocol ("smtp", "imap", or "pop3"), or ErrValidation if protocol
// is not one of those three. This is the one place that maps a
// protocol string to a field on Account — shared by TestConnection
// and resolveFetcher (see CODE_REVIEW.md "Primitive Obsession /
// Repeated Switches"). Selecting the matching *Factory for the
// protocol is deliberately left to each caller: TestConnection needs
// a TesterFactory, resolveFetcher needs a Fetcher-returning factory,
// and unifying those two into one generic lookup would trade a small
// amount of duplication for a less type-safe abstraction — not a
// clear win, so it was left as-is (see ARCHITECTURE.md §5).
func connConfigForProtocol(a Account, protocol string) (*ConnectionConfig, error) {
	switch protocol {
	case "smtp":
		return a.SMTP, nil
	case "imap":
		return a.IMAP, nil
	case "pop3":
		return a.POP3, nil
	default:
		return nil, fmt.Errorf("%w: unknown protocol %q", ErrValidation, protocol)
	}
}

// ConnTester is implemented by each protocol's mailer client
// (smtp.Client, imap.Client, pop3.Client) to support the
// test-connection endpoint. See PLAN.md §1 design principle: Service
// only depends on this interface, never on a concrete mailer package.
type ConnTester interface {
	TestConnection(ctx context.Context) error
}

// TesterFactory builds a ConnTester for one account's connection
// config and credentials. Wired in by internal/app once
// internal/mailer/{smtp,imap,pop3} exist (Fase 2-4); nil until then.
type TesterFactory func(cfg ConnectionConfig, username, secret string) ConnTester

// SMTPSenderFactory builds a mailer.Sender for one account's SMTP
// config, from address, and credentials. Wired in by internal/app once
// internal/mailer/smtp exists (Fase 2); nil until then.
type SMTPSenderFactory func(cfg ConnectionConfig, fromAddress, username, secret string) mailer.Sender

// IMAPFactory builds a mailer.FetcherChecker (fetch + unread count)
// for one account's IMAP config and credentials. Wired in by
// internal/app once internal/mailer/imap exists (Fase 3); nil until
// then.
type IMAPFactory func(cfg ConnectionConfig, username, secret string) mailer.FetcherChecker

// POP3Factory builds a mailer.Fetcher (fetch only — POP3 has no
// unseen-flag concept) for one account's POP3 config and credentials.
// Wired in by internal/app once internal/mailer/pop3 exists (Fase 4);
// nil until then.
type POP3Factory func(cfg ConnectionConfig, username, secret string) mailer.Fetcher

// Service holds account business logic: input validation and
// dispatching test-connection/send/fetch/check calls to the relevant
// mailer implementation.
type Service struct {
	repo *Repository

	smtpTester TesterFactory
	imapTester TesterFactory
	pop3Tester TesterFactory

	smtpSender  SMTPSenderFactory
	imapFactory IMAPFactory
	pop3Factory POP3Factory
}

// defaultFetchLimit bounds how many recent messages CheckNew fetches
// to compute a "new since last check" count (see CheckNew).
const defaultCheckFetchLimit = 50

// NewService builds a Service backed by repo.
func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

// SetSMTPTester wires the SMTP protocol tester (called from
// internal/app during Fase 2 setup).
func (s *Service) SetSMTPTester(f TesterFactory) { s.smtpTester = f }

// SetIMAPTester wires the IMAP protocol tester (called from
// internal/app during Fase 3 setup).
func (s *Service) SetIMAPTester(f TesterFactory) { s.imapTester = f }

// SetPOP3Tester wires the POP3 protocol tester (called from
// internal/app during Fase 4 setup).
func (s *Service) SetPOP3Tester(f TesterFactory) { s.pop3Tester = f }

// SetSMTPSender wires the SMTP send implementation (called from
// internal/app during Fase 2 setup).
func (s *Service) SetSMTPSender(f SMTPSenderFactory) { s.smtpSender = f }

// SetIMAPFactory wires the IMAP fetch/check implementation (called
// from internal/app during Fase 3 setup).
func (s *Service) SetIMAPFactory(f IMAPFactory) { s.imapFactory = f }

// SetPOP3Factory wires the POP3 fetch implementation (called from
// internal/app during Fase 4 setup).
func (s *Service) SetPOP3Factory(f POP3Factory) { s.pop3Factory = f }

// Validate checks the required fields for an account: name, email,
// username, at least one protocol configured, and a non-empty
// TLSMode + valid port for every protocol that is configured.
// requireSecret controls whether secret must be non-empty: true for
// Create (a password is always mandatory), true for Update only when
// the caller actually supplied a new password (secret is otherwise
// ignored) — see Update, which used to fake a non-empty placeholder
// value to route through this same check (CODE_REVIEW.md "Mysterious
// Name / magic value").
func Validate(a Account, requireSecret bool, secret string) error {
	if a.Name == "" {
		return fmt.Errorf("%w: name is required", ErrValidation)
	}
	if a.Email == "" {
		return fmt.Errorf("%w: email is required", ErrValidation)
	}
	if a.Username == "" {
		return fmt.Errorf("%w: username is required", ErrValidation)
	}
	if requireSecret && secret == "" {
		return fmt.Errorf("%w: password/secret is required", ErrValidation)
	}
	if a.SMTP == nil && a.IMAP == nil && a.POP3 == nil {
		return fmt.Errorf("%w: at least one of smtp, imap, pop3 must be configured", ErrValidation)
	}
	for proto, c := range map[string]*ConnectionConfig{"smtp": a.SMTP, "imap": a.IMAP, "pop3": a.POP3} {
		if c == nil {
			continue
		}
		if c.Host == "" {
			return fmt.Errorf("%w: %s.host is required", ErrValidation, proto)
		}
		if c.Port <= 0 || c.Port > 65535 {
			return fmt.Errorf("%w: %s.port must be between 1 and 65535", ErrValidation, proto)
		}
		switch c.TLSMode {
		case TLSModeTLS, TLSModeStartTLS, TLSModeNone:
		default:
			return fmt.Errorf("%w: %s.tls_mode must be one of tls, starttls, none (got %q)", ErrValidation, proto, c.TLSMode)
		}
		// POP3 has no STARTTLS support in mailer/pop3 (the underlying
		// go-pop3 library has none) — reject it here at save time
		// instead of letting the account through and only failing later
		// on every send/fetch/check/test-connection call.
		if proto == "pop3" && c.TLSMode == TLSModeStartTLS {
			return fmt.Errorf("%w: pop3.tls_mode \"starttls\" is not supported (use \"tls\" or \"none\")", ErrValidation)
		}
	}
	return nil
}

// Create validates and persists a new account.
func (s *Service) Create(ctx context.Context, a Account, secret string) (Account, error) {
	if err := Validate(a, true, secret); err != nil {
		return Account{}, err
	}
	return s.repo.Create(ctx, a, secret)
}

// Get returns one account by id.
func (s *Service) Get(ctx context.Context, id string) (Account, error) {
	return s.repo.Get(ctx, id)
}

// List returns all accounts.
func (s *Service) List(ctx context.Context) ([]Account, error) {
	return s.repo.List(ctx)
}

// Update validates and persists changes to an existing account.
// secret is optional — pass nil to keep the existing credential.
func (s *Service) Update(ctx context.Context, a Account, secret *string) (Account, error) {
	var secretValue string
	if secret != nil {
		secretValue = *secret
	}
	if err := Validate(a, secret != nil, secretValue); err != nil {
		return Account{}, err
	}
	return s.repo.Update(ctx, a, secret)
}

// Delete removes an account.
func (s *Service) Delete(ctx context.Context, id string) error {
	return s.repo.Delete(ctx, id)
}

// TestConnection dials+authenticates against the given protocol
// ("smtp", "imap", or "pop3") for an existing account, without
// sending/fetching anything.
func (s *Service) TestConnection(ctx context.Context, accountID string, protocol string) error {
	if protocol == "" {
		protocol = DefaultProtocol
	}
	a, err := s.repo.Get(ctx, accountID)
	if err != nil {
		return err
	}
	cfg, err := connConfigForProtocol(a, protocol)
	if err != nil {
		return err
	}
	if cfg == nil {
		return fmt.Errorf("%w: account has no %s configuration", ErrValidation, protocol)
	}

	var factory TesterFactory
	switch protocol {
	case "smtp":
		factory = s.smtpTester
	case "imap":
		factory = s.imapTester
	case "pop3":
		factory = s.pop3Tester
	}
	if factory == nil {
		return fmt.Errorf("account: %s tester not wired up yet (see PLAN.md Fase 2-4)", protocol)
	}

	secret, err := s.repo.Secret(ctx, accountID)
	if err != nil {
		return err
	}
	return factory(*cfg, a.Username, secret).TestConnection(ctx)
}

// Send delivers msg via the account's SMTP configuration.
func (s *Service) Send(ctx context.Context, accountID string, msg mailer.OutgoingMessage) error {
	a, err := s.repo.Get(ctx, accountID)
	if err != nil {
		return err
	}
	if a.SMTP == nil {
		return fmt.Errorf("%w: account has no smtp configuration", ErrValidation)
	}
	if s.smtpSender == nil {
		return errors.New("account: smtp sender not wired up yet (see PLAN.md Fase 2)")
	}
	secret, err := s.repo.Secret(ctx, accountID)
	if err != nil {
		return err
	}
	return s.smtpSender(*a.SMTP, a.Email, a.Username, secret).Send(ctx, msg)
}

// resolveFetcher returns the mailer.Fetcher for protocol ("imap" or
// "pop3", defaulting to DefaultProtocol) on the given account, plus
// its decrypted secret.
// resolveFetcher also returns the resolved protocol (never "") so
// callers that need it for a cache key (FetchMessages, CheckNew,
// MarkRead) use exactly this value instead of re-implementing the
// same "" -> DefaultProtocol default themselves — a prior version had
// each caller re-guard protocol=="" independently, which (a) was the
// exact "Duplicated Code" CODE_REVIEW.md flagged and (b) had already
// caused one real bug where a caller's un-defaulted copy of protocol
// disagreed with the fetcher this function actually built (see
// PLAN.md §10 for that incident).
func (s *Service) resolveFetcher(ctx context.Context, accountID, protocol string) (fetcher mailer.Fetcher, resolvedProtocol string, err error) {
	if protocol == "" {
		protocol = DefaultProtocol
	}
	a, err := s.repo.Get(ctx, accountID)
	if err != nil {
		return nil, "", err
	}
	cfg, err := connConfigForProtocol(a, protocol)
	if err != nil {
		return nil, "", err
	}
	if protocol == "smtp" {
		return nil, "", fmt.Errorf("%w: protocol %q is not valid for fetch/check (must be imap or pop3)", ErrValidation, protocol)
	}
	if cfg == nil {
		return nil, "", fmt.Errorf("%w: account has no %s configuration", ErrValidation, protocol)
	}

	secret, err := s.repo.Secret(ctx, accountID)
	if err != nil {
		return nil, "", err
	}

	switch protocol {
	case "imap":
		if s.imapFactory == nil {
			return nil, "", errors.New("account: imap not wired up yet (see PLAN.md Fase 3)")
		}
		return s.imapFactory(*cfg, a.Username, secret), protocol, nil
	case "pop3":
		if s.pop3Factory == nil {
			return nil, "", errors.New("account: pop3 not wired up yet (see PLAN.md Fase 4)")
		}
		return s.pop3Factory(*cfg, a.Username, secret), protocol, nil
	}
	return nil, "", fmt.Errorf("%w: unreachable protocol %q", ErrValidation, protocol)
}

// FetchMessages returns up to limit messages (skipping offset) for
// protocol ("imap" or "pop3") + folder on the given account.
//
// Unless refresh is true, it serves from messages_cache first (see
// Repository.ListMessages) and only dials the mail server when the
// cache has nothing for this exact (account, protocol, folder) yet —
// this is what makes "fetch berikutnya lebih cepat" (PRD.MD §6.3)
// literally true: the first call for a mailbox pays the network cost
// and populates the cache, later calls don't. Pass refresh=true (or
// call CheckNew, which always dials) to force a live re-fetch that
// also refreshes the cache — e.g. after the caller knows new mail has
// arrived.
func (s *Service) FetchMessages(ctx context.Context, accountID, protocol, folder string, limit, offset int, refresh bool) ([]mailer.Message, error) {
	folder = mailer.DefaultFolder(folder)
	fetcher, protocol, err := s.resolveFetcher(ctx, accountID, protocol)
	if err != nil {
		return nil, err
	}
	// protocol is now resolveFetcher's resolved value (never ""), used
	// below for the cache key — see resolveFetcher's doc comment.

	if !refresh {
		cached, err := s.repo.ListMessages(ctx, accountID, protocol, folder, limit, offset)
		if err != nil {
			return nil, err
		}
		if len(cached) > 0 {
			return cached, nil
		}
	}

	msgs, err := fetcher.Fetch(ctx, folder, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("account: fetch: %w", err)
	}
	if err := s.repo.UpsertMessages(ctx, accountID, protocol, msgs); err != nil {
		return nil, err
	}
	return msgs, nil
}

// CheckNew reports the current unread count (protocol-reported, IMAP
// only — see mailer.Checker) and how many messages among the most
// recent defaultCheckFetchLimit are not already in messages_cache
// ("new since last check"), then updates the cache.
func (s *Service) CheckNew(ctx context.Context, accountID, protocol, folder string) (unread, newCount int, err error) {
	folder = mailer.DefaultFolder(folder)
	fetcher, protocol, err := s.resolveFetcher(ctx, accountID, protocol)
	if err != nil {
		return 0, 0, err
	}

	if checker, ok := fetcher.(mailer.Checker); ok {
		unread, err = checker.Check(ctx, folder)
		if err != nil {
			return 0, 0, fmt.Errorf("account: check: %w", err)
		}
	}

	existing, err := s.repo.ExistingUIDs(ctx, accountID, protocol, folder)
	if err != nil {
		return 0, 0, err
	}
	msgs, err := fetcher.Fetch(ctx, folder, defaultCheckFetchLimit, 0)
	if err != nil {
		return 0, 0, fmt.Errorf("account: fetch for check: %w", err)
	}
	for _, m := range msgs {
		if !existing[m.UID] {
			newCount++
		}
	}
	if err := s.repo.UpsertMessages(ctx, accountID, protocol, msgs); err != nil {
		return 0, 0, err
	}
	return unread, newCount, nil
}

// MarkRead marks the message identified by uid in folder as read (see
// PRD.MD §6.3 "mark as read"). Only protocols whose Fetcher also
// implements mailer.Marker support this — currently IMAP only, via the
// \Seen flag; POP3 has no per-message flag concept. Also updates
// messages_cache so a subsequent cached FetchMessages call reflects
// the new read state without a live re-fetch.
func (s *Service) MarkRead(ctx context.Context, accountID, protocol, folder, uid string) error {
	folder = mailer.DefaultFolder(folder)
	fetcher, protocol, err := s.resolveFetcher(ctx, accountID, protocol)
	if err != nil {
		return err
	}
	marker, ok := fetcher.(mailer.Marker)
	if !ok {
		return fmt.Errorf("%w: protocol %q does not support marking messages as read", ErrValidation, protocol)
	}
	if err := marker.MarkRead(ctx, folder, uid); err != nil {
		return fmt.Errorf("account: mark read: %w", err)
	}
	return s.repo.MarkMessageRead(ctx, accountID, protocol, folder, uid)
}
