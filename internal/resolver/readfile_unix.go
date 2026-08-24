//go:build !windows

package resolver

import (
	"os"
	"syscall"
)

// openNoFollowBlocking opens a file for reading without ever blocking on it.
//
// O_NONBLOCK makes opening a FIFO with no writer return at once instead of
// waiting for one; the caller then rejects it by descriptor type. Reads from a
// regular file are unaffected by the flag.
func openNoFollowBlocking(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
