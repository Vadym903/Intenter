package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every release ships checksums.txt.sig, a signature over checksums.txt made
// with the release key. Both installers verify it — with cosign, else openssl
// (install.sh) or .NET (install.ps1), else a one-line notice — before trusting
// the checksums it vouches for (research R-05, contracts/release-and-
// signing.md §3). The fake release the harness serves is signed with a
// throwaway key (harness_test.go's release.sign), and INTENTER_SIGNING_KEY_FILE
// points the installer under test at its public half — the same test-mode
// override the installers accept only under INTENTER_TEST_MODE=1.

func TestInstallShVerifiesTheSignature(t *testing.T) {
	skipOnWindows(t)
	env := newEnv(t, newRelease(t, fakeLatest))

	got := env.run()
	if got.ExitCode != 0 {
		t.Fatalf("exit code = %d\n%s", got.ExitCode, got.Output())
	}
	requireLine(t, got.Output(), "verified signature (")
	refuteLine(t, got.Output(), "signature not verified")
}

func TestInstallShRefusesATamperedSignature(t *testing.T) {
	// The signature is checked before the checksum, so a stale or substituted
	// .sig is refused even though checksums.txt itself was not touched.
	skipOnWindows(t)
	release := newRelease(t, fakeLatest)
	env := newEnv(t, release)
	rc := env.writeRC(t, ".zshrc", "# my shell\n")
	env.Extra["SHELL"] = "/bin/zsh"
	before := readFile(t, rc)
	release.tamperSignature(t)

	got := env.run()
	if got.ExitCode != 8 {
		t.Fatalf("exit code = %d, want 8\n%s", got.ExitCode, got.Output())
	}
	requireLine(t, got.Output(), "signature verification failed")
	if exists(env.installedBinary()) {
		t.Error("a binary was installed although the signature did not verify")
	}
	if readFile(t, rc) != before {
		t.Error("a failed signature verification must not touch shell startup files")
	}
}

func TestInstallShTreatsAMissingSignatureAsADownloadFailure(t *testing.T) {
	skipOnWindows(t)
	release := newRelease(t, fakeLatest)
	env := newEnv(t, release)
	release.removeSignature(t)

	got := env.run()
	if got.ExitCode != 2 {
		t.Fatalf("exit code = %d, want 2\n%s", got.ExitCode, got.Output())
	}
	if exists(env.installedBinary()) {
		t.Error("a binary was installed although checksums.txt.sig was missing")
	}
}

func TestInstallShNoticesWhenNoVerifierIsAvailable(t *testing.T) {
	// INTENTER_TEST_NO_VERIFIER forces the no-verifier branch without having to
	// hide cosign/openssl from PATH, which this machine has installed.
	skipOnWindows(t)
	env := newEnv(t, newRelease(t, fakeLatest))
	env.Extra["INTENTER_TEST_NO_VERIFIER"] = "1"

	got := env.run()
	if got.ExitCode != 0 {
		t.Fatalf("exit code = %d\n%s", got.ExitCode, got.Output())
	}
	if !exists(env.installedBinary()) {
		t.Fatalf("no binary at %s\n%s", env.installedBinary(), got.Output())
	}
	if count := strings.Count(got.Output(), "signature not verified"); count != 1 {
		t.Errorf("notice printed %d times, want exactly 1:\n%s", count, got.Output())
	}
	requireLine(t, got.Output(), "docs/install.md#verifying-a-download-by-hand")
	refuteLine(t, got.Output(), "verified signature (")
}

func TestInstallPs1VerifiesTheSignature(t *testing.T) {
	eachShell(t, func(t *testing.T, shell string) {
		env := newEnv(t, newRelease(t, fakeLatest))
		env.Extra["INTENTER_NO_MODIFY_PATH"] = "1"

		got := env.runPS(shell)
		if got.ExitCode != 0 {
			t.Fatalf("exit code = %d\n%s", got.ExitCode, got.Output())
		}
		requireLine(t, got.Output(), "verified signature (")
		refuteLine(t, got.Output(), "signature not verified")
	})
}

func TestInstallPs1RefusesATamperedSignature(t *testing.T) {
	eachShell(t, func(t *testing.T, shell string) {
		release := newRelease(t, fakeLatest)
		env := newEnv(t, release)
		env.Extra["INTENTER_NO_MODIFY_PATH"] = "1"
		release.tamperSignature(t)

		got := env.runPS(shell)
		if got.ExitCode != 8 {
			t.Fatalf("exit code = %d, want 8\n%s", got.ExitCode, got.Output())
		}
		requireLine(t, got.Output(), "signature verification failed")
		if exists(env.installedBinary()) {
			t.Error("a binary was installed although the signature did not verify")
		}
	})
}

func TestInstallPs1TreatsAMissingSignatureAsADownloadFailure(t *testing.T) {
	eachShell(t, func(t *testing.T, shell string) {
		release := newRelease(t, fakeLatest)
		env := newEnv(t, release)
		env.Extra["INTENTER_NO_MODIFY_PATH"] = "1"
		release.removeSignature(t)

		got := env.runPS(shell)
		if got.ExitCode != 2 {
			t.Fatalf("exit code = %d, want 2\n%s", got.ExitCode, got.Output())
		}
		if exists(env.installedBinary()) {
			t.Error("a binary was installed although checksums.txt.sig was missing")
		}
	})
}

func TestInstallPs1NoticesWhenNoVerifierIsAvailable(t *testing.T) {
	eachShell(t, func(t *testing.T, shell string) {
		env := newEnv(t, newRelease(t, fakeLatest))
		env.Extra["INTENTER_NO_MODIFY_PATH"] = "1"
		env.Extra["INTENTER_TEST_NO_VERIFIER"] = "1"

		got := env.runPS(shell)
		if got.ExitCode != 0 {
			t.Fatalf("exit code = %d\n%s", got.ExitCode, got.Output())
		}
		if !exists(env.installedBinary()) {
			t.Fatalf("no binary at %s\n%s", env.installedBinary(), got.Output())
		}
		if count := strings.Count(got.Output(), "signature not verified"); count != 1 {
			t.Errorf("notice printed %d times, want exactly 1:\n%s", count, got.Output())
		}
		requireLine(t, got.Output(), "docs/install.md#verifying-a-download-by-hand")
		refuteLine(t, got.Output(), "verified signature (")
	})
}

// The embedded key each installer verifies against must be byte-identical to
// the repository's cosign.pub (contracts/release-and-signing.md §2) — the
// same invariant internal/updater/testutil_test.go checks for the updater's
// copy.

func TestInstallShEmbeddedKeyMatchesCosignPub(t *testing.T) {
	requireEmbeddedKeyMatchesCosignPub(t, "install.sh")
}

func TestInstallPs1EmbeddedKeyMatchesCosignPub(t *testing.T) {
	requireEmbeddedKeyMatchesCosignPub(t, "install.ps1")
}

func requireEmbeddedKeyMatchesCosignPub(t *testing.T, scriptName string) {
	t.Helper()

	want, err := os.ReadFile(filepath.Join(repoRoot(), "cosign.pub"))
	if err != nil {
		t.Fatalf("read repository cosign.pub: %v", err)
	}
	script, err := os.ReadFile(filepath.Join(repoRoot(), scriptName))
	if err != nil {
		t.Fatalf("read %s: %v", scriptName, err)
	}

	got := extractPublicKeyBlock(t, string(script))
	if strings.TrimSpace(got) != strings.TrimSpace(string(want)) {
		t.Errorf("%s's embedded public key has drifted from the repository root cosign.pub", scriptName)
	}
}

// extractPublicKeyBlock returns the PEM block embedded in an installer
// script, from its BEGIN marker through its END marker.
func extractPublicKeyBlock(t *testing.T, content string) string {
	t.Helper()

	const beginMarker = "-----BEGIN PUBLIC KEY-----"
	const endMarker = "-----END PUBLIC KEY-----"

	start := strings.Index(content, beginMarker)
	if start < 0 {
		t.Fatal("no embedded public key block found")
	}
	relEnd := strings.Index(content[start:], endMarker)
	if relEnd < 0 {
		t.Fatal("embedded public key block has no end marker")
	}
	end := start + relEnd + len(endMarker)
	return content[start:end]
}
