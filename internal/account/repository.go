package account

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"xmail/internal/cryptox"
)

// ErrNotFound is returned by Get/Secret/Update/Delete when no account
// exists with the given id.
var ErrNotFound = errors.New("account: not found")

// Repository persists Account records (and their encrypted credentials)
// in SQLite.
type Repository struct {
	db            *sql.DB
	encryptionKey []byte
}

// NewRepository builds a Repository backed by db, encrypting/decrypting
// credentials with encryptionKey (must be 32 bytes, see internal/config).
func NewRepository(db *sql.DB, encryptionKey []byte) *Repository {
	return &Repository{db: db, encryptionKey: encryptionKey}
}

// Create inserts a new account and its encrypted credential. If a.ID is
// empty, a new UUID is generated.
func (r *Repository) Create(ctx context.Context, a Account, secret string) (Account, error) {
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	a.CreatedAt = now
	a.UpdatedAt = now

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Account{}, fmt.Errorf("account: begin tx: %w", err)
	}
	defer tx.Rollback()

	if err := insertAccount(ctx, tx, a); err != nil {
		return Account{}, err
	}
	if err := r.upsertSecret(ctx, tx, a.ID, secret); err != nil {
		return Account{}, err
	}
	if err := tx.Commit(); err != nil {
		return Account{}, fmt.Errorf("account: commit: %w", err)
	}
	return a, nil
}

func insertAccount(ctx context.Context, tx *sql.Tx, a Account) error {
	smtpHost, smtpPort, smtpTLS := connFields(a.SMTP)
	imapHost, imapPort, imapTLS := connFields(a.IMAP)
	pop3Host, pop3Port, pop3TLS := connFields(a.POP3)
	_, err := tx.ExecContext(ctx, `
		INSERT INTO accounts (
			id, name, email,
			smtp_host, smtp_port, smtp_tls_mode,
			imap_host, imap_port, imap_tls_mode,
			pop3_host, pop3_port, pop3_tls_mode,
			username, created_at, updated_at
		) VALUES (?,?,?, ?,?,?, ?,?,?, ?,?,?, ?,?,?)`,
		a.ID, a.Name, a.Email,
		smtpHost, smtpPort, smtpTLS,
		imapHost, imapPort, imapTLS,
		pop3Host, pop3Port, pop3TLS,
		a.Username, a.CreatedAt.Format(time.RFC3339), a.UpdatedAt.Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("account: insert: %w", err)
	}
	return nil
}

func (r *Repository) upsertSecret(ctx context.Context, tx *sql.Tx, accountID, secret string) error {
	ciphertext, nonce, err := cryptox.Encrypt(r.encryptionKey, []byte(secret))
	if err != nil {
		return fmt.Errorf("account: encrypt secret: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO credentials (account_id, encrypted_secret, nonce) VALUES (?, ?, ?)
		ON CONFLICT(account_id) DO UPDATE SET encrypted_secret = excluded.encrypted_secret, nonce = excluded.nonce`,
		accountID, ciphertext, nonce)
	if err != nil {
		return fmt.Errorf("account: store secret: %w", err)
	}
	return nil
}

// Get returns the account with the given id (without its credential).
func (r *Repository) Get(ctx context.Context, id string) (Account, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, name, email,
			smtp_host, smtp_port, smtp_tls_mode,
			imap_host, imap_port, imap_tls_mode,
			pop3_host, pop3_port, pop3_tls_mode,
			username, created_at, updated_at
		FROM accounts WHERE id = ?`, id)
	a, err := scanAccount(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	if err != nil {
		return Account{}, fmt.Errorf("account: get: %w", err)
	}
	return a, nil
}

// List returns all accounts (without credentials), ordered by name.
func (r *Repository) List(ctx context.Context) ([]Account, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, email,
			smtp_host, smtp_port, smtp_tls_mode,
			imap_host, imap_port, imap_tls_mode,
			pop3_host, pop3_port, pop3_tls_mode,
			username, created_at, updated_at
		FROM accounts ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("account: list: %w", err)
	}
	defer rows.Close()

	var out []Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("account: list scan: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("account: list rows: %w", err)
	}
	return out, nil
}

// Update replaces the mutable fields of an existing account. If secret
// is non-nil, the stored credential is re-encrypted and replaced.
func (r *Repository) Update(ctx context.Context, a Account, secret *string) (Account, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Account{}, fmt.Errorf("account: begin tx: %w", err)
	}
	defer tx.Rollback()

	a.UpdatedAt = time.Now().UTC()
	smtpHost, smtpPort, smtpTLS := connFields(a.SMTP)
	imapHost, imapPort, imapTLS := connFields(a.IMAP)
	pop3Host, pop3Port, pop3TLS := connFields(a.POP3)
	res, err := tx.ExecContext(ctx, `
		UPDATE accounts SET
			name = ?, email = ?,
			smtp_host = ?, smtp_port = ?, smtp_tls_mode = ?,
			imap_host = ?, imap_port = ?, imap_tls_mode = ?,
			pop3_host = ?, pop3_port = ?, pop3_tls_mode = ?,
			username = ?, updated_at = ?
		WHERE id = ?`,
		a.Name, a.Email,
		smtpHost, smtpPort, smtpTLS,
		imapHost, imapPort, imapTLS,
		pop3Host, pop3Port, pop3TLS,
		a.Username, a.UpdatedAt.Format(time.RFC3339), a.ID,
	)
	if err != nil {
		return Account{}, fmt.Errorf("account: update: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Account{}, ErrNotFound
	}

	if secret != nil {
		if err := r.upsertSecret(ctx, tx, a.ID, *secret); err != nil {
			return Account{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Account{}, fmt.Errorf("account: commit: %w", err)
	}
	return r.Get(ctx, a.ID)
}

// Delete removes an account and (via ON DELETE CASCADE) its credential
// and cached messages.
func (r *Repository) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM accounts WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("account: delete: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Secret returns the decrypted credential for an account, used
// internally by mailer clients — never exposed via API/MCP responses.
func (r *Repository) Secret(ctx context.Context, id string) (string, error) {
	var ciphertext, nonce []byte
	err := r.db.QueryRowContext(ctx, `SELECT encrypted_secret, nonce FROM credentials WHERE account_id = ?`, id).
		Scan(&ciphertext, &nonce)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("account: read secret: %w", err)
	}
	plain, err := cryptox.Decrypt(r.encryptionKey, ciphertext, nonce)
	if err != nil {
		return "", fmt.Errorf("account: decrypt secret: %w", err)
	}
	return string(plain), nil
}

// rowScanner abstracts *sql.Row and *sql.Rows for scanAccount.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanAccount(row rowScanner) (Account, error) {
	var a Account
	var smtpHost, smtpTLS, imapHost, imapTLS, pop3Host, pop3TLS sql.NullString
	var smtpPort, imapPort, pop3Port sql.NullInt64
	var createdAt, updatedAt string

	err := row.Scan(
		&a.ID, &a.Name, &a.Email,
		&smtpHost, &smtpPort, &smtpTLS,
		&imapHost, &imapPort, &imapTLS,
		&pop3Host, &pop3Port, &pop3TLS,
		&a.Username, &createdAt, &updatedAt,
	)
	if err != nil {
		return Account{}, err
	}

	a.SMTP = toConnConfig(smtpHost, smtpPort, smtpTLS)
	a.IMAP = toConnConfig(imapHost, imapPort, imapTLS)
	a.POP3 = toConnConfig(pop3Host, pop3Port, pop3TLS)
	a.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	a.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return a, nil
}

func toConnConfig(host sql.NullString, port sql.NullInt64, tlsMode sql.NullString) *ConnectionConfig {
	if !host.Valid || host.String == "" {
		return nil
	}
	return &ConnectionConfig{
		Host:    host.String,
		Port:    int(port.Int64),
		TLSMode: TLSMode(tlsMode.String),
	}
}

// connFields returns c's host/port/tls_mode as SQL-bindable values,
// or three nils if c is unconfigured — used for all three protocol
// columns (smtp_*/imap_*/pop3_*) so this nil-guard is written once
// (see CODE_REVIEW.md "Duplicated Code (minor)": previously three
// near-identical single-field functions).
func connFields(c *ConnectionConfig) (host, port, tlsMode any) {
	if c == nil {
		return nil, nil, nil
	}
	return c.Host, c.Port, string(c.TLSMode)
}
