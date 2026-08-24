package storage

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver: no CGO on any target (research R-02)
)

// TimeFormat is how timestamps are stored: RFC3339 in UTC with nanoseconds, so
// rows sort lexicographically and compare exactly (data-model.md §2.1).
const TimeFormat = "2006-01-02T15:04:05.000000000Z07:00"

// DB owns the SQLite connection pool used by the daemon.
type DB struct {
	sql  *sql.DB
	path string
	// readOnly is set for the CLI's direct-read fallback when the daemon is down.
	readOnly bool
}

// Open opens (creating if needed) the database at path with the pragmas the
// specification requires: WAL, synchronous=NORMAL, foreign_keys=ON and a 5 s
// busy timeout (PROTOTYPE_SPEC.md §23.1).
func Open(path string) (*DB, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("storage: empty database path")
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("storage: create %s: %w", dir, err)
		}
	}
	return open(path, false)
}

// OpenReadOnly opens an existing database without writing to it. `intenter
// history` and `approvals` use it when the daemon is unreachable (§23.1).
func OpenReadOnly(path string) (*DB, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("storage: %s is not available: %w", path, err)
	}
	return open(path, true)
}

func open(path string, readOnly bool) (*DB, error) {
	handle, err := sql.Open("sqlite", dsn(path, readOnly))
	if err != nil {
		return nil, fmt.Errorf("storage: open %s: %w", path, err)
	}
	// SQLite allows one writer at a time; a small pool plus busy_timeout keeps
	// concurrent hook evaluations correct without SQLITE_BUSY errors.
	handle.SetMaxOpenConns(4)
	handle.SetMaxIdleConns(4)
	handle.SetConnMaxLifetime(0)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := handle.PingContext(ctx); err != nil {
		handle.Close()
		return nil, fmt.Errorf("storage: connect %s: %w", path, err)
	}

	db := &DB{sql: handle, path: path, readOnly: readOnly}
	if err := db.verifyPragmas(ctx); err != nil {
		handle.Close()
		return nil, err
	}
	return db, nil
}

// dsn builds the connection string. modernc.org/sqlite applies `_pragma`
// parameters to every pooled connection, which is required because
// busy_timeout and foreign_keys are per-connection settings.
func dsn(path string, readOnly bool) string {
	params := []string{
		"_pragma=busy_timeout(5000)",
		"_pragma=journal_mode(WAL)",
		"_pragma=synchronous(NORMAL)",
		"_pragma=foreign_keys(ON)",
	}
	if readOnly {
		params = append(params, "mode=ro")
	}
	return "file:" + fileURIPath(path) + "?" + strings.Join(params, "&")
}

// fileURIPath escapes a filesystem path for use in a file: URI. Windows paths
// are converted to forward slashes with a leading slash.
func fileURIPath(path string) string {
	converted := filepath.ToSlash(path)
	if len(converted) > 1 && converted[1] == ':' {
		converted = "/" + converted
	}
	escaped := (&url.URL{Path: converted}).EscapedPath()
	return escaped
}

func (db *DB) verifyPragmas(ctx context.Context) error {
	var journalMode string
	if err := db.sql.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		return fmt.Errorf("storage: read journal_mode: %w", err)
	}
	// A read-only database cannot be switched to WAL; that is not an error.
	if !db.readOnly && !strings.EqualFold(journalMode, "wal") {
		return fmt.Errorf("storage: journal_mode is %q, want wal", journalMode)
	}

	var foreignKeys int
	if err := db.sql.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return fmt.Errorf("storage: read foreign_keys: %w", err)
	}
	if foreignKeys != 1 {
		return fmt.Errorf("storage: foreign_keys is off")
	}
	return nil
}

// SQL exposes the underlying handle for repositories and tests.
func (db *DB) SQL() *sql.DB { return db.sql }

// Path is the database file location.
func (db *DB) Path() string { return db.path }

// ReadOnly reports whether the handle refuses writes.
func (db *DB) ReadOnly() bool { return db.readOnly }

// Close releases the connection pool.
func (db *DB) Close() error {
	if db == nil || db.sql == nil {
		return nil
	}
	return db.sql.Close()
}

// IntegrityCheck runs PRAGMA integrity_check for `intenter doctor` (§12.5).
func (db *DB) IntegrityCheck(ctx context.Context) (string, error) {
	var result string
	if err := db.sql.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return "", fmt.Errorf("storage: integrity_check: %w", err)
	}
	return result, nil
}

// WithTx runs fn inside a transaction, committing on success and rolling back
// on error or panic.
func (db *DB) WithTx(ctx context.Context, fn func(*sql.Tx) error) (err error) {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage: begin: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("storage: commit: %w", err)
	}
	return nil
}

// formatTime renders a timestamp for storage.
func formatTime(t time.Time) string { return t.UTC().Format(TimeFormat) }

// parseTime reads a stored timestamp.
func parseTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	if parsed, err := time.Parse(TimeFormat, value); err == nil {
		return parsed.UTC(), nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("storage: parse timestamp %q: %w", value, err)
	}
	return parsed.UTC(), nil
}
