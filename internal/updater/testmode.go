package updater

import (
	"os"
	"strings"
	"time"

	"github.com/Vadym903/Intenter/internal/platform"
)

// Test-mode overrides (003 contracts/update-cli.md §"Test overrides"). Both are
// ignored entirely unless INTENTER_TEST_MODE=1, so a variable left in a shell
// profile — or set by something else on the machine — cannot make a real
// installation believe it has a terminal or that the date is different.
const (
	// EnvTestTTY makes the prompt treat stdio as interactive. Go tests have no
	// pseudo-terminal, so without this the start-up prompt could only be
	// exercised through a shell wrapper.
	EnvTestTTY = "INTENTER_TEST_TTY"
	// EnvTestNow overrides the clock, so deferral, back-off and reminder
	// intervals can be tested without sleeping through them.
	EnvTestNow = "INTENTER_TEST_NOW"
)

// TestTTY reports whether the harness is claiming an interactive terminal.
func TestTTY() bool {
	return platform.TestMode() && strings.TrimSpace(os.Getenv(EnvTestTTY)) == "1"
}

// Now is the clock the update feature reads. Outside test mode it is the wall
// clock, and an unparsable override is the wall clock too: a broken variable
// must not move a machine's idea of the date.
func Now() time.Time {
	if !platform.TestMode() {
		return time.Now()
	}
	raw := strings.TrimSpace(os.Getenv(EnvTestNow))
	if raw == "" {
		return time.Now()
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Now()
	}
	return parsed
}
