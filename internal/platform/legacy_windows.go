//go:build windows

package platform

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

// legacyRunKeyValue is the Run key value name the pre-rename product used
// (contracts/identity-and-rename.md: "Windows Run key value").
const legacyRunKeyValue = "AgentGuard"

// legacyDataDir mirrors defaultDataDir (dirs_windows.go) under the old name.
func legacyDataDir(home string) string {
	if base := os.Getenv("LOCALAPPDATA"); base != "" {
		return filepath.Join(filepath.Clean(base), "AgentGuard")
	}
	return filepath.Join(home, "AppData", "Local", "AgentGuard")
}

// legacyConfigDir mirrors defaultConfigDir (dirs_windows.go) under the old
// name.
func legacyConfigDir(home string) string {
	if base := os.Getenv("APPDATA"); base != "" {
		return filepath.Join(filepath.Clean(base), "AgentGuard")
	}
	return filepath.Join(home, "AppData", "Roaming", "AgentGuard")
}

// legacyRuntimeDir mirrors defaultRuntimeDir (dirs_windows.go) under the old
// name.
func legacyRuntimeDir(home string) string {
	return filepath.Join(legacyDataDir(home), "runtime")
}

func legacyServiceLeftover(_ Platform) (LegacyLeftover, bool) {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return LegacyLeftover{}, false
	}
	defer key.Close()

	if _, _, err := key.GetStringValue(legacyRunKeyValue); err != nil {
		return LegacyLeftover{}, false
	}
	return LegacyLeftover{
		Kind: LegacyKindService,
		Path: `HKCU\` + runKeyPath + `\` + legacyRunKeyValue,
		Fix:  fmt.Sprintf(`reg delete "HKCU\%s" /v %s /f`, runKeyPath, legacyRunKeyValue),
	}, true
}

// removeLegacyService deletes the pre-rename Run key value, if one is
// registered. Absent is not an error. The runner is unused here: like the
// current Windows service manager (service_windows.go), this touches the
// registry directly rather than shelling out.
func removeLegacyService(_ context.Context, _ Platform, _ CommandRunner) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		if err == registry.ErrNotExist {
			return nil
		}
		return fmt.Errorf("platform: open the Run key: %w", err)
	}
	defer key.Close()

	if err := key.DeleteValue(legacyRunKeyValue); err != nil && err != registry.ErrNotExist {
		return fmt.Errorf("platform: remove the legacy Run key value: %w", err)
	}
	return nil
}
