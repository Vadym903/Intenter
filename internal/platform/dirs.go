package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Environment overrides for tests and advanced use (PROTOTYPE_SPEC.md §8.2,
// contracts/cli.md).
const (
	EnvDataDir    = "INTENTER_DATA_DIR"
	EnvConfigDir  = "INTENTER_CONFIG_DIR"
	EnvRuntimeDir = "INTENTER_RUNTIME_DIR"
	EnvEndpoint   = "INTENTER_ENDPOINT"
	// EnvTestMode gates the test-only home redirection so a stray
	// INTENTER_TEST_HOME cannot move a real user's home directory.
	EnvTestMode = "INTENTER_TEST_MODE"
	EnvTestHome = "INTENTER_TEST_HOME"
)

// DirMode is the owner-only permission used for every Intenter directory.
const DirMode os.FileMode = 0o700

type dirSet struct {
	data    string
	config  string
	runtime string
}

// apply lets explicit overrides win over environment and defaults.
func (d dirSet) apply(overrides Overrides) dirSet {
	if overrides.DataDir != "" {
		d.data = absClean(overrides.DataDir)
	}
	if overrides.ConfigDir != "" {
		d.config = absClean(overrides.ConfigDir)
	}
	if overrides.RuntimeDir != "" {
		d.runtime = absClean(overrides.RuntimeDir)
	}
	return d
}

func absClean(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(path)
}

func resolveDirs(home string) (dirSet, error) {
	data := envDir(EnvDataDir)
	if data == "" {
		data = defaultDataDir(home)
	}
	config := envDir(EnvConfigDir)
	if config == "" {
		config = defaultConfigDir(home)
	}
	runtimeDir := envDir(EnvRuntimeDir)
	if runtimeDir == "" {
		runtimeDir = defaultRuntimeDir(home)
	}
	if data == "" || config == "" || runtimeDir == "" {
		return dirSet{}, fmt.Errorf("platform: could not determine Intenter directories")
	}
	return dirSet{data: data, config: config, runtime: runtimeDir}, nil
}

func envDir(key string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return ""
	}
	if abs, err := filepath.Abs(value); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(value)
}

// homeDir returns the canonical user home. Tests may redirect it with
// INTENTER_TEST_HOME, but only when INTENTER_TEST_MODE=1 (§28.3).
//
// The path is symlink-resolved: scope classification compares canonical paths
// (I-14), so a home directory reached through a symlink — /var vs /private/var
// on macOS — would otherwise never match its own contents.
func homeDir() (string, error) {
	if TestMode() {
		if override := envDir(EnvTestHome); override != "" {
			return canonical(override), nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return canonical(home), nil
}

// canonical resolves symlinks where possible, falling back to a lexical clean
// for paths that do not exist yet.
func canonical(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(path)
}

// TestMode reports whether Intenter runs under the test harness.
func TestMode() bool {
	return strings.TrimSpace(os.Getenv(EnvTestMode)) == "1"
}

func tempDir() string {
	return canonical(os.TempDir())
}

func goos() string { return runtime.GOOS }

// EnsureDir creates dir and its parents with owner-only permissions. On
// Windows the mode is ignored and the inherited user ACL applies (§8.2).
func EnsureDir(dir string) error {
	if dir == "" {
		return fmt.Errorf("platform: empty directory path")
	}
	if err := os.MkdirAll(dir, DirMode); err != nil {
		return fmt.Errorf("platform: create %s: %w", dir, err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(dir, DirMode); err != nil {
			return fmt.Errorf("platform: chmod %s: %w", dir, err)
		}
	}
	return nil
}

// EnsureDirs creates every Intenter directory the daemon needs.
func EnsureDirs(p Platform) error {
	for _, dir := range []string{p.DataDir(), p.ConfigDir(), p.RuntimeDir(), LogDir(p)} {
		if err := EnsureDir(dir); err != nil {
			return err
		}
	}
	return nil
}

// LogDir is where daemon.log and hook.log are written.
func LogDir(p Platform) string { return filepath.Join(p.DataDir(), "logs") }

// BackupDir is where Claude settings backups are kept (§12.2 step 2).
func BackupDir(p Platform) string { return filepath.Join(p.DataDir(), "backups") }

// DatabasePath is the SQLite file location (§23.1).
func DatabasePath(p Platform) string { return filepath.Join(p.DataDir(), "intenter.db") }

// DaemonInfoPath is the daemon.json descriptor written at startup (§9.3).
func DaemonInfoPath(p Platform) string { return filepath.Join(p.DataDir(), "daemon.json") }

// PidFilePath is the single-instance pid file (§9.1).
func PidFilePath(p Platform) string { return filepath.Join(p.RuntimeDir(), "intenter.pid") }

// LockFilePath is the single-instance lock (§9.1).
func LockFilePath(p Platform) string { return filepath.Join(p.RuntimeDir(), "intenter.lock") }

// ConfigFilePath is the optional TOML configuration file (§12.6).
func ConfigFilePath(p Platform) string { return filepath.Join(p.ConfigDir(), "config.toml") }

// resolveEndpoint applies INTENTER_ENDPOINT over the platform default.
func resolveEndpoint(runtimeDir, home string) string {
	if override := strings.TrimSpace(os.Getenv(EnvEndpoint)); override != "" {
		return override
	}
	return defaultEndpoint(runtimeDir, home)
}
