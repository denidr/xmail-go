package storage

import (
	"path/filepath"
	"testing"
)

func TestOpen_AppliesMigrationsAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "xmail.db")

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()

	for _, table := range []string{"accounts", "credentials", "messages_cache", "api_keys"} {
		var name string
		if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name); err != nil {
			t.Errorf("table %s not created: %v", table, err)
		}
	}

	var firstOpenCount int
	if err := db.QueryRow(`SELECT COUNT(1) FROM schema_migrations`).Scan(&firstOpenCount); err != nil {
		t.Fatalf("count schema_migrations after first Open: %v", err)
	}
	if firstOpenCount == 0 {
		t.Fatal("schema_migrations is empty after first Open() — no migration was recorded")
	}

	// Re-opening (re-running migrate against the same DB) must not error
	// and must not re-apply migrations — the count must stay exactly the
	// same as after the first Open(), whatever the current number of
	// embedded migration files happens to be (not hardcoded, since that
	// count changes as migrations/*.sql grows).
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open() error = %v", err)
	}
	defer db2.Close()

	var secondOpenCount int
	if err := db2.QueryRow(`SELECT COUNT(1) FROM schema_migrations`).Scan(&secondOpenCount); err != nil {
		t.Fatalf("count schema_migrations after second Open: %v", err)
	}
	if secondOpenCount != firstOpenCount {
		t.Errorf("schema_migrations count = %d after reopen, want %d (migration re-applied?)", secondOpenCount, firstOpenCount)
	}
}

func TestOpen_ForeignKeysEnforced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "xmail.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`INSERT INTO credentials (account_id, encrypted_secret, nonce) VALUES ('does-not-exist', x'00', x'00')`)
	if err == nil {
		t.Error("insert with dangling account_id succeeded, want foreign key violation")
	}
}
