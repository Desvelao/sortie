// Package database wraps the SQLite operational store (pure-Go driver).
// It never stores file contents.
package database

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"

	_ "modernc.org/sqlite"
)

// DB is the application database.
type DB struct{ sql *sql.DB }

var memCounter atomic.Int64

const pragmas = "_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"

// DefaultPath returns $XDG_DATA_HOME/sortie/database.sqlite
// (~/.local/share/sortie/database.sqlite).
func DefaultPath() (string, error) {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "sortie", "database.sqlite"), nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".local", "share", "sortie", "database.sqlite"), nil
}

// Open opens (creating if needed) the database at path.
func Open(path string) (*DB, error) {
	// The database holds full file paths and copy history: keep it private.
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	_ = os.Chmod(dir, 0o700) // keep the directory private
	if f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600); err == nil {
		f.Close() // created 0600 instead of the umask default
	}
	u := url.URL{Path: path}
	d, err := open("file:" + u.EscapedPath() + "?" + pragmas + "&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Chmod(path+suffix, 0o600) // also tightens existing files
	}
	return d, nil
}

// OpenMemory opens a private in-memory database (tests, ephemeral use).
func OpenMemory() (*DB, error) {
	return open(fmt.Sprintf("file:fsmem%d?mode=memory&cache=shared&%s", memCounter.Add(1), pragmas))
}

func open(dsn string) (*DB, error) {
	s, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection: serialises access; transactions must not call d.sql.
	s.SetMaxOpenConns(1)
	if err := s.Ping(); err != nil {
		s.Close()
		return nil, fmt.Errorf("opening database: %w", err)
	}
	if _, err := s.Exec(schema); err != nil {
		s.Close()
		return nil, fmt.Errorf("creating schema: %w", err)
	}
	return &DB{sql: s}, nil
}

// Close closes the database.
func (d *DB) Close() error { return d.sql.Close() }
