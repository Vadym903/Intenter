package install

import (
	"archive/zip"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The Windows installer has to work in two shells that behave differently:
// Windows PowerShell 5.1, which every Windows 10/11 machine has and which still
// defaults to older TLS, and PowerShell 7, which many developers install. Both
// are tested, because "works in pwsh" is not the same promise as "works on a
// fresh machine".

// powerShells lists the interpreters present on this machine.
func powerShells(t *testing.T) map[string]string {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("install.ps1 is the Windows installer; install.sh covers macOS and Linux")
	}

	found := map[string]string{}
	for name, program := range map[string]string{
		"Windows PowerShell 5.1": "powershell.exe",
		"PowerShell 7":           "pwsh",
	} {
		if path, err := exec.LookPath(program); err == nil {
			found[name] = path
		}
	}
	if len(found) == 0 {
		t.Fatal("no PowerShell interpreter found on a Windows machine")
	}
	return found
}

// runPS runs install.ps1 through one interpreter.
func (e *env) runPS(interpreter string, args ...string) result {
	e.t.Helper()
	full := append([]string{
		"-NoProfile", "-ExecutionPolicy", "Bypass",
		"-File", filepath.Join(repoRoot(), "install.ps1"),
	}, args...)
	return e.runWith(interpreter, full...)
}

// eachShell runs a case under every PowerShell on the machine.
func eachShell(t *testing.T, run func(t *testing.T, interpreter string)) {
	for name, interpreter := range powerShells(t) {
		t.Run(name, func(t *testing.T) { run(t, interpreter) })
	}
}

// TestInstallPs1ScriptblockFormRunsUnderStrictMode pins the documented
// `& ([scriptblock]::Create((irm ...))) -Uninstall` form. Parameters bind into
// a scriptblock-local scope there, so every `$script:X` read used to fault
// under StrictMode ("The variable '$script:Version' cannot be retrieved") and
// the post-publish uninstall gate exited 1 on every release.
func TestInstallPs1ScriptblockFormRunsUnderStrictMode(t *testing.T) {
	eachShell(t, func(t *testing.T, shell string) {
		env := newEnv(t, newRelease(t, fakeLatest))
		// A binary must be present: without one the uninstall takes the
		// "nothing to remove" branch, which is not guarded by -DryRun.
		if err := os.MkdirAll(env.InstallDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(env.installedBinary(), []byte("stub"), 0o755); err != nil {
			t.Fatalf("write stub: %v", err)
		}

		command := "& ([scriptblock]::Create((Get-Content -Raw '" +
			filepath.Join(repoRoot(), "install.ps1") + "'))) -Uninstall -DryRun"
		got := env.runWith(shell, "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", command)
		if got.ExitCode != 0 {
			t.Fatalf("exit code = %d\n%s", got.ExitCode, got.Output())
		}
		if !strings.Contains(got.Output(), "Would remove") {
			t.Errorf("no dry-run plan in the output:\n%s", got.Output())
		}
	})
}

func TestInstallPs1InstallsAndExplainsWhatItDid(t *testing.T) {
	eachShell(t, func(t *testing.T, shell string) {
		env := newEnv(t, newRelease(t, fakeLatest))
		env.Extra["INTENTER_NO_MODIFY_PATH"] = "1"

		got := env.runPS(shell)
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
		requireLine(t, got.Output(), "verified sha256")
		requireLine(t, got.Output(), "Intenter "+fakeLatest+" installed to")
		requireLine(t, got.Output(), "Next step: intenter setup claude")
	})
}

func TestInstallPs1UpgradesInPlace(t *testing.T) {
	eachShell(t, func(t *testing.T, shell string) {
		env := newEnv(t, newRelease(t, fakeLatest))
		env.Extra["INTENTER_NO_MODIFY_PATH"] = "1"
		env.preinstall(t, fakeOlder)

		before := env.installedBinary()
		got := env.runPS(shell)
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
	})
}

func TestInstallPs1IsANoOpWhenAlreadyCurrent(t *testing.T) {
	eachShell(t, func(t *testing.T, shell string) {
		env := newEnv(t, newRelease(t, fakeLatest))
		env.Extra["INTENTER_NO_MODIFY_PATH"] = "1"
		env.preinstall(t, fakeLatest)

		got := env.runPS(shell)
		if got.ExitCode != 0 {
			t.Fatalf("exit code = %d\n%s", got.ExitCode, got.Output())
		}
		requireLine(t, got.Output(), "already installed")
	})
}

func TestInstallPs1PinsAVersion(t *testing.T) {
	eachShell(t, func(t *testing.T, shell string) {
		env := newEnv(t, newRelease(t, fakeOlder))
		env.Extra["INTENTER_NO_MODIFY_PATH"] = "1"

		got := env.runPS(shell, "-Version", fakeOlder)
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
	})
}

func TestInstallPs1RefusesATamperedDownload(t *testing.T) {
	eachShell(t, func(t *testing.T, shell string) {
		release := newRelease(t, fakeLatest)
		env := newEnv(t, release)
		env.Extra["INTENTER_NO_MODIFY_PATH"] = "1"
		release.corrupt(t, fakeLatest)

		got := env.runPS(shell)
		if got.ExitCode != 3 {
			t.Fatalf("exit code = %d, want 3\n%s", got.ExitCode, got.Output())
		}
		requireLine(t, got.Output(), "checksum verification failed")
		if exists(env.installedBinary()) {
			t.Error("a binary was installed from an archive that failed verification")
		}
	})
}

func TestInstallPs1UnblocksTheBinary(t *testing.T) {
	// Windows marks files that came from the internet, and a marked binary
	// meets a SmartScreen dialog instead of running.
	eachShell(t, func(t *testing.T, shell string) {
		env := newEnv(t, newRelease(t, fakeLatest))
		env.Extra["INTENTER_NO_MODIFY_PATH"] = "1"
		env.runPS(shell)

		zone := env.installedBinary() + ":Zone.Identifier"
		if _, err := os.Stat(zone); err == nil {
			t.Error("the binary still carries its mark of the web")
		}
	})
}

func TestInstallPs1ReportsAnUnreachableRelease(t *testing.T) {
	eachShell(t, func(t *testing.T, shell string) {
		env := newEnv(t, newRelease(t, fakeLatest))
		env.Extra["INTENTER_LATEST_URL"] = "https://releases.invalid/releases/latest"

		got := env.runPS(shell)
		if got.ExitCode != 2 {
			t.Fatalf("exit code = %d, want 2\n%s", got.ExitCode, got.Output())
		}
		requireLine(t, got.Output(), "cannot determine the latest release")
		requireLine(t, got.Output(), "-Version")
	})
}

func TestInstallPs1NamesTheProxyWhenOneIsConfigured(t *testing.T) {
	eachShell(t, func(t *testing.T, shell string) {
		env := newEnv(t, newRelease(t, fakeLatest))
		env.Extra["INTENTER_LATEST_URL"] = "https://releases.invalid/releases/latest"
		env.Extra["HTTPS_PROXY"] = "http://proxy.example.internal:3128"

		got := env.runPS(shell)
		if got.ExitCode != 2 {
			t.Fatalf("exit code = %d, want 2\n%s", got.ExitCode, got.Output())
		}
		requireLine(t, got.Output(), "proxy.example.internal:3128")
	})
}

func TestInstallPs1DryRunChangesNothing(t *testing.T) {
	eachShell(t, func(t *testing.T, shell string) {
		env := newEnv(t, newRelease(t, fakeLatest))

		got := env.runPS(shell, "-DryRun")
		if got.ExitCode != 0 {
			t.Fatalf("exit code = %d\n%s", got.ExitCode, got.Output())
		}
		requireLine(t, got.Output(), "Would install Intenter")
		if exists(env.installedBinary()) {
			t.Error("-DryRun installed a binary")
		}
	})
}

func TestInstallPs1NoModifyPathSaysWhatToDoInstead(t *testing.T) {
	eachShell(t, func(t *testing.T, shell string) {
		env := newEnv(t, newRelease(t, fakeLatest))

		got := env.runPS(shell, "-NoModifyPath")
		if got.ExitCode != 0 {
			t.Fatalf("exit code = %d\n%s", got.ExitCode, got.Output())
		}
		// Without the instruction the binary is installed and unusable.
		requireLine(t, got.Output(), "Add "+env.InstallDir+" to your PATH")
		requireLine(t, got.Output(), "$env:Path")
	})
}

func TestInstallPs1RejectsAnUnknownAgent(t *testing.T) {
	eachShell(t, func(t *testing.T, shell string) {
		env := newEnv(t, newRelease(t, fakeLatest))

		got := env.runPS(shell, "-Setup", "copilot")
		if got.ExitCode != 1 {
			t.Fatalf("exit code = %d, want 1\n%s", got.ExitCode, got.Output())
		}
		requireLine(t, got.Output(), "unknown agent")
	})
}

func TestInstallPs1HelpExplainsEveryOption(t *testing.T) {
	eachShell(t, func(t *testing.T, shell string) {
		env := newEnv(t, newRelease(t, fakeLatest))

		got := env.runPS(shell, "-Help")
		if got.ExitCode != 0 {
			t.Fatalf("exit code = %d\n%s", got.ExitCode, got.Output())
		}
		for _, flag := range []string{
			"-Version", "-InstallDir", "-NoModifyPath", "-Setup",
			"-Uninstall", "-Purge", "-DryRun", "-Help",
		} {
			requireLine(t, got.Output(), flag)
		}
	})
}

func TestUninstallPs1RemovesEverythingItInstalled(t *testing.T) {
	eachShell(t, func(t *testing.T, shell string) {
		env := newEnv(t, newRelease(t, fakeLatest))
		env.Extra["INTENTER_NO_MODIFY_PATH"] = "1"

		if got := env.runPS(shell); got.ExitCode != 0 {
			t.Fatalf("install: %d\n%s", got.ExitCode, got.Output())
		}

		got := env.runPS(shell, "-Uninstall")
		if got.ExitCode != 0 {
			t.Fatalf("uninstall: %d\n%s", got.ExitCode, got.Output())
		}
		if exists(env.installedBinary()) {
			t.Error("the binary is still there")
		}
		requireLine(t, got.Output(), "Intenter has been removed")
	})
}

func TestUninstallPs1WithNothingInstalledIsFine(t *testing.T) {
	eachShell(t, func(t *testing.T, shell string) {
		env := newEnv(t, newRelease(t, fakeLatest))

		got := env.runPS(shell, "-Uninstall")
		if got.ExitCode != 0 {
			t.Fatalf("exit code = %d, want 0\n%s", got.ExitCode, got.Output())
		}
		requireLine(t, got.Output(), "nothing to remove")
	})
}

func TestInstallPs1UpdatesTheUserPath(t *testing.T) {
	// This writes to the real user PATH in the registry, so it is opt-in: a
	// developer running the suite should not have their environment edited.
	if os.Getenv("INTENTER_INSTALL_REGISTRY_TESTS") != "1" {
		t.Skip("set INTENTER_INSTALL_REGISTRY_TESTS=1 to exercise the real user PATH")
	}

	eachShell(t, func(t *testing.T, shell string) {
		env := newEnv(t, newRelease(t, fakeLatest))

		got := env.runPS(shell)
		if got.ExitCode != 0 {
			t.Fatalf("exit code = %d\n%s", got.ExitCode, got.Output())
		}
		t.Cleanup(func() { env.runPS(shell, "-Uninstall") })

		requireLine(t, got.Output(), "added "+env.InstallDir+" to your PATH")
		requireLine(t, got.Output(), "Open a new terminal")

		if !strings.Contains(userPath(t, shell), env.InstallDir) {
			t.Errorf("the user PATH does not contain %s", env.InstallDir)
		}

		if got := env.runPS(shell, "-Uninstall"); got.ExitCode != 0 {
			t.Fatalf("uninstall: %d\n%s", got.ExitCode, got.Output())
		}
		if strings.Contains(userPath(t, shell), env.InstallDir) {
			t.Errorf("the user PATH still contains %s after uninstalling", env.InstallDir)
		}
	})
}

// userPath reads the persisted user PATH.
func userPath(t *testing.T, shell string) string {
	t.Helper()
	out, err := exec.Command(shell, "-NoProfile", "-Command",
		"[Environment]::GetEnvironmentVariable('Path','User')").Output()
	if err != nil {
		t.Fatalf("read user PATH: %v", err)
	}
	return string(out)
}

// AG-180: downloads are pinned to HTTPS end to end (Save-Url's manual
// redirect-following), the same protection install.sh's `curl --proto
// =https` gives. These two tests exercise the two halves of that: the
// mechanics (an allowed redirect is actually followed) and the policy (an
// https-labeled source that is not really speaking TLS is never silently
// downgraded to plaintext).

func TestInstallPs1FollowsARedirectedDownload(t *testing.T) {
	eachShell(t, func(t *testing.T, shell string) {
		release := newRelease(t, fakeLatest)
		env := newEnv(t, release)
		env.Extra["INTENTER_NO_MODIFY_PATH"] = "1"

		// A thin front door that 302s every request to the real release
		// server, standing in for the hop GitHub's own release-asset
		// redirect makes (github.com -> objects.githubusercontent.com).
		// INTENTER_LATEST_URL is left at its default (the release server
		// directly), so only the download step -- Save-Url -- goes through
		// this extra hop.
		front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, release.baseURL+r.URL.Path, http.StatusFound)
		}))
		t.Cleanup(front.Close)
		env.Extra["INTENTER_DOWNLOAD_BASE"] = front.URL + "/releases/download"

		got := env.runPS(shell)
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
	})
}

func TestInstallPs1DoesNotDowngradeAnHttpsLabeledDownloadBase(t *testing.T) {
	eachShell(t, func(t *testing.T, shell string) {
		release := newRelease(t, fakeLatest)
		env := newEnv(t, release)
		env.Extra["INTENTER_NO_MODIFY_PATH"] = "1"

		// A download base that is labeled https:// gets no escape hatch, even
		// though this test's fake server only speaks plain HTTP -- the pin
		// does not trust the URL's own claim, it requires the transport to
		// actually be TLS. -Version short-circuits Resolve-Version, so
		// INTENTER_LATEST_URL is never dereferenced and can stay unreachable.
		env.Extra["INTENTER_LATEST_URL"] = "https://releases.invalid/releases/latest"
		env.Extra["INTENTER_DOWNLOAD_BASE"] = strings.Replace(release.baseURL, "http://", "https://", 1) + "/releases/download"

		got := env.runPS(shell, "-Version", fakeLatest)
		if got.ExitCode != 2 {
			t.Fatalf("exit code = %d, want 2\n%s", got.ExitCode, got.Output())
		}
		requireLine(t, got.Output(), "download failed")
		refuteLine(t, got.Output(), "refusing a plaintext download")
		if exists(env.installedBinary()) {
			t.Error("a binary was installed over a downgraded connection")
		}
	})
}

// AG-181: the checksums.txt lookup is anchored on the 1-2 spaces between the
// hash and the filename (install.sh's `grep " \{1,2\}${archive}\$"`), so an
// unrelated line whose filename happens to end with the target archive's name
// cannot be picked up instead of the real entry.
func TestInstallPs1ChecksumLookupIgnoresAFilenameSuffixCollision(t *testing.T) {
	eachShell(t, func(t *testing.T, shell string) {
		release := newRelease(t, fakeLatest)
		env := newEnv(t, release)
		env.Extra["INTENTER_NO_MODIFY_PATH"] = "1"

		checksumsPath := filepath.Join(release.dir, "checksums.txt")
		original, err := os.ReadFile(checksumsPath)
		if err != nil {
			t.Fatalf("read checksums.txt: %v", err)
		}
		decoy := strings.Repeat("0", 64) + "  evil_" + assetName(fakeLatest) + "\n"
		if err := os.WriteFile(checksumsPath, append([]byte(decoy), original...), 0o644); err != nil {
			t.Fatalf("write checksums.txt: %v", err)
		}
		release.sign(t, release.privateKey)

		got := env.runPS(shell)
		if got.ExitCode != 0 {
			t.Fatalf("exit code = %d\n%s", got.ExitCode, got.Output())
		}
		if !exists(env.installedBinary()) {
			t.Fatalf("no binary at %s\n%s", env.installedBinary(), got.Output())
		}
	})
}

// AG-182: Assert-SafeArchive refuses to extract an archive with an entry
// that would write outside the destination directory, before Expand-Archive
// ever runs -- the PowerShell equivalent of install.sh's `tar -xzf ...
// intenter`, which only ever asks for one named member.
func TestInstallPs1RefusesAZipEntryThatEscapesTheDestination(t *testing.T) {
	eachShell(t, func(t *testing.T, shell string) {
		release := newRelease(t, fakeLatest)
		env := newEnv(t, release)
		env.Extra["INTENTER_NO_MODIFY_PATH"] = "1"

		writeMaliciousZip(t, filepath.Join(release.dir, assetName(fakeLatest)))
		writeChecksums(t, release.dir)
		release.sign(t, release.privateKey)

		got := env.runPS(shell)
		if got.ExitCode != 2 {
			t.Fatalf("exit code = %d, want 2\n%s", got.ExitCode, got.Output())
		}
		requireLine(t, got.Output(), "unsafe path")
		if exists(env.installedBinary()) {
			t.Error("a binary was installed from an archive with a traversal entry")
		}
	})
}

// writeMaliciousZip writes a valid intenter.exe entry alongside a second
// entry whose name tries to escape the extraction directory.
func writeMaliciousZip(t *testing.T, path string) {
	t.Helper()

	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer file.Close()

	archive := zip.NewWriter(file)
	good, err := archive.Create("intenter.exe")
	if err != nil {
		t.Fatalf("zip entry: %v", err)
	}
	if _, err := good.Write([]byte("not a real binary")); err != nil {
		t.Fatalf("zip write: %v", err)
	}

	// archive/zip's Writer does not sanitize the name it is given; this keeps
	// the literal traversal path a crafted archive would carry.
	traversal, err := archive.CreateHeader(&zip.FileHeader{Name: "../evil.txt", Method: zip.Deflate})
	if err != nil {
		t.Fatalf("zip traversal entry: %v", err)
	}
	if _, err := traversal.Write([]byte("escaped")); err != nil {
		t.Fatalf("zip write: %v", err)
	}

	if err := archive.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
}
