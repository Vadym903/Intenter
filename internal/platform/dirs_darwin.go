//go:build darwin

package platform

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Vadym903/Intenter/internal/action"
)

// defaultDataDir is ~/Library/Application Support/Intenter (§8.2).
func defaultDataDir(home string) string {
	return filepath.Join(home, "Library", "Application Support", "Intenter")
}

// defaultConfigDir is the same as the data directory on macOS.
func defaultConfigDir(home string) string { return defaultDataDir(home) }

// defaultRuntimeDir is $TMPDIR/intenter-<uid>, falling back to /tmp.
func defaultRuntimeDir(string) string {
	base := os.Getenv("TMPDIR")
	if base == "" {
		base = "/tmp"
	}
	return filepath.Join(filepath.Clean(base), fmt.Sprintf("intenter-%d", os.Getuid()))
}

// defaultEndpoint is a Unix domain socket inside the runtime directory (§10.1).
func defaultEndpoint(runtimeDir, _ string) string {
	return filepath.Join(runtimeDir, "intenter.sock")
}

func defaultShellDialect() action.Dialect { return action.DialectPosix }
