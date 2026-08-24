package resolver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A repository the user pulled can name any file anything. A FIFO or an
// enormous file under a config name (package.json, .npmrc, .git) must not block
// or exhaust the daemon on the request path (§26). readConfigFile is what the
// package-manager and git readers use to stay safe.

func TestReadConfigFileRejectsNonRegularFiles(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "package.json")
	mustMkfifo(t, fifo)

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := readConfigFile(fifo); err == nil {
			t.Errorf("a FIFO must be refused, not read")
		}
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("readConfigFile blocked on a FIFO — a malicious repo could hang the daemon")
	}
}

func TestReadConfigFileRejectsOversizeFiles(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, ".npmrc")
	if err := os.WriteFile(big, make([]byte, MaxConfigFileBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readConfigFile(big); err == nil {
		t.Error("a file over the cap must be refused")
	}
}

func TestReadConfigFileReadsAnOrdinaryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package.json")
	if err := os.WriteFile(path, []byte(`{"name":"demo"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	content, err := readConfigFile(path)
	if err != nil {
		t.Fatalf("an ordinary file must read: %v", err)
	}
	if !strings.Contains(string(content), "demo") {
		t.Errorf("content = %q", content)
	}
}

func TestFileExistsRejectsAFifo(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "package.json")
	mustMkfifo(t, fifo)
	if fileExists(fifo) {
		t.Error("a FIFO is not a marker file and must not read as existing")
	}
}

// TestDetectPackageManagerDoesNotBlockOnAFifo is the end-to-end version: the
// context builder calls DetectPackageManager on every request, and a FIFO
// package.json used to hang it forever.
func TestDetectPackageManagerDoesNotBlockOnAFifo(t *testing.T) {
	dir := t.TempDir()
	mustMkfifo(t, filepath.Join(dir, "package.json"))

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = DetectPackageManager(dir, filepath.Dir(dir))
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("DetectPackageManager blocked on a FIFO package.json")
	}
}
