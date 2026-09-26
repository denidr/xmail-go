// Package storage manages the SQLite connection and schema migrations
// for xmail. See PLAN.md §2 (schema) and Fase 1.
package storage

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Open opens the SQLite database at path (creating it if needed),
// enables foreign key enforcement (off by default in SQLite), and
// applies any pending migrations from the embedded migrations
// directory, tracked in a schema_migrations table so re-running Open
// against an already-migrated database is a no-op.
func Open(path string) (*sql.DB, error) {
	// foreign_keys is per-connection in SQLite (off by default); busy_timeout
	// makes a second process (service + CLI, or two replicas on one volume)
	// wait for a writer instead of failing with SQLITE_BUSY.
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("storage: open %s: %w", path, err)
	}
	// SQLite only supports one writer at a time; a single connection
	// avoids "database is locked" errors under concurrent requests.
	db.SetMaxOpenConns(1)

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("storage: migrate: %w", err)
	}
	return db, nil
}

func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		filename TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		var already int
		if err := db.QueryRow(`SELECT COUNT(1) FROM schema_migrations WHERE filename = ?`, name).Scan(&already); err != nil {
			return fmt.Errorf("check migration %s: %w", name, err)
		}
		if already > 0 {
			continue
		}

		sqlBytes, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}

		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin tx for %s: %w", name, err)
		}
		if _, err := tx.Exec(string(sqlBytes)); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (filename) VALUES (?)`, name); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}
	}
	return nil
}
