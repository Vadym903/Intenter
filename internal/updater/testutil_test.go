package updater

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeRelease is a release published by a local server: one archive for the
// running OS and architecture, the checksums file that vouches for it, and a
// signature over that file made by a key generated for the test.
//
// It exists so download, verification and replacement can be exercised end to
// end without publishing anything — the same reason tools/releaseserve exists
// for the installers.
type fakeRelease struct {
	Version string
	// Dir holds the assets on disk.
	Dir string
	// URL is the base of the server serving them.
	URL string
	// BinaryOutput is what the binary inside the archive prints for `version`.
	BinaryOutput string
	// PrivateKey signed checksums.txt.sig; PublicKey is its counterpart, for a
	// Fetcher to verify against.
	PrivateKey *ecdsa.PrivateKey
	PublicKey  *ecdsa.PublicKey
}

// testSigningKey generates a fresh ECDSA P-256 key pair, the same curve
// cosign uses (research R-05).
func testSigningKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}
	return key
}

// publishRelease builds a release whose binary reports the given version.
func publishRelease(t *testing.T, version string) *fakeRelease {
	t.Helper()
	return publishReleaseSaying(t, version, version)
}

// publishReleaseSaying builds a release whose binary reports something other
// than the version it is published as, so the sanity check can be tested.
func publishReleaseSaying(t *testing.T, version, reports string) *fakeRelease {
	t.Helper()
	dir := t.TempDir()

	binary := fakeBinary(t, reports)
	archive := AssetName(version)
	archivePath := filepath.Join(dir, archive)
	if strings.HasSuffix(archive, ".zip") {
		writeZip(t, archivePath, ExecutableName(), binary)
	} else {
		writeTarGz(t, archivePath, ExecutableName(), binary)
	}
	writeChecksums(t, filepath.Join(dir, "checksums.txt"), map[string]string{archive: sha256Of(t, archivePath)})

	privateKey := testSigningKey(t)
	release := &fakeRelease{
		Version:      version,
		Dir:          dir,
		BinaryOutput: reports,
		PrivateKey:   privateKey,
		PublicKey:    &privateKey.PublicKey,
	}
	release.sign(t)
	release.URL = serveRelease(t, release)
	return release
}

// sign (re)writes checksums.txt.sig to match the checksums.txt currently on
// disk — the way the release pipeline signs whatever it just published
// (contracts/release-and-signing.md §2).
func (r *fakeRelease) sign(t *testing.T) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(r.Dir, "checksums.txt"))
	if err != nil {
		t.Fatalf("read checksums.txt: %v", err)
	}
	digest := sha256.Sum256(data)
	signature, err := ecdsa.SignASN1(rand.Reader, r.PrivateKey, digest[:])
	if err != nil {
		t.Fatalf("sign checksums.txt: %v", err)
	}
	encoded := base64.StdEncoding.EncodeToString(signature)
	if err := os.WriteFile(filepath.Join(r.Dir, "checksums.txt.sig"), []byte(encoded), 0o644); err != nil {
		t.Fatalf("write checksums.txt.sig: %v", err)
	}
}

// serveRelease starts an HTTP server for a release directory.
func serveRelease(t *testing.T, release *fakeRelease) string {
	t.Helper()
	tag := "v" + release.Version

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/releases/latest":
			http.Redirect(w, r, "/releases/tag/"+tag, http.StatusFound)
		case strings.HasPrefix(r.URL.Path, "/releases/download/"+tag+"/"):
			name := filepath.Base(r.URL.Path)
			path := filepath.Join(release.Dir, name)
			if _, err := os.Stat(path); err != nil {
				http.NotFound(w, r)
				return
			}
			http.ServeFile(w, r, path)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// Corrupt rewrites checksums.txt so nothing in it matches, without
// re-signing — what a checksums file altered after publication looks like:
// the signature that vouched for the original bytes is now stale, so
// verification fails before the (also wrong) checksum is ever compared.
func (r *fakeRelease) Corrupt(t *testing.T) {
	t.Helper()
	writeChecksums(t, filepath.Join(r.Dir, "checksums.txt"), map[string]string{
		AssetName(r.Version): strings.Repeat("0", 64),
	})
}

// CorruptArchive rewrites the published archive after checksums.txt was
// already signed for the original bytes — what a mirror handing out the
// wrong file looks like: the signature over checksums.txt still verifies,
// but the archive no longer matches the checksum it lists.
func (r *fakeRelease) CorruptArchive(t *testing.T) {
	t.Helper()
	path := filepath.Join(r.Dir, AssetName(r.Version))
	if err := os.WriteFile(path, []byte("not the archive that was signed for"), 0o644); err != nil {
		t.Fatalf("corrupt archive: %v", err)
	}
}

// RemoveSignature deletes checksums.txt.sig, what an old release host or a
// stripped mirror would leave missing.
func (r *fakeRelease) RemoveSignature(t *testing.T) {
	t.Helper()
	if err := os.Remove(filepath.Join(r.Dir, "checksums.txt.sig")); err != nil {
		t.Fatalf("remove checksums.txt.sig: %v", err)
	}
}

// sources points the updater at this release.
func (r *fakeRelease) sources() Sources {
	return Sources{
		Repo:         "Vadym903/Intenter",
		LatestURL:    r.URL + "/releases/latest",
		AtomURL:      r.URL + "/releases.atom",
		DownloadBase: r.URL + "/releases/download",
		Overridden:   true,
	}
}

// fakeBinary is a tiny executable that prints a version, so the sanity run
// after extraction has something real to run. A shell script is enough on
// POSIX; on Windows a batch file plays the same part.
func fakeBinary(t *testing.T, reports string) []byte {
	t.Helper()
	if runtime.GOOS == "windows" {
		return []byte("@echo off\r\necho intenter " + reports + "\r\n")
	}
	return []byte("#!/bin/sh\necho \"intenter " + reports + "\"\n")
}

func writeTarGz(t *testing.T, path, name string, content []byte) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer file.Close()

	gz := gzip.NewWriter(file)
	archive := tar.NewWriter(gz)
	header := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}
	if err := archive.WriteHeader(header); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := archive.Write(content); err != nil {
		t.Fatalf("tar write: %v", err)
	}
	// A real archive carries more than the binary; including a second entry
	// proves extraction picks the one it wants rather than the first.
	notes := []byte("see the release page\n")
	if err := archive.WriteHeader(&tar.Header{Name: "README.md", Mode: 0o644, Size: int64(len(notes)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := archive.Write(notes); err != nil {
		t.Fatalf("tar write: %v", err)
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
}

func writeZip(t *testing.T, path, name string, content []byte) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer file.Close()

	archive := zip.NewWriter(file)
	for entryName, entry := range map[string][]byte{name: content, "README.md": []byte("see the release page\n")} {
		writer, err := archive.Create(entryName)
		if err != nil {
			t.Fatalf("zip entry %s: %v", entryName, err)
		}
		if _, err := writer.Write(entry); err != nil {
			t.Fatalf("zip write: %v", err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
}

// writeChecksums writes the two-space format `sha256sum` produces, which is
// what GoReleaser publishes and what install.sh already parses.
func writeChecksums(t *testing.T, path string, sums map[string]string) {
	t.Helper()
	var builder strings.Builder
	for name, sum := range sums {
		fmt.Fprintf(&builder, "%s  %s\n", sum, name)
	}
	if err := os.WriteFile(path, []byte(builder.String()), 0o644); err != nil {
		t.Fatalf("write checksums: %v", err)
	}
}

func sha256Of(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
