//go:build windows

package updater

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockOffset is the byte the lock covers. Windows byte-range locks are
// mandatory: a locked range cannot be read through any other handle, and the
// holder's record lives at the start of the file, where other terminals read
// it (PromptLockHolder). Locking one byte far past the end of the file — which
// Windows permits — keeps the record readable while a second lock still fails.
const lockOffset = 0x40000000

// lockFile takes an exclusive lock, failing immediately if another process
// holds one. The handle is released when the process exits, so a terminal
// closed mid-prompt never silences the next one.
//
// Contention is reported as errLockBusy; anything else means this filesystem
// could not attempt the lock, which the caller treats differently.
func lockFile(file *os.File) error {
	overlapped := &windows.Overlapped{Offset: lockOffset}
	err := windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, overlapped)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
		return errLockBusy
	}
	return err
}

func unlockFile(file *os.File) error {
	overlapped := &windows.Overlapped{Offset: lockOffset}
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, overlapped)
}
