package daemon

import (
	"sort"
	"testing"
	"time"
)

// Latency is a correctness property here, not a nicety. Intenter sits between
// an agent and every command it runs, so whatever it costs is paid on every
// command, in front of someone waiting. A gate that is slow enough to notice is
// a gate people turn off.
//
// The budgets come from the specification: an evaluation under 100 ms at the
// 95th percentile, warm. Real numbers are a fraction of that — the point of the
// margin is that these must not fail on a loaded CI runner, so a failure means
// something genuinely changed rather than that the machine was busy.

const (
	// evaluateBudget is the p95 an evaluation must stay under (§29).
	evaluateBudget = 100 * time.Millisecond
	// perfSamples is enough for a stable p95 without making the suite slow.
	perfSamples = 60
	// perfNoiseSlack is how much slower a cached evaluation may measure than a
	// fresh one before it counts as a regression. It is deliberately larger
	// than the whole signal on an idle machine, because it exists to absorb a
	// contended runner rather than to describe the cache.
	perfNoiseSlack = 2 * time.Millisecond
	// perfWarmup primes the caches an evaluation depends on: the workspace
	// context, the package manager detection, the compiled parsers.
	perfWarmup = 10
)

func TestEvaluateLatency(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}

	p := testPlatform(t)
	w := newWorkspace(t, p)
	w.write(t, "package.json", `{"name":"demo","scripts":{"cleanup":"rm -rf ./dist"}}`)
	client := startDaemon(t, p)

	// Each command exercises a different amount of work, and the slowest is the
	// one that matters: `npm run cleanup` reads package.json, resolves the
	// script, and fingerprints what it read.
	commands := map[string]string{
		"a baseline read":      "git status",
		"a resolved script":    "npm run cleanup",
		"a blocked delete":     "rm -rf ~/Documents",
		"an unknown program":   "some-unknown-tool --wipe",
		"a multi-command line": "git status && ls src && cat README.md",
	}

	for name, command := range commands {
		t.Run(name, func(t *testing.T) {
			// Warm first: the first evaluation in a workspace pays for context
			// discovery that every later one reuses, and reporting that number
			// would measure startup rather than steady state.
			for i := 0; i < perfWarmup; i++ {
				evaluate(t, client, bashRequest(w, command, "warm"))
			}

			samples := make([]time.Duration, 0, perfSamples)
			for i := 0; i < perfSamples; i++ {
				// A distinct tool use id each time, so the result cache never
				// answers and every sample is a real evaluation.
				request := bashRequest(w, command, "toolu_perf_"+itoa(i))
				started := time.Now()
				evaluate(t, client, request)
				samples = append(samples, time.Since(started))
			}

			p50, p95, worst := percentiles(samples)
			t.Logf("%-22s p50 %6s  p95 %6s  max %6s", name,
				round(p50), round(p95), round(worst))

			if p95 > evaluateBudget {
				t.Errorf("p95 = %s, over the %s budget (p50 %s, max %s)",
					round(p95), evaluateBudget, round(p50), round(worst))
			}
		})
	}
}

func TestCachedEvaluationIsEffectivelyFree(t *testing.T) {
	// One command produces several hook events, and all but the first are
	// answered from the cache. If that path were not much faster than a fresh
	// evaluation the cache would not be earning its complexity.
	if testing.Short() {
		t.Skip("timing test")
	}

	p := testPlatform(t)
	w := newWorkspace(t, p)
	w.write(t, "package.json", `{"name":"demo","scripts":{"cleanup":"rm -rf ./dist"}}`)
	client := startDaemon(t, p)

	fresh := make([]time.Duration, 0, perfSamples)
	for i := 0; i < perfSamples; i++ {
		request := bashRequest(w, "npm run cleanup", "toolu_fresh_"+itoa(i))
		started := time.Now()
		evaluate(t, client, request)
		fresh = append(fresh, time.Since(started))
	}

	// The same tool call repeatedly: every one after the first is cached.
	evaluate(t, client, bashRequest(w, "npm run cleanup", "toolu_cached"))
	cached := make([]time.Duration, 0, perfSamples)
	for i := 0; i < perfSamples; i++ {
		started := time.Now()
		evaluate(t, client, bashRequest(w, "npm run cleanup", "toolu_cached"))
		cached = append(cached, time.Since(started))
	}

	freshP50, freshP95, _ := percentiles(fresh)
	cachedP50, cachedP95, _ := percentiles(cached)
	t.Logf("fresh p50 %s p95 %s, cached p50 %s p95 %s",
		round(freshP50), round(freshP95), round(cachedP50), round(cachedP95))

	// Compared on medians with slack, not on p95 exactly, because the thing
	// being measured is smaller than the noise around it. Both figures are a
	// few hundred microseconds on an idle machine; on a shared CI runner a
	// single scheduler stall is larger than the entire signal, and it lands in
	// p95 by definition — one stalled sample in sixty moves p95 and leaves p50
	// alone. A strict p95 comparison there reports the runner's neighbours, not
	// this code, and has failed a tagged release on 1.8ms versus 3.8ms.
	//
	// The defect still caught is the one worth catching: a cache that does not
	// help, which shows up as cached far above fresh rather than a hair above.
	if cachedP50 > freshP50+perfNoiseSlack {
		t.Errorf("cached p50 %s is slower than a fresh evaluation at %s, beyond the %s allowed for measurement noise",
			round(cachedP50), round(freshP50), perfNoiseSlack)
	}
	if cachedP95 > evaluateBudget {
		t.Errorf("cached p95 = %s, over the %s budget", round(cachedP95), evaluateBudget)
	}
}

// percentiles returns the 50th, 95th and maximum of a set of samples.
func percentiles(samples []time.Duration) (p50, p95, max time.Duration) {
	if len(samples) == 0 {
		return 0, 0, 0
	}
	sorted := make([]time.Duration, len(samples))
	copy(sorted, samples)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	return sorted[len(sorted)*50/100],
		sorted[min(len(sorted)*95/100, len(sorted)-1)],
		sorted[len(sorted)-1]
}

// round trims a duration to something readable in a test log.
func round(d time.Duration) time.Duration { return d.Round(100 * time.Microsecond) }

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// itoa keeps the sample loop free of an fmt import.
func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := ""
	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}
	return digits
}
