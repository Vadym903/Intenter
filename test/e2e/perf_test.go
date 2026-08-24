package e2e

import (
	"sort"
	"testing"
	"time"
)

// This is the number a user actually feels: the whole hook, from Claude Code
// handing over a command to getting an answer back. It includes starting a
// process, reading the payload, connecting to the daemon, resolving, deciding,
// writing the audit row and printing the response.
//
// Process start dominates, which is why the budget is five times the daemon's
// own. Claude waits for this before running anything, on every single command,
// so a regression here is felt on every keystroke of a session.
//
// (The task named harness_test.go; this lives in its own file because that one
// is the harness rather than a place for tests.)

const (
	// hookBudget is the p95 the whole round trip must stay under (§29).
	hookBudget = 500 * time.Millisecond
	// hookSamples is enough for a stable p95 without a slow suite; each one
	// starts a process, so this is the expensive test in the suite.
	hookSamples = 30
	hookWarmup  = 3
)

func TestHookRoundTripLatency(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}

	env := NewEnv(t)
	env.HideRealClaude()
	env.SetScripts(`"cleanup": "rm -rf ./dist"`)

	// The three shapes a session actually produces, in rough order of cost.
	commands := map[string]string{
		"an allowed read":   "git status",
		"a resolved script": "npm run cleanup",
		"a blocked delete":  "rm -rf ~/Documents",
	}

	for name, command := range commands {
		t.Run(name, func(t *testing.T) {
			for i := 0; i < hookWarmup; i++ {
				env.PreToolUse("perf-warm", "toolu_warm_"+itoa(int64(i)), command)
			}

			samples := make([]time.Duration, 0, hookSamples)
			for i := 0; i < hookSamples; i++ {
				// A new tool use id each time, so nothing is answered from the
				// cache and every sample is the full path.
				started := time.Now()
				env.PreToolUse("perf", "toolu_perf_"+itoa(int64(i)), command)
				samples = append(samples, time.Since(started))
			}

			p50, p95, worst := hookPercentiles(samples)
			t.Logf("%-18s p50 %6s  p95 %6s  max %6s", name,
				p50.Round(time.Millisecond), p95.Round(time.Millisecond), worst.Round(time.Millisecond))

			if p95 > hookBudget {
				t.Errorf("p95 = %s, over the %s budget (p50 %s, max %s)",
					p95.Round(time.Millisecond), hookBudget,
					p50.Round(time.Millisecond), worst.Round(time.Millisecond))
			}
		})
	}
}

func TestHookIsNoSlowerWhenTheDaemonIsDown(t *testing.T) {
	// The failure path is on the same hot path: when the daemon is unreachable
	// the hook must give up quickly and defer, because a session that stalls
	// for seconds per command is worse than one with no gate at all.
	if testing.Short() {
		t.Skip("timing test")
	}

	env := NewEnv(t)
	env.HideRealClaude()
	env.DisableLazyStart()
	env.StopDaemon()
	env.ForgetDaemon()

	samples := make([]time.Duration, 0, hookSamples)
	for i := 0; i < hookSamples; i++ {
		started := time.Now()
		env.PreToolUse("perf-down", "toolu_down_"+itoa(int64(i)), "git status")
		samples = append(samples, time.Since(started))
	}

	p50, p95, worst := hookPercentiles(samples)
	t.Logf("daemon down: p50 %s  p95 %s  max %s",
		p50.Round(time.Millisecond), p95.Round(time.Millisecond), worst.Round(time.Millisecond))

	if p95 > hookBudget {
		t.Errorf("p95 = %s with the daemon down, over the %s budget — a session would stall "+
			"on every command", p95.Round(time.Millisecond), hookBudget)
	}
}

// hookPercentiles returns the 50th, 95th and maximum of a set of samples.
func hookPercentiles(samples []time.Duration) (p50, p95, max time.Duration) {
	if len(samples) == 0 {
		return 0, 0, 0
	}
	sorted := make([]time.Duration, len(samples))
	copy(sorted, samples)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	index95 := len(sorted) * 95 / 100
	if index95 >= len(sorted) {
		index95 = len(sorted) - 1
	}
	return sorted[len(sorted)*50/100], sorted[index95], sorted[len(sorted)-1]
}
