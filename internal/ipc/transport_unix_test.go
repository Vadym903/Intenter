//go:build darwin || linux

package ipc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnixSocketPermissionsAndStaleCleanup(t *testing.T) {
	dir := shortTempDir(t)
	endpoint := filepath.Join(dir, "intenter.sock")

	// A socket file left behind by a crashed daemon must not block bind.
	if err := os.WriteFile(endpoint, nil, 0o600); err != nil {
		t.Fatalf("write stale socket: %v", err)
	}

	listener, err := Listen(endpoint)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer listener.Close()

	info, err := os.Stat(endpoint)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("socket mode = %v, want 0600 (§10.1)", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Errorf("socket directory mode = %v, want 0700 (§10.1)", dirInfo.Mode().Perm())
	}
}

func TestUnixSocketPathLengthIsChecked(t *testing.T) {
	long := filepath.Join(shortTempDir(t), strings.Repeat("d", maxSocketPathLen), "intenter.sock")

	_, err := Listen(long)
	if err == nil {
		t.Fatal("an over-long socket path must be rejected")
	}
	if !strings.Contains(err.Error(), "INTENTER_RUNTIME_DIR") {
		t.Errorf("error must suggest INTENTER_RUNTIME_DIR, got %v", err)
	}
}
