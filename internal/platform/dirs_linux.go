//go:build linux

package platform

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Vadym903/Intenter/internal/action"
)

// defaultDataDir is $XDG_DATA_HOME/intenter (default ~/.local/share) (§8.2).
func defaultDataDir(home string) string {
	if base := os.Getenv("XDG_DATA_HOME"); base != "" {
		return filepath.Join(filepath.Clean(base), "intenter")
	}
	return filepath.Join(home, ".local", "share", "intenter")
}

// defaultConfigDir is $XDG_CONFIG_HOME/intenter (default ~/.config).
func defaultConfigDir(home string) string {
	if base := os.Getenv("XDG_CONFIG_HOME"); base != "" {
		return filepath.Join(filepath.Clean(base), "intenter")
	}
	return filepath.Join(home, ".config", "intenter")
}

// defaultRuntimeDir is $XDG_RUNTIME_DIR/intenter, falling back to
// /tmp/intenter-<uid>.
func defaultRuntimeDir(string) string {
	if base := os.Getenv("XDG_RUNTIME_DIR"); base != "" {
		return filepath.Join(filepath.Clean(base), "intenter")
	}
	return filepath.Join("/tmp", fmt.Sprintf("intenter-%d", os.Getuid()))
}

// defaultEndpoint is a Unix domain socket inside the runtime directory (§10.1).
func defaultEndpoint(runtimeDir, _ string) string {
	return filepath.Join(runtimeDir, "intenter.sock")
}

func defaultShellDialect() action.Dialect { return action.DialectPosix }
