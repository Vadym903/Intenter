package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Lock guarantees a single daemon instance per user (§9.1). It holds an
// OS-level advisory lock on a file in the runtime directory and publishes the
// pid next to it so `daemon stop` can find the process.
type Lock struct {
	file     *os.File
	lockPath string
	pidPath  string
}

// ErrAlreadyRunning is returned when another daemon already holds the lock.
type ErrAlreadyRunning struct {
	PID int
}

func (e *ErrAlreadyRunning) Error() string {
	if e.PID > 0 {
		return fmt.Sprintf("another intenter daemon is already running (pid %d)", e.PID)
	}
	return "another intenter daemon is already running"
}

// AcquireLock takes the single-instance lock, or reports who holds it.
func AcquireLock(lockPath, pidPath string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return nil, fmt.Errorf("daemon: create runtime directory: %w", err)
	}

	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("daemon: open lock file %s: %w", lockPath, err)
	}

	if err := lockFile(file); err != nil {
		file.Close()
		return nil, &ErrAlreadyRunning{PID: ReadPidFile(pidPath)}
	}

	lock := &Lock{file: file, lockPath: lockPath, pidPath: pidPath}
	if err := lock.writePid(os.Getpid()); err != nil {
		_ = lock.Release()
		return nil, err
	}
	return lock, nil
}

func (l *Lock) writePid(pid int) error {
	if err := os.WriteFile(l.pidPath, []byte(strconv.Itoa(pid)+"\n"), 0o600); err != nil {
		return fmt.Errorf("daemon: write pid file %s: %w", l.pidPath, err)
	}
	return nil
}

// Release unlocks and removes the lock and pid files.
//
// The pid file goes last, after the lock is free: `daemon stop`, the updater's
// restart and the uninstaller all take its disappearance as the moment the
// next daemon can start or the data can be purged. Removing it first would
// hand a successor a lock that is still held.
func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := unlockFile(l.file)
	closeErr := l.file.Close()
	l.file = nil

	os.Remove(l.pidPath)
	os.Remove(l.lockPath)

	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

// ReadPidFile returns the pid recorded in a pid file, or 0.
func ReadPidFile(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return 0
	}
	return pid
}
