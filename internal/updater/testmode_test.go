package updater

import (
	"testing"
	"time"

	"github.com/Vadym903/Intenter/internal/platform"
)

func TestTheTestOverridesAreInertOutsideTestMode(t *testing.T) {
	// These variables let the e2e tests drive a prompt that would otherwise
	// need a pseudo-terminal, and move the clock past a 24-hour deferral. Left
	// live in a real installation they would let anything on the machine make
	// Intenter believe it has a terminal, or that the date is whatever suits
	// it — so the gate matters more than the feature.
	t.Setenv(EnvTestTTY, "1")
	t.Setenv(EnvTestNow, "2001-01-01T00:00:00Z")
	t.Setenv(platform.EnvTestMode, "")

	if TestTTY() {
		t.Error("INTENTER_TEST_TTY must do nothing without INTENTER_TEST_MODE=1")
	}
	if year := Now().Year(); year == 2001 {
		t.Error("INTENTER_TEST_NOW must do nothing without INTENTER_TEST_MODE=1")
	}
}

func TestTheTestOverridesApplyInTestMode(t *testing.T) {
	t.Setenv(platform.EnvTestMode, "1")
	t.Setenv(EnvTestTTY, "1")
	t.Setenv(EnvTestNow, "2026-08-16T12:00:00Z")

	if !TestTTY() {
		t.Error("the tty override must apply in test mode")
	}
	want := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	if got := Now(); !got.Equal(want) {
		t.Errorf("Now() = %s, want %s", got, want)
	}
}

func TestAnUnreadableClockOverrideFallsBackToTheWallClock(t *testing.T) {
	t.Setenv(platform.EnvTestMode, "1")
	t.Setenv(EnvTestNow, "yesterday")

	if got := Now(); time.Since(got) > time.Minute {
		t.Errorf("Now() = %s, want roughly now", got)
	}
}

func TestOnlyTheExactTTYValueCounts(t *testing.T) {
	t.Setenv(platform.EnvTestMode, "1")
	for _, value := range []string{"", "0", "true", "yes"} {
		t.Setenv(EnvTestTTY, value)
		if TestTTY() {
			t.Errorf("INTENTER_TEST_TTY=%q must not claim a terminal", value)
		}
	}
}
