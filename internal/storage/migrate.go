package storage

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Vadym903/Intenter/internal/version"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type migration struct {
	version int
	name    string
	sql     string
}

// ErrSchemaTooNew is returned when the database was written by a newer build.
// The daemon refuses to run rather than corrupt data (PROTOTYPE_SPEC.md §23.4).
type ErrSchemaTooNew struct {
	Found int
	Known int
}

func (e *ErrSchemaTooNew) Error() string {
	return fmt.Sprintf("storage: database schema v%d is newer than this build supports (v%d); upgrade intenter", e.Found, e.Known)
}

// Migrate applies every pending migration, each in its own transaction, and
// records it in schema_version. It is idempotent across restarts.
func Migrate(ctx context.Context, db *DB) error {
	migrations, err := loadMigrations()
	if err != nil {
		return err
	}

	current, err := SchemaVersion(ctx, db)
	if err != nil {
		return err
	}
	if current > version.SchemaVersion {
		return &ErrSchemaTooNew{Found: current, Known: version.SchemaVersion}
	}

	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		if m.version > version.SchemaVersion {
			return fmt.Errorf("storage: migration %s exceeds the known schema version v%d", m.name, version.SchemaVersion)
		}
		if err := applyMigration(ctx, db, m); err != nil {
			return err
		}
	}
	return nil
}

func applyMigration(ctx context.Context, db *DB, m migration) error {
	err := db.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			return fmt.Errorf("storage: apply migration %s: %w", m.name, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_version (version, applied_at) VALUES (?, ?)`,
			m.version, formatTime(time.Now()),
		); err != nil {
			return fmt.Errorf("storage: record migration %s: %w", m.name, err)
		}
		return nil
	})
	return err
}

// SchemaVersion returns the highest applied schema version, or 0 for a fresh
// database.
func SchemaVersion(ctx context.Context, db *DB) (int, error) {
	var exists int
	err := db.sql.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='schema_version'`,
	).Scan(&exists)
	if err != nil {
		return 0, fmt.Errorf("storage: inspect schema: %w", err)
	}
	if exists == 0 {
		return 0, nil
	}

	var current sql.NullInt64
	if err := db.sql.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_version`).Scan(&current); err != nil {
		return 0, fmt.Errorf("storage: read schema_version: %w", err)
	}
	if !current.Valid {
		return 0, nil
	}
	return int(current.Int64), nil
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("storage: read migrations: %w", err)
	}

	out := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		number, _, found := strings.Cut(entry.Name(), "_")
		if !found {
			return nil, fmt.Errorf("storage: migration %s must be named <version>_<name>.sql", entry.Name())
		}
		parsed, err := strconv.Atoi(number)
		if err != nil {
			return nil, fmt.Errorf("storage: migration %s has a non-numeric version: %w", entry.Name(), err)
		}
		content, err := migrationFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("storage: read migration %s: %w", entry.Name(), err)
		}
		out = append(out, migration{version: parsed, name: entry.Name(), sql: string(content)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// OpenAndMigrate is the daemon's startup path: open the database and bring it
// to the current schema (§9.3 step 3).
func OpenAndMigrate(ctx context.Context, path string) (*DB, error) {
	db, err := Open(path)
	if err != nil {
		return nil, err
	}
	if err := Migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
