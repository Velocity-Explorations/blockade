package store

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // register "sqlite" driver
)

// SQLiteStore is a file-backed Store. Spent tokens persist across proxy
// restarts, preventing replay even after a crash or redeploy.
//
// Pass ":memory:" as path for an in-process SQLite database (useful in tests).
type SQLiteStore struct {
	db *sql.DB
}

// OpenSQLite opens (or creates) a SQLite database at path and initialises the
// schema. Returns an error if the file cannot be opened or the schema cannot
// be applied.
func OpenSQLite(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", path, err)
	}
	// Single-writer mode is fine for a POC proxy.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS used_tokens (
		token_key TEXT    PRIMARY KEY NOT NULL,
		used_at   INTEGER NOT NULL
	)`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS pending_addresses (
		addr       TEXT    PRIMARY KEY NOT NULL,
		sats       INTEGER NOT NULL,
		expires_at INTEGER NOT NULL
	)`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init pending schema: %w", err)
	}
	return &SQLiteStore{db: db}, nil
}

func (s *SQLiteStore) IsUsed(key string) (bool, error) {
	var count int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM used_tokens WHERE token_key = ?", key,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("query used_tokens: %w", err)
	}
	return count > 0, nil
}

func (s *SQLiteStore) MarkUsed(key string) error {
	_, err := s.db.Exec(
		"INSERT OR IGNORE INTO used_tokens (token_key, used_at) VALUES (?, ?)",
		key, time.Now().Unix(),
	)
	if err != nil {
		return fmt.Errorf("insert used_tokens: %w", err)
	}
	return nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteStore) AddPending(addr string, sats int64, expiresAt time.Time) error {
	_, err := s.db.Exec(
		"INSERT OR REPLACE INTO pending_addresses (addr, sats, expires_at) VALUES (?, ?, ?)",
		addr, sats, expiresAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("insert pending_addresses: %w", err)
	}
	return nil
}

func (s *SQLiteStore) GetPending(addr string) (PendingEntry, bool, error) {
	var sats int64
	var expiresAtUnix int64
	err := s.db.QueryRow(
		"SELECT sats, expires_at FROM pending_addresses WHERE addr = ?", addr,
	).Scan(&sats, &expiresAtUnix)
	if err == sql.ErrNoRows {
		return PendingEntry{}, false, nil
	}
	if err != nil {
		return PendingEntry{}, false, fmt.Errorf("query pending_addresses: %w", err)
	}
	return PendingEntry{Sats: sats, ExpiresAt: time.Unix(expiresAtUnix, 0)}, true, nil
}

func (s *SQLiteStore) DeletePending(addr string) error {
	_, err := s.db.Exec("DELETE FROM pending_addresses WHERE addr = ?", addr)
	if err != nil {
		return fmt.Errorf("delete pending_addresses: %w", err)
	}
	return nil
}

func (s *SQLiteStore) PruneExpiredPending(before time.Time) error {
	_, err := s.db.Exec(
		"DELETE FROM pending_addresses WHERE expires_at < ?", before.Unix(),
	)
	if err != nil {
		return fmt.Errorf("prune pending_addresses: %w", err)
	}
	return nil
}
