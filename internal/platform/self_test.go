package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A hook entry is written once by `intenter setup claude` and read by Claude
// Code for months. If it records the path of today's binary rather than the
// path the installation maintains, the next upgrade silently breaks the
// integration — Claude keeps calling a file that no longer exists.
//
// The case that makes this real is Homebrew: `intenter` is on PATH as a
// symlink into `Cellar/intenter/<version>/bin/`, and `brew upgrade` removes
// that version directory.

// cellarLayout builds a Homebrew-shaped installation and returns the PATH
// symlink and the versioned file it points at.
func cellarLayout(t *testing.T, version string) (link, target string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs elevation on Windows; the junction case is covered separately")
	}

	prefix := t.TempDir()
	binDir := filepath.Join(prefix, "bin")
	cellarBin := filepath.Join(prefix, "Cellar", "intenter", version, "bin")

	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(cellarBin, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	target = filepath.Join(cellarBin, "intenter")
	if err := os.WriteFile(target, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}

	link = filepath.Join(binDir, "intenter")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	return link, target
}

func TestStablePathPrefersThePathEntry(t *testing.T) {
	link, target := cellarLayout(t, "0.1.0")
	t.Setenv("PATH", filepath.Dir(link))

	// However the binary was invoked, the answer is the name PATH knows.
	for _, invoked := range []string{link, target} {
		got, err := stableExecutablePath(invoked)
		if err != nil {
			t.Fatalf("stableExecutablePath(%s): %v", invoked, err)
		}
		if got != link {
			t.Errorf("invoked as %s → %s, want the PATH entry %s", invoked, got, link)
		}
	}
}

func TestStablePathSurvivesAHomebrewUpgrade(t *testing.T) {
	// The regression this whole rule exists for: the path recorded before an
	// upgrade must still name the binary afterwards.
	link, _ := cellarLayout(t, "0.1.0")
	t.Setenv("PATH", filepath.Dir(link))

	before, err := stableExecutablePath(link)
	if err != nil {
		t.Fatalf("before: %v", err)
	}

	// `brew upgrade`: a new version directory, the old one removed, relinked.
	prefix := filepath.Dir(filepath.Dir(link))
	newBin := filepath.Join(prefix, "Cellar", "intenter", "0.2.0", "bin")
	if err := os.MkdirAll(newBin, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	newTarget := filepath.Join(newBin, "intenter")
	if err := os.WriteFile(newTarget, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(prefix, "Cellar", "intenter", "0.1.0")); err != nil {
		t.Fatalf("remove old version: %v", err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	if err := os.Symlink(newTarget, link); err != nil {
		t.Fatalf("relink: %v", err)
	}

	if _, err := os.Stat(before); err != nil {
		t.Fatalf("the recorded path %s did not survive the upgrade: %v", before, err)
	}

	after, err := stableExecutablePath(link)
	if err != nil {
		t.Fatalf("after: %v", err)
	}
	if after != before {
		t.Errorf("path changed across the upgrade: %s → %s", before, after)
	}
}

func TestStablePathAcrossTwoCellarVersions(t *testing.T) {
	// The Homebrew case in full: two versions installed side by side, the
	// symlink moved from one to the other, exactly as `brew upgrade` leaves
	// things before it cleans up the old version.
	//
	// A hook written from the first version must still name the binary after
	// the second, or Claude Code calls a file that is about to be deleted.
	link, firstTarget := cellarLayout(t, "0.1.0")
	t.Setenv("PATH", filepath.Dir(link))

	before, err := stableExecutablePath(link)
	if err != nil {
		t.Fatalf("before: %v", err)
	}
	if before == firstTarget {
		t.Fatalf("recorded the versioned path %s; an upgrade would invalidate it", before)
	}

	prefix := filepath.Dir(filepath.Dir(link))
	secondBin := filepath.Join(prefix, "Cellar", "intenter", "0.2.0", "bin")
	if err := os.MkdirAll(secondBin, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	secondTarget := filepath.Join(secondBin, "intenter")
	if err := os.WriteFile(secondTarget, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Relink, both versions still present.
	if err := os.Remove(link); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	if err := os.Symlink(secondTarget, link); err != nil {
		t.Fatalf("relink: %v", err)
	}

	after, err := stableExecutablePath(link)
	if err != nil {
		t.Fatalf("after: %v", err)
	}
	if after != before {
		t.Errorf("the recorded path changed across the upgrade: %s → %s", before, after)
	}

	// And once brew removes the old version, the recorded path still resolves.
	if err := os.RemoveAll(filepath.Join(prefix, "Cellar", "intenter", "0.1.0")); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(before)
	if err != nil {
		t.Fatalf("the recorded path %s no longer resolves: %v", before, err)
	}
	if resolved != canonical(secondTarget) {
		t.Errorf("the recorded path resolves to %s, want the new version %s",
			resolved, canonical(secondTarget))
	}
}

func TestStablePathFallsBackToTheSymlinkOutsidePath(t *testing.T) {
	// A package-manager install that is not on this process's PATH — a service
	// started with a minimal environment, for instance. The versioned target is
	// still the wrong answer, so the symlink wins.
	link, target := cellarLayout(t, "0.1.0")
	t.Setenv("PATH", t.TempDir())

	got, err := stableExecutablePath(link)
	if err != nil {
		t.Fatalf("stableExecutablePath: %v", err)
	}
	if got != link {
		t.Errorf("got %s, want the symlink %s rather than the versioned %s", got, link, target)
	}
}

func TestStablePathResolvesAnOrdinarySymlink(t *testing.T) {
	// Outside a package manager a symlink carries no upgrade promise, so the
	// real file is the safer record.
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs elevation on Windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "intenter-real")
	if err := os.WriteFile(target, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	link := filepath.Join(dir, "intenter")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	t.Setenv("PATH", t.TempDir())

	got, err := stableExecutablePath(link)
	if err != nil {
		t.Fatalf("stableExecutablePath: %v", err)
	}
	// canonical is the package's own resolver: on macOS a temp directory lives
	// under /var, which is itself a link to /private/var.
	if got != canonical(target) {
		t.Errorf("got %s, want the resolved %s", got, canonical(target))
	}
}

func TestStablePathOfADirectInstall(t *testing.T) {
	// `curl | sh` puts a real file in ~/.local/bin. There is nothing to prefer,
	// and the answer must be that file whether or not it is on PATH.
	dir := t.TempDir()
	exe := filepath.Join(dir, "intenter")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}

	tests := map[string]struct {
		path string
		want string
	}{
		// On PATH the entry itself is returned, as the user spelled it.
		"on PATH": {dir, exe},
		// Otherwise the resolved path, which on macOS means /var → /private/var.
		"not on PATH": {t.TempDir(), canonical(exe)},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv("PATH", tc.path)
			got, err := stableExecutablePath(exe)
			if err != nil {
				t.Fatalf("stableExecutablePath: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestStablePathIgnoresRelativePathEntries(t *testing.T) {
	// A relative PATH entry resolves against the working directory, which is
	// not a property of the installation and would be meaningless in a hook.
	dir := t.TempDir()
	exe := filepath.Join(dir, "intenter")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}

	working, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(working) })
	t.Setenv("PATH", ".")

	got, err := stableExecutablePath(exe)
	if err != nil {
		t.Fatalf("stableExecutablePath: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("got %q, want an absolute path", got)
	}
}

func TestPackageManagerRootsAreRecognized(t *testing.T) {
	versioned := map[string]string{
		"homebrew":        "/opt/homebrew/Cellar/intenter/0.1.0/bin/intenter",
		"linuxbrew":       "/home/linuxbrew/.linuxbrew/Cellar/intenter/0.1.0/bin/intenter",
		"asdf-style":      "/home/u/.asdf/installs/intenter/versions/0.1.0/bin/intenter",
		"winget packages": `C:\Users\u\AppData\Local\Microsoft\WinGet\Packages\Intenter.Intenter\intenter.exe`,
	}
	for name, path := range versioned {
		if !underPackageManagerRoot(path) {
			t.Errorf("%s: %q must be recognized as versioned", name, path)
		}
	}

	stable := map[string]string{
		"local bin":  "/home/u/.local/bin/intenter",
		"system bin": "/usr/local/bin/intenter",
		"windows":    `C:\Users\u\AppData\Local\Intenter\bin\intenter.exe`,
	}
	for name, path := range stable {
		if underPackageManagerRoot(path) {
			t.Errorf("%s: %q must not be treated as versioned", name, path)
		}
	}
}

func TestSelfExecutablePathIsAbsoluteAndReal(t *testing.T) {
	// The running test binary: whatever the layout, the answer must name a file
	// that exists, because a hook is written from it.
	got, err := SelfExecutablePath()
	if err != nil {
		t.Fatalf("SelfExecutablePath: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("got %q, want an absolute path", got)
	}
	if _, err := os.Stat(got); err != nil {
		t.Errorf("%q does not exist: %v", got, err)
	}
	if strings.TrimSpace(got) != got {
		t.Errorf("got %q, want no surrounding whitespace", got)
	}
}
