//go:build !windows

package resolver

import (
	"syscall"
	"testing"
)

// mustMkfifo creates a named pipe, skipping the test where the filesystem
// cannot make one.
func mustMkfifo(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o644); err != nil {
		t.Skipf("mkfifo unsupported here: %v", err)
	}
}
