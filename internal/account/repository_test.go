package account

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"xmail/internal/storage"
)

func newTestRepo(t *testing.T) *Repository {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "xmail.db"))
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewRepository(db, bytes.Repeat([]byte{0x11}, 32))
}

func sampleAccount() Account {
	return Account{
		Name:     "Test Account",
		Email:    "test@example.com",
		Username: "test@example.com",
		SMTP:     &ConnectionConfig{Host: "smtp.example.com", Port: 587, TLSMode: TLSModeStartTLS},
		IMAP:     &ConnectionConfig{Host: "imap.example.com", Port: 993, TLSMode: TLSModeTLS},
	}
}

func TestRepository_CreateGetList(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepo(t)

	created, err := repo.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.ID == "" {
		t.Fatal("Create() did not assign an ID")
	}

	got, err := repo.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Email != created.Email || got.SMTP.Host != "smtp.example.com" || got.IMAP.TLSMode != TLSModeTLS {
		t.Errorf("Get() = %+v, mismatched fields", got)
	}
	if got.POP3 != nil {
		t.Errorf("POP3 = %+v, want nil (not configured)", got.POP3)
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List() len = %d, want 1", len(list))
	}
}

func TestRepository_CredentialStoredEncrypted(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepo(t)

	created, err := repo.Create(ctx, sampleAccount(), "hunter2")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	var raw []byte
	if err := repo.db.QueryRow(`SELECT encrypted_secret FROM credentials WHERE account_id = ?`, created.ID).Scan(&raw); err != nil {
		t.Fatalf("read raw credential: %v", err)
	}
	if bytes.Contains(raw, []byte("hunter2")) {
		t.Error("encrypted_secret column contains the plaintext password")
	}

	secret, err := repo.Secret(ctx, created.ID)
	if err != nil {
		t.Fatalf("Secret() error = %v", err)
	}
	if secret != "hunter2" {
		t.Errorf("Secret() = %q, want %q", secret, "hunter2")
	}
}

func TestRepository_UpdateWithAndWithoutSecret(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepo(t)

	created, err := repo.Create(ctx, sampleAccount(), "original-pw")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	created.Name = "Renamed"
	if _, err := repo.Update(ctx, created, nil); err != nil {
		t.Fatalf("Update() (no secret change) error = %v", err)
	}
	secret, err := repo.Secret(ctx, created.ID)
	if err != nil {
		t.Fatalf("Secret() error = %v", err)
	}
	if secret != "original-pw" {
		t.Errorf("Secret() after no-op secret update = %q, want unchanged %q", secret, "original-pw")
	}

	newSecret := "rotated-pw"
	updated, err := repo.Update(ctx, created, &newSecret)
	if err != nil {
		t.Fatalf("Update() (with secret change) error = %v", err)
	}
	if updated.Name != "Renamed" {
		t.Errorf("Name = %q, want %q", updated.Name, "Renamed")
	}
	secret, err = repo.Secret(ctx, created.ID)
	if err != nil {
		t.Fatalf("Secret() error = %v", err)
	}
	if secret != "rotated-pw" {
		t.Errorf("Secret() after rotation = %q, want %q", secret, "rotated-pw")
	}
}

func TestRepository_DeleteCascades(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepo(t)

	created, err := repo.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if err := repo.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	if _, err := repo.Get(ctx, created.ID); err != ErrNotFound {
		t.Errorf("Get() after delete = %v, want ErrNotFound", err)
	}

	var count int
	if err := repo.db.QueryRow(`SELECT COUNT(1) FROM credentials WHERE account_id = ?`, created.ID).Scan(&count); err != nil {
		t.Fatalf("count credentials: %v", err)
	}
	if count != 0 {
		t.Errorf("credentials row not cascade-deleted, count = %d", count)
	}
}

func TestRepository_GetNotFound(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepo(t)

	if _, err := repo.Get(ctx, "does-not-exist"); err != ErrNotFound {
		t.Errorf("Get() error = %v, want ErrNotFound", err)
	}
}

func TestRepository_DeleteNotFound(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepo(t)

	if err := repo.Delete(ctx, "does-not-exist"); err != ErrNotFound {
		t.Errorf("Delete() error = %v, want ErrNotFound", err)
	}
}
