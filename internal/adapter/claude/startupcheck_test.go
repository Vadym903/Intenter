package claude

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Vadym903/Intenter/internal/config"
	"github.com/Vadym903/Intenter/internal/platform"
	"github.com/Vadym903/Intenter/internal/updater"
)

// The start-up check is what makes the update prompt appear at all. Setup adds
// it and uninstall takes it away, and the file it lives in belongs to the user
// — so what happens to the rest of that file matters as much as the block.

// runUninstall runs the removal steps against a fixture.
func (f *setupFixture) runUninstall(t *testing.T, options UninstallOptions) *UninstallResult {
	t.Helper()

	uninstall := NewUninstall(f.platform, config.Default(), platform.NewUnmanagedService(), options)
	uninstall.now = func() time.Time { return time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC) }

	result, _ := uninstall.Run(context.Background())
	return result
}

// zshrc is the start-up file these tests watch.
func (f *setupFixture) zshrc() string {
	return filepath.Join(f.platform.HomeDir(), ".zshrc")
}

func stepNamed(steps []Step, name string) (Step, bool) {
	for _, step := range steps {
		if step.Name == name {
			return step, true
		}
	}
	return Step{}, false
}

func TestSetupInstallsTheStartupCheck(t *testing.T) {
	f := newSetupFixture(t, userSettings)
	if err := os.WriteFile(f.zshrc(), []byte("export PS1='> '\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("SHELL", "/bin/zsh")

	result := f.runSetup(t, SetupOptions{})

	step, ok := stepNamed(result.Steps, "Start-up update check")
	if !ok {
		t.Fatalf("setup has no start-up check step: %+v", result.Steps)
	}
	if !step.OK() {
		t.Fatalf("the step failed: %v", step.Err)
	}
	if len(result.StartupCheckFiles) == 0 {
		t.Error("the result must say which files were written")
	}

	content, err := os.ReadFile(f.zshrc())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(content), updater.MarkerBegin) {
		t.Errorf(".zshrc has no block:\n%s", content)
	}
	if !strings.Contains(string(content), "update --startup") {
		t.Errorf("the block does not call the check:\n%s", content)
	}
}

func TestUninstallRemovesTheStartupCheck(t *testing.T) {
	// SC-007: everything outside the block comes back exactly as it was.
	f := newSetupFixture(t, userSettings)
	original := "export PS1='> '\n# my own comment\n"
	if err := os.WriteFile(f.zshrc(), []byte(original), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("SHELL", "/bin/zsh")

	f.runSetup(t, SetupOptions{})
	if !strings.Contains(readString(t, f.zshrc()), updater.MarkerBegin) {
		t.Fatal("the block was not installed")
	}

	result := f.runUninstall(t, UninstallOptions{})
	step, ok := stepNamed(result.Steps, "Start-up update check removed")
	if !ok {
		t.Fatalf("uninstall has no removal step: %+v", result.Steps)
	}
	if !step.OK() {
		t.Fatalf("the step failed: %v", step.Err)
	}

	if got := readString(t, f.zshrc()); got != original {
		t.Errorf("the file did not come back:\nwant %q\ngot  %q", original, got)
	}
}

func TestUninstallLeavesNothingRunningAtTerminalStart(t *testing.T) {
	// A leftover block calling a binary that is gone would print an error in
	// every new terminal, forever, from a tool the user believes they removed.
	f := newSetupFixture(t, userSettings)
	t.Setenv("SHELL", "/bin/zsh")
	f.runSetup(t, SetupOptions{})
	f.runUninstall(t, UninstallOptions{})

	check := &updater.StartupCheck{Home: f.platform.HomeDir()}
	if installed := check.Status(nil).Installed; len(installed) != 0 {
		t.Errorf("the block survives in %v", installed)
	}
}

func TestSetupRespectsNoStartupCheck(t *testing.T) {
	f := newSetupFixture(t, userSettings)
	if err := os.WriteFile(f.zshrc(), []byte("export PS1='> '\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("SHELL", "/bin/zsh")

	result := f.runSetup(t, SetupOptions{NoStartupCheck: true})

	step, _ := stepNamed(result.Steps, "Start-up update check")
	if !strings.Contains(step.Detail, "skipped") {
		t.Errorf("the step must say it was skipped, got %q", step.Detail)
	}
	if strings.Contains(readString(t, f.zshrc()), updater.MarkerBegin) {
		t.Error("--no-startup-check must leave shell files alone")
	}
}

func TestSetupRespectsTheConfigurationSwitch(t *testing.T) {
	f := newSetupFixture(t, userSettings)
	if err := os.WriteFile(f.zshrc(), []byte("export PS1='> '\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("SHELL", "/bin/zsh")

	cfg := config.Default()
	cfg.Updates.StartupHook = false
	setup := NewSetup(f.platform, cfg, platform.NewUnmanagedService(), SetupOptions{})
	setup.now = func() time.Time { return time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC) }
	result, _ := setup.Run(context.Background())

	step, _ := stepNamed(result.Steps, "Start-up update check")
	if !strings.Contains(step.Detail, "startup_hook") {
		t.Errorf("the step must name the setting that disabled it, got %q", step.Detail)
	}
	if strings.Contains(readString(t, f.zshrc()), updater.MarkerBegin) {
		t.Error("updates.startup_hook = false must leave shell files alone")
	}
}

func TestADryRunWritesNothing(t *testing.T) {
	f := newSetupFixture(t, userSettings)
	if err := os.WriteFile(f.zshrc(), []byte("export PS1='> '\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("SHELL", "/bin/zsh")

	result := f.runSetup(t, SetupOptions{DryRun: true})

	step, _ := stepNamed(result.Steps, "Start-up update check")
	if !strings.Contains(step.Detail, "would") {
		t.Errorf("a dry run must say what it would do, got %q", step.Detail)
	}
	if strings.Contains(readString(t, f.zshrc()), updater.MarkerBegin) {
		t.Error("a dry run wrote to a shell start-up file")
	}
}

func TestUninstallSaysNothingWasThereWhenNothingWas(t *testing.T) {
	f := newSetupFixture(t, userSettings)

	result := f.runUninstall(t, UninstallOptions{})
	step, ok := stepNamed(result.Steps, "Start-up update check removed")
	if !ok || !step.OK() {
		t.Fatalf("the step must succeed on a machine that never had it: %+v", step)
	}
	if !strings.Contains(step.Detail, "none") {
		t.Errorf("detail = %q, want it to say there was nothing", step.Detail)
	}
}

func readString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
