//go:build windows

package resolver

import "testing"

// mustMkfifo skips on Windows, which has no filesystem FIFO a workspace could
// contain.
func mustMkfifo(t *testing.T, path string) {
	t.Helper()
	t.Skip("no filesystem FIFO on Windows")
}
