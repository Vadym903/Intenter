package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// These tests cover the "Pre-rename install" doctor check
// (specs/005-make-product-usable/contracts/identity-and-rename.md §1,
// §2.3–2.5): doctor must find and explain every trace of a pre-rename
// AgentGuard install still on a machine, and stay quiet once none remain.

// legacyDataDirFor mirrors the pre-rename product's per-OS data directory
// (internal/platform/legacy_{darwin,linux,windows}.go) so this test can
// fabricate a leftover for doctor to find, without an unexported function
// from another package to call.
func legacyDataDirFor(home string) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "AgentGuard")
	case "windows":
		return filepath.Join(home, "AppData", "Local", "AgentGuard")
	default:
		return filepath.Join(home, ".local", "share", "agentguard")
	}
}

func TestDoctorReportsPreRenameLeftovers(t *testing.T) {
	// A variable already set on the machine running the test would otherwise
	// point legacyDataDirFor's formula somewhere outside the fixture
	// (internal/platform/legacy_test.go's legacyTestPlatform does the same).
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("LOCALAPPDATA", "")
	f := startFixture(t)

	settingsPath := filepath.Join(f.home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	legacyHook := `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[` +
		`{"type":"command","command":"/usr/local/bin/agentguard hook claude","timeout":10}` +
		`]}]}}`
	if err := os.WriteFile(settingsPath, []byte(legacyHook), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	legacyData := legacyDataDirFor(f.home)
	if err := os.MkdirAll(legacyData, 0o755); err != nil {
		t.Fatalf("mkdir legacy data dir: %v", err)
	}

	out, _, _ := f.inWorkspace(t, "doctor")
	if !strings.Contains(out, "✗ Pre-rename install") {
		t.Fatalf("doctor must mark the pre-rename check failed:\n%s", out)
	}
	if !strings.Contains(out, legacyData) {
		t.Errorf("the check must name the legacy data dir:\n%s", out)
	}
	if !strings.Contains(out, "PreToolUse") {
		t.Errorf("the check must name the legacy hook event:\n%s", out)
	}
	if !strings.Contains(out, "intenter setup claude") {
		t.Errorf("the check must say how to fix the hook entries:\n%s", out)
	}
	if !strings.Contains(out, "replaces them") {
		t.Errorf("the hook fix must explain what running setup does:\n%s", out)
	}
}

func TestDoctorPreRenameCheckIsQuietOnACleanMachine(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("LOCALAPPDATA", "")
	f := startFixture(t)

	out, _, _ := f.inWorkspace(t, "doctor", "--json")
	var report DoctorReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	for _, check := range report.Checks {
		if check.Name != "Pre-rename install" {
			continue
		}
		if !check.OK || check.Detail != "none found" {
			t.Errorf("want OK with \"none found\" on a clean machine, got OK=%v detail=%q", check.OK, check.Detail)
		}
		return
	}
	t.Error("want a Pre-rename install check in the report")
}
