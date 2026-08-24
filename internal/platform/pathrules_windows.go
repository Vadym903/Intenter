//go:build windows

package platform

import (
	"os"
	"path/filepath"
	"strings"
)

// Windows paths are case-insensitive.
func osCaseInsensitive() bool { return true }

// osSystemRoots lists the Windows system roots (§16.3). \Windows is treated as
// a system root on every drive letter present in the environment.
func osSystemRoots() []string {
	roots := []string{}
	for _, key := range []string{"SystemRoot", "ProgramFiles", "ProgramFiles(x86)", "ProgramData"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			roots = append(roots, filepath.Clean(value))
		}
	}
	if drive := strings.TrimSpace(os.Getenv("SystemDrive")); drive != "" {
		roots = append(roots, filepath.Join(drive+`\`, "Recovery"))
		roots = append(roots, filepath.Join(drive+`\`, "Windows"))
	}
	if len(roots) == 0 {
		roots = append(roots, `C:\Windows`, `C:\Program Files`, `C:\ProgramData`)
	}
	return roots
}

// osTempRoots uses the per-user temp directory; Windows has no shared /tmp.
func osTempRoots(temp string) []string {
	roots := []string{}
	if temp != "" {
		roots = append(roots, filepath.Clean(temp))
	}
	for _, key := range []string{"TEMP", "TMP"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			roots = append(roots, filepath.Clean(value))
		}
	}
	return roots
}

// osStandardHomeDirs adds the Windows profile directory that counts as broad.
func osStandardHomeDirs() []string { return []string{"AppData"} }

// osSensitiveDirs adds the Windows credential store, the per-user Startup
// folder (where a dropped shortcut or script runs at every login), and the
// PowerShell profile directories (AG-123): any file there whose name matches
// $PROFILE (or a host-specific variant such as
// Microsoft.PowerShell_profile.ps1) is executed at the start of every new
// PowerShell session, and any module placed under ...\PowerShell\Modules is
// auto-loaded the first time a command with a matching name is typed — the
// same "code execution on the next shell" hazard homePersistenceFiles
// protects for bash/zsh, which these directories have no shared-platform
// equivalent of (§16.6).
func osSensitiveDirs(home string) []string {
	appData := strings.TrimSpace(os.Getenv("APPDATA"))
	if appData == "" {
		appData = filepath.Join(home, "AppData", "Roaming")
	}
	documents := filepath.Join(home, "Documents")
	return []string{
		recursivePattern(filepath.Join(appData, "Microsoft", "Credentials")),
		recursivePattern(filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs", "Startup")),
		recursivePattern(filepath.Join(documents, "WindowsPowerShell")),
		recursivePattern(filepath.Join(documents, "PowerShell")),
	}
}

// osToolCacheDirs adds the Windows package manager caches (§16.6).
func osToolCacheDirs(home string) []string {
	localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if localAppData == "" {
		localAppData = filepath.Join(home, "AppData", "Local")
	}
	return []string{
		recursivePattern(filepath.Join(localAppData, "npm-cache")),
		recursivePattern(filepath.Join(localAppData, "pnpm")),
		recursivePattern(filepath.Join(localAppData, "Yarn")),
	}
}

// isDriveRoot reports whether path is a bare drive root such as C:\.
func isDriveRoot(path string) bool {
	if len(path) == 3 && path[1] == ':' && os.IsPathSeparator(path[2]) {
		return true
	}
	// filepath.Clean("C:\\") keeps the separator; "C:" alone is a drive-relative
	// path and is treated as a root as well.
	return len(path) == 2 && path[1] == ':'
}
