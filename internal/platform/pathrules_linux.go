//go:build linux

package platform

import "path/filepath"

// Linux filesystems are case-sensitive.
func osCaseInsensitive() bool { return false }

// osSystemRoots lists the Linux system roots (§16.3).
func osSystemRoots() []string {
	return []string{
		"/", "/bin", "/sbin", "/usr", "/etc", "/var", "/lib", "/lib32", "/lib64",
		"/boot", "/dev", "/proc", "/sys", "/opt", "/root", "/srv", "/snap",
	}
}

// osTempRoots carves the shared and per-user temp locations out of SYSTEM.
func osTempRoots(temp string) []string {
	roots := []string{"/tmp", "/var/tmp"}
	if temp != "" {
		roots = append(roots, filepath.Clean(temp))
	}
	return roots
}

// osStandardHomeDirs adds no Linux-specific broad home directories beyond the
// shared list.
func osStandardHomeDirs() []string { return nil }

// osSensitiveDirs adds no Linux-specific credential locations beyond the
// shared list.
func osSensitiveDirs(string) []string { return nil }

// osToolCacheDirs adds no Linux-specific caches beyond ~/.cache.
func osToolCacheDirs(string) []string { return nil }

// isDriveRoot is meaningless on Linux.
func isDriveRoot(string) bool { return false }
