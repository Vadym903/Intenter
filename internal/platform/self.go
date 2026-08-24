package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// packageManagerRoots are the directory names package managers version their
// payloads under. A path containing one of them is specific to the version
// installed today, which is exactly what must not be written into a hook.
var packageManagerRoots = []string{"Cellar", "versions", "Packages"}

// SelfExecutablePath is the path of the running binary that will still be
// correct after an upgrade. Hook entries and service definitions embed it
// (§8.1, §12.1), and they are written once and read for months.
//
// Resolving symlinks is the wrong answer for a package-manager install:
// Homebrew puts `intenter` on PATH as a symlink into
// `Cellar/intenter/<version>/bin/`, and the resolved path stops existing the
// moment `brew upgrade` runs — leaving Claude with a hook that points at a
// deleted file. So the preference is:
//
//  1. the PATH entry that refers to this same file, which is the name the user
//     and the package manager both maintain;
//  2. failing that, the unresolved path when it is a symlink into a versioned
//     package-manager directory;
//  3. failing that, the resolved path, which is right for a direct install.
func SelfExecutablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("platform: locate own executable: %w", err)
	}
	return stableExecutablePath(exe)
}

// stableExecutablePath applies the preference above to a given executable path.
// It is separate from SelfExecutablePath so the package-manager layouts it
// exists for can be tested without being installed through one.
func stableExecutablePath(exe string) (string, error) {
	resolved := exe
	if target, err := filepath.EvalSymlinks(exe); err == nil {
		resolved = target
	}

	if stable, ok := pathEntryFor(exe, resolved); ok {
		return stable, nil
	}
	if exe != resolved && underPackageManagerRoot(resolved) {
		return absOrSelf(exe), nil
	}

	abs, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("platform: absolute path of %s: %w", resolved, err)
	}
	return abs, nil
}

// pathEntryFor looks for the entry in PATH that names this same binary.
//
// It compares file identity rather than paths, so it finds the entry whether it
// is the file itself, a symlink to it, or a Windows junction.
func pathEntryFor(exe, resolved string) (string, bool) {
	target, err := os.Stat(resolved)
	if err != nil {
		return "", false
	}

	base := filepath.Base(exe)
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" || !filepath.IsAbs(dir) {
			// A relative entry resolves against the working directory, which is
			// not a property of the installation.
			continue
		}
		for _, candidate := range candidateNames(base) {
			path := filepath.Join(dir, candidate)
			info, err := os.Stat(path)
			if err != nil || info.IsDir() || !os.SameFile(info, target) {
				continue
			}
			return path, true
		}
	}
	return "", false
}

// candidateNames returns the file names a PATH lookup would try. On Windows an
// executable is usually invoked without its extension.
func candidateNames(base string) []string {
	if !isWindows() || filepath.Ext(base) != "" {
		return []string{base}
	}
	names := []string{base}
	for _, ext := range windowsExecExtensions() {
		names = append(names, base+ext)
	}
	return names
}

// windowsExecExtensions reads PATHEXT, falling back to the usual defaults.
func windowsExecExtensions() []string {
	raw := os.Getenv("PATHEXT")
	if strings.TrimSpace(raw) == "" {
		raw = ".COM;.EXE;.BAT;.CMD"
	}
	var out []string
	for _, ext := range strings.Split(raw, ";") {
		if ext = strings.TrimSpace(ext); ext != "" {
			out = append(out, strings.ToLower(ext))
		}
	}
	return out
}

// underPackageManagerRoot reports whether a path lives inside a versioned
// package-manager directory, where the version is part of the path.
//
// It splits on both separators rather than the host's, so a Windows layout can
// be checked from a macOS or Linux test run — the same reason the rest of the
// codebase avoids filepath helpers when reasoning about a path that may have
// come from another platform.
func underPackageManagerRoot(path string) bool {
	for _, element := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' }) {
		for _, root := range packageManagerRoots {
			if strings.EqualFold(element, root) {
				return true
			}
		}
	}
	return false
}

func isWindows() bool { return runtime.GOOS == "windows" }
