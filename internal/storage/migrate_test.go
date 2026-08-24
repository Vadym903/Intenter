package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Vadym903/Intenter/internal/version"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "intenter.db")
	db, err := OpenAndMigrate(context.Background(), path)
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOpenAppliesRequiredPragmas(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	var journalMode string
	if err := db.SQL().QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("journal_mode = %q, want wal (§23.1)", journalMode)
	}

	var foreignKeys, busyTimeout, synchronous int
	if err := db.SQL().QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatalf("foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Errorf("foreign_keys = %d, want 1", foreignKeys)
	}
	if err := db.SQL().QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatalf("busy_timeout: %v", err)
	}
	if busyTimeout < 5000 {
		t.Errorf("busy_timeout = %d, want >= 5000", busyTimeout)
	}
	if err := db.SQL().QueryRowContext(ctx, "PRAGMA synchronous").Scan(&synchronous); err != nil {
		t.Fatalf("synchronous: %v", err)
	}
	if synchronous != 1 {
		t.Errorf("synchronous = %d, want 1 (NORMAL)", synchronous)
	}
}

func TestMigrateCreatesSchemaV1(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	got, err := SchemaVersion(ctx, db)
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if got != version.SchemaVersion {
		t.Errorf("schema version = %d, want %d", got, version.SchemaVersion)
	}

	tables := []string{
		"schema_version", "meta", "projects", "approvals", "approval_conditions",
		"approval_events", "audit_events", "agent_rule_imports",
	}
	for _, table := range tables {
		var count int
		err := db.SQL().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count)
		if err != nil {
			t.Fatalf("inspect %s: %v", table, err)
		}
		if count != 1 {
			t.Errorf("table %s is missing", table)
		}
	}

	indexes := []string{
		"idx_approvals_project_state", "idx_approval_events_approval",
		"idx_audit_at", "idx_audit_session_tool", "idx_audit_project_at",
	}
	for _, index := range indexes {
		var count int
		err := db.SQL().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, index).Scan(&count)
		if err != nil {
			t.Fatalf("inspect %s: %v", index, err)
		}
		if count != 1 {
			t.Errorf("index %s is missing", index)
		}
	}
}

func TestMigrateIsIdempotentAcrossRestarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "intenter.db")
	ctx := context.Background()

	for range 3 {
		db, err := OpenAndMigrate(ctx, path)
		if err != nil {
			t.Fatalf("OpenAndMigrate: %v", err)
		}
		var applied int
		if err := db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_version`).Scan(&applied); err != nil {
			t.Fatalf("count schema_version: %v", err)
		}
		if applied != version.SchemaVersion {
			t.Errorf("schema_version rows = %d, want %d", applied, version.SchemaVersion)
		}
		db.Close()
	}
}

func TestMigrateRefusesNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "intenter.db")
	ctx := context.Background()

	db, err := OpenAndMigrate(ctx, path)
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	future := version.SchemaVersion + 1
	if _, err := db.SQL().ExecContext(ctx,
		`INSERT INTO schema_version (version, applied_at) VALUES (?, ?)`,
		future, "2026-01-01T00:00:00.000000000Z"); err != nil {
		t.Fatalf("insert future version: %v", err)
	}
	db.Close()

	if _, err := OpenAndMigrate(ctx, path); err == nil {
		t.Fatal("expected a refusal to run against a newer schema (§23.4)")
	} else {
		var tooNew *ErrSchemaTooNew
		if !errors.As(err, &tooNew) {
			t.Fatalf("error = %v, want ErrSchemaTooNew", err)
		}
		if tooNew.Found != future || tooNew.Known != version.SchemaVersion {
			t.Errorf("ErrSchemaTooNew = %+v", tooNew)
		}
	}
}

func TestForeignKeysAreEnforced(t *testing.T) {
	db := openTestDB(t)
	_, err := db.SQL().Exec(`
		INSERT INTO approvals (project_id, kind, semantic_ops, envelope, network, engine_version,
			origin, created_from_raw_command, created_by_agent, state, created_at)
		VALUES ('missing-project','EXACT','[]','[]','[]',1,'cli','x','claude','ACTIVE','now')`)
	if err == nil {
		t.Error("inserting an approval for a missing project must violate the foreign key")
	}
}

func TestOpenReadOnlyRefusesWritesAndMissingFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "intenter.db")
	ctx := context.Background()

	if _, err := OpenReadOnly(path); err == nil {
		t.Error("OpenReadOnly must fail when the database does not exist")
	}

	db, err := OpenAndMigrate(ctx, path)
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	db.Close()

	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	defer ro.Close()

	if !ro.ReadOnly() {
		t.Error("ReadOnly() must report true")
	}
	if _, err := ro.SQL().Exec(`INSERT INTO meta (key, value) VALUES ('x','y')`); err == nil {
		t.Error("a read-only handle must refuse writes")
	}
	if _, err := SchemaVersion(ctx, ro); err != nil {
		t.Errorf("read-only schema inspection must work: %v", err)
	}
}

func TestIntegrityCheck(t *testing.T) {
	db := openTestDB(t)
	got, err := db.IntegrityCheck(context.Background())
	if err != nil {
		t.Fatalf("IntegrityCheck: %v", err)
	}
	if got != "ok" {
		t.Errorf("integrity_check = %q, want ok", got)
	}
}

func TestOpenRejectsEmptyPath(t *testing.T) {
	if _, err := Open(""); err == nil {
		t.Error("Open(\"\") must fail")
	}
}

func TestOpenHandlesPathsWithSpaces(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Application Support", "Intenter")
	db, err := OpenAndMigrate(context.Background(), filepath.Join(dir, "intenter.db"))
	if err != nil {
		t.Fatalf("paths with spaces must work (macOS data dir): %v", err)
	}
	db.Close()
}
