// Package cache is a small sqlite key/value store for TypeSafe answers.
package cache

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Store is an open cache.
type Store struct {
	db   *sql.DB
	Path string
}

// DefaultPath is ~/.cache/jevpsql/cache.db.
func DefaultPath() string {
	if p := os.Getenv("JEVPSQL_CACHE"); p != "" {
		return p
	}
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = filepath.Join(os.Getenv("HOME"), ".cache")
	}
	return filepath.Join(base, "jevpsql", "cache.db")
}

// Open creates the file (mode 0600) and schema if needed.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	f.Close()
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)&_pragma=synchronous(normal)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS cache (k TEXT PRIMARY KEY, v JSON NOT NULL, created_at TEXT NOT NULL)`); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, Path: path}, nil
}

// Close releases the database.
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	return s.db.Close()
}

// Key builds the cache key: sha256(model + kind + question + options + canonical_json).
func Key(model, kind, question string, options []string, canonical []byte) string {
	h := sha256.New()
	h.Write([]byte(model))
	h.Write([]byte{0})
	h.Write([]byte(kind))
	h.Write([]byte{0})
	h.Write([]byte(question))
	h.Write([]byte{0})
	h.Write([]byte(strings.Join(options, "\x01")))
	h.Write([]byte{0})
	h.Write(canonical)
	return hex.EncodeToString(h.Sum(nil))
}

// Get returns the stored JSON for key.
func (s *Store) Get(ctx context.Context, key string) (json.RawMessage, bool, error) {
	if s == nil {
		return nil, false, nil
	}
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT v FROM cache WHERE k = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return json.RawMessage(v), true, nil
}

// GetMany looks up several keys in one transaction.
func (s *Store) GetMany(ctx context.Context, keys []string) (map[string]json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	if s == nil || len(keys) == 0 {
		return out, nil
	}
	stmt, err := s.db.PrepareContext(ctx, `SELECT v FROM cache WHERE k = ?`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	for _, k := range keys {
		var v string
		err := stmt.QueryRowContext(ctx, k).Scan(&v)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, err
		}
		out[k] = json.RawMessage(v)
	}
	return out, nil
}

// PutMany stores several answers in one transaction.
func (s *Store) PutMany(ctx context.Context, kv map[string]json.RawMessage) error {
	if s == nil || len(kv) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO cache (k, v, created_at) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	now := time.Now().UTC().Format(time.RFC3339)
	for k, v := range kv {
		if _, err := stmt.ExecContext(ctx, k, string(v), now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Clear removes every entry.
func (s *Store) Clear(ctx context.Context) (int64, error) {
	if s == nil {
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM cache`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// Count returns the number of cached answers.
func (s *Store) Count(ctx context.Context) (int64, error) {
	if s == nil {
		return 0, nil
	}
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM cache`).Scan(&n)
	return n, err
}

// Describe is a one-line summary for \cache.
func (s *Store) Describe(ctx context.Context) string {
	if s == nil {
		return "cache: disabled"
	}
	n, err := s.Count(ctx)
	if err != nil {
		return fmt.Sprintf("cache: %s (error: %v)", s.Path, err)
	}
	return fmt.Sprintf("cache: %s (%d entries)", s.Path, n)
}
