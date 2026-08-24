package updater

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// acceptAnyBinary stands in for running the downloaded build. The unit tests
// use it wherever the point is the download itself; the real check has its own
// test below and is exercised for real by the end-to-end suite.
func acceptAnyBinary(string, string) error { return nil }

func TestAReleaseIsDownloadedVerifiedAndExtracted(t *testing.T) {
	release := publishRelease(t, "0.2.0")
	fetcher := &Fetcher{Sources: release.sources(), PublicKey: release.PublicKey, SanityCheck: acceptAnyBinary}

	binary, err := fetcher.Fetch(context.Background(), "0.2.0", filepath.Join(t.TempDir(), "stage"))
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if filepath.Base(binary) != ExecutableName() {
		t.Errorf("extracted %q, want %q", filepath.Base(binary), ExecutableName())
	}
	info, err := os.Stat(binary)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() == 0 {
		t.Error("the extracted binary is empty")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		t.Errorf("mode = %v, want an executable", info.Mode().Perm())
	}
	// Only the binary is extracted; the archive's other entries stay in it.
	if _, err := os.Stat(filepath.Join(filepath.Dir(binary), "README.md")); err == nil {
		t.Error("extraction wrote a file that is not the binary")
	}
}

func TestATamperedChecksumsFileWithAStaleSignatureIsRefused(t *testing.T) {
	// A checksums file altered after it was signed is never trusted — the
	// signature is checked before the (also wrong) checksum is ever compared,
	// so this is caught as a signature failure, not a checksum one.
	release := publishRelease(t, "0.2.0")
	release.Corrupt(t)

	dir := filepath.Join(t.TempDir(), "stage")
	fetcher := &Fetcher{Sources: release.sources(), PublicKey: release.PublicKey, SanityCheck: acceptAnyBinary}

	_, err := fetcher.Fetch(context.Background(), "0.2.0", dir)
	if err == nil {
		t.Fatal("a stale signature must fail")
	}
	if got := ExitCodeFor(err); got != ExitSignature {
		t.Errorf("exit code = %d, want %d", got, ExitSignature)
	}
	if !strings.Contains(err.Error(), "signature verification failed") {
		t.Errorf("error = %v, want it to say what failed", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ExecutableName())); statErr == nil {
		t.Error("nothing must be extracted when the signature does not verify")
	}
}

func TestATamperedArchiveWithAValidSignatureFailsTheChecksum(t *testing.T) {
	// The signature vouches for checksums.txt, not for the archive by itself:
	// a mirror that hands out the wrong bytes for an otherwise correctly
	// signed release is still caught, by the checksum it no longer matches.
	release := publishRelease(t, "0.2.0")
	release.CorruptArchive(t)

	dir := filepath.Join(t.TempDir(), "stage")
	fetcher := &Fetcher{Sources: release.sources(), PublicKey: release.PublicKey, SanityCheck: acceptAnyBinary}

	_, err := fetcher.Fetch(context.Background(), "0.2.0", dir)
	if err == nil {
		t.Fatal("a checksum mismatch must fail")
	}
	if got := ExitCodeFor(err); got != ExitChecksum {
		t.Errorf("exit code = %d, want %d", got, ExitChecksum)
	}
	if !strings.Contains(err.Error(), "checksum verification failed") {
		t.Errorf("error = %v, want it to say what failed", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ExecutableName())); statErr == nil {
		t.Error("nothing must be extracted from an archive that failed verification")
	}
}

func TestAMissingSignatureIsRefused(t *testing.T) {
	release := publishRelease(t, "0.2.0")
	release.RemoveSignature(t)

	dir := filepath.Join(t.TempDir(), "stage")
	fetcher := &Fetcher{Sources: release.sources(), PublicKey: release.PublicKey, SanityCheck: acceptAnyBinary}

	_, err := fetcher.Fetch(context.Background(), "0.2.0", dir)
	if got := ExitCodeFor(err); got != ExitSignature {
		t.Errorf("exit code = %d, want %d", got, ExitSignature)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ExecutableName())); statErr == nil {
		t.Error("nothing must be extracted when the signature is missing")
	}
}

func TestASignatureFromAnUnrelatedKeyIsRefused(t *testing.T) {
	release := publishRelease(t, "0.2.0")
	wrongKey := testSigningKey(t)

	dir := filepath.Join(t.TempDir(), "stage")
	fetcher := &Fetcher{
		Sources:     release.sources(),
		PublicKey:   &wrongKey.PublicKey,
		SanityCheck: acceptAnyBinary,
	}

	_, err := fetcher.Fetch(context.Background(), "0.2.0", dir)
	if got := ExitCodeFor(err); got != ExitSignature {
		t.Errorf("exit code = %d, want %d", got, ExitSignature)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ExecutableName())); statErr == nil {
		t.Error("nothing must be extracted when the key does not match")
	}
}

func TestAnAssetMissingFromTheChecksumsFileIsRefused(t *testing.T) {
	release := publishRelease(t, "0.2.0")
	writeChecksums(t, filepath.Join(release.Dir, "checksums.txt"), map[string]string{
		"intenter_0.2.0_plan9_386.tar.gz": strings.Repeat("a", 64),
	})
	release.sign(t)

	fetcher := &Fetcher{Sources: release.sources(), PublicKey: release.PublicKey, SanityCheck: acceptAnyBinary}
	_, err := fetcher.Fetch(context.Background(), "0.2.0", filepath.Join(t.TempDir(), "stage"))
	if ExitCodeFor(err) != ExitChecksum {
		t.Fatalf("err = %v (exit %d), want a checksum failure", err, ExitCodeFor(err))
	}
}

func TestAMissingReleaseIsADownloadFailure(t *testing.T) {
	release := publishRelease(t, "0.2.0")
	fetcher := &Fetcher{Sources: release.sources(), SanityCheck: acceptAnyBinary}

	_, err := fetcher.Fetch(context.Background(), "9.9.9", filepath.Join(t.TempDir(), "stage"))
	if ExitCodeFor(err) != ExitDownload {
		t.Fatalf("err = %v (exit %d), want a download failure", err, ExitCodeFor(err))
	}
}

func TestAnInterruptedDownloadLeavesNothingInstalled(t *testing.T) {
	// A server that closes mid-transfer is what a dropped connection looks
	// like; it must fail rather than produce a truncated binary.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1024")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("half"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		if hijacker, ok := w.(http.Hijacker); ok {
			connection, _, err := hijacker.Hijack()
			if err == nil {
				connection.Close()
			}
		}
	}))
	defer server.Close()

	fetcher := &Fetcher{
		Sources:     Sources{DownloadBase: server.URL + "/releases/download", Overridden: true},
		SanityCheck: acceptAnyBinary,
	}
	dir := filepath.Join(t.TempDir(), "stage")

	if _, err := fetcher.Fetch(context.Background(), "0.2.0", dir); err == nil {
		t.Fatal("a truncated download must fail")
	}
	if _, err := os.Stat(filepath.Join(dir, ExecutableName())); err == nil {
		t.Error("a truncated download must not produce a binary")
	}
}

func TestAnArchiveWithoutTheBinaryIsRefused(t *testing.T) {
	release := publishRelease(t, "0.2.0")
	asset := AssetName("0.2.0")
	path := filepath.Join(release.Dir, asset)

	if strings.HasSuffix(asset, ".zip") {
		writeZip(t, path, "somethingelse", []byte("not the binary"))
	} else {
		writeTarGz(t, path, "somethingelse", []byte("not the binary"))
	}
	writeChecksums(t, filepath.Join(release.Dir, "checksums.txt"), map[string]string{asset: sha256Of(t, path)})
	release.sign(t)

	fetcher := &Fetcher{Sources: release.sources(), PublicKey: release.PublicKey, SanityCheck: acceptAnyBinary}
	_, err := fetcher.Fetch(context.Background(), "0.2.0", filepath.Join(t.TempDir(), "stage"))
	if err == nil || !strings.Contains(err.Error(), "contains no") {
		t.Fatalf("err = %v, want a complaint that the archive holds no binary", err)
	}
}

func TestABuildThatReportsTheWrongVersionIsRefused(t *testing.T) {
	// A correctly-checksummed archive can still be the wrong build — a release
	// published with the previous binary in it, say. Running it is the only way
	// to know, and it happens before anything is replaced.
	release := publishReleaseSaying(t, "0.2.0", "0.1.0")
	fetcher := &Fetcher{
		Sources:   release.sources(),
		PublicKey: release.PublicKey,
		SanityCheck: func(path, want string) error {
			return runVersionCheckWith(path, want, release.BinaryOutput)
		},
	}

	_, err := fetcher.Fetch(context.Background(), "0.2.0", filepath.Join(t.TempDir(), "stage"))
	if err == nil || !strings.Contains(err.Error(), "0.1.0") {
		t.Fatalf("err = %v, want a complaint naming what the build reported", err)
	}
}

func TestTheSanityCheckRunsTheBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in build is a shell script; the e2e suite covers the real thing")
	}
	dir := t.TempDir()
	good := filepath.Join(dir, "good")
	if err := os.WriteFile(good, []byte("#!/bin/sh\necho 'intenter 0.2.0'\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := runVersionCheck(good, "0.2.0"); err != nil {
		t.Errorf("a build reporting the right version must pass: %v", err)
	}
	if err := runVersionCheck(good, "0.3.0"); err == nil {
		t.Error("a build reporting a different version must fail")
	}

	broken := filepath.Join(dir, "broken")
	if err := os.WriteFile(broken, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := runVersionCheck(broken, "0.2.0"); err == nil {
		t.Error("a build that does not run must fail")
	}
}

func TestAssetNamesMatchWhatIsPublished(t *testing.T) {
	// If this drifts from the release workflow, every update fails with a 404.
	got := AssetName("v0.2.0")
	want := fmt.Sprintf("intenter_0.2.0_%s_%s", runtime.GOOS, runtime.GOARCH)
	if !strings.HasPrefix(got, want) {
		t.Errorf("asset = %q, want it to start with %q", got, want)
	}
	if runtime.GOOS == "windows" {
		if !strings.HasSuffix(got, ".zip") {
			t.Errorf("asset = %q, want a .zip on Windows", got)
		}
	} else if !strings.HasSuffix(got, ".tar.gz") {
		t.Errorf("asset = %q, want a .tar.gz", got)
	}
}

func TestPlainHTTPDownloadsAreRefused(t *testing.T) {
	release := publishRelease(t, "0.2.0")
	sources := release.sources()
	sources.Overridden = false

	fetcher := &Fetcher{Sources: sources, SanityCheck: acceptAnyBinary}
	_, err := fetcher.Fetch(context.Background(), "0.2.0", filepath.Join(t.TempDir(), "stage"))
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("err = %v, want plain HTTP to be refused", err)
	}
}

func TestExitCodeForUnwrapsFailures(t *testing.T) {
	if got := ExitCodeFor(nil); got != 0 {
		t.Errorf("no error = %d, want 0", got)
	}
	if got := ExitCodeFor(errors.New("plain")); got != ExitUsage {
		t.Errorf("a plain error = %d, want %d", got, ExitUsage)
	}
	wrapped := fmt.Errorf("while updating: %w", failf(ExitChecksum, "mismatch"))
	if got := ExitCodeFor(wrapped); got != ExitChecksum {
		t.Errorf("a wrapped failure = %d, want %d", got, ExitChecksum)
	}
}

// runVersionCheckWith stands in for running a build that cannot be executed on
// this platform, reporting what it would have printed.
func runVersionCheckWith(path, want, reports string) error {
	if strings.Contains(reports, want) {
		return nil
	}
	return failf(ExitDownload,
		"updater: the downloaded build reports %q, not %s; nothing was installed",
		"intenter "+reports, want)
}
