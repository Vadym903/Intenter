package platform

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// legacyTestPlatform builds a platform rooted in a temp directory, and
// neutralizes the OS environment variables the legacy default-directory
// functions read (XDG_*, TMPDIR, LOCALAPPDATA, APPDATA) so a variable set on
// the machine running the tests — or the legacy runtime directory's own
// fallback to a fixed, unsandboxed /tmp path — cannot leak a real path into a
// fixture or, worse, leave files behind outside the temp directory.
func legacyTestPlatform(t *testing.T) Platform {
	t.Helper()
	base := t.TempDir()

	t.Setenv(EnvTestMode, "1")
	t.Setenv(EnvTestHome, filepath.Join(base, "home"))
	t.Setenv(EnvDataDir, filepath.Join(base, "data"))
	t.Setenv(EnvConfigDir, filepath.Join(base, "config"))
	t.Setenv(EnvRuntimeDir, filepath.Join(base, "run"))
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("LOCALAPPDATA", "")
	t.Setenv("APPDATA", "")

	// The legacy runtime dir falls back to $TMPDIR/agentguard-<uid> on macOS
	// and /tmp/agentguard-<uid> on Linux when no XDG_RUNTIME_DIR is set —
	// both real, machine-wide paths outside t.TempDir(). Redirecting both
	// keeps every legacy path under this test's own sandbox.
	tmpBase := filepath.Join(base, "tmp")
	if err := os.MkdirAll(tmpBase, 0o755); err != nil {
		t.Fatalf("mkdir tmp: %v", err)
	}
	t.Setenv("TMPDIR", tmpBase)
	xdgRuntime := filepath.Join(base, "xdg-runtime")
	if err := os.MkdirAll(xdgRuntime, 0o700); err != nil {
		t.Fatalf("mkdir xdg-runtime: %v", err)
	}
	t.Setenv("XDG_RUNTIME_DIR", xdgRuntime)

	if err := os.MkdirAll(filepath.Join(base, "home"), 0o755); err != nil {
		t.Fatalf("mkdir home: %v", err)
	}
	p, err := New()
	if err != nil {
		t.Fatalf("platform.New: %v", err)
	}
	return p
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}

func mustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("%s must not exist, stat err = %v", path, err)
	}
}

func TestMigrateLegacyDataDirMovesContentAndRenamesDatabase(t *testing.T) {
	p := legacyTestPlatform(t)
	home := p.HomeDir()

	legacyData := legacyDataDir(home)
	writeFile(t, filepath.Join(legacyData, legacyDatabaseFile), "db-content")
	writeFile(t, filepath.Join(legacyData, legacyDatabaseFile+"-wal"), "wal-content")
	writeFile(t, filepath.Join(legacyData, legacyDatabaseFile+"-shm"), "shm-content")
	writeFile(t, filepath.Join(legacyData, "logs", "daemon.log"), "log-content")

	legacyConfig := legacyConfigDir(home)
	if legacyConfig != legacyData {
		writeFile(t, filepath.Join(legacyConfig, "config.toml"), "config-content")
	}

	migrated, err := MigrateLegacyDataDir(p, nil)
	if err != nil {
		t.Fatalf("MigrateLegacyDataDir: %v", err)
	}
	if !migrated {
		t.Fatal("migrated = false, want true")
	}

	if got := readFile(t, filepath.Join(p.DataDir(), currentDatabaseFile)); got != "db-content" {
		t.Errorf("intenter.db content = %q, want %q", got, "db-content")
	}
	if got := readFile(t, filepath.Join(p.DataDir(), currentDatabaseFile+"-wal")); got != "wal-content" {
		t.Errorf("intenter.db-wal content = %q, want %q", got, "wal-content")
	}
	if got := readFile(t, filepath.Join(p.DataDir(), currentDatabaseFile+"-shm")); got != "shm-content" {
		t.Errorf("intenter.db-shm content = %q, want %q", got, "shm-content")
	}
	if got := readFile(t, filepath.Join(p.DataDir(), "logs", "daemon.log")); got != "log-content" {
		t.Errorf("logs/daemon.log content = %q, want %q", got, "log-content")
	}
	mustNotExist(t, legacyData)

	if legacyConfig != legacyData {
		if got := readFile(t, filepath.Join(p.ConfigDir(), "config.toml")); got != "config-content" {
			t.Errorf("config.toml content = %q, want %q", got, "config-content")
		}
		mustNotExist(t, legacyConfig)
	}
}

func TestMigrateLegacyDataDirIsNoOpWhenTheNewDirAlreadyHasContent(t *testing.T) {
	p := legacyTestPlatform(t)
	legacyData := legacyDataDir(p.HomeDir())

	writeFile(t, filepath.Join(legacyData, legacyDatabaseFile), "old")
	writeFile(t, filepath.Join(p.DataDir(), currentDatabaseFile), "already-here")

	migrated, err := MigrateLegacyDataDir(p, nil)
	if err != nil {
		t.Fatalf("MigrateLegacyDataDir: %v", err)
	}
	if migrated {
		t.Error("migrated = true, want false: the new directory already has content")
	}

	if got := readFile(t, filepath.Join(p.DataDir(), currentDatabaseFile)); got != "already-here" {
		t.Errorf("existing intenter.db was overwritten, content = %q", got)
	}
	if got := readFile(t, filepath.Join(legacyData, legacyDatabaseFile)); got != "old" {
		t.Errorf("legacy database was changed, content = %q", got)
	}
}

// TestMigrateLegacyDataDirTreatsEmptyScaffoldingAsAbsent covers the daemon
// start-up order: platform.EnsureDirs may have already created the new data
// directory (with an empty "logs" subdirectory) before migration runs, and
// that must not be mistaken for "already in use".
func TestMigrateLegacyDataDirTreatsEmptyScaffoldingAsAbsent(t *testing.T) {
	p := legacyTestPlatform(t)
	legacyData := legacyDataDir(p.HomeDir())
	writeFile(t, filepath.Join(legacyData, legacyDatabaseFile), "content")

	if err := os.MkdirAll(filepath.Join(p.DataDir(), "logs"), 0o755); err != nil {
		t.Fatalf("mkdir scaffold: %v", err)
	}

	migrated, err := MigrateLegacyDataDir(p, nil)
	if err != nil {
		t.Fatalf("MigrateLegacyDataDir: %v", err)
	}
	if !migrated {
		t.Fatal("migrated = false, want true: an empty scaffold must not block migration")
	}
	if got := readFile(t, filepath.Join(p.DataDir(), currentDatabaseFile)); got != "content" {
		t.Errorf("intenter.db content = %q, want %q", got, "content")
	}

	// Nothing legacy is ever deleted, so the (now empty) legacy directory
	// itself must survive even though its content moved out.
	info, err := os.Stat(legacyData)
	if err != nil || !info.IsDir() {
		t.Fatalf("the legacy directory must still exist (empty), stat err = %v", err)
	}
	entries, err := os.ReadDir(legacyData)
	if err != nil {
		t.Fatalf("read %s: %v", legacyData, err)
	}
	if len(entries) != 0 {
		t.Errorf("legacy directory still has entries: %v", entries)
	}
}

func TestMigrateLegacyDataDirDeletesNothingOnAFailedRename(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory mode bits do not deny writes on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root writes to read-only directories")
	}

	p := legacyTestPlatform(t)
	legacyData := legacyDataDir(p.HomeDir())
	writeFile(t, filepath.Join(legacyData, legacyDatabaseFile), "content")

	parent := filepath.Dir(p.DataDir())
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatalf("chmod parent: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })

	migrated, err := MigrateLegacyDataDir(p, nil)
	if err == nil {
		t.Fatal("expected an error from a rename into a read-only parent")
	}
	if migrated {
		t.Error("migrated = true despite the failure")
	}

	if got := readFile(t, filepath.Join(legacyData, legacyDatabaseFile)); got != "content" {
		t.Errorf("the legacy database must survive a failed rename, content = %q", got)
	}
	mustNotExist(t, p.DataDir())
}

func TestLegacyLeftoversReportsDataConfigAndRuntimeFiles(t *testing.T) {
	p := legacyTestPlatform(t)
	home := p.HomeDir()

	legacyData := legacyDataDir(home)
	writeFile(t, filepath.Join(legacyData, legacyDatabaseFile), "content")

	legacyConfig := legacyConfigDir(home)
	if legacyConfig != legacyData {
		writeFile(t, filepath.Join(legacyConfig, "config.toml"), "content")
	}

	legacyRuntime := legacyRuntimeDir(home)
	for _, name := range legacyRuntimeFileNames {
		writeFile(t, filepath.Join(legacyRuntime, name), "x")
	}

	leftovers := LegacyLeftovers(p)

	byKind := map[LegacyKind][]LegacyLeftover{}
	for _, l := range leftovers {
		byKind[l.Kind] = append(byKind[l.Kind], l)
	}

	if got := byKind[LegacyKindDataDir]; len(got) != 1 || got[0].Path != legacyData {
		t.Errorf("data-dir leftovers = %v, want one entry for %q", got, legacyData)
	}
	if legacyConfig != legacyData {
		if got := byKind[LegacyKindConfigDir]; len(got) != 1 || got[0].Path != legacyConfig {
			t.Errorf("config-dir leftovers = %v, want one entry for %q", got, legacyConfig)
		}
	}
	if got := byKind[LegacyKindRuntimeFile]; len(got) != len(legacyRuntimeFileNames) {
		t.Errorf("runtime-file leftovers = %v, want %d entries", got, len(legacyRuntimeFileNames))
	}
	for _, l := range leftovers {
		if l.Fix == "" {
			t.Errorf("leftover %+v has no Fix", l)
		}
	}
}

func TestLegacyLeftoversIsEmptyOnAFreshMachine(t *testing.T) {
	p := legacyTestPlatform(t)
	if got := LegacyLeftovers(p); len(got) != 0 {
		t.Errorf("leftovers = %v, want none", got)
	}
}

func TestRemoveStaleLegacyRuntimeFilesRemovesOnlyTheThreeNames(t *testing.T) {
	p := legacyTestPlatform(t)
	legacyRuntime := legacyRuntimeDir(p.HomeDir())

	for _, name := range legacyRuntimeFileNames {
		writeFile(t, filepath.Join(legacyRuntime, name), "x")
	}
	writeFile(t, filepath.Join(legacyRuntime, "keep-me.txt"), "keep")

	if err := RemoveStaleLegacyRuntimeFiles(p); err != nil {
		t.Fatalf("RemoveStaleLegacyRuntimeFiles: %v", err)
	}

	for _, name := range legacyRuntimeFileNames {
		mustNotExist(t, filepath.Join(legacyRuntime, name))
	}
	if got := readFile(t, filepath.Join(legacyRuntime, "keep-me.txt")); got != "keep" {
		t.Errorf("unrelated file was touched, content = %q", got)
	}
	if info, err := os.Stat(legacyRuntime); err != nil || !info.IsDir() {
		t.Errorf("the legacy runtime directory itself must survive, stat err = %v", err)
	}
}

func TestRemoveStaleLegacyRuntimeFilesIsANoOpWithNothingThere(t *testing.T) {
	p := legacyTestPlatform(t)
	if err := RemoveStaleLegacyRuntimeFiles(p); err != nil {
		t.Errorf("RemoveStaleLegacyRuntimeFiles: %v", err)
	}
}

// legacyServiceDefinitionPath returns where the current OS's legacy service
// manager keeps its registration, and whether that OS is covered by the
// fake-runner tests below (Windows removes a registry value instead of a
// file, so it is exercised separately, the same way service_test.go treats
// it).
//
// The paths are spelled out here rather than calling the package's own
// legacyPlistPath/legacyUnitPath: those are only compiled for their own GOOS,
// and this file (like every "legacy" file) is one of the few allowed to name
// the old product directly (scripts/check-rename.sh).
func legacyServiceDefinitionPath(t *testing.T, p Platform) (string, bool) {
	t.Helper()
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(p.HomeDir(), "Library", "LaunchAgents", "com.agentguard.daemon.plist"), true
	case "linux":
		return filepath.Join(p.HomeDir(), ".config", "systemd", "user", "agentguard.service"), true
	default:
		return "", false
	}
}

func TestRemoveLegacyServiceRemovesTheRegistration(t *testing.T) {
	p := legacyTestPlatform(t)
	path, ok := legacyServiceDefinitionPath(t, p)
	if !ok {
		t.Skip("the Windows manager writes the registry, covered separately")
	}
	writeFile(t, path, "legacy service definition")

	if leftover, found := legacyServiceLeftover(p); !found || leftover.Kind != LegacyKindService {
		t.Fatalf("legacyServiceLeftover = %+v, %v, want a service leftover before removal", leftover, found)
	}

	runner := newRecordingRunner()
	if err := RemoveLegacyServiceWithRunner(context.Background(), p, runner.run); err != nil {
		t.Fatalf("RemoveLegacyServiceWithRunner: %v", err)
	}

	mustNotExist(t, path)
	if _, found := legacyServiceLeftover(p); found {
		t.Error("the service must no longer be reported as a leftover")
	}

	switch runtime.GOOS {
	case "darwin":
		if !runner.ranMatching("launchctl", "bootout") {
			t.Errorf("calls = %v, want a launchctl bootout", runner.calls)
		}
	case "linux":
		if !runner.ranMatching("systemctl", "--user", "disable", "--now") {
			t.Errorf("calls = %v, want systemctl disable --now", runner.calls)
		}
		if !runner.ranMatching("systemctl", "--user", "daemon-reload") {
			t.Errorf("calls = %v, want systemctl daemon-reload", runner.calls)
		}
	}
}

func TestRemoveLegacyServiceWhenNothingIsRegisteredIsANoOp(t *testing.T) {
	p := legacyTestPlatform(t)
	if _, ok := legacyServiceDefinitionPath(t, p); !ok {
		t.Skip("the Windows manager writes the registry, covered separately")
	}

	runner := newRecordingRunner()
	if err := RemoveLegacyServiceWithRunner(context.Background(), p, runner.run); err != nil {
		t.Errorf("RemoveLegacyServiceWithRunner: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Errorf("calls = %v, want none: nothing legacy is registered", runner.calls)
	}
}
