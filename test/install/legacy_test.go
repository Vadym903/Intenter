package install

// Regression tests for the installers' cleanup of the pre-rename AgentGuard
// install, per contracts/identity-and-rename.md §1/§2.3-§2.5 (T020). The old
// identity is named here only to build fixtures a legacy layout would have
// left behind — install.sh and install.ps1 never use or trust it, only
// detect and remove it.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// legacyRC is the shape the pre-rename installer left in a shell rc file: the
// block sits between whatever the user already had, exactly like the
// current-name block does today.
func legacyRC(installDir string) string {
	return "\n# >>> agentguard >>>\n" +
		"# Added by the AgentGuard installer. Remove this block, or run the\n" +
		"# installer with --uninstall, to undo it.\n" +
		"export PATH=\"" + installDir + ":$PATH\"\n" +
		"# <<< agentguard <<<\n"
}

func TestInstallShRemovesLegacyArtifactsOnInstall(t *testing.T) {
	skipOnWindows(t)
	env := newEnv(t, newRelease(t, fakeLatest))
	env.Extra["SHELL"] = "/bin/zsh"

	// A machine that still carries the pre-rename install: its binary in the
	// same directory, its PATH block sandwiched between the user's own lines,
	// and its fish conf.
	if err := os.MkdirAll(env.InstallDir, 0o755); err != nil {
		t.Fatalf("mkdir install dir: %v", err)
	}
	legacyBinary := filepath.Join(env.InstallDir, "agentguard")
	if err := os.WriteFile(legacyBinary, []byte("#!/bin/sh\necho legacy\n"), 0o755); err != nil {
		t.Fatalf("write legacy binary: %v", err)
	}

	original := "# my shell\nexport EDITOR=vim\nalias ll='ls -la'\n"
	rc := env.writeRC(t, ".zshrc", original+legacyRC(env.InstallDir))

	legacyFish := filepath.Join(env.Home, ".config", "fish", "conf.d", "agentguard.fish")
	if err := os.MkdirAll(filepath.Dir(legacyFish), 0o755); err != nil {
		t.Fatalf("mkdir fish conf dir: %v", err)
	}
	if err := os.WriteFile(legacyFish, []byte("# >>> agentguard >>>\nfish_add_path "+env.InstallDir+"\n# <<< agentguard <<<\n"), 0o644); err != nil {
		t.Fatalf("write legacy fish conf: %v", err)
	}

	got := env.run()
	if got.ExitCode != 0 {
		t.Fatalf("exit code = %d\n%s", got.ExitCode, got.Output())
	}

	if exists(legacyBinary) {
		t.Error("the legacy agentguard binary is still there")
	}
	if !exists(env.installedBinary()) {
		t.Fatal("the new binary was not installed")
	}
	version, err := env.installedVersion()
	if err != nil {
		t.Fatalf("the installed binary does not run: %v", err)
	}
	if version != fakeLatest {
		t.Errorf("installed %s, want %s", version, fakeLatest)
	}
	if exists(legacyFish) {
		t.Error("the legacy fish conf is still there")
	}

	content := readFile(t, rc)
	if strings.Contains(content, ">>> agentguard >>>") {
		t.Errorf("the legacy PATH block is still there:\n%s", content)
	}
	if !strings.Contains(content, ">>> intenter >>>") {
		t.Errorf("the current PATH block is missing:\n%s", content)
	}
	if !strings.HasPrefix(content, original) {
		t.Errorf("the user's own configuration must be untouched:\n%s", content)
	}

	requireLine(t, got.Output(), "legacy")
}

func TestInstallShDryRunDoesNotTouchLegacyArtifacts(t *testing.T) {
	skipOnWindows(t)
	env := newEnv(t, newRelease(t, fakeLatest))

	if err := os.MkdirAll(env.InstallDir, 0o755); err != nil {
		t.Fatalf("mkdir install dir: %v", err)
	}
	legacyBinary := filepath.Join(env.InstallDir, "agentguard")
	if err := os.WriteFile(legacyBinary, []byte("legacy"), 0o755); err != nil {
		t.Fatalf("write legacy binary: %v", err)
	}

	got := env.run("--dry-run")
	if got.ExitCode != 0 {
		t.Fatalf("exit code = %d\n%s", got.ExitCode, got.Output())
	}
	if !exists(legacyBinary) {
		t.Error("--dry-run removed the legacy binary")
	}
}

func TestUninstallShRemovesLegacyArtifactsAndRestoresTheShellFile(t *testing.T) {
	// The rc-file half of contracts/identity-and-rename.md §2.3's acceptance
	// check: starting from a legacy + current layout, --uninstall must leave
	// the rc file byte-identical to what it was before any install.
	skipOnWindows(t)
	env := newEnv(t, newRelease(t, fakeLatest))
	env.Extra["SHELL"] = "/bin/zsh"

	original := "# my shell\nexport EDITOR=vim\nalias ll='ls -la'\n"
	rc := env.writeRC(t, ".zshrc", original)

	if got := env.run(); got.ExitCode != 0 {
		t.Fatalf("install: %d\n%s", got.ExitCode, got.Output())
	}

	// A legacy install sitting alongside the one just installed.
	legacyBinary := filepath.Join(env.InstallDir, "agentguard")
	if err := os.WriteFile(legacyBinary, []byte("#!/bin/sh\necho legacy\n"), 0o755); err != nil {
		t.Fatalf("write legacy binary: %v", err)
	}
	withLegacyBlock := readFile(t, rc) + legacyRC(env.InstallDir)
	if err := os.WriteFile(rc, []byte(withLegacyBlock), 0o644); err != nil {
		t.Fatalf("write rc: %v", err)
	}
	legacyFish := filepath.Join(env.Home, ".config", "fish", "conf.d", "agentguard.fish")
	if err := os.MkdirAll(filepath.Dir(legacyFish), 0o755); err != nil {
		t.Fatalf("mkdir fish conf dir: %v", err)
	}
	if err := os.WriteFile(legacyFish, []byte("# >>> agentguard >>>\nfish_add_path "+env.InstallDir+"\n# <<< agentguard <<<\n"), 0o644); err != nil {
		t.Fatalf("write legacy fish conf: %v", err)
	}

	got := env.run("--uninstall")
	if got.ExitCode != 0 {
		t.Fatalf("uninstall: %d\n%s", got.ExitCode, got.Output())
	}

	if exists(legacyBinary) {
		t.Error("the legacy agentguard binary is still there")
	}
	if exists(env.installedBinary()) {
		t.Error("the current binary is still there")
	}
	if exists(legacyFish) {
		t.Error("the legacy fish conf is still there")
	}
	if got := readFile(t, rc); got != original {
		t.Errorf("the rc file did not return to its pre-install state:\n got %q\nwant %q", got, original)
	}
}

// legacyInstallDir is where the pre-rename install.ps1 put the binary,
// computed the same way install.ps1's Get-LegacyInstallDir falls back: under
// the harness's USERPROFILE, since these tests do not set LOCALAPPDATA.
func legacyInstallDir(e *env) string {
	return filepath.Join(e.Home, "AppData", "Local", "AgentGuard", "bin")
}

func TestInstallPs1RemovesLegacyBinaryOnInstall(t *testing.T) {
	eachShell(t, func(t *testing.T, shell string) {
		env := newEnv(t, newRelease(t, fakeLatest))
		env.Extra["INTENTER_NO_MODIFY_PATH"] = "1"

		legacyDir := legacyInstallDir(env)
		if err := os.MkdirAll(legacyDir, 0o755); err != nil {
			t.Fatalf("mkdir legacy dir: %v", err)
		}
		legacyBinary := filepath.Join(legacyDir, "agentguard.exe")
		if err := os.WriteFile(legacyBinary, []byte("legacy"), 0o755); err != nil {
			t.Fatalf("write legacy binary: %v", err)
		}

		got := env.runPS(shell)
		if got.ExitCode != 0 {
			t.Fatalf("exit code = %d\n%s", got.ExitCode, got.Output())
		}
		if exists(legacyBinary) {
			t.Error("the legacy agentguard binary is still there")
		}
		if exists(legacyDir) {
			t.Error("the now-empty legacy directory is still there")
		}
		if !exists(env.installedBinary()) {
			t.Error("the new binary was not installed")
		}
	})
}

func TestUninstallPs1RemovesLegacyBinary(t *testing.T) {
	eachShell(t, func(t *testing.T, shell string) {
		env := newEnv(t, newRelease(t, fakeLatest))
		env.Extra["INTENTER_NO_MODIFY_PATH"] = "1"

		legacyDir := legacyInstallDir(env)
		if err := os.MkdirAll(legacyDir, 0o755); err != nil {
			t.Fatalf("mkdir legacy dir: %v", err)
		}
		legacyBinary := filepath.Join(legacyDir, "agentguard.exe")
		if err := os.WriteFile(legacyBinary, []byte("legacy"), 0o755); err != nil {
			t.Fatalf("write legacy binary: %v", err)
		}

		if got := env.runPS(shell); got.ExitCode != 0 {
			t.Fatalf("install: %d\n%s", got.ExitCode, got.Output())
		}

		got := env.runPS(shell, "-Uninstall")
		if got.ExitCode != 0 {
			t.Fatalf("uninstall: %d\n%s", got.ExitCode, got.Output())
		}
		if exists(legacyBinary) {
			t.Error("the legacy agentguard binary is still there")
		}
		if exists(legacyDir) {
			t.Error("the now-empty legacy directory is still there")
		}
	})
}

func TestInstallPs1UpdatesTheUserPathAwayFromLegacy(t *testing.T) {
	// Touches the real user PATH in the registry, so it is opt-in like
	// TestInstallPs1UpdatesTheUserPath in install_ps1_test.go.
	if os.Getenv("INTENTER_INSTALL_REGISTRY_TESTS") != "1" {
		t.Skip("set INTENTER_INSTALL_REGISTRY_TESTS=1 to exercise the real user PATH")
	}

	eachShell(t, func(t *testing.T, shell string) {
		env := newEnv(t, newRelease(t, fakeLatest))

		legacyDir := legacyInstallDir(env)
		if err := os.MkdirAll(legacyDir, 0o755); err != nil {
			t.Fatalf("mkdir legacy dir: %v", err)
		}

		// Seed the real user PATH with the legacy directory, the way an old
		// install would have left it, then clean it up however the test ends.
		addLegacyToUserPath(t, shell, legacyDir)
		t.Cleanup(func() { removeFromUserPath(t, shell, legacyDir) })

		got := env.runPS(shell)
		if got.ExitCode != 0 {
			t.Fatalf("exit code = %d\n%s", got.ExitCode, got.Output())
		}
		t.Cleanup(func() { env.runPS(shell, "-Uninstall") })

		if strings.Contains(userPath(t, shell), legacyDir) {
			t.Errorf("the legacy directory is still on the user PATH")
		}
	})
}

func addLegacyToUserPath(t *testing.T, shell, dir string) {
	t.Helper()
	script := "$current = [Environment]::GetEnvironmentVariable('Path','User'); " +
		"[Environment]::SetEnvironmentVariable('Path', ($current + ';' + '" + dir + "'), 'User')"
	if out, err := exec.Command(shell, "-NoProfile", "-Command", script).CombinedOutput(); err != nil {
		t.Fatalf("seed legacy PATH entry: %v\n%s", err, out)
	}
}

func removeFromUserPath(t *testing.T, shell, dir string) {
	t.Helper()
	script := "$kept = ([Environment]::GetEnvironmentVariable('Path','User') -split ';') | " +
		"Where-Object { $_ -ne '' -and $_ -ne '" + dir + "' }; " +
		"[Environment]::SetEnvironmentVariable('Path', ($kept -join ';'), 'User')"
	if out, err := exec.Command(shell, "-NoProfile", "-Command", script).CombinedOutput(); err != nil {
		t.Fatalf("clean up user PATH: %v\n%s", err, out)
	}
}
