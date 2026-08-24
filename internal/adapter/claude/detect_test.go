package claude

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeClaude writes an executable shim that prints a version string.
func fakeClaude(t *testing.T, dir, output string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the shim relies on a POSIX shebang")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "claude")
	script := "#!/bin/sh\necho '" + output + "'\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write shim: %v", err)
	}
	return path
}

// detectPlatform builds a platform whose home is the given directory and whose
// PATH lookup always fails, so only the well-known locations are searched.
func detectPlatform(home string) fakePlatform {
	return fakePlatform{home: home}
}

func TestDetectFindsClaudeInItsInstallLocations(t *testing.T) {
	// §12.2 step 1: Claude's own installer uses these paths, and they are not
	// always on PATH.
	locations := map[string]func(home string) string{
		"~/.local/bin":    func(home string) string { return filepath.Join(home, ".local", "bin") },
		"~/.claude/local": func(home string) string { return filepath.Join(home, ".claude", "local") },
	}

	for name, locate := range locations {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			executable := fakeClaude(t, locate(home), "2.1.233 (Claude Code)")

			install, err := Detect(context.Background(), detectPlatform(home), "")
			if err != nil {
				t.Fatalf("detect: %v", err)
			}
			if !install.Found() {
				t.Fatal("want a detected installation")
			}
			if install.Executable != executable {
				t.Errorf("executable = %q, want %q", install.Executable, executable)
			}
			if install.Version != "2.1.233" {
				t.Errorf("version = %q, want 2.1.233", install.Version)
			}
			if len(install.Warnings) != 0 {
				t.Errorf("warnings = %v, want none for a current version", install.Warnings)
			}
		})
	}
}

func TestDetectReportsAMissingInstallation(t *testing.T) {
	// Not an error to interpret: there is simply nothing to integrate with.
	home := t.TempDir()

	install, err := Detect(context.Background(), detectPlatform(home), "")
	if err == nil {
		t.Fatal("want an error when Claude Code is absent")
	}
	if install.Found() {
		t.Error("nothing should have been found")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %v, want it to say what is missing", err)
	}
	// The settings path is still reported, so setup can say where it looked.
	if install.SettingsPath == "" {
		t.Error("want the settings path even when the agent is missing")
	}
}

func TestDetectWarnsAboutAnOlderVersion(t *testing.T) {
	// A warning rather than a failure: refusing to install would be worse than
	// a possible hook-behavior difference.
	home := t.TempDir()
	fakeClaude(t, filepath.Join(home, ".local", "bin"), "1.9.0")

	install, err := Detect(context.Background(), detectPlatform(home), "")
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if !install.Found() {
		t.Fatal("an old version is still an installation")
	}
	if len(install.Warnings) == 0 {
		t.Fatal("want a warning about the version")
	}
	if !strings.Contains(install.Warnings[0], MinimumVersion) {
		t.Errorf("warning = %q, want the minimum version named", install.Warnings[0])
	}
}

func TestDetectHandlesAnUnreadableVersion(t *testing.T) {
	home := t.TempDir()
	fakeClaude(t, filepath.Join(home, ".local", "bin"), "not a version at all")

	install, err := Detect(context.Background(), detectPlatform(home), "")
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if !install.Found() {
		t.Fatal("the executable was found even if its version was not")
	}
	if install.Version != "" {
		t.Errorf("version = %q, want none", install.Version)
	}
	if len(install.Warnings) == 0 {
		t.Error("want a warning that the version could not be read")
	}
}

func TestDetectHonorsASettingsOverride(t *testing.T) {
	home := t.TempDir()
	fakeClaude(t, filepath.Join(home, ".local", "bin"), "2.1.233")
	override := filepath.Join(t.TempDir(), "custom.json")

	install, err := Detect(context.Background(), detectPlatform(home), override)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if install.SettingsPath != override {
		t.Errorf("settings path = %q, want the override", install.SettingsPath)
	}
}

func TestDetectDefaultsToTheUserSettingsFile(t *testing.T) {
	home := t.TempDir()
	fakeClaude(t, filepath.Join(home, ".local", "bin"), "2.1.233")

	install, err := Detect(context.Background(), detectPlatform(home), "")
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	want := filepath.Join(home, ".claude", "settings.json")
	if install.SettingsPath != want {
		t.Errorf("settings path = %q, want %q", install.SettingsPath, want)
	}
	if install.ConfigDir != filepath.Join(home, ".claude") {
		t.Errorf("config dir = %q", install.ConfigDir)
	}
}

func TestVersionComparison(t *testing.T) {
	tests := []struct {
		version string
		older   bool
		known   bool
	}{
		{"2.1.233", false, true},
		{"2.0.0", false, true},
		{"1.9.99", true, true},
		{"1.0.0", true, true},
		{"3.0.0", false, true},
		{"2.0.1", false, true},
		{"garbage", false, false},
		{"", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			older, known := olderThanMinimum(tt.version)
			if known != tt.known {
				t.Fatalf("known = %v, want %v", known, tt.known)
			}
			if known && older != tt.older {
				t.Errorf("older = %v, want %v", older, tt.older)
			}
		})
	}
}

func TestEnsureSettingsFileIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")

	if err := EnsureSettingsFile(path); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"model":"claude-opus-5"}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := EnsureSettingsFile(path); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(content), "claude-opus-5") {
		t.Errorf("an existing settings file must not be replaced, got %s", content)
	}
}
