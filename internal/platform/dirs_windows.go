//go:build windows

package platform

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"github.com/Vadym903/Intenter/internal/action"
)

// defaultDataDir is %LOCALAPPDATA%\Intenter (§8.2).
func defaultDataDir(home string) string {
	if base := os.Getenv("LOCALAPPDATA"); base != "" {
		return filepath.Join(filepath.Clean(base), "Intenter")
	}
	return filepath.Join(home, "AppData", "Local", "Intenter")
}

// defaultConfigDir is %APPDATA%\Intenter.
func defaultConfigDir(home string) string {
	if base := os.Getenv("APPDATA"); base != "" {
		return filepath.Join(filepath.Clean(base), "Intenter")
	}
	return filepath.Join(home, "AppData", "Roaming", "Intenter")
}

// defaultRuntimeDir holds pid and lock files; the IPC endpoint is a named pipe,
// so no socket lives here.
func defaultRuntimeDir(home string) string {
	return filepath.Join(defaultDataDir(home), "runtime")
}

// defaultEndpoint is a per-user named pipe (§10.1). The user name and the
// runtime directory are hashed together: the hash keeps the pipe name valid
// for any account name, and the runtime directory keeps two installations
// apart — a test harness beside a real one, or two INTENTER_DATA_DIR setups
// — where on macOS and Linux the socket's own location already does that.
func defaultEndpoint(runtimeDir string, home string) string {
	return `\\.\pipe\intenter-` + userToken(home, runtimeDir)
}

func userToken(home, runtimeDir string) string {
	name := os.Getenv("USERNAME")
	if strings.TrimSpace(name) == "" {
		name = filepath.Base(home)
	}
	sum := sha256.Sum256([]byte(strings.ToLower(name) + "\x00" + strings.ToLower(filepath.Clean(runtimeDir))))
	return hex.EncodeToString(sum[:])[:16]
}

func defaultShellDialect() action.Dialect { return action.DialectCmd }
