//go:build windows

package resolver

import "os"

// openNoFollowBlocking opens a file for reading. Windows has no FIFO that can
// live under a workspace path, so a plain open cannot block on one; the caller
// still rejects anything that is not a regular file by descriptor type.
func openNoFollowBlocking(path string) (*os.File, error) {
	return os.Open(path)
}
