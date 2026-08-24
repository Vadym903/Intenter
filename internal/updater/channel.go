package updater

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Install channels (003 data-model §7). The channel decides one thing: whether
// this tool may replace its own executable, or must ask a package manager to.
const (
	// ChannelHomebrew: brew owns the file; overwriting it corrupts brew's
	// bookkeeping and the next `brew upgrade` silently reverts us.
	ChannelHomebrew = "homebrew"
	// ChannelWinget: the same, for the Windows package manager.
	ChannelWinget = "winget"
	// ChannelScript: installed by install.sh/install.ps1 into their default
	// location, which is ours to replace.
	ChannelScript = "script"
	// ChannelManual: somewhere else the user put it — ours to replace, after
	// saying where.
	ChannelManual = "manual"
	// ChannelUnknown: the running executable could not be located at all.
	ChannelUnknown = "unknown"
)

// channelHintFile is written by the installers so a copy that was moved out of
// the default directory is still recognized as script-installed.
const channelHintFile = "install-channel"

// Install describes the copy of Intenter that is running.
type Install struct {
	// Channel is one of the constants above.
	Channel string
	// Path is the stable path hooks and service definitions reference, and the
	// file an update replaces.
	Path string
	// Resolved is Path with symlinks followed — for a Homebrew install, the
	// versioned file inside the Cellar.
	Resolved string
	// Writable reports whether the *directory* holding Path can be written,
	// which is what a rename-based swap actually needs.
	Writable bool
}

// SelfManaged reports whether the updater may replace the executable itself.
func (i Install) SelfManaged() bool {
	return i.Channel == ChannelScript || i.Channel == ChannelManual
}

// PackageManaged reports whether a package manager owns the file.
func (i Install) PackageManaged() bool {
	return i.Channel == ChannelHomebrew || i.Channel == ChannelWinget
}

// DetectInstall classifies the running copy. It never fails: an installation it
// cannot place is `unknown`, which the updater treats as "ask first".
func DetectInstall(stablePath, homeDir, dataDir string) Install {
	install := Install{Channel: ChannelUnknown, Path: stablePath}
	if strings.TrimSpace(stablePath) == "" {
		return install
	}

	install.Resolved = stablePath
	if resolved, err := filepath.EvalSymlinks(stablePath); err == nil {
		install.Resolved = resolved
	}
	install.Channel = classifyChannel(stablePath, install.Resolved, homeDir, readChannelHint(dataDir))
	install.Writable = directoryIsWritable(filepath.Dir(stablePath))
	return install
}

// classifyChannel decides the channel from paths alone, so every layout can be
// tested from any operating system. It splits on both separators for the same
// reason internal/platform does: a Windows path has to be classifiable from a
// Linux test run.
func classifyChannel(stable, resolved, homeDir, hint string) string {
	switch {
	case underElements(resolved, "Cellar"), underElements(stable, "Cellar"):
		return ChannelHomebrew
	case underElements(resolved, "Microsoft", "WinGet", "Packages"),
		underElements(stable, "Microsoft", "WinGet", "Links"),
		underElements(stable, "Microsoft", "WinGet", "Packages"):
		return ChannelWinget
	}

	if hint == ChannelScript && stable != "" {
		return ChannelScript
	}
	if isScriptInstallDir(stable, homeDir) {
		return ChannelScript
	}
	return ChannelManual
}

// scriptInstallDirs are where the one-line installers put the binary by
// default (feature 002), relative to the home directory. A copy anywhere else
// is `manual`, which behaves the same but says where it is before replacing
// anything.
var scriptInstallDirs = [][]string{
	{".local", "bin"},
	{"AppData", "Local", "Intenter", "bin"},
}

// isScriptInstallDir reports whether the executable sits in an installer
// default. It compares path *elements* rather than joined strings so a Windows
// layout can be classified from a Linux test run, the same reason
// internal/platform splits on both separators.
func isScriptInstallDir(stable, homeDir string) bool {
	elements := pathElements(stable)
	if len(elements) == 0 {
		return false
	}
	parent := elements[:len(elements)-1]

	home := pathElements(homeDir)
	if len(home) == 0 {
		return false
	}
	for _, suffix := range scriptInstallDirs {
		want := make([]string, 0, len(home)+len(suffix))
		want = append(want, home...)
		want = append(want, suffix...)
		if sameElements(parent, want) {
			return true
		}
	}
	return false
}

// underElements reports whether a path contains the given run of directory
// names in order, comparing case-insensitively because Windows and macOS both
// are.
func underElements(path string, wanted ...string) bool {
	if path == "" || len(wanted) == 0 {
		return false
	}
	elements := pathElements(path)
	for start := 0; start+len(wanted) <= len(elements); start++ {
		if sameElements(elements[start:start+len(wanted)], wanted) {
			return true
		}
	}
	return false
}

// pathElements splits a path on either separator, so paths from another
// platform can still be reasoned about.
func pathElements(path string) []string {
	return strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' })
}

func sameElements(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if !strings.EqualFold(got[i], want[i]) {
			return false
		}
	}
	return true
}

// readChannelHint returns what the installer recorded, if anything.
func readChannelHint(dataDir string) string {
	if strings.TrimSpace(dataDir) == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(dataDir, channelHintFile))
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(string(data)))
}

// directoryIsWritable reports whether a new file can be created in dir, which
// is what replacing the executable by rename requires. Checking the mode bits
// instead would be wrong on every filesystem with ACLs.
func directoryIsWritable(dir string) bool {
	if strings.TrimSpace(dir) == "" {
		return false
	}
	probe, err := os.CreateTemp(dir, ".intenter-write-*")
	if err != nil {
		return false
	}
	name := probe.Name()
	probe.Close()
	os.Remove(name)
	return true
}

// ExecutableName is what the binary is called on this platform.
func ExecutableName() string {
	if runtime.GOOS == "windows" {
		return "intenter.exe"
	}
	return "intenter"
}

// OnPath lists every copy of Intenter reachable through PATH, in the order a
// lookup would find them — so the first is what typing `intenter` runs.
//
// This matters because a machine can easily end up with two: a Homebrew install
// and a `curl | sh` one, or a leftover in ~/.local/bin after switching. An
// update replaces the copy that is *running*, and if that is not the copy on
// PATH the user updates something they never invoke.
func OnPath() []string {
	seen := map[string]bool{}
	var found []string

	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		candidate := filepath.Join(dir, ExecutableName())
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		resolved := candidate
		if target, err := filepath.EvalSymlinks(candidate); err == nil {
			resolved = target
		}
		if seen[resolved] {
			continue
		}
		seen[resolved] = true
		found = append(found, candidate)
	}
	return found
}

// Shadowing returns the copy on PATH that would run instead of this one, or an
// empty string when the running copy is the one PATH finds.
func Shadowing(stablePath string) string {
	if strings.TrimSpace(stablePath) == "" {
		return ""
	}
	onPath := OnPath()
	if len(onPath) == 0 {
		return ""
	}
	if sameFile(onPath[0], stablePath) {
		return ""
	}
	return onPath[0]
}

// sameFile compares two paths by identity rather than spelling, so a symlink
// and its target are not mistaken for two installations.
func sameFile(a, b string) bool {
	infoA, errA := os.Stat(a)
	infoB, errB := os.Stat(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return os.SameFile(infoA, infoB)
}
