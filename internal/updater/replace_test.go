package updater

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestTheBinaryIsReplacedInPlace(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ExecutableName())
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}

	source := filepath.Join(t.TempDir(), "downloaded")
	if err := os.WriteFile(source, []byte("new"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := Replace(source, target); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(content) != "new" {
		t.Errorf("content = %q, want the new binary", content)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		t.Errorf("mode = %v, want an executable — a downloaded file arrives without the bit", info.Mode().Perm())
	}
}

func TestReplacingLeavesNoStagingFileBehind(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ExecutableName())
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	source := filepath.Join(t.TempDir(), "downloaded")
	if err := os.WriteFile(source, []byte("new"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := Replace(source, target); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, entry := range entries {
		if entry.Name() != ExecutableName() {
			t.Errorf("%s was left in the install directory", entry.Name())
		}
	}
}

func TestReplacingIntoANewLocationWorks(t *testing.T) {
	// A first install through the updater has nothing to displace.
	dir := t.TempDir()
	target := filepath.Join(dir, ExecutableName())
	source := filepath.Join(t.TempDir(), "downloaded")
	if err := os.WriteFile(source, []byte("new"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := Replace(source, target); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("the binary was not installed: %v", err)
	}
}

func TestAReadOnlyDirectoryFailsWithoutDamage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory mode bits do not deny writes on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root writes to read-only directories")
	}

	dir := t.TempDir()
	target := filepath.Join(dir, ExecutableName())
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })

	source := filepath.Join(t.TempDir(), "downloaded")
	if err := os.WriteFile(source, []byte("new"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	err := Replace(source, target)
	if err == nil {
		t.Fatal("replacing into a read-only directory must fail")
	}
	if got := ExitCodeFor(err); got != ExitNotWritable {
		t.Errorf("exit code = %d, want %d", got, ExitNotWritable)
	}
	if content, readErr := os.ReadFile(target); readErr != nil || string(content) != "old" {
		t.Errorf("the installed binary must be untouched, got %q (%v)", content, readErr)
	}
}

func TestARunningCopyIsReplaced(t *testing.T) {
	// The case the whole rename dance exists for: on Windows the file cannot be
	// overwritten while it is mapped as a running image, and on POSIX the
	// running process must keep working afterwards.
	if runtime.GOOS == "windows" {
		t.Skip("building a Windows helper binary needs a compiler step the e2e suite already does")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, ExecutableName())
	if err := os.WriteFile(target, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}

	running := exec.Command(target)
	if err := running.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		running.Process.Kill()
		running.Wait()
	})

	source := filepath.Join(t.TempDir(), "downloaded")
	if err := os.WriteFile(source, []byte("#!/bin/sh\necho new\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := Replace(source, target); err != nil {
		t.Fatalf("replacing a running binary must work: %v", err)
	}
	content, err := os.ReadFile(target)
	if err != nil || !strings.Contains(string(content), "echo new") {
		t.Errorf("content = %q (%v), want the new binary", content, err)
	}
}

func TestStaleReplacementsAreCleanedUp(t *testing.T) {
	// Windows cannot delete the displaced binary while it is running, so the
	// next start does it. Only that file: a `.new` may belong to an update
	// happening right now.
	dir := t.TempDir()
	target := filepath.Join(dir, ExecutableName())
	staged := filepath.Join(dir, "."+ExecutableName()+".new")
	displaced := target + ".old"

	for _, path := range []string{target, staged, displaced} {
		if err := os.WriteFile(path, []byte("x"), 0o755); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	CleanStaleReplacements(target)

	if _, err := os.Stat(displaced); err == nil {
		t.Error("the displaced binary must be removed")
	}
	if _, err := os.Stat(staged); err != nil {
		t.Error("a staging file must be left alone: another process may be about to rename it")
	}
	if _, err := os.Stat(target); err != nil {
		t.Error("the installed binary must not be touched")
	}
}

func TestCleaningUpWithoutAPathIsHarmless(t *testing.T) {
	CleanStaleReplacements("")
}
