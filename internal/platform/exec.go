package platform

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
)

// ErrExecutableNotFound is returned when a program cannot be located.
var ErrExecutableNotFound = errors.New("executable not found")

// FindExecutable resolves a program name through PATH. exec.LookPath already
// honors PATHEXT on Windows; an absolute or explicitly relative path is checked
// directly (§8.1).
func FindExecutable(name string) (string, error) {
	if name == "" {
		return "", ErrExecutableNotFound
	}
	if filepath.IsAbs(name) || containsSeparator(name) {
		resolved, err := exec.LookPath(name)
		if err != nil {
			return "", errors.Join(ErrExecutableNotFound, err)
		}
		return absOrSelf(resolved), nil
	}
	resolved, err := exec.LookPath(name)
	if err != nil {
		return "", errors.Join(ErrExecutableNotFound, err)
	}
	return absOrSelf(resolved), nil
}

// FindExecutableIn resolves a program name, additionally probing the given
// candidate paths first. Setup uses it for Claude's non-PATH install locations
// (§12.2 step 1).
func FindExecutableIn(name string, candidates []string) (string, error) {
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		if !isExecutableMode(info) {
			continue
		}
		return absOrSelf(candidate), nil
	}
	return FindExecutable(name)
}

func containsSeparator(name string) bool {
	for i := 0; i < len(name); i++ {
		if os.IsPathSeparator(name[i]) {
			return true
		}
	}
	return false
}

func absOrSelf(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

func isExecutableMode(info os.FileInfo) bool {
	if isWindows() {
		return true
	}
	return info.Mode()&0o111 != 0
}
