package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Vadym903/Intenter/internal/config"
	"github.com/Vadym903/Intenter/internal/platform"
)

// setupFixture is an isolated installation with a fake Claude Code.
type setupFixture struct {
	platform     platform.Platform
	settingsPath string
}

func newSetupFixture(t *testing.T, settings string) *setupFixture {
	t.Helper()

	base := t.TempDir()
	home := filepath.Join(base, "home")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	fakeClaude(t, filepath.Join(home, ".local", "bin"), "2.1.233")

	settingsPath := filepath.Join(home, ".claude", "settings.json")
	if settings != "" {
		if err := os.WriteFile(settingsPath, []byte(settings), 0o600); err != nil {
			t.Fatalf("write settings: %v", err)
		}
	}

	return &setupFixture{
		platform: fakePlatform{
			home:       home,
			dataDir:    filepath.Join(base, "data"),
			runtimeDir: filepath.Join(base, "run"),
			executable: filepath.Join(base, "bin", "intenter"),
		},
		settingsPath: settingsPath,
	}
}

// runSetup runs the steps up to but not including the ones that need a live
// daemon, which the e2e scenario covers against the real binary.
func (f *setupFixture) runSetup(t *testing.T, options SetupOptions) *SetupResult {
	t.Helper()

	setup := NewSetup(f.platform, config.Default(), platform.NewUnmanagedService(), options)
	setup.now = func() time.Time { return time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC) }

	result, _ := setup.Run(context.Background())
	return result
}

func TestSetupDryRunChangesNothing(t *testing.T) {
	// --dry-run has to be trustworthy: a user runs it precisely because they
	// are not ready to have their settings edited.
	f := newSetupFixture(t, userSettings)
	before, err := os.ReadFile(f.settingsPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	result := f.runSetup(t, SetupOptions{DryRun: true})
	if len(result.Steps) == 0 {
		t.Fatal("want steps to be reported")
	}

	after, err := os.ReadFile(f.settingsPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("the settings were modified during a dry run:\n%s", after)
	}
	if _, err := os.Stat(filepath.Join(f.platform.DataDir(), "intenter.db")); err == nil {
		t.Error("a dry run must not create the database")
	}
	if _, err := os.Stat(filepath.Join(f.platform.DataDir(), "backups")); err == nil {
		t.Error("a dry run must not write a backup")
	}
}

func TestSetupDryRunStillReportsWhatItWouldDo(t *testing.T) {
	f := newSetupFixture(t, userSettings)
	result := f.runSetup(t, SetupOptions{DryRun: true})

	report := ""
	for _, step := range result.Steps {
		report += step.String() + "\n"
	}
	for _, want := range []string{"Claude Code detected", "2.1.233", "would write", "would create"} {
		if !strings.Contains(report, want) {
			t.Errorf("the dry-run report must mention %q:\n%s", want, report)
		}
	}
}

func TestSetupDetectsAndReportsTheVersion(t *testing.T) {
	f := newSetupFixture(t, `{}`)
	result := f.runSetup(t, SetupOptions{DryRun: true})

	if result.Installation == nil || !result.Installation.Found() {
		t.Fatal("want a detected installation")
	}
	if result.Installation.Version != "2.1.233" {
		t.Errorf("version = %q", result.Installation.Version)
	}
	if !result.Steps[0].OK() {
		t.Errorf("detection failed: %v", result.Steps[0].Err)
	}
}

func TestSetupFailsClearlyWithoutClaude(t *testing.T) {
	// Nothing to integrate with is a plain, actionable failure — and setup
	// must stop rather than half-configure the machine.
	base := t.TempDir()
	home := filepath.Join(base, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	fixture := &setupFixture{platform: fakePlatform{
		home:    home,
		dataDir: filepath.Join(base, "data"),
	}}

	result := fixture.runSetup(t, SetupOptions{})
	if !result.Failed() {
		t.Fatal("setup must fail when Claude Code is absent")
	}
	if len(result.Steps) != 1 {
		t.Errorf("steps = %d, want setup to stop at the first failure", len(result.Steps))
	}
	if _, err := os.Stat(filepath.Join(base, "data", "intenter.db")); err == nil {
		t.Error("nothing may be created after a failed step")
	}
}

func TestSetupBacksUpBeforeEditing(t *testing.T) {
	// I-9: the backup exists before the hooks are written, so a user can
	// always get back to what they had.
	f := newSetupFixture(t, userSettings)
	result := f.runSetup(t, SetupOptions{})

	if result.BackupPath == "" {
		t.Fatal("want a backup path")
	}
	content, err := os.ReadFile(result.BackupPath)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(content) != userSettings {
		t.Error("the backup must hold the settings as they were before setup")
	}
}

func TestSetupInstallsHooksAndPreservesTheRest(t *testing.T) {
	f := newSetupFixture(t, userSettings)
	f.runSetup(t, SetupOptions{})

	installed, err := HooksInstalled(f.settingsPath)
	if err != nil {
		t.Fatalf("read hooks: %v", err)
	}
	if len(installed) != len(hookEvents) {
		t.Errorf("installed = %v, want %d hooks", installed, len(hookEvents))
	}

	tree := readTree(t, f.settingsPath)
	if tree["model"] != "claude-opus-5" {
		t.Error("the user's own settings must survive setup")
	}
	permissions, _ := tree["permissions"].(map[string]any)
	if permissions == nil {
		t.Fatal("the user's permissions must survive setup")
	}
}

func TestSetupIsIdempotent(t *testing.T) {
	// §12.4: running setup twice converges rather than accumulating.
	f := newSetupFixture(t, userSettings)

	f.runSetup(t, SetupOptions{})
	first := readTree(t, f.settingsPath)

	f.runSetup(t, SetupOptions{})
	second := readTree(t, f.settingsPath)

	if !reflect.DeepEqual(first, second) {
		firstJSON, _ := json.MarshalIndent(first, "", "  ")
		secondJSON, _ := json.MarshalIndent(second, "", "  ")
		t.Errorf("setup is not idempotent:\n%s\n---\n%s", firstJSON, secondJSON)
	}
}

func TestSetupInventoriesExistingPermissionsWithoutApprovingThem(t *testing.T) {
	// §12.2 step 6 and I-8: setup reports what the agent already permits, and
	// creates nothing. A rule becomes trust only at first use, after the
	// command has actually been resolved.
	f := newSetupFixture(t, `{
	  "permissions": {
	    "allow": ["Bash(npm run test)", "Bash(git status)", "Bash(npm run:*)"]
	  }
	}`)

	setup := NewSetup(f.platform, config.Default(), platform.NewUnmanagedService(), SetupOptions{})
	result := &SetupResult{}
	step := setup.inventoryRules(context.Background(), result)

	if !step.OK() {
		t.Fatalf("inventory failed: %v", step.Err)
	}
	if result.RuleInventory.Total() != 3 {
		t.Errorf("inventory = %+v, want three rules", result.RuleInventory)
	}
	if result.RuleInventory.Exact != 2 || result.RuleInventory.Prefix != 1 {
		t.Errorf("inventory = %+v, want two exact and one pattern", result.RuleInventory)
	}
	if !strings.Contains(step.Detail, "imported at first use") {
		t.Errorf("detail = %q, want it to say when a rule becomes trust", step.Detail)
	}

	// Counting is all that happens: converting a string rule here would be
	// granting trust without ever resolving the command (I-8).
	if _, err := os.Stat(filepath.Join(f.platform.DataDir(), "intenter.db")); err == nil {
		t.Error("the inventory step must not create anything")
	}
}

func TestInventoryOfAMachineWithNoRules(t *testing.T) {
	f := newSetupFixture(t, `{"model":"claude-opus-5"}`)

	setup := NewSetup(f.platform, config.Default(), platform.NewUnmanagedService(), SetupOptions{})
	result := &SetupResult{}
	step := setup.inventoryRules(context.Background(), result)

	if !step.OK() {
		t.Fatalf("inventory failed: %v", step.Err)
	}
	if result.RuleInventory.Total() != 0 {
		t.Errorf("inventory = %+v, want none", result.RuleInventory)
	}
	if !strings.Contains(step.Detail, "no existing shell permissions") {
		t.Errorf("detail = %q", step.Detail)
	}
}

func TestSetupCreatesAMissingSettingsFile(t *testing.T) {
	f := newSetupFixture(t, "")
	f.runSetup(t, SetupOptions{})

	installed, err := HooksInstalled(f.settingsPath)
	if err != nil {
		t.Fatalf("read hooks: %v", err)
	}
	if len(installed) != len(hookEvents) {
		t.Errorf("installed = %v, want %d hooks in a newly created file", installed, len(hookEvents))
	}
}

func TestSetupRefusesToTouchUnparsableSettings(t *testing.T) {
	const broken = `{"model": "claude-opus-5",`
	f := newSetupFixture(t, broken)

	result := f.runSetup(t, SetupOptions{})
	if !result.Failed() {
		t.Fatal("setup must fail rather than rewrite a file it cannot parse")
	}

	content, err := os.ReadFile(f.settingsPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(content) != broken {
		t.Errorf("the settings were modified:\n%s", content)
	}
}

func TestSetupStepsCarryTheirDuration(t *testing.T) {
	// The report shows how long each step took, so a slow one is visible
	// rather than looking like a hang (§12.2 step 8).
	f := newSetupFixture(t, `{}`)
	result := f.runSetup(t, SetupOptions{DryRun: true})

	for _, step := range result.Steps {
		if !strings.Contains(step.String(), "s)") {
			t.Errorf("step %q must report its duration: %s", step.Name, step.String())
		}
	}
}

func TestUninstallRestoresTheSettings(t *testing.T) {
	f := newSetupFixture(t, userSettings)
	before := readTree(t, f.settingsPath)

	f.runSetup(t, SetupOptions{})

	uninstall := NewUninstall(f.platform, config.Default(), platform.NewUnmanagedService(),
		UninstallOptions{})
	uninstall.now = func() time.Time { return time.Date(2026, 8, 16, 13, 0, 0, 0, time.UTC) }
	result, err := uninstall.Run(context.Background())
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if result.Failed() {
		t.Fatalf("uninstall reported a failure: %+v", result.Steps)
	}

	after := readTree(t, f.settingsPath)
	if !reflect.DeepEqual(before, after) {
		beforeJSON, _ := json.MarshalIndent(before, "", "  ")
		afterJSON, _ := json.MarshalIndent(after, "", "  ")
		t.Errorf("uninstall did not restore the settings:\nbefore:\n%s\nafter:\n%s", beforeJSON, afterJSON)
	}
}

func TestUninstallKeepsTheDataByDefault(t *testing.T) {
	// The approval history is the record of what the user consented to; losing
	// it on an ordinary uninstall would be a surprise.
	f := newSetupFixture(t, userSettings)
	f.runSetup(t, SetupOptions{})

	dataDir := f.platform.DataDir()
	if _, err := os.Stat(dataDir); err != nil {
		t.Skipf("no data directory was created: %v", err)
	}

	uninstall := NewUninstall(f.platform, config.Default(), platform.NewUnmanagedService(),
		UninstallOptions{})
	if _, err := uninstall.Run(context.Background()); err != nil {
		t.Fatalf("uninstall: %v", err)
	}

	if _, err := os.Stat(dataDir); err != nil {
		t.Errorf("the data directory must survive an ordinary uninstall: %v", err)
	}
}

func TestUninstallPurgeRemovesTheData(t *testing.T) {
	f := newSetupFixture(t, userSettings)
	f.runSetup(t, SetupOptions{})

	dataDir := f.platform.DataDir()
	uninstall := NewUninstall(f.platform, config.Default(), platform.NewUnmanagedService(),
		UninstallOptions{Purge: true})
	if _, err := uninstall.Run(context.Background()); err != nil {
		t.Fatalf("uninstall: %v", err)
	}

	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Errorf("--purge must remove the data directory, got %v", err)
	}

	// The settings are still the user's own, which is the point of purging
	// only Intenter's data.
	if _, err := os.Stat(f.settingsPath); err != nil {
		t.Errorf("the Claude settings must survive --purge: %v", err)
	}
}

func TestUninstallWithoutAnInstallationIsSafe(t *testing.T) {
	f := newSetupFixture(t, userSettings)
	before := readTree(t, f.settingsPath)

	uninstall := NewUninstall(f.platform, config.Default(), platform.NewUnmanagedService(),
		UninstallOptions{})
	if _, err := uninstall.Run(context.Background()); err != nil {
		t.Fatalf("uninstall: %v", err)
	}

	after := readTree(t, f.settingsPath)
	if !reflect.DeepEqual(before, after) {
		t.Error("uninstalling what was never installed must change nothing")
	}
}
