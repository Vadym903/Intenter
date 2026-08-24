package claude

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Vadym903/Intenter/internal/platform"
)

// VersionTimeout bounds `claude --version`; a hung agent binary must not hang
// setup (§12.2 step 1).
const VersionTimeout = 5 * time.Second

// MinimumVersion is the oldest Claude Code whose hook behavior Intenter was
// verified against (research R-10). Older versions still work; setup only
// warns, because refusing to install would be worse than a possible mismatch.
const MinimumVersion = "2.0.0"

// Installation is what setup found on this machine.
type Installation struct {
	// Executable is the resolved path to the agent, empty when not found.
	Executable string
	// Version is what `claude --version` reported, empty when unavailable.
	Version string
	// SettingsPath is the user-scope settings file hooks are installed into.
	SettingsPath string
	// ConfigDir is Claude's own directory, `~/.claude`.
	ConfigDir string
	// Warnings are non-fatal findings worth telling the user about.
	Warnings []string
}

// Found reports whether an agent installation was located.
func (i *Installation) Found() bool { return i != nil && i.Executable != "" }

// Detect locates Claude Code (§12.2 step 1).
//
// A missing agent is a plain, actionable failure rather than an error to
// interpret: there is nothing to integrate with.
func Detect(ctx context.Context, p platform.Platform, settingsOverride string) (*Installation, error) {
	home := p.HomeDir()
	if home == "" {
		return nil, fmt.Errorf("claude: no home directory could be determined")
	}

	install := &Installation{
		ConfigDir:    filepath.Join(home, ".claude"),
		SettingsPath: settingsOverride,
	}
	if install.SettingsPath == "" {
		install.SettingsPath = filepath.Join(install.ConfigDir, "settings.json")
	}

	install.Executable = findExecutable(p, home)
	if install.Executable == "" {
		return install, fmt.Errorf(
			"claude: Claude Code was not found on PATH or in %s", filepath.Join(home, ".local", "bin"))
	}

	version, err := readVersion(ctx, install.Executable)
	if err != nil {
		install.Warnings = append(install.Warnings,
			"could not read the Claude Code version: "+err.Error())
	} else {
		install.Version = version
		if older, known := olderThanMinimum(version); known && older {
			install.Warnings = append(install.Warnings, fmt.Sprintf(
				"Claude Code %s is older than the %s Intenter was verified against; "+
					"the hook behavior may differ", version, MinimumVersion))
		}
	}
	return install, nil
}

// findExecutable looks on PATH and in the locations Claude's own installer
// uses (§12.2 step 1).
func findExecutable(p platform.Platform, home string) string {
	if path, err := p.FindExecutable("claude"); err == nil && path != "" {
		return path
	}
	for _, candidate := range []string{
		filepath.Join(home, ".local", "bin", "claude"),
		filepath.Join(home, ".claude", "local", "claude"),
		filepath.Join(home, ".local", "bin", "claude.exe"),
		filepath.Join(home, ".claude", "local", "claude.exe"),
	} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

// versionPattern extracts a semantic version from whatever `--version` prints.
var versionPattern = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// readVersion runs `claude --version` under a timeout.
func readVersion(ctx context.Context, executable string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, VersionTimeout)
	defer cancel()

	output, err := exec.CommandContext(ctx, executable, "--version").Output()
	if err != nil {
		return "", err
	}
	match := versionPattern.FindString(string(output))
	if match == "" {
		return "", fmt.Errorf("no version in %q", strings.TrimSpace(string(output)))
	}
	return match, nil
}

// olderThanMinimum compares two dotted versions. The second return value is
// false when either version could not be parsed, so an unparsable version never
// produces a misleading warning.
func olderThanMinimum(version string) (older bool, known bool) {
	found, ok := parseVersion(version)
	if !ok {
		return false, false
	}
	minimum, ok := parseVersion(MinimumVersion)
	if !ok {
		return false, false
	}
	for i := range found {
		if found[i] != minimum[i] {
			return found[i] < minimum[i], true
		}
	}
	return false, true
}

// parseVersion reads major, minor and patch out of a version string.
func parseVersion(version string) ([3]int, bool) {
	match := versionPattern.FindStringSubmatch(version)
	if match == nil {
		return [3]int{}, false
	}
	var parts [3]int
	for i := 0; i < 3; i++ {
		value, err := strconv.Atoi(match[i+1])
		if err != nil {
			return [3]int{}, false
		}
		parts[i] = value
	}
	return parts, true
}

// EnsureSettingsFile creates an empty settings file when there is none, so the
// hook installer always has valid JSON to edit (§12.2 step 1).
func EnsureSettingsFile(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("claude: create the settings directory: %w", err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		return fmt.Errorf("claude: create %s: %w", path, err)
	}
	return nil
}
