package updater

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// lockWait is how long a writer waits for the state lock. Writers hold it only
// for a read-modify-write of one small file, so anything longer than this means
// something is wrong rather than busy — and the start-up path must not stall a
// terminal while it finds out.
const lockWait = 2 * time.Second

// lockPoll is how often a waiting writer retries. The lock is taken
// non-blocking and retried rather than held blocking, because a blocking
// acquisition has no deadline and this one must have one.
const lockPoll = 10 * time.Millisecond

// promptLockStale is how old a prompt-lock record may be before it is reported
// as nobody's. The lock itself is released by the operating system when its
// process exits; this only governs what the recorded contents are believed to
// mean afterwards.
const promptLockStale = 10 * time.Minute

// ErrLockHeld reports that another process holds the lock.
var ErrLockHeld = errors.New("updater: another terminal is prompting or updating")

// errLockBusy is what the platform lock call returns when someone else holds
// the lock, as opposed to a lock that could not be attempted at all.
var errLockBusy = errors.New("updater: lock is busy")

// LockOwner is what a lock holder records for anyone who cannot take the lock.
type LockOwner struct {
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
}

// Lock is a held file lock.
type Lock struct {
	file *os.File
	// Advisory is false when this machine's filesystem could not lock at all.
	// The caller still proceeds: on a home directory that does not support
	// locking, "two terminals might both prompt" is a far better outcome than
	// "no terminal ever prompts".
	Advisory bool
}

// Release unlocks and closes. The file itself stays: deleting a lock file makes
// two processes able to hold locks on two different inodes with the same name.
func (l *Lock) Release() {
	if l == nil || l.file == nil {
		return
	}
	if l.Advisory {
		_ = unlockFile(l.file)
	}
	l.file.Close()
	l.file = nil
}

// acquireLock takes the state lock, waiting up to lockWait for it.
func acquireLock(path string) (func(), error) {
	lock, err := lockWithDeadline(path, time.Now().Add(lockWait))
	if err != nil {
		return func() {}, err
	}
	return lock.Release, nil
}

// AcquirePromptLock takes the prompt lock without waiting, so a terminal that
// loses the race starts silently instead of pausing. It returns ErrLockHeld
// when another process holds it.
func AcquirePromptLock(path string) (*Lock, error) {
	lock, err := lockOnce(path)
	if err != nil {
		return nil, err
	}
	if err := writeLockOwner(lock.file); err != nil {
		lock.Release()
		return nil, err
	}
	return lock, nil
}

// PromptLockHolder describes who holds the prompt lock, for the "an update is
// already in progress" message. A record older than promptLockStale is not
// reported: whatever wrote it is gone, and naming a dead process helps nobody.
func PromptLockHolder(path string) (LockOwner, bool) {
	owner := readLockOwner(path)
	if owner.PID == 0 || time.Since(owner.StartedAt) > promptLockStale {
		return LockOwner{}, false
	}
	return owner, true
}

// lockWithDeadline retries a non-blocking acquisition until the deadline. A
// blocking lock would have no deadline at all, which is the wrong trade on a
// path that runs when a user opens a terminal.
func lockWithDeadline(path string, deadline time.Time) (*Lock, error) {
	for {
		lock, err := lockOnce(path)
		if err == nil {
			return lock, nil
		}
		if !errors.Is(err, ErrLockHeld) || !time.Now().Before(deadline) {
			return nil, err
		}
		time.Sleep(lockPoll)
	}
}

// lockOnce opens the lock file and tries once to take it.
func lockOnce(path string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("updater: create %s: %w", filepath.Dir(path), err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("updater: open lock %s: %w", path, err)
	}

	switch err := lockFile(file); {
	case err == nil:
		return &Lock{file: file, Advisory: true}, nil
	case errors.Is(err, errLockBusy):
		file.Close()
		return nil, ErrLockHeld
	default:
		// Networked and unusual filesystems refuse to lock at all. Refusing to
		// prompt on those machines would be a silent, permanent loss of the
		// feature, so the lock degrades to none rather than to a failure.
		return &Lock{file: file, Advisory: false}, nil
	}
}

// readLockOwner returns the record in a lock file, or the zero value.
func readLockOwner(path string) LockOwner {
	data, err := os.ReadFile(path)
	if err != nil {
		return LockOwner{}
	}
	var owner LockOwner
	if err := json.Unmarshal(data, &owner); err != nil {
		return LockOwner{}
	}
	return owner
}

// writeLockOwner records this process in the lock file it holds.
func writeLockOwner(file *os.File) error {
	return writeOwnerAs(file, LockOwner{PID: os.Getpid(), StartedAt: time.Now().UTC()})
}

func writeOwnerAs(file *os.File, owner LockOwner) error {
	data, err := json.Marshal(owner)
	if err != nil {
		return fmt.Errorf("updater: encode lock owner: %w", err)
	}
	if err := file.Truncate(0); err != nil {
		return fmt.Errorf("updater: write lock owner: %w", err)
	}
	if _, err := file.WriteAt(data, 0); err != nil {
		return fmt.Errorf("updater: write lock owner: %w", err)
	}
	return nil
}
