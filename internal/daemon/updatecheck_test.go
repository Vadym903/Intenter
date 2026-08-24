package daemon

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Vadym903/Intenter/internal/config"
	"github.com/Vadym903/Intenter/internal/logging"
)

// countingChecker builds a checker whose work is counted rather than performed.
func countingChecker(t *testing.T, updates config.UpdatesConfig, due func(time.Time) bool) (*updateChecker, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	return &updateChecker{
		updates: updates,
		logger:  logging.Discard(),
		check: func(context.Context) error {
			calls.Add(1)
			return nil
		},
		due:   due,
		now:   time.Now,
		every: 5 * time.Millisecond,
		delay: time.Millisecond,
	}, calls
}

func always(time.Time) bool { return true }
func never(time.Time) bool  { return false }

func TestTheDaemonChecksWhenAChecKIsDue(t *testing.T) {
	checker, calls := countingChecker(t, config.Default().Updates, always)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go checker.run(ctx)

	waitForCount(t, calls, 2)
}

func TestNothingIsCheckedWhenNothingIsDue(t *testing.T) {
	checker, calls := countingChecker(t, config.Default().Updates, never)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go checker.run(ctx)

	time.Sleep(50 * time.Millisecond)
	if got := calls.Load(); got != 0 {
		t.Errorf("%d checks ran while none was due", got)
	}
}

func TestCheckingOffMeansNoRequestsAtAll(t *testing.T) {
	// SC-008: with checking disabled the update feature must produce zero
	// network requests. The daemon is the one component that would otherwise
	// keep making them without anybody asking.
	updates := config.Default().Updates
	updates.Check = false
	checker, calls := countingChecker(t, updates, always)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		checker.run(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("run must return immediately when checking is switched off")
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("%d checks ran with checking switched off", got)
	}
}

func TestTheFirstCheckWaitsForTheDelay(t *testing.T) {
	// Logging in is the worst moment to start a network request on somebody's
	// machine, and the least likely one to succeed.
	checker, calls := countingChecker(t, config.Default().Updates, always)
	checker.delay = 80 * time.Millisecond
	checker.every = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go checker.run(ctx)

	time.Sleep(30 * time.Millisecond)
	if got := calls.Load(); got != 0 {
		t.Errorf("%d checks ran before the start-up delay elapsed", got)
	}
	waitForCount(t, calls, 1)
}

func TestCancellingStopsTheChecker(t *testing.T) {
	checker, calls := countingChecker(t, config.Default().Updates, always)
	checker.delay = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		checker.run(ctx)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the checker must stop when the daemon does")
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("%d checks ran after cancellation", got)
	}
}

func TestAFailedCheckDoesNotStopTheSchedule(t *testing.T) {
	// Networks come and go; one failure must not silence checking until the
	// next daemon restart.
	calls := &atomic.Int32{}
	checker := &updateChecker{
		updates: config.Default().Updates,
		logger:  logging.Discard(),
		check: func(context.Context) error {
			calls.Add(1)
			return errors.New("no route to host")
		},
		due:   always,
		now:   time.Now,
		every: 5 * time.Millisecond,
		delay: time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go checker.run(ctx)

	waitForCount(t, calls, 3)
}

func TestANilCheckerIsHarmless(t *testing.T) {
	var checker *updateChecker
	checker.run(context.Background())
}

func waitForCount(t *testing.T, calls *atomic.Int32, want int32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if calls.Load() >= want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("checks ran %d times, want at least %d", calls.Load(), want)
}
