package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func skipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("install.sh is the POSIX installer; install.ps1 covers Windows")
	}
}

func TestInstallShInstallsAndExplainsWhatItDid(t *testing.T) {
	skipOnWindows(t)
	env := newEnv(t, newRelease(t, fakeLatest))

	got := env.run()
	if got.ExitCode != 0 {
		t.Fatalf("exit code = %d\n%s", got.ExitCode, got.Output())
	}

	if !exists(env.installedBinary()) {
		t.Fatalf("no binary at %s\n%s", env.installedBinary(), got.Output())
	}
	version, err := env.installedVersion()
	if err != nil {
		t.Fatalf("the installed binary does not run: %v", err)
	}
	if version != fakeLatest {
		t.Errorf("installed %s, want %s", version, fakeLatest)
	}

	// The checksum is printed so a careful user can compare it with the
	// release page, and so a silent skip of verification is visible.
	requireLine(t, got.Output(), "verified sha256")
	requireLine(t, got.Output(), "Intenter "+fakeLatest+" installed to")
	requireLine(t, got.Output(), "Next step: intenter setup claude")
}

func TestInstallShIsExecutableAndOwnedByTheUser(t *testing.T) {
	skipOnWindows(t)
	env := newEnv(t, newRelease(t, fakeLatest))
	env.run()

	info, err := os.Stat(env.installedBinary())
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o755 {
		t.Errorf("mode = %#o, want 0755", mode)
	}
}

func TestInstallShUpgradesInPlace(t *testing.T) {
	// The path must not change across an upgrade: Claude's hooks and the
	// service registration point at it, and they are not rewritten here.
	skipOnWindows(t)
	env := newEnv(t, newRelease(t, fakeLatest))
	env.preinstall(t, fakeOlder)

	before := env.installedBinary()
	got := env.run()
	if got.ExitCode != 0 {
		t.Fatalf("exit code = %d\n%s", got.ExitCode, got.Output())
	}

	requireLine(t, got.Output(), "upgraded from "+fakeOlder)
	if env.installedBinary() != before {
		t.Errorf("the install path changed: %s → %s", before, env.installedBinary())
	}
	version, err := env.installedVersion()
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if version != fakeLatest {
		t.Errorf("installed %s, want %s", version, fakeLatest)
	}
}

func TestInstallShIsANoOpWhenAlreadyCurrent(t *testing.T) {
	// Re-running the one-liner is the documented way to upgrade, so running it
	// when nothing changed must be cheap and quiet rather than an error.
	skipOnWindows(t)
	env := newEnv(t, newRelease(t, fakeLatest))
	env.preinstall(t, fakeLatest)

	got := env.run()
	if got.ExitCode != 0 {
		t.Fatalf("exit code = %d\n%s", got.ExitCode, got.Output())
	}
	requireLine(t, got.Output(), "already installed")
}

func TestInstallShPinsAVersion(t *testing.T) {
	skipOnWindows(t)
	env := newEnv(t, newRelease(t, fakeOlder))

	got := env.run("--version", fakeOlder)
	if got.ExitCode != 0 {
		t.Fatalf("exit code = %d\n%s", got.ExitCode, got.Output())
	}
	version, err := env.installedVersion()
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if version != fakeOlder {
		t.Errorf("installed %s, want the pinned %s", version, fakeOlder)
	}

	// A leading v is how the tag is written on the release page, so both forms
	// have to work.
	env2 := newEnv(t, newRelease(t, fakeOlder))
	if got := env2.run("--version", "v"+fakeOlder); got.ExitCode != 0 {
		t.Errorf("a v-prefixed version failed: %d\n%s", got.ExitCode, got.Output())
	}
}

func TestInstallShRefusesATamperedDownload(t *testing.T) {
	// The reason the checksum is verified at all: whatever arrived is not what
	// was published, so nothing may be installed from it.
	skipOnWindows(t)
	release := newRelease(t, fakeLatest)
	env := newEnv(t, release)
	release.corrupt(t, fakeLatest)

	got := env.run()
	if got.ExitCode != 3 {
		t.Fatalf("exit code = %d, want 3\n%s", got.ExitCode, got.Output())
	}
	requireLine(t, got.Output(), "checksum verification failed")
	if exists(env.installedBinary()) {
		t.Error("a binary was installed from an archive that failed verification")
	}
}

func TestInstallShLeavesNothingBehindWhenItFails(t *testing.T) {
	skipOnWindows(t)
	release := newRelease(t, fakeLatest)
	env := newEnv(t, release)
	release.corrupt(t, fakeLatest)

	env.run()

	// The install directory may exist, but no partial binary may.
	for _, name := range []string{"intenter", "intenter.tmp"} {
		if exists(filepath.Join(env.InstallDir, name)) {
			t.Errorf("%s was left behind after a failed install", name)
		}
	}
	entries, err := os.ReadDir(env.InstallDir)
	if err == nil {
		for _, entry := range entries {
			if strings.Contains(entry.Name(), "intenter") {
				t.Errorf("leftover file: %s", entry.Name())
			}
		}
	}
}

func TestInstallShReportsAnUnreachableRelease(t *testing.T) {
	skipOnWindows(t)
	env := newEnv(t, newRelease(t, fakeLatest))
	env.Extra["INTENTER_LATEST_URL"] = "https://127.0.0.1:9/releases/latest"

	got := env.run()
	if got.ExitCode != 2 {
		t.Fatalf("exit code = %d, want 2\n%s", got.ExitCode, got.Output())
	}
	requireLine(t, got.Output(), "cannot determine the latest release")
	// A user who cannot reach the network still needs a way forward.
	requireLine(t, got.Output(), "--version")
}

func TestInstallShNamesTheProxyWhenOneIsConfigured(t *testing.T) {
	// A download that fails through a proxy looks exactly like one that fails
	// without one, and on a corporate network the proxy is the answer far more
	// often than the network is. The failure is forced with an unreachable
	// release URL, because curl does not send localhost requests to a proxy.
	skipOnWindows(t)
	env := newEnv(t, newRelease(t, fakeLatest))
	env.Extra["INTENTER_LATEST_URL"] = "https://releases.invalid/releases/latest"
	env.Extra["HTTPS_PROXY"] = "http://proxy.example.internal:3128"

	got := env.run()
	if got.ExitCode != 2 {
		t.Fatalf("exit code = %d, want 2\n%s", got.ExitCode, got.Output())
	}
	requireLine(t, got.Output(), "proxy.example.internal:3128")
}

func TestInstallShRefusesAnUnsupportedArchitecture(t *testing.T) {
	// Spoof uname so the unsupported branch is reachable on a supported machine.
	skipOnWindows(t)
	env := newEnv(t, newRelease(t, fakeLatest))

	shim := filepath.Join(env.Home, "shims")
	if err := os.MkdirAll(shim, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	fake := "#!/bin/sh\nif [ \"$1\" = \"-m\" ]; then echo mips64; else /usr/bin/uname \"$@\"; fi\n"
	if err := os.WriteFile(filepath.Join(shim, "uname"), []byte(fake), 0o755); err != nil {
		t.Fatalf("write shim: %v", err)
	}
	env.Extra["PATH"] = shim + string(os.PathListSeparator) + os.Getenv("PATH")

	got := env.run()
	if got.ExitCode != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", got.ExitCode, got.Output())
	}
	requireLine(t, got.Output(), "unsupported architecture")
	requireLine(t, got.Output(), "build from source")
	if exists(env.installedBinary()) {
		t.Error("nothing may be installed for an architecture with no build")
	}
}

func TestInstallShDryRunChangesNothing(t *testing.T) {
	skipOnWindows(t)
	env := newEnv(t, newRelease(t, fakeLatest))
	rc := env.writeRC(t, ".zshrc", "# my shell\nexport EDITOR=vim\n")
	before := readFile(t, rc)

	got := env.run("--dry-run")
	if got.ExitCode != 0 {
		t.Fatalf("exit code = %d\n%s", got.ExitCode, got.Output())
	}
	requireLine(t, got.Output(), "Would install Intenter")

	if exists(env.installedBinary()) {
		t.Error("--dry-run installed a binary")
	}
	if readFile(t, rc) != before {
		t.Error("--dry-run modified a shell startup file")
	}
}

func TestInstallShAddsAPathBlockAndLeavesTheRestAlone(t *testing.T) {
	skipOnWindows(t)
	env := newEnv(t, newRelease(t, fakeLatest))
	original := "# my shell\nexport EDITOR=vim\nalias ll='ls -la'\n"
	rc := env.writeRC(t, ".zshrc", original)
	env.Extra["SHELL"] = "/bin/zsh"

	got := env.run()
	if got.ExitCode != 0 {
		t.Fatalf("exit code = %d\n%s", got.ExitCode, got.Output())
	}

	content := readFile(t, rc)
	if !strings.Contains(content, env.InstallDir) {
		t.Errorf("the rc file has no PATH entry:\n%s", content)
	}
	if !strings.Contains(content, ">>> intenter >>>") {
		t.Errorf("the entry must be marked so uninstall can find exactly it:\n%s", content)
	}
	if !strings.HasPrefix(content, original) {
		t.Errorf("the user's own configuration must be untouched:\n%s", content)
	}
	requireLine(t, got.Output(), "Open a new terminal")
}

func TestInstallShWritesThePathBlockOnlyOnce(t *testing.T) {
	skipOnWindows(t)
	env := newEnv(t, newRelease(t, fakeLatest))
	rc := env.writeRC(t, ".zshrc", "# my shell\n")
	env.Extra["SHELL"] = "/bin/zsh"

	env.run()
	env.preinstall(t, fakeOlder) // force the second run to do work
	env.run()

	if count := strings.Count(readFile(t, rc), ">>> intenter >>>"); count != 1 {
		t.Errorf("the rc file has %d PATH blocks, want 1:\n%s", count, readFile(t, rc))
	}
}

func TestInstallShRespectsNoModifyPath(t *testing.T) {
	// Some people manage PATH themselves and would rather be told than helped.
	skipOnWindows(t)
	env := newEnv(t, newRelease(t, fakeLatest))
	rc := env.writeRC(t, ".zshrc", "# my shell\n")
	env.Extra["SHELL"] = "/bin/zsh"

	got := env.run("--no-modify-path")
	if got.ExitCode != 0 {
		t.Fatalf("exit code = %d\n%s", got.ExitCode, got.Output())
	}
	if strings.Contains(readFile(t, rc), "intenter") {
		t.Error("--no-modify-path still edited the rc file")
	}
	// It has to say what to do instead, or the binary is unusable.
	requireLine(t, got.Output(), "export PATH=")
	requireLine(t, got.Output(), env.InstallDir)
}

func TestInstallShOffersTheStartupCheckAfterOptingOutOfShellEdits(t *testing.T) {
	// Feature 003 adds a second managed block to the same shell files, and
	// --no-modify-path declines it too: somebody who refused one edit did not
	// ask for the other. That leaves the user with no way to be told about
	// releases unless the installer says how to opt back in.
	//
	// (That the flag actually reaches `setup claude` is covered where setup can
	// observe it: TestSetupRespectsNoStartupCheck in internal/adapter/claude.
	// Here the real binary runs, and setup stops at "no Claude Code" long
	// before the start-up step.)
	skipOnWindows(t)
	env := newEnv(t, newRelease(t, fakeLatest))
	env.Extra["SHELL"] = "/bin/zsh"

	got := env.run("--no-modify-path", "--setup", "claude")
	requireLine(t, got.Output(), "intenter update startup enable")
}

func TestInstallShSkipsThePathBlockWhenAlreadyOnPath(t *testing.T) {
	skipOnWindows(t)
	env := newEnv(t, newRelease(t, fakeLatest))
	rc := env.writeRC(t, ".zshrc", "# my shell\n")
	env.Extra["SHELL"] = "/bin/zsh"
	env.Extra["PATH"] = env.InstallDir + string(os.PathListSeparator) + os.Getenv("PATH")

	got := env.run()
	if got.ExitCode != 0 {
		t.Fatalf("exit code = %d\n%s", got.ExitCode, got.Output())
	}
	if strings.Contains(readFile(t, rc), "intenter") {
		t.Error("the installer edited an rc file although the directory was already on PATH")
	}
	refuteLine(t, got.Output(), "Open a new terminal")
}

func TestInstallShRejectsAnUnknownFlag(t *testing.T) {
	skipOnWindows(t)
	env := newEnv(t, newRelease(t, fakeLatest))

	got := env.run("--recursive")
	if got.ExitCode != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", got.ExitCode, got.Output())
	}
	requireLine(t, got.Output(), "unknown option")
	requireLine(t, got.Output(), "Usage:")
	if exists(env.installedBinary()) {
		t.Error("a mistyped flag must not install anything")
	}
}

func TestInstallShHelpExplainsEveryOption(t *testing.T) {
	skipOnWindows(t)
	env := newEnv(t, newRelease(t, fakeLatest))

	got := env.run("--help")
	if got.ExitCode != 0 {
		t.Fatalf("exit code = %d\n%s", got.ExitCode, got.Output())
	}
	for _, flag := range []string{
		"--version", "--prefix", "--no-modify-path", "--setup",
		"--uninstall", "--purge", "--dry-run", "--help",
	} {
		requireLine(t, got.Output(), flag)
	}
}

func TestInstallShAcceptsYesWithoutPrompting(t *testing.T) {
	// `curl | sh` hands the script its own source on stdin, so it can never
	// read an answer — but people type --yes out of habit and it must not fail.
	skipOnWindows(t)
	env := newEnv(t, newRelease(t, fakeLatest))

	if got := env.run("--yes"); got.ExitCode != 0 {
		t.Fatalf("exit code = %d\n%s", got.ExitCode, got.Output())
	}
}

func TestInstallShHonoursAPrefix(t *testing.T) {
	skipOnWindows(t)
	env := newEnv(t, newRelease(t, fakeLatest))
	prefix := filepath.Join(env.Home, "opt", "bin")

	got := env.run("--prefix", prefix)
	if got.ExitCode != 0 {
		t.Fatalf("exit code = %d\n%s", got.ExitCode, got.Output())
	}
	if !exists(filepath.Join(prefix, "intenter")) {
		t.Errorf("nothing installed in %s\n%s", prefix, got.Output())
	}
}

func TestInstallShRunsSetupWhenAsked(t *testing.T) {
	skipOnWindows(t)
	env := newEnv(t, newRelease(t, fakeLatest))
	env.claudeShim(t)

	got := env.run("--setup", "claude")
	// Setup may not complete in a bare environment; what matters is that it was
	// attempted and that the summary does not then tell the user to run it.
	requireLine(t, got.Output(), "Intenter "+fakeLatest+" installed to")
	refuteLine(t, got.Output(), "Next step: intenter setup claude")
}

func TestInstallShWorksUnderEveryPosixShell(t *testing.T) {
	// /bin/sh is dash on Debian, BusyBox ash on Alpine and bash in POSIX mode
	// elsewhere. A bashism runs fine on the author's machine and fails on a
	// stranger's, which is the failure mode hardest to hear about.
	skipOnWindows(t)

	shells := map[string][]string{
		"dash":         {"dash"},
		"bash --posix": {"bash", "--posix"},
		"zsh as sh":    {"zsh", "--emulate", "sh"},
		"busybox sh":   {"busybox", "sh"},
	}

	for name, argv := range shells {
		t.Run(name, func(t *testing.T) {
			if _, err := exec.LookPath(argv[0]); err != nil {
				t.Skipf("%s is not installed here", argv[0])
			}

			env := newEnv(t, newRelease(t, fakeLatest))
			args := append(append([]string{}, argv[1:]...), filepath.Join(repoRoot(), "install.sh"))
			got := env.runWith(argv[0], args...)

			if got.ExitCode != 0 {
				t.Fatalf("exit code = %d\n%s", got.ExitCode, got.Output())
			}
			version, err := env.installedVersion()
			if err != nil {
				t.Fatalf("the binary does not run after installing under %s: %v", name, err)
			}
			if version != fakeLatest {
				t.Errorf("installed %s, want %s", version, fakeLatest)
			}
		})
	}
}

func TestInstallShRejectsAnUnknownAgent(t *testing.T) {
	skipOnWindows(t)
	env := newEnv(t, newRelease(t, fakeLatest))

	got := env.run("--setup", "copilot")
	if got.ExitCode != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", got.ExitCode, got.Output())
	}
	requireLine(t, got.Output(), "unknown agent")
}
