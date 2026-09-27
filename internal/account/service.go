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
	DefaultProtocol   = ProtocolIMAP
	DefaultFetchLimit = 20
)

// connConfigForProtocol returns the account's ConnectionConfig for
// protocol ("smtp", "imap", or "pop3"), or ErrValidation if protocol
// is not one of those three. This is the one place that maps a
// protocol string to a field on Account — the matching implementation
// constructors come from the registered Protocol table
// (Service.protocols), so neither this mapping nor the constructor
// lookup is re-implemented per operation.
func connConfigForProtocol(a Account, protocol string) (*ConnectionConfig, error) {
	switch protocol {
	case ProtocolSMTP:
		return a.SMTP, nil
	case ProtocolIMAP:
		return a.IMAP, nil
	case ProtocolPOP3:
		return a.POP3, nil
	default:
		return nil, fmt.Errorf("%w: unknown protocol %q", ErrValidation, protocol)
	}
}

// canonicalFolder resolves the folder to use for protocol: empty means
// the protocol's default folder, and POP3 — which has no folder
// concept — is always its only folder, INBOX, whatever the caller
// passed. Resolving it once here keeps the Message cache key
// (CacheKey.Folder) and the folder recorded per message in agreement
// for a folder-less protocol.
func canonicalFolder(protocol, folder string) string {
	if protocol == ProtocolPOP3 {
		folder = ""
	}
	return mailer.DefaultFolder(folder)
}

// Service holds account business logic: input validation and
// dispatching test-connection/send/fetch/check calls to the registered
// Protocol implementations. Message-cache reads/writes go through
// cache (MessageCache), not repo — Repository owns only accounts and
// credentials.
type Service struct {
	repo      *Repository
	cache     *MessageCache
	protocols map[string]Protocol
}

// defaultCheckFetchLimit bounds how many recent messages CheckNew
// fetches to compute a "new since last check" count (see CheckNew).
const defaultCheckFetchLimit = 50

// NewService builds a Service backed by repo, sharing repo's database
// for the Message cache.
func NewService(repo *Repository) *Service {
	return &Service{
		repo:      repo,
		cache:     NewMessageCache(repo.db),
		protocols: make(map[string]Protocol),
	}
}

// RegisterProtocol wires a mailer protocol's constructors, keyed by its
// Protocol name (called from internal/app once the concrete
// internal/mailer/{smtp,imap,pop3} packages exist). Until a protocol is
// registered, operations on it return a clear "not registered" error
// rather than a silent no-op.
func (s *Service) RegisterProtocol(name string, p Protocol) {
	s.protocols[name] = p
}

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
	// Iterated in a fixed smtp/imap/pop3 order (not a map) so the error
	// returned for an account with more than one invalid protocol is
	// deterministic — a map literal iterates in random order, which made
	// "which protocol's error surfaces" vary run to run.
	for _, pc := range []struct {
		proto string
		cfg   *ConnectionConfig
	}{{ProtocolSMTP, a.SMTP}, {ProtocolIMAP, a.IMAP}, {ProtocolPOP3, a.POP3}} {
		proto, c := pc.proto, pc.cfg
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
		if proto == ProtocolPOP3 && c.TLSMode == TLSModeStartTLS {
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
// sending/fetching anything. It builds whichever implementation the
// protocol registers (Sender for SMTP, Fetcher for IMAP/POP3) and calls
// its TestConnection.
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
	p, err := s.protocolFor(protocol)
	if err != nil {
		return err
	}

	secret, err := s.repo.Secret(ctx, accountID)
	if err != nil {
		return err
	}

	var tester testConn
	switch {
	case p.Sender != nil:
		tester = p.Sender(*cfg, "", a.Username, secret)
	case p.Fetcher != nil:
		tester = p.Fetcher(*cfg, a.Username, secret)
	default:
		return fmt.Errorf("account: protocol %q registers no implementation", protocol)
	}
	return tester.TestConnection(ctx)
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
	p, err := s.protocolFor(ProtocolSMTP)
	if err != nil {
		return err
	}
	if p.Sender == nil {
		return fmt.Errorf("account: protocol %q registers no sender", ProtocolSMTP)
	}
	secret, err := s.repo.Secret(ctx, accountID)
	if err != nil {
		return err
	}
	return p.Sender(*a.SMTP, a.Email, a.Username, secret).Send(ctx, msg)
}

// resolveFetcher returns the mailer.Fetcher for protocol ("imap" or
// "pop3", defaulting to DefaultProtocol) on the given account, plus the
// resolved protocol (never "") so callers that need it for a cache key
// (FetchMessages, CheckNew, MarkRead) use exactly this value instead of
// re-implementing the same "" -> DefaultProtocol default themselves — a
// prior version had each caller re-guard protocol=="" independently,
// which both duplicated logic and had already caused one real bug where
// a caller's un-defaulted copy of protocol disagreed with the fetcher
// this function actually built.
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
	if protocol == ProtocolSMTP {
		return nil, "", fmt.Errorf("%w: protocol %q is not valid for fetch/check (must be imap or pop3)", ErrValidation, protocol)
	}
	if cfg == nil {
		return nil, "", fmt.Errorf("%w: account has no %s configuration", ErrValidation, protocol)
	}
	p, err := s.protocolFor(protocol)
	if err != nil {
		return nil, "", err
	}
	if p.Fetcher == nil {
		return nil, "", fmt.Errorf("account: protocol %q registers no fetcher", protocol)
	}

	secret, err := s.repo.Secret(ctx, accountID)
	if err != nil {
		return nil, "", err
	}
	return p.Fetcher(*cfg, a.Username, secret), protocol, nil
}

// ListFolders returns the mailboxes available for an account's protocol.
// Only protocols whose registration carries a FolderLister support this
// — currently IMAP; POP3 (INBOX only) and SMTP (no mailbox at all)
// return ErrValidation, consistent with how MarkRead rejects POP3.
func (s *Service) ListFolders(ctx context.Context, accountID, protocol string) ([]mailer.Folder, error) {
	if protocol == "" {
		protocol = DefaultProtocol
	}
	a, err := s.repo.Get(ctx, accountID)
	if err != nil {
		return nil, err
	}
	cfg, err := connConfigForProtocol(a, protocol)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, fmt.Errorf("%w: account has no %s configuration", ErrValidation, protocol)
	}
	p, err := s.protocolFor(protocol)
	if err != nil {
		return nil, err
	}
	if p.FolderLister == nil {
		return nil, fmt.Errorf("%w: protocol %q has no folders", ErrValidation, protocol)
	}
	secret, err := s.repo.Secret(ctx, accountID)
	if err != nil {
		return nil, err
	}
	return p.FolderLister(*cfg, a.Username, secret).ListFolders(ctx)
}

// domainError maps a protocol-level error to a caller error when the
// failure was actually the caller's input. The one such case is a
// mailbox that does not exist (mailer.ErrFolderNotFound) -> ErrValidation
// (HTTP 400) instead of a server error (500). Everything else — dial
// failures, auth errors, timeouts, other IMAP errors — passes through
// unchanged, so a real server problem is never disguised as bad input.
// Keeping the mapping here (rather than in internal/api or
// internal/mcpserver) means both skins classify alike.
func domainError(err error) error {
	if errors.Is(err, mailer.ErrFolderNotFound) {
		return fmt.Errorf("%w: %s", ErrValidation, err)
	}
	return err
}

// FetchMessages returns up to limit messages (skipping offset) for
// protocol ("imap" or "pop3") + folder on the given account. limit <= 0
// falls back to DefaultFetchLimit (single source of truth for both the
// REST and MCP adapters).
//
// Unless refresh is true, it serves from the Message cache (see
// MessageCache.Get) — but only when the cache actually covers the
// requested window. It dials the mail server when the cache can't
// answer: nothing cached yet, the window extends past what's been
// cached, or the mailbox isn't known to be fully cached. This is what
// makes "the next fetch is faster" literally true without silently
// truncating a larger request to an older, smaller
// cached page (a real bug — see CODE_REVIEW.md round 5). Pass
// refresh=true (or call CheckNew, which always dials) to force a live
// re-fetch that also refreshes the cache.
func (s *Service) FetchMessages(ctx context.Context, accountID, protocol, folder string, limit, offset int, refresh bool) ([]mailer.Message, error) {
	if limit <= 0 {
		limit = DefaultFetchLimit
	}
	fetcher, protocol, err := s.resolveFetcher(ctx, accountID, protocol)
	if err != nil {
		return nil, err
	}
	// protocol is now resolveFetcher's resolved value (never ""), used
	// below for the cache key — see resolveFetcher's doc comment.
	folder = canonicalFolder(protocol, folder)
	key := CacheKey{AccountID: accountID, Protocol: protocol, Folder: folder}
	window := Window{Limit: limit, Offset: offset}

	if !refresh {
		cached, served, err := s.cache.Get(ctx, key, window)
		if err != nil {
			return nil, err
		}
		if served {
			return cached, nil
		}
	}

	msgs, err := fetcher.Fetch(ctx, folder, limit, offset)
	if err != nil {
		return nil, domainError(fmt.Errorf("account: fetch: %w", err))
	}
	if err := s.cache.Upsert(ctx, key, msgs, offset); err != nil {
		return nil, err
	}
	if err := s.cache.Record(ctx, key, window, len(msgs)); err != nil {
		return nil, err
	}
	return msgs, nil
}

// CheckNew reports the current unread count (protocol-reported, IMAP
// only — see mailer.Checker) and how many messages among the most
// recent defaultCheckFetchLimit are not already in the Message cache
// ("new since last check"), then updates the cache.
func (s *Service) CheckNew(ctx context.Context, accountID, protocol, folder string) (unread, newCount int, err error) {
	fetcher, protocol, err := s.resolveFetcher(ctx, accountID, protocol)
	if err != nil {
		return 0, 0, err
	}
	folder = canonicalFolder(protocol, folder)

	if checker, ok := fetcher.(mailer.Checker); ok {
		unread, err = checker.Check(ctx, folder)
		if err != nil {
			return 0, 0, domainError(fmt.Errorf("account: check: %w", err))
		}
	}

	key := CacheKey{AccountID: accountID, Protocol: protocol, Folder: folder}
	existing, err := s.cache.ExistingUIDs(ctx, key)
	if err != nil {
		return 0, 0, err
	}
	msgs, err := fetcher.Fetch(ctx, folder, defaultCheckFetchLimit, 0)
	if err != nil {
		return 0, 0, domainError(fmt.Errorf("account: fetch for check: %w", err))
	}
	for _, m := range msgs {
		if !existing[m.UID] {
			newCount++
		}
	}
	if err := s.cache.Upsert(ctx, key, msgs, 0); err != nil {
		return 0, 0, err
	}
	// Record this fetch's coverage too, so a subsequent cached
	// FetchMessages whose window fits inside defaultCheckFetchLimit can
	// answer without dialing.
	if err := s.cache.Record(ctx, key, Window{Limit: defaultCheckFetchLimit}, len(msgs)); err != nil {
		return 0, 0, err
	}
	return unread, newCount, nil
}

// MarkRead marks the message identified by uid in folder as read. Only
// protocols whose Fetcher also implements mailer.Marker support this —
// currently IMAP only, via the \Seen flag; POP3 has no per-message flag
// concept. Also updates the Message cache so a subsequent cached
// FetchMessages call reflects the new read state without a live
// re-fetch.
func (s *Service) MarkRead(ctx context.Context, accountID, protocol, folder, uid string) error {
	fetcher, protocol, err := s.resolveFetcher(ctx, accountID, protocol)
	if err != nil {
		return err
	}
	folder = canonicalFolder(protocol, folder)
	marker, ok := fetcher.(mailer.Marker)
	if !ok {
		return fmt.Errorf("%w: protocol %q does not support marking messages as read", ErrValidation, protocol)
	}
	if err := marker.MarkRead(ctx, folder, uid); err != nil {
		return domainError(fmt.Errorf("account: mark read: %w", err))
	}
	return s.cache.MarkRead(ctx, CacheKey{AccountID: accountID, Protocol: protocol, Folder: folder}, uid)
}
