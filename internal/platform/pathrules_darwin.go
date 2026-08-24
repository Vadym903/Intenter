//go:build darwin

package platform

import "path/filepath"

// macOS is case-insensitive on the default APFS volume configuration.
func osCaseInsensitive() bool { return true }

// osSystemRoots lists the macOS system roots (§16.3).
func osSystemRoots() []string {
	return []string{
		"/", "/System", "/usr", "/bin", "/sbin", "/etc", "/var", "/Library",
		"/Applications", "/private", "/dev", "/opt", "/cores",
	}
}

// osTempRoots carves the shared and per-user temp locations out of SYSTEM.
func osTempRoots(temp string) []string {
	roots := []string{"/tmp", "/private/tmp", "/var/tmp", "/private/var/tmp"}
	if temp != "" {
		roots = append(roots, filepath.Clean(temp))
	}
	return roots
}

// osStandardHomeDirs adds the macOS-specific broad home directory.
func osStandardHomeDirs() []string { return []string{"Library"} }

// osSensitiveDirs adds the macOS keychain and the per-user persistence
// locations: a plist dropped in LaunchAgents runs at every login (§16.6).
func osSensitiveDirs(home string) []string {
	return []string{
		recursivePattern(filepath.Join(home, "Library", "Keychains")),
		recursivePattern(filepath.Join(home, "Library", "LaunchAgents")),
		recursivePattern(filepath.Join(home, "Library", "LaunchDaemons")),
	}
}

// osToolCacheDirs adds ~/Library/Caches (§16.6).
func osToolCacheDirs(home string) []string {
	return []string{recursivePattern(filepath.Join(home, "Library", "Caches"))}
}

// isDriveRoot is meaningless on macOS.
func isDriveRoot(string) bool { return false }
