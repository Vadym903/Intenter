package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Vadym903/Intenter/internal/adapter/claude"
)

// Doctor exists for the moment something is wrong and the user cannot tell
// what, so every failing check has to say what to do about it.

func TestDoctorReportsEveryCheck(t *testing.T) {
	f := startFixture(t)

	out, _, _ := f.inWorkspace(t, "doctor")
	for _, want := range []string{
		"Intenter doctor",
		"Binary path", "Configuration", "Database", "Daemon",
		"Service", "Endpoint", "Claude Code", "Hooks", "Agent command", "Pre-rename install",
		"Settings backup",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor is missing the %q check:\n%s", want, out)
		}
	}
}

// An upgrade from a version before `/intenter` existed leaves an installation
// with no command at all. Nothing rewrites it automatically, so doctor is what
// makes the gap visible — and it has to name the fix, not just the fault.
func TestDoctorReportsAMissingOrStaleAgentCommand(t *testing.T) {
	f := startFixture(t)

	out, _, _ := f.inWorkspace(t, "doctor")
	if !strings.Contains(out, "missing or out of date") {
		t.Errorf("a missing /intenter must be reported:\n%s", out)
	}
	if !strings.Contains(out, "intenter setup claude") {
		t.Errorf("the report must name the command that fixes it:\n%s", out)
	}

	// With the file in place and current, the check passes and says where it is.
	configDir := filepath.Join(f.home, ".claude")
	if _, err := installSkillForTest(configDir, f.home); err != nil {
		t.Fatalf("install the skill: %v", err)
	}
	out, _, _ = f.inWorkspace(t, "doctor")
	if !strings.Contains(out, "/intenter at ") {
		t.Errorf("an installed command must be reported as present:\n%s", out)
	}
}

func TestDoctorJSONShape(t *testing.T) {
	f := startFixture(t)

	out, _, _ := f.inWorkspace(t, "doctor", "--json")
	var report DoctorReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("doctor --json is not valid JSON: %v\n%s", err, out)
	}
	if len(report.Checks) == 0 {
		t.Fatal("want checks in the report")
	}
	for _, check := range report.Checks {
		if check.Name == "" {
			t.Error("every check must have a name")
		}
		if !check.OK && check.Fix == "" {
			t.Errorf("check %q failed without saying how to fix it", check.Name)
		}
	}
}

func TestDoctorEveryFailingCheckSuggestsAFix(t *testing.T) {
	// The property that makes doctor worth running.
	isolate(t)

	out, _, _ := runCLI(t, "doctor", "--json")
	var report DoctorReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	if report.OK {
		t.Skip("nothing failed in this environment")
	}

	failed := 0
	for _, check := range report.Checks {
		if check.OK {
			continue
		}
		failed++
		if check.Fix == "" {
			t.Errorf("check %q failed without a fix: %s", check.Name, check.Detail)
		}
	}
	if failed == 0 {
		t.Error("the report says it failed but no check did")
	}
}

func TestDoctorReportsPathsLeftBehindByAnUpgrade(t *testing.T) {
	// A hook entry is written once and read for months. When an upgrade moves
	// the binary underneath it, Claude reports a hook error and carries on
	// ungated — quietly, which is why doctor has to say it out loud.
	f := startFixture(t)

	settingsPath := filepath.Join(f.home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	stale := `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[` +
		`{"type":"command","command":"\"/opt/homebrew/Cellar/intenter/0.1.0/bin/intenter\" hook claude","timeout":10}` +
		`]}]}}`
	if err := os.WriteFile(settingsPath, []byte(stale), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	out, _, _ := f.inWorkspace(t, "doctor")
	if !strings.Contains(out, "Installed paths") {
		t.Fatalf("doctor is missing the installed-paths check:\n%s", out)
	}
	if !strings.Contains(out, "Cellar/intenter/0.1.0") {
		t.Errorf("the check must name the stale path so the user can see what happened:\n%s", out)
	}
	if !strings.Contains(out, "intenter setup claude") {
		t.Errorf("the check must say how to fix it:\n%s", out)
	}
}

func TestDoctorIsQuietWhenThePathsAgree(t *testing.T) {
	// The same check must not nag when nothing is wrong, which is the only
	// reason anyone keeps running it.
	f := startFixture(t)

	out, _, _ := f.inWorkspace(t, "doctor", "--json")
	var report DoctorReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	for _, check := range report.Checks {
		if check.Name != "Installed paths" {
			continue
		}
		if !check.OK {
			t.Errorf("installed paths reported a problem with nothing installed: %s", check.Detail)
		}
		return
	}
	t.Error("want an installed-paths check in the report")
}

func TestDoctorReportsAnUnreachableDaemon(t *testing.T) {
	isolate(t)

	out, _, code := runCLI(t, "doctor")
	if code == ExitOK {
		t.Fatal("doctor must fail when the daemon is not reachable")
	}
	if !strings.Contains(out, "✗ Daemon") {
		t.Errorf("the daemon check must be marked failed:\n%s", out)
	}
	if !strings.Contains(out, "intenter daemon start") {
		t.Errorf("doctor must say how to start the daemon:\n%s", out)
	}
}

func TestDoctorReportsMissingHooks(t *testing.T) {
	f := startFixture(t)

	out, _, _ := f.inWorkspace(t, "doctor")
	if !strings.Contains(out, "Hooks") {
		t.Fatalf("want a hooks check:\n%s", out)
	}
	if !strings.Contains(out, "intenter setup claude") {
		t.Errorf("a missing integration must point at setup:\n%s", out)
	}
}

func TestDoctorPassesWithAHealthyDaemon(t *testing.T) {
	f := startFixture(t)

	out, _, _ := f.inWorkspace(t, "doctor")
	for _, want := range []string{"✓ Daemon", "✓ Database"} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in a healthy report:\n%s", want, out)
		}
	}
}

func TestStatusReportsWhatIsTrustedAndRecent(t *testing.T) {
	f := startFixture(t)
	first := f.evaluate(t, "npm run cleanup", "toolu_1")
	f.inWorkspace(t, "approve", itoa(*first.AuditEventID))
	f.evaluate(t, "rm -rf ~/Documents", "toolu_2")
	f.evaluate(t, "npm run cleanup", "toolu_3")

	out, _, code := f.inWorkspace(t, "status")
	if code != ExitOK {
		t.Fatalf("exit code = %d\n%s", code, out)
	}
	for _, want := range []string{
		"Intenter", "daemon", "endpoint", "database",
		"Trusted here", "active", "Last 24 hours", "allowed", "asked", "blocked",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("status is missing %q:\n%s", want, out)
		}
	}
}

func TestStatusJSONShape(t *testing.T) {
	f := startFixture(t)
	f.evaluate(t, "git status", "toolu_1")

	out, _, code := f.inWorkspace(t, "status", "--json")
	if code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("status --json is not valid JSON: %v\n%s", err, out)
	}
	for _, key := range []string{"daemon", "counts"} {
		if _, present := decoded[key]; !present {
			t.Errorf("status --json is missing %q:\n%s", key, out)
		}
	}
}

func TestStatusNeedsTheDaemon(t *testing.T) {
	isolate(t)

	_, errOut, code := runCLI(t, "status")
	if code != ExitDaemonUnreached {
		t.Errorf("exit code = %d, want %d", code, ExitDaemonUnreached)
	}
	if !strings.Contains(errOut, "daemon") {
		t.Errorf("stderr = %q", errOut)
	}
}

// Telling a user to install what they already have sends them in a circle.
// Someone whose Claude Code is the VS Code extension has no `claude` on PATH,
// and doctor has to name what it found instead of declaring it missing.
func TestDoctorNamesTheVSCodeExtensionInsteadOfAskingForAnInstall(t *testing.T) {
	f := startFixture(t)
	extension := filepath.Join(f.home, ".vscode", "extensions", "anthropic.claude-code-2.1.4")
	if err := os.MkdirAll(extension, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// This is the whole point of the case: no `claude` to find. The machine
	// running the tests may well have one, so take it out of reach.
	t.Setenv("PATH", t.TempDir())

	out, _, _ := f.inWorkspace(t, "doctor")
	if !strings.Contains(out, "Claude Code") {
		t.Fatalf("doctor must report the Claude Code check:\n%s", out)
	}
	if strings.Contains(out, "install Claude Code") {
		t.Errorf("doctor told a user with the extension to install Claude Code:\n%s", out)
	}
	if !strings.Contains(out, "extension for VS Code") {
		t.Errorf("doctor must name what it found:\n%s", out)
	}
}

// installSkillForTest writes the real `/intenter` command, the way setup does.
func installSkillForTest(configDir, dataDir string) (claude.SkillInstall, error) {
	return claude.InstallSkill(configDir, dataDir, SkillActions(), time.Now())
}
