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
	// Evidence says how Claude Code was found when there was no executable to
	// find — the VS Code extension, or a configuration directory it left.
	Evidence string
	// Warnings are non-fatal findings worth telling the user about.
	Warnings []string
}

// Found reports whether an agent installation was located.
//
// An executable is not required. The VS Code extension bundles its own copy of
// the CLI and deliberately does not put `claude` on PATH, so for a developer
// whose Claude Code is that extension there is no binary to find — and hooks go
// into a settings file either way. Refusing to install for them would be
// refusing the integration to the people who need it most.
func (i *Installation) Found() bool {
	return i != nil && (i.Executable != "" || i.Evidence != "")
}

// Describe names what was found, for the setup report.
func (i *Installation) Describe() string {
	switch {
	case i == nil:
		return ""
	case i.Version != "":
		return i.Version
	case i.Evidence != "":
		return "version unknown, found by " + i.Evidence
	default:
		return "version unknown"
	}
}

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
		// No binary is not the same as no Claude Code. Look for the evidence a
		// VS Code-only installation leaves instead.
		install.Evidence = findEvidence(home)
		if install.Evidence == "" {
			return install, fmt.Errorf(
				"claude: Claude Code was not found — no `claude` on PATH or in %s, "+
					"and no Claude Code extension in %s",
				filepath.Join(home, ".local", "bin"), filepath.Join(home, ".vscode", "extensions"))
		}
		install.Warnings = append(install.Warnings,
			"no `claude` on PATH, so the version could not be read; found by "+install.Evidence+
				". The hooks work either way — they are installed into the settings file both "+
				"the extension and the CLI read.")
		return install, nil
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

// editorExtensionDirs are the places an editor keeps its installed extensions.
//
// VS Code proper, its Insiders build, the server-side halves used by Remote-SSH
// and devcontainers, and the forks that install the same extension from Open
// VSX. Each is relative to the home directory.
var editorExtensionDirs = []struct {
	editor string
	path   []string
}{
	{"VS Code", []string{".vscode", "extensions"}},
	{"VS Code Insiders", []string{".vscode-insiders", "extensions"}},
	{"VS Code Remote", []string{".vscode-server", "extensions"}},
	{"VS Code Remote Insiders", []string{".vscode-server-insiders", "extensions"}},
	{"Cursor", []string{".cursor", "extensions"}},
	{"Windsurf", []string{".windsurf", "extensions"}},
}

// extensionGlob matches the published Claude Code extension, whose directory
// carries its version: `anthropic.claude-code-2.1.4`.
const extensionGlob = "anthropic.claude-code-*"

// findEvidence looks for Claude Code where there is no executable to find.
//
// The VS Code extension bundles its own CLI and does not add `claude` to PATH,
// so an extension-only machine has an editor extension directory and nothing on
// PATH at all.
//
// Only the extension directory counts. A `~/.claude` directory would be weaker
// evidence than it looks: it outlives an uninstall of Claude Code, and Intenter
// creates it itself when it writes the settings file — so accepting it would
// eventually have setup confirming its own earlier run.
func findEvidence(home string) string {
	for _, candidate := range editorExtensionDirs {
		pattern := filepath.Join(append([]string{home}, candidate.path...)...)
		matches, err := filepath.Glob(filepath.Join(pattern, extensionGlob))
		if err == nil && len(matches) > 0 {
			return "the Claude Code extension for " + candidate.editor
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
