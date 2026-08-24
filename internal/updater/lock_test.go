package updater

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func lockPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "prompt.lock")
}

func TestOnlyOneHolderAtATime(t *testing.T) {
	// This is the whole reason the lock exists: twenty terminals opening at
	// once must produce one prompt, not twenty.
	path := lockPath(t)

	first, err := AcquirePromptLock(path)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	if _, err := AcquirePromptLock(path); !errors.Is(err, ErrLockHeld) {
		t.Fatalf("second acquire = %v, want ErrLockHeld", err)
	}

	first.Release()

	second, err := AcquirePromptLock(path)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	second.Release()
}

func TestTheHolderIsRecordedForOtherTerminals(t *testing.T) {
	path := lockPath(t)

	if _, ok := PromptLockHolder(path); ok {
		t.Error("an unheld lock must report no holder")
	}

	lock, err := AcquirePromptLock(path)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer lock.Release()

	owner, ok := PromptLockHolder(path)
	if !ok {
		t.Fatal("a held lock must report its holder")
	}
	if owner.PID != os.Getpid() {
		t.Errorf("holder pid = %d, want %d", owner.PID, os.Getpid())
	}
	if time.Since(owner.StartedAt) > time.Minute {
		t.Errorf("holder started_at = %s, want roughly now", owner.StartedAt)
	}
}

func TestAStaleRecordIsNotReportedAsAHolder(t *testing.T) {
	// The lock is gone with the process that held it; a leftover record must
	// not make a message claim an update is running when none is.
	path := lockPath(t)
	stale := LockOwner{PID: 4242, StartedAt: time.Now().Add(-2 * promptLockStale)}
	writeOwnerFile(t, path, stale)

	if _, ok := PromptLockHolder(path); ok {
		t.Error("a record older than the staleness window must not be reported")
	}
}

func TestTheStateLockSerializesWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.lock")

	release, err := acquireLock(path)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	// A second writer waits rather than failing, but not forever.
	done := make(chan error, 1)
	go func() {
		second, err := acquireLock(path)
		if err == nil {
			second()
		}
		done <- err
	}()

	select {
	case err := <-done:
		t.Fatalf("the second writer returned %v while the first still held the lock", err)
	case <-time.After(50 * time.Millisecond):
	}

	release()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("the second writer failed after the lock was free: %v", err)
		}
	case <-time.After(lockWait):
		t.Error("the second writer did not take the freed lock")
	}
}

func TestAWriterGivesUpRatherThanHangs(t *testing.T) {
	// A wedged holder must cost one bounded wait, not a hung terminal.
	path := filepath.Join(t.TempDir(), "state.lock")

	release, err := acquireLock(path)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer release()

	start := time.Now()
	if _, err := lockWithDeadline(path, time.Now().Add(50*time.Millisecond)); !errors.Is(err, ErrLockHeld) {
		t.Fatalf("err = %v, want ErrLockHeld", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("waited %s for a 50ms deadline", elapsed)
	}
}

func TestReleasingTwiceIsHarmless(t *testing.T) {
	lock, err := AcquirePromptLock(lockPath(t))
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	lock.Release()
	lock.Release()

	var absent *Lock
	absent.Release()
}

func writeOwnerFile(t *testing.T, path string, owner LockOwner) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer file.Close()
	if err := writeOwnerAs(file, owner); err != nil {
		t.Fatalf("write owner: %v", err)
	}
}
