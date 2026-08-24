package platform

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
)

// This file, and every legacy_<os>.go beside it, is the one place the
// pre-rename product's identifiers ("agentguard", "AgentGuard", …) may
// appear: everything here only detects, migrates or removes them, never uses
// them (contracts/identity-and-rename.md §2, enforced by
// scripts/check-rename.sh).

// LegacyKind categorizes a single piece of the pre-rename installation found
// on disk or registered with the OS.
type LegacyKind string

const (
	LegacyKindService     LegacyKind = "service"
	LegacyKindDataDir     LegacyKind = "data-dir"
	LegacyKindConfigDir   LegacyKind = "config-dir"
	LegacyKindRuntimeFile LegacyKind = "runtime-file"
	LegacyKindBinary      LegacyKind = "binary"
)

// The pre-rename identifiers this file detects, migrates away from, or
// removes. Never used to read or trust anything under the old name — only to
// find it.
const (
	legacyBinaryName   = "agentguard"
	legacyDatabaseFile = "agentguard.db"
	// currentDatabaseFile mirrors DatabasePath's file name (dirs.go); kept
	// local here so this file needs no non-legacy import for one constant.
	currentDatabaseFile = "intenter.db"
)

// legacyRuntimeFileNames are the only files RemoveStaleLegacyRuntimeFiles and
// LegacyLeftovers touch inside the legacy runtime directory.
var legacyRuntimeFileNames = []string{"agentguard.sock", "agentguard.pid", "agentguard.lock"}

// LegacyLeftover is one thing `doctor` can report and tell the user how to
// clean up. Fix is the exact command that removes it (or, for the
// directories that setup and the daemon migrate automatically on their own,
// a hint that they will and the equivalent manual command).
type LegacyLeftover struct {
	Kind LegacyKind
	Path string
	Fix  string
}

// LegacyLeftovers reports every trace of the pre-rename installation still on
// this machine, for `intenter doctor`. It only reads state; nothing is
// changed.
func LegacyLeftovers(p Platform) []LegacyLeftover {
	var leftovers []LegacyLeftover

	home := p.HomeDir()

	legacyData := legacyDataDir(home)
	if legacyData != p.DataDir() {
		if info, err := os.Stat(legacyData); err == nil && info.IsDir() {
			leftovers = append(leftovers, LegacyLeftover{
				Kind: LegacyKindDataDir,
				Path: legacyData,
				Fix:  fmt.Sprintf("run `intenter setup claude` to migrate it automatically, or: mv %q %q", legacyData, p.DataDir()),
			})
		}
	}

	legacyConfig := legacyConfigDir(home)
	if legacyConfig != legacyData && legacyConfig != p.ConfigDir() {
		if info, err := os.Stat(legacyConfig); err == nil && info.IsDir() {
			leftovers = append(leftovers, LegacyLeftover{
				Kind: LegacyKindConfigDir,
				Path: legacyConfig,
				Fix:  fmt.Sprintf("run `intenter setup claude` to migrate it automatically, or: mv %q %q", legacyConfig, p.ConfigDir()),
			})
		}
	}

	legacyRuntime := legacyRuntimeDir(home)
	for _, name := range legacyRuntimeFileNames {
		path := filepath.Join(legacyRuntime, name)
		if _, err := os.Stat(path); err == nil {
			leftovers = append(leftovers, LegacyLeftover{
				Kind: LegacyKindRuntimeFile,
				Path: path,
				Fix:  fmt.Sprintf("rm %q", path),
			})
		}
	}

	if leftover, ok := legacyServiceLeftover(p); ok {
		leftovers = append(leftovers, leftover)
	}

	if path, err := legacyBinaryOnPath(); err == nil && path != "" {
		leftovers = append(leftovers, LegacyLeftover{
			Kind: LegacyKindBinary,
			Path: path,
			Fix:  fmt.Sprintf("rm %q", path),
		})
	}

	return leftovers
}

// MigrateLegacyDataDir moves the pre-rename data and config directories into
// their new locations the first time either is found, renaming the database
// file along the way (contracts/identity-and-rename.md: "Data dir", "Config
// dir", "Runtime/data files" rows). It is safe to call on every setup run and
// every daemon start: once migrated, the legacy directories are gone and
// every call after that is a fast no-op.
//
// A directory is migrated only when its new location does not exist yet, or
// exists but holds no files of its own — which covers both a truly fresh
// machine and one where platform.EnsureDirs already created the empty
// scaffold directories before this ran. Nothing legacy is ever deleted: on
// failure (for example a rename across filesystems) both locations are left
// as they were and the error is returned, so the legacy directory survives to
// be reported by `doctor` and retried on the next call.
func MigrateLegacyDataDir(p Platform, log *slog.Logger) (bool, error) {
	migrated := false
	home := p.HomeDir()

	legacyData := legacyDataDir(home)
	dataMigrated, err := migrateOneDir(legacyData, p.DataDir())
	if err != nil {
		return migrated, err
	}
	if dataMigrated {
		migrated = true
		if err := renameLegacyDatabase(p.DataDir()); err != nil {
			return migrated, err
		}
		logLegacyMigration(log, "data", legacyData, p.DataDir())
	}

	legacyConfig := legacyConfigDir(home)
	if legacyConfig == legacyData {
		// macOS keeps one legacy directory for both data and config; it was
		// already handled above. Treating it as a second, independent source
		// here would let it migrate again into a different new config dir
		// (settable on its own via INTENTER_CONFIG_DIR even on macOS),
		// silently splitting one legacy directory across two new ones.
		return migrated, nil
	}

	configMigrated, err := migrateOneDir(legacyConfig, p.ConfigDir())
	if err != nil {
		return migrated, err
	}
	if configMigrated {
		migrated = true
		logLegacyMigration(log, "config", legacyConfig, p.ConfigDir())
	}

	return migrated, nil
}

// migrateOneDir moves oldDir's content into newDir and reports whether
// anything moved. It never touches oldDir or newDir when they are the same
// path (macOS shares one directory for data and config) or when oldDir does
// not exist.
func migrateOneDir(oldDir, newDir string) (bool, error) {
	if oldDir == "" || newDir == "" || oldDir == newDir {
		return false, nil
	}
	info, err := os.Lstat(oldDir)
	if err != nil || !info.IsDir() {
		return false, nil
	}
	if dirHasFiles(newDir) {
		// The new location is already in use; leave the legacy directory for
		// doctor to report rather than risk overwriting real data.
		return false, nil
	}

	if err := os.MkdirAll(filepath.Dir(newDir), DirMode); err != nil {
		return false, fmt.Errorf("platform: create %s: %w", filepath.Dir(newDir), err)
	}

	if _, err := os.Stat(newDir); err != nil {
		// The simple, exact case: the destination does not exist yet, so the
		// whole directory moves in one atomic rename.
		if err := os.Rename(oldDir, newDir); err != nil {
			return false, fmt.Errorf("platform: move %s to %s: %w", oldDir, newDir, err)
		}
		return true, nil
	}

	// The destination exists but holds no files (only empty scaffolding).
	// Move the legacy directory's own entries into it one at a time instead
	// of replacing it, so nothing legacy — not even an empty directory — is
	// ever removed.
	entries, err := os.ReadDir(oldDir)
	if err != nil {
		return false, fmt.Errorf("platform: read %s: %w", oldDir, err)
	}
	moved := false
	for _, entry := range entries {
		src := filepath.Join(oldDir, entry.Name())
		dst := filepath.Join(newDir, entry.Name())
		if err := os.Rename(src, dst); err != nil {
			return moved, fmt.Errorf("platform: move %s to %s: %w", src, dst, err)
		}
		moved = true
	}
	return moved, nil
}

// dirHasFiles reports whether dir contains any regular file, at any depth. A
// directory that does not exist, or exists with nothing but empty
// subdirectories, counts as having none.
func dirHasFiles(dir string) bool {
	hasFiles := false
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			hasFiles = true
			return fs.SkipAll
		}
		return nil
	})
	return hasFiles
}

// renameLegacyDatabase renames the pre-rename database file, and its WAL and
// shared-memory siblings when present, to the current name inside dataDir.
func renameLegacyDatabase(dataDir string) error {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		oldPath := filepath.Join(dataDir, legacyDatabaseFile+suffix)
		newPath := filepath.Join(dataDir, currentDatabaseFile+suffix)
		if _, err := os.Stat(oldPath); err != nil {
			continue
		}
		if _, err := os.Stat(newPath); err == nil {
			continue // something already uses the new name; do not clobber it
		}
		if err := os.Rename(oldPath, newPath); err != nil {
			return fmt.Errorf("platform: rename %s: %w", oldPath, err)
		}
	}
	return nil
}

// logLegacyMigration records what moved. log may be nil, for callers with
// nothing better to pass.
func logLegacyMigration(log *slog.Logger, kind, from, to string) {
	if log == nil {
		return
	}
	log.Info("platform: migrated legacy directory", "kind", kind, "from", from, "to", to)
}

// RemoveStaleLegacyRuntimeFiles deletes the socket, pid and lock files left in
// the pre-rename runtime directory, if it exists. It touches only those three
// names, never the directory itself or anything else in it.
func RemoveStaleLegacyRuntimeFiles(p Platform) error {
	dir := legacyRuntimeDir(p.HomeDir())
	if dir == "" {
		return nil
	}
	for _, name := range legacyRuntimeFileNames {
		path := filepath.Join(dir, name)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("platform: remove %s: %w", path, err)
		}
	}
	return nil
}

// RemoveLegacyService removes a pre-rename service registration if one is
// present, so an upgrade never leaves two competing daemons registered
// (contracts/identity-and-rename.md). Absent is not an error.
func RemoveLegacyService(ctx context.Context, p Platform) error {
	return removeLegacyService(ctx, p, execRunner)
}

// RemoveLegacyServiceWithRunner is RemoveLegacyService with an injected
// command runner, so tests never touch the machine's real service system —
// the same seam service_test.go uses for the current service managers.
func RemoveLegacyServiceWithRunner(ctx context.Context, p Platform, runner CommandRunner) error {
	return removeLegacyService(ctx, p, runner)
}

// legacyBinaryOnPath looks for a pre-rename binary still on PATH, left behind
// by an installer that has not been re-run since the rename.
func legacyBinaryOnPath() (string, error) {
	return FindExecutable(legacyBinaryName)
}
