package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Meta keys recorded by setup for `doctor` (§12.4, data-model.md §2.7).
const (
	MetaIntenterVersion     = "intenter_version"
	MetaHooksVersion        = "hooks_version"
	MetaClaudeVersion       = "claude_version"
	MetaClaudeSettingsPath  = "claude_settings_path"
	MetaLastBackupPath      = "last_backup_path"
	MetaServiceMode         = "service_mode"
	MetaEngineVersion       = "engine_version"
	MetaInstalledBinaryPath = "installed_binary_path"
	MetaSetupAt             = "setup_at"
)

// MetaRepo stores small key/value installation facts.
type MetaRepo struct{ db *DB }

// Set writes or replaces one key.
func (r *MetaRepo) Set(ctx context.Context, key, value string) error {
	_, err := r.db.sql.ExecContext(ctx,
		`INSERT INTO meta (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("storage: set meta %s: %w", key, err)
	}
	return nil
}

// SetAll writes several keys.
func (r *MetaRepo) SetAll(ctx context.Context, values map[string]string) error {
	for key, value := range values {
		if err := r.Set(ctx, key, value); err != nil {
			return err
		}
	}
	return nil
}

// Get returns one key, or ErrNotFound.
func (r *MetaRepo) Get(ctx context.Context, key string) (string, error) {
	var value string
	err := r.db.sql.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("meta %s: %w", key, ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("storage: get meta %s: %w", key, err)
	}
	return value, nil
}

// Lookup returns one key and whether it exists, without an error for a miss.
func (r *MetaRepo) Lookup(ctx context.Context, key string) (string, bool, error) {
	value, err := r.Get(ctx, key)
	if errors.Is(err, ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

// All returns every meta key.
func (r *MetaRepo) All(ctx context.Context) (map[string]string, error) {
	rows, err := r.db.sql.QueryContext(ctx, `SELECT key, value FROM meta ORDER BY key`)
	if err != nil {
		return nil, fmt.Errorf("storage: list meta: %w", err)
	}
	defer rows.Close()

	out := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("storage: scan meta: %w", err)
		}
		out[key] = value
	}
	return out, rows.Err()
}
