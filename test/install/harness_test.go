// Package install exercises the one-line installers end to end against a fake
// release, on the machine running the tests.
//
// The installers are the least forgiving code in the project: they run once, on
// a stranger's machine, with no Intenter present to report what went wrong.
// A published release cannot be a prerequisite for testing them — that would
// mean every fix ships before it is checked — so these tests build the real
// binary, package it exactly as a release does, and serve it the way GitHub
// does.
package install

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Vadym903/Intenter/internal/releaseserve"
)

const (
	// fakeLatest is the version the fake release publishes as "latest".
	fakeLatest = "9.9.9"
	// fakeOlder stands in for an already-installed previous release.
	fakeOlder = "0.0.1"
)

var (
	buildOnce  sync.Once
	builtPath  string
	buildError error
)

// repoRoot locates the module root from this file's own path.
func repoRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "."
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

// buildIntenter compiles the real binary once per test run, with the version
// baked in the way a release does.
func buildIntenter(t *testing.T, version string) string {
	t.Helper()

	// The default build is cached; a version-stamped one is cheap enough to
	// redo, and only the upgrade tests need it.
	if version == fakeLatest {
		buildOnce.Do(func() { builtPath, buildError = compile(fakeLatest) })
		if buildError != nil {
			t.Fatalf("%v", buildError)
		}
		return builtPath
	}

	path, err := compile(version)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return path
}

func compile(version string) (string, error) {
	dir, err := os.MkdirTemp("", "intenter-build-")
	if err != nil {
		return "", fmt.Errorf("temp dir: %w", err)
	}

	name := "intenter"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	out := filepath.Join(dir, name)

	ldflags := "-X github.com/Vadym903/Intenter/internal/version.Version=" + version
	cmd := exec.Command("go", "build", "-ldflags", ldflags, "-o", out, "./cmd/intenter")
	cmd.Dir = repoRoot()
	if arch := osArch(); arch != runtime.GOARCH {
		// The asset is named for the OS architecture; build the binary to match.
		cmd.Env = append(os.Environ(), "GOARCH="+arch)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("build intenter %s: %v\n%s", version, err, stderr.String())
	}
	return out, nil
}

// release is a directory of assets served as one GitHub release.
type release struct {
	dir     string
	tag     string
	baseURL string
	// privateKey signed checksums.txt.sig; pubKeyPath is a PEM file holding
	// its public half, for INTENTER_SIGNING_KEY_FILE to point installs under
	// test at instead of the embedded release key (installers' T043/T044).
	privateKey *ecdsa.PrivateKey
	pubKeyPath string
}

// newRelease packages the built binary for this OS and architecture exactly as
// the release workflow does, signs it, and serves it.
func newRelease(t *testing.T, version string) *release {
	t.Helper()

	dir := t.TempDir()
	binary := buildIntenter(t, version)
	asset := assetName(version)

	if strings.HasSuffix(asset, ".zip") {
		writeZip(t, filepath.Join(dir, asset), binary)
	} else {
		writeTarGz(t, filepath.Join(dir, asset), binary)
	}
	writeChecksums(t, dir)

	handler, err := releaseserve.Handler(dir, "v"+version)
	if err != nil {
		t.Fatalf("release handler: %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	rel := &release{dir: dir, tag: "v" + version, baseURL: server.URL}
	rel.sign(t, testSigningKey(t))
	return rel
}

// testSigningKey generates a fresh ECDSA P-256 key pair, the same curve
// cosign uses (research R-05) and internal/updater's tests generate.
func testSigningKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}
	return key
}

// sign (re)signs checksums.txt with key, writes checksums.txt.sig next to it
// — the format `cosign sign-blob --key` produces (contracts/release-and-
// signing.md §2) — and writes key's public half to a PEM file the installers'
// INTENTER_SIGNING_KEY_FILE override can point at.
func (r *release) sign(t *testing.T, key *ecdsa.PrivateKey) {
	t.Helper()

	r.privateKey = key
	data, err := os.ReadFile(filepath.Join(r.dir, "checksums.txt"))
	if err != nil {
		t.Fatalf("read checksums.txt: %v", err)
	}
	digest := sha256.Sum256(data)
	signature, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatalf("sign checksums.txt: %v", err)
	}
	encoded := base64.StdEncoding.EncodeToString(signature)
	if err := os.WriteFile(filepath.Join(r.dir, "checksums.txt.sig"), []byte(encoded), 0o644); err != nil {
		t.Fatalf("write checksums.txt.sig: %v", err)
	}

	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	block := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	// A fresh path each call: reusing one across a mid-test re-sign would let
	// a stale env value keep pointing at the old key.
	pubKeyPath := filepath.Join(t.TempDir(), "test-signing-key.pub")
	if err := os.WriteFile(pubKeyPath, block, 0o644); err != nil {
		t.Fatalf("write public key: %v", err)
	}
	r.pubKeyPath = pubKeyPath
}

// tamperSignature rewrites checksums.txt.sig with a signature made by an
// unrelated key, over the same (untouched) checksums.txt — what a stale or
// substituted signature file looks like: the checksums are unaltered, but
// nothing vouches for them anymore.
func (r *release) tamperSignature(t *testing.T) {
	t.Helper()
	other := testSigningKey(t)
	data, err := os.ReadFile(filepath.Join(r.dir, "checksums.txt"))
	if err != nil {
		t.Fatalf("read checksums.txt: %v", err)
	}
	digest := sha256.Sum256(data)
	signature, err := ecdsa.SignASN1(rand.Reader, other, digest[:])
	if err != nil {
		t.Fatalf("sign checksums.txt: %v", err)
	}
	encoded := base64.StdEncoding.EncodeToString(signature)
	if err := os.WriteFile(filepath.Join(r.dir, "checksums.txt.sig"), []byte(encoded), 0o644); err != nil {
		t.Fatalf("write checksums.txt.sig: %v", err)
	}
}

// removeSignature deletes checksums.txt.sig, what an old release host or a
// stripped mirror would leave missing.
func (r *release) removeSignature(t *testing.T) {
	t.Helper()
	if err := os.Remove(filepath.Join(r.dir, "checksums.txt.sig")); err != nil {
		t.Fatalf("remove checksums.txt.sig: %v", err)
	}
}

// assetName is the archive name for the running platform.
func assetName(version string) string {
	name := fmt.Sprintf("intenter_%s_%s_%s", version, runtime.GOOS, osArch())
	if runtime.GOOS == "windows" {
		return name + ".zip"
	}
	return name + ".tar.gz"
}

// osArch is the architecture the installer will request: the OS's, not the Go
// toolchain's. install.ps1 asks for the asset matching OSArchitecture, so a
// 32-bit toolchain on 64-bit Windows must still serve the amd64 asset.
// Mirrors Get-Architecture in install.ps1.
func osArch() string {
	if runtime.GOOS != "windows" {
		return runtime.GOARCH
	}
	arch := os.Getenv("PROCESSOR_ARCHITEW6432") // set when running under WOW64
	if arch == "" {
		arch = os.Getenv("PROCESSOR_ARCHITECTURE")
	}
	switch strings.ToUpper(arch) {
	case "AMD64":
		return "amd64"
	case "ARM64":
		return "arm64"
	}
	return runtime.GOARCH
}

func writeTarGz(t *testing.T, path, binary string) {
	t.Helper()

	content, err := os.ReadFile(binary)
	if err != nil {
		t.Fatalf("read binary: %v", err)
	}

	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer file.Close()

	gz := gzip.NewWriter(file)
	archive := tar.NewWriter(gz)

	// A release archive carries the binary plus the two files GoReleaser adds.
	entries := map[string][]byte{
		"intenter":  content,
		"README.md": []byte("# Intenter\n"),
		"LICENSE":   []byte("MIT\n"),
	}
	for _, name := range []string{"intenter", "README.md", "LICENSE"} {
		mode := int64(0o644)
		if name == "intenter" {
			mode = 0o755
		}
		header := &tar.Header{
			Name: name, Mode: mode, Size: int64(len(entries[name])), ModTime: time.Now(),
		}
		if err := archive.WriteHeader(header); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := archive.Write(entries[name]); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
}

func writeZip(t *testing.T, path, binary string) {
	t.Helper()

	content, err := os.ReadFile(binary)
	if err != nil {
		t.Fatalf("read binary: %v", err)
	}

	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer file.Close()

	archive := zip.NewWriter(file)
	entries := map[string][]byte{
		"intenter.exe": content,
		"README.md":    []byte("# Intenter\n"),
		"LICENSE":      []byte("MIT\n"),
	}
	for _, name := range []string{"intenter.exe", "README.md", "LICENSE"} {
		writer, err := archive.Create(name)
		if err != nil {
			t.Fatalf("zip entry: %v", err)
		}
		if _, err := writer.Write(entries[name]); err != nil {
			t.Fatalf("zip write: %v", err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
}

// writeChecksums produces the `<sha256>  <asset>` file a release publishes.
func writeChecksums(t *testing.T, dir string) {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}

	var lines strings.Builder
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "checksums.txt" {
			continue
		}
		lines.WriteString(fmt.Sprintf("%s  %s\n", sha256File(t, filepath.Join(dir, entry.Name())), entry.Name()))
	}
	if err := os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(lines.String()), 0o644); err != nil {
		t.Fatalf("write checksums: %v", err)
	}
}

func sha256File(t *testing.T, path string) string {
	t.Helper()

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer file.Close()

	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		t.Fatalf("hash %s: %v", path, err)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

// corrupt rewrites an asset so its checksum no longer matches, standing in for
// a tampered or truncated download.
func (r *release) corrupt(t *testing.T, version string) {
	t.Helper()
	path := filepath.Join(r.dir, assetName(version))
	if err := os.WriteFile(path, []byte("not the archive you were promised"), 0o644); err != nil {
		t.Fatalf("corrupt %s: %v", path, err)
	}
}

// env is one isolated machine: its own home, install directory and PATH.
type env struct {
	t       *testing.T
	release *release

	Home       string
	InstallDir string
	DataDir    string
	// Extra holds environment overrides a single test needs.
	Extra map[string]string
}

func newEnv(t *testing.T, rel *release) *env {
	t.Helper()

	// Kept short: a Unix socket path has a hard length limit, and the daemon
	// binds one under the data directory.
	base, err := os.MkdirTemp("/tmp", "agi")
	if err != nil {
		base = t.TempDir()
	} else {
		t.Cleanup(func() { os.RemoveAll(base) })
	}

	home := filepath.Join(base, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("mkdir home: %v", err)
	}

	return &env{
		t:          t,
		release:    rel,
		Home:       home,
		InstallDir: filepath.Join(home, ".local", "bin"),
		DataDir:    filepath.Join(base, "data"),
		Extra:      map[string]string{},
	}
}

// environ builds the environment an installer run sees.
func (e *env) environ() []string {
	values := map[string]string{
		"HOME":                   e.Home,
		"USERPROFILE":            e.Home,
		"INTENTER_INSTALL_DIR":   e.InstallDir,
		"INTENTER_LATEST_URL":    e.release.baseURL + "/releases/latest",
		"INTENTER_DOWNLOAD_BASE": e.release.baseURL + "/releases/download",
		// The installed binary must not touch the developer's real state if a
		// test runs it.
		"INTENTER_TEST_MODE":   "1",
		"INTENTER_TEST_HOME":   e.Home,
		"INTENTER_DATA_DIR":    e.DataDir,
		"INTENTER_CONFIG_DIR":  filepath.Join(e.DataDir, "config"),
		"INTENTER_RUNTIME_DIR": filepath.Join(e.DataDir, "run"),
		// The fake release is signed with a throwaway key, not the real
		// release key, so the installer under test must be pointed at it.
		// Honored by the installers only under INTENTER_TEST_MODE=1 (T043/44).
		"INTENTER_SIGNING_KEY_FILE": e.release.pubKeyPath,
	}
	for name, value := range e.Extra {
		values[name] = value
	}

	// PATH and the platform's own variables are inherited; everything else is
	// replaced, so a developer's settings cannot change a result.
	out := []string{"PATH=" + os.Getenv("PATH")}
	for _, name := range []string{"SystemRoot", "TEMP", "TMP", "ComSpec", "PATHEXT", "USERNAME", "LOGNAME"} {
		if value := os.Getenv(name); value != "" {
			out = append(out, name+"="+value)
		}
	}
	for name, value := range values {
		out = append(out, name+"="+value)
	}
	return out
}

// result is one installer run.
type result struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
}

// Output is stdout and stderr together, which is what a user sees.
func (r result) Output() string { return r.Stdout + r.Stderr }

// run executes the POSIX installer with the given arguments.
func (e *env) run(args ...string) result {
	e.t.Helper()
	return e.runWith("sh", append([]string{filepath.Join(repoRoot(), "install.sh")}, args...)...)
}

// runWith executes an installer through a chosen interpreter.
func (e *env) runWith(interpreter string, args ...string) result {
	e.t.Helper()

	cmd := exec.Command(interpreter, args...)
	cmd.Env = e.environ()
	cmd.Dir = e.Home
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	started := time.Now()
	err := cmd.Run()
	elapsed := time.Since(started)

	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			e.t.Fatalf("run %s: %v\n%s%s", interpreter, err, stdout.String(), stderr.String())
		}
		code = exitErr.ExitCode()
	}
	return result{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: code, Duration: elapsed}
}

// installedBinary is where the installer should have put the binary.
func (e *env) installedBinary() string {
	name := "intenter"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(e.InstallDir, name)
}

// installedVersion asks the installed binary what it is.
func (e *env) installedVersion() (string, error) {
	out, err := exec.Command(e.installedBinary(), "version").Output()
	if err != nil {
		return "", err
	}
	// The first line reads "intenter <version>".
	fields := strings.Fields(strings.SplitN(string(out), "\n", 2)[0])
	if len(fields) < 2 {
		return "", fmt.Errorf("unexpected version output: %q", out)
	}
	return fields[1], nil
}

// preinstall puts a binary of the given version in place, standing in for an
// installation an upgrade will replace.
func (e *env) preinstall(t *testing.T, version string) {
	t.Helper()

	if err := os.MkdirAll(e.InstallDir, 0o755); err != nil {
		t.Fatalf("mkdir install dir: %v", err)
	}
	source := buildIntenter(t, version)
	content, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read %s: %v", source, err)
	}
	if err := os.WriteFile(e.installedBinary(), content, 0o755); err != nil {
		t.Fatalf("write installed binary: %v", err)
	}
}

// writeRC creates a shell rc file the installer may add a PATH block to.
func (e *env) writeRC(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(e.Home, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// readFile returns a file's contents, or "" when it does not exist.
func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(content)
}

// exists reports whether a path is present.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// claudeShim puts a fake `claude` on PATH so `setup claude` has something to
// find, without a real Claude Code installation.
func (e *env) claudeShim(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the shim relies on a POSIX shebang")
	}

	dir := filepath.Join(e.Home, "shims")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	script := "#!/bin/sh\necho '2.1.233'\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatalf("write shim: %v", err)
	}
	e.Extra["PATH"] = dir + string(os.PathListSeparator) + os.Getenv("PATH")
}

// runShellFunction sources install.sh and calls one of its functions, so a
// helper can be tested on its own rather than only through a full install.
//
// Sourcing works because the script only acts when `main` runs, and `main` is
// skipped when INTENTER_SOURCE_ONLY is set.
func runShellFunction(t *testing.T, function string, args ...string) string {
	t.Helper()

	script := "INTENTER_SOURCE_ONLY=1 . " + filepath.Join(repoRoot(), "install.sh") + "; " +
		function + " " + strings.Join(args, " ")

	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(), "INTENTER_SOURCE_ONLY=1")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run %s: %v\n%s", function, err, out)
	}
	return strings.TrimSpace(string(out))
}

// requireLine fails unless the output contains a line.
func requireLine(t *testing.T, out, want string) {
	t.Helper()
	if !strings.Contains(out, want) {
		t.Errorf("output is missing %q:\n%s", want, out)
	}
}

// refuteLine fails when the output contains a line it should not.
func refuteLine(t *testing.T, out, unwanted string) {
	t.Helper()
	if strings.Contains(out, unwanted) {
		t.Errorf("output must not contain %q:\n%s", unwanted, out)
	}
}
