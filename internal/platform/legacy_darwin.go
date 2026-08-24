//go:build darwin

package platform

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
)

// legacyServiceLabel is the LaunchAgent label the pre-rename product used
// (contracts/identity-and-rename.md: "launchd label / plist").
const legacyServiceLabel = "com.agentguard.daemon"

// legacyDataDir mirrors defaultDataDir (dirs_darwin.go) under the old name.
func legacyDataDir(home string) string {
	return filepath.Join(home, "Library", "Application Support", "AgentGuard")
}

// legacyConfigDir is the same directory as legacyDataDir, matching the
// current product's macOS layout.
func legacyConfigDir(home string) string { return legacyDataDir(home) }

// legacyRuntimeDir mirrors defaultRuntimeDir (dirs_darwin.go) under the old
// name.
func legacyRuntimeDir(string) string {
	base := os.Getenv("TMPDIR")
	if base == "" {
		base = "/tmp"
	}
	return filepath.Join(filepath.Clean(base), fmt.Sprintf("agentguard-%d", os.Getuid()))
}

// legacyPlistPath is where the pre-rename LaunchAgent definition lived.
func legacyPlistPath(home string) string {
	return filepath.Join(home, "Library", "LaunchAgents", legacyServiceLabel+".plist")
}

func legacyServiceLeftover(p Platform) (LegacyLeftover, bool) {
	path := legacyPlistPath(p.HomeDir())
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		return LegacyLeftover{}, false
	}
	return LegacyLeftover{
		Kind: LegacyKindService,
		Path: path,
		Fix:  fmt.Sprintf("launchctl bootout gui/$(id -u)/%s; rm %q", legacyServiceLabel, path),
	}, true
}

// removeLegacyService unloads and deletes the pre-rename LaunchAgent, if one
// is registered. Absent is not an error.
func removeLegacyService(ctx context.Context, p Platform, runner CommandRunner) error {
	path := legacyPlistPath(p.HomeDir())
	if _, err := os.Stat(path); err != nil {
		return nil
	}

	if current, err := user.Current(); err == nil {
		// A service that was never loaded is not an error to remove.
		_, _ = runner(ctx, "launchctl", "bootout", "gui/"+current.Uid+"/"+legacyServiceLabel)
	}

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("platform: remove %s: %w", path, err)
	}
	return nil
}
