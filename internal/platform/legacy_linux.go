//go:build linux

package platform

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// legacyUnitName is the systemd --user unit the pre-rename product used
// (contracts/identity-and-rename.md: "systemd user unit").
const legacyUnitName = "agentguard.service"

// legacyDataDir mirrors defaultDataDir (dirs_linux.go) under the old name.
func legacyDataDir(home string) string {
	if base := os.Getenv("XDG_DATA_HOME"); base != "" {
		return filepath.Join(filepath.Clean(base), "agentguard")
	}
	return filepath.Join(home, ".local", "share", "agentguard")
}

// legacyConfigDir mirrors defaultConfigDir (dirs_linux.go) under the old
// name.
func legacyConfigDir(home string) string {
	if base := os.Getenv("XDG_CONFIG_HOME"); base != "" {
		return filepath.Join(filepath.Clean(base), "agentguard")
	}
	return filepath.Join(home, ".config", "agentguard")
}

// legacyRuntimeDir mirrors defaultRuntimeDir (dirs_linux.go) under the old
// name.
func legacyRuntimeDir(string) string {
	if base := os.Getenv("XDG_RUNTIME_DIR"); base != "" {
		return filepath.Join(filepath.Clean(base), "agentguard")
	}
	return filepath.Join("/tmp", fmt.Sprintf("agentguard-%d", os.Getuid()))
}

// legacyUnitPath is where the pre-rename systemd unit file lived.
func legacyUnitPath(home string) string {
	return filepath.Join(home, ".config", "systemd", "user", legacyUnitName)
}

func legacyServiceLeftover(p Platform) (LegacyLeftover, bool) {
	path := legacyUnitPath(p.HomeDir())
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		return LegacyLeftover{}, false
	}
	return LegacyLeftover{
		Kind: LegacyKindService,
		Path: path,
		Fix:  fmt.Sprintf("systemctl --user disable --now %s && rm %q", legacyUnitName, path),
	}, true
}

// removeLegacyService disables and deletes the pre-rename systemd --user
// unit, if one is registered. Absent is not an error.
func removeLegacyService(ctx context.Context, p Platform, runner CommandRunner) error {
	path := legacyUnitPath(p.HomeDir())
	if _, err := os.Stat(path); err != nil {
		return nil
	}

	// Disabling a unit that is not enabled is not an error worth reporting.
	_, _ = runner(ctx, "systemctl", "--user", "disable", "--now", legacyUnitName)

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("platform: remove %s: %w", path, err)
	}
	_, _ = runner(ctx, "systemctl", "--user", "daemon-reload")
	return nil
}
