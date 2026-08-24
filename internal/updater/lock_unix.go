//go:build darwin || linux

package updater

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// lockFile takes an exclusive lock, failing immediately if another process
// holds one. The kernel releases it when the process exits, so a terminal
// closed mid-prompt never silences the next one.
//
// Contention is reported as errLockBusy; anything else means this filesystem
// could not attempt the lock, which the caller treats differently.
func lockFile(file *os.File) error {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return errLockBusy
	}
	return err
}

func unlockFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
