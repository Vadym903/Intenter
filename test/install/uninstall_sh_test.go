package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Uninstalling is a trust exercise: a tool that gates every command an agent
// runs has to be removable by someone who has decided they no longer want it,
// and it has to leave their machine as it found it. Anything left behind — a
// PATH line, a hook, a service entry — is a thing they did not ask for and now
// have to hunt down.

func TestUninstallShRemovesEverythingItInstalled(t *testing.T) {
	skipOnWindows(t)
	env := newEnv(t, newRelease(t, fakeLatest))
	env.Extra["SHELL"] = "/bin/zsh"
	rc := env.writeRC(t, ".zshrc", "# my shell\n")

	if got := env.run(); got.ExitCode != 0 {
		t.Fatalf("install: %d\n%s", got.ExitCode, got.Output())
	}
	if !exists(env.installedBinary()) {
		t.Fatal("nothing was installed to remove")
	}

	got := env.run("--uninstall")
	if got.ExitCode != 0 {
		t.Fatalf("uninstall: %d\n%s", got.ExitCode, got.Output())
	}

	if exists(env.installedBinary()) {
		t.Error("the binary is still there")
	}
	if strings.Contains(readFile(t, rc), "intenter") {
		t.Errorf("the PATH entry is still there:\n%s", readFile(t, rc))
	}
	requireLine(t, got.Output(), "Intenter has been removed")
}

func TestUninstallShLeavesTheShellFileByteIdentical(t *testing.T) {
	// The rc file belongs to the user and holds things Intenter has never
	// heard of. Editing it is a privilege that lasts exactly as long as the
	// file comes back unchanged.
	skipOnWindows(t)
	env := newEnv(t, newRelease(t, fakeLatest))
	env.Extra["SHELL"] = "/bin/zsh"

	original := "# my shell\nexport EDITOR=vim\n\n# a deliberate blank line above\nalias ll='ls -la'\n"
	rc := env.writeRC(t, ".zshrc", original)

	env.run()
	env.run("--uninstall")

	if got := readFile(t, rc); got != original {
		t.Errorf("the rc file changed across install → uninstall:\n got %q\nwant %q", got, original)
	}
}

func TestUninstallShSurvivesRepeatedCycles(t *testing.T) {
	// Someone evaluating the tool will install and remove it several times.
	skipOnWindows(t)
	env := newEnv(t, newRelease(t, fakeLatest))
	env.Extra["SHELL"] = "/bin/zsh"

	original := "# my shell\n"
	rc := env.writeRC(t, ".zshrc", original)

	for i := 0; i < 3; i++ {
		env.run()
		env.run("--uninstall")
	}

	if got := readFile(t, rc); got != original {
		t.Errorf("the rc file drifted over three cycles:\n got %q\nwant %q", got, original)
	}
}

func TestUninstallShWithNothingInstalledIsFine(t *testing.T) {
	// Someone who is not sure whether it is installed should be able to just
	// run it, and hear that there was nothing to do.
	skipOnWindows(t)
	env := newEnv(t, newRelease(t, fakeLatest))

	got := env.run("--uninstall")
	if got.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", got.ExitCode, got.Output())
	}
	requireLine(t, got.Output(), "nothing to remove")
}

func TestUninstallShKeepsTheDataByDefault(t *testing.T) {
	// Approvals are work the user did. Removing the tool must not throw them
	// away, so that reinstalling picks up where they left off.
	skipOnWindows(t)
	env := newEnv(t, newRelease(t, fakeLatest))
	env.run()

	// Something in the data directory stands in for a database of approvals.
	if err := os.MkdirAll(env.DataDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	marker := filepath.Join(env.DataDir, "intenter.db")
	if err := os.WriteFile(marker, []byte("approvals"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	got := env.run("--uninstall")
	if got.ExitCode != 0 {
		t.Fatalf("exit code = %d\n%s", got.ExitCode, got.Output())
	}
	if !exists(marker) {
		t.Error("uninstall deleted the approvals database without being asked to")
	}
	requireLine(t, got.Output(), "approvals and history are kept")
}

func TestUninstallShDryRunChangesNothing(t *testing.T) {
	skipOnWindows(t)
	env := newEnv(t, newRelease(t, fakeLatest))
	env.run()

	got := env.run("--uninstall", "--dry-run")
	if got.ExitCode != 0 {
		t.Fatalf("exit code = %d\n%s", got.ExitCode, got.Output())
	}
	requireLine(t, got.Output(), "Would remove Intenter")
	if !exists(env.installedBinary()) {
		t.Error("--dry-run removed the binary")
	}
}

func TestUninstallShRemovesAPathEntryEvenWithoutABinary(t *testing.T) {
	// A user who deleted the binary by hand still has the PATH line, and the
	// documented way to clean up has to work from wherever they are.
	skipOnWindows(t)
	env := newEnv(t, newRelease(t, fakeLatest))
	env.Extra["SHELL"] = "/bin/zsh"
	rc := env.writeRC(t, ".zshrc", "# my shell\n")

	env.run()
	if err := os.Remove(env.installedBinary()); err != nil {
		t.Fatalf("remove binary: %v", err)
	}

	got := env.run("--uninstall")
	if got.ExitCode != 0 {
		t.Fatalf("exit code = %d\n%s", got.ExitCode, got.Output())
	}
	if strings.Contains(readFile(t, rc), "intenter") {
		t.Errorf("the PATH entry survived:\n%s", readFile(t, rc))
	}
}

func TestUpgradeThenUninstallLeavesNoTrace(t *testing.T) {
	// The whole lifecycle in one test, because each step is where the next one
	// finds its mess. Named so the release workflow can run just this shape.
	skipOnWindows(t)
	env := newEnv(t, newRelease(t, fakeLatest))
	env.Extra["SHELL"] = "/bin/zsh"

	original := "# my shell\n"
	rc := env.writeRC(t, ".zshrc", original)

	env.preinstall(t, fakeOlder)
	upgraded := env.run()
	if upgraded.ExitCode != 0 {
		t.Fatalf("upgrade: %d\n%s", upgraded.ExitCode, upgraded.Output())
	}
	requireLine(t, upgraded.Output(), "upgraded from "+fakeOlder)

	removed := env.run("--uninstall")
	if removed.ExitCode != 0 {
		t.Fatalf("uninstall: %d\n%s", removed.ExitCode, removed.Output())
	}
	if exists(env.installedBinary()) {
		t.Error("the binary survived")
	}
	if got := readFile(t, rc); got != original {
		t.Errorf("the rc file changed:\n got %q\nwant %q", got, original)
	}
}
