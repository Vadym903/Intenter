package updater

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Vadym903/Intenter/internal/config"
)

// at parses a fixed instant, so eligibility can be reasoned about rather than
// raced against the wall clock.
func at(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return parsed
}

func newStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(t.TempDir())
}

func updatesConfig() config.UpdatesConfig { return config.Default().Updates }

func knownLatest(version string, found time.Time) *LatestKnown {
	return &LatestKnown{Version: version, NotesURL: "https://example.test/" + version, FoundAt: found}
}

func TestAMissingStateFileReadsAsTheZeroState(t *testing.T) {
	// Every fresh install starts here, and so does every machine whose data
	// directory was wiped. It must not be an error condition.
	store := newStore(t)

	state, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if state.Schema != StateSchema {
		t.Errorf("schema = %d, want %d", state.Schema, StateSchema)
	}
	if state.LatestKnown != nil || state.SkippedVersion != "" {
		t.Errorf("a missing file must yield nothing known: %+v", state)
	}
	if !state.CheckDue(time.Now(), updatesConfig()) {
		t.Error("a machine that has never checked is due for a check")
	}
}

func TestStateSurvivesARoundTrip(t *testing.T) {
	store := newStore(t)
	now := at(t, "2026-08-16T10:00:00Z")

	written := UpdateState{
		InstalledVersion: "0.1.0",
		InstallChannel:   ChannelScript,
		Channel:          config.ChannelStable,
		LatestKnown:      knownLatest("0.2.0", now),
		SkippedVersion:   "0.1.5",
		DeferredUntil:    timePtr(now.Add(time.Hour)),
		LastPromptAt:     timePtr(now),
		StartupHook:      StartupHookState{InstalledFiles: []string{"/home/u/.zshrc"}, InstalledAt: timePtr(now)},
	}
	if err := store.Save(written); err != nil {
		t.Fatalf("Save: %v", err)
	}

	read, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if read.LatestKnown == nil || read.LatestKnown.Version != "0.2.0" {
		t.Errorf("latest_known = %+v", read.LatestKnown)
	}
	if read.SkippedVersion != "0.1.5" || read.InstallChannel != ChannelScript {
		t.Errorf("state not preserved: %+v", read)
	}
	if len(read.StartupHook.InstalledFiles) != 1 {
		t.Errorf("startup hook files = %v", read.StartupHook.InstalledFiles)
	}
	if read.DeferredUntil == nil || !read.DeferredUntil.Equal(now.Add(time.Hour)) {
		t.Errorf("deferred_until = %v", read.DeferredUntil)
	}
}

func TestSaveLeavesNoTemporaryFilesBehind(t *testing.T) {
	// The state directory is written on every check; a leaked temp file per
	// check would fill a home directory quietly over months.
	store := newStore(t)
	for i := 0; i < 3; i++ {
		if err := store.Save(UpdateState{InstalledVersion: "0.1.0"}); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	entries, err := os.ReadDir(store.Dir())
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(store.StatePath()) {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Errorf("directory contains %v, want only state.json", names)
	}
}

func TestACorruptStateFileDoesNotBreakTheTerminal(t *testing.T) {
	store := newStore(t)
	if err := os.MkdirAll(store.Dir(), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(store.StatePath(), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := store.Load(); err == nil {
		t.Error("Load must report a file it could not parse")
	}
	state := store.LoadOrZero()
	if state.Schema != StateSchema || state.LatestKnown != nil {
		t.Errorf("LoadOrZero = %+v, want the zero state", state)
	}
}

func TestMutateReadsModifiesAndWritesUnderTheLock(t *testing.T) {
	store := newStore(t)

	if _, err := store.Mutate(func(s *UpdateState) { s.SkippedVersion = "0.2.0" }); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	if _, err := store.Mutate(func(s *UpdateState) { s.InstalledVersion = "0.1.0" }); err != nil {
		t.Fatalf("Mutate: %v", err)
	}

	state, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if state.SkippedVersion != "0.2.0" || state.InstalledVersion != "0.1.0" {
		t.Errorf("the second write dropped the first: %+v", state)
	}
}

func TestEligibilityRules(t *testing.T) {
	now := at(t, "2026-08-16T12:00:00Z")
	cfg := updatesConfig()

	base := func() UpdateState {
		return UpdateState{LatestKnown: knownLatest("0.2.0", now)}
	}

	tests := map[string]struct {
		state     func() UpdateState
		installed string
		cfg       config.UpdatesConfig
		want      bool
	}{
		"a newer release with nothing in the way": {base, "0.1.0", cfg, true},
		"nothing known yet": {
			func() UpdateState { return UpdateState{} }, "0.1.0", cfg, false,
		},
		"already running it":      {base, "0.2.0", cfg, false},
		"running something newer": {base, "0.3.0", cfg, false},
		"the user skipped exactly this version": {
			func() UpdateState { s := base(); s.SkippedVersion = "0.2.0"; return s }, "0.1.0", cfg, false,
		},
		"the skipped version is an older one": {
			func() UpdateState { s := base(); s.SkippedVersion = "0.1.5"; return s }, "0.1.0", cfg, true,
		},
		"deferred until later": {
			func() UpdateState { s := base(); s.DeferredUntil = timePtr(now.Add(time.Hour)); return s }, "0.1.0", cfg, false,
		},
		"the deferral has passed": {
			func() UpdateState { s := base(); s.DeferredUntil = timePtr(now.Add(-time.Hour)); return s }, "0.1.0", cfg, true,
		},
		"prompted within the reminder interval": {
			func() UpdateState { s := base(); s.LastPromptAt = timePtr(now.Add(-time.Hour)); return s }, "0.1.0", cfg, false,
		},
		"prompted longer ago than the interval": {
			func() UpdateState { s := base(); s.LastPromptAt = timePtr(now.Add(-25 * time.Hour)); return s }, "0.1.0", cfg, true,
		},
		"another terminal is updating": {
			func() UpdateState {
				s := base()
				s.UpdateInProgress = &InProgress{PID: 1, StartedAt: now.Add(-time.Minute)}
				return s
			}, "0.1.0", cfg, false,
		},
		"a long-abandoned update marker": {
			func() UpdateState {
				s := base()
				s.UpdateInProgress = &InProgress{PID: 1, StartedAt: now.Add(-time.Hour)}
				return s
			}, "0.1.0", cfg, true,
		},
		"checking is switched off": {
			base, "0.1.0", config.UpdatesConfig{Check: false, Channel: config.ChannelStable}, false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if got := test.state().Eligible(now, test.installed, test.cfg); got != test.want {
				t.Errorf("Eligible = %v, want %v", got, test.want)
			}
		})
	}
}

func TestStableInstallationsAreNeverOfferedAPrerelease(t *testing.T) {
	now := at(t, "2026-08-16T12:00:00Z")
	state := UpdateState{LatestKnown: knownLatest("0.3.0-rc.1", now)}

	stable := updatesConfig()
	if state.Eligible(now, "0.2.0", stable) {
		t.Error("a stable installation must not be offered a pre-release")
	}

	optedIn := stable
	optedIn.Channel = config.ChannelPrerelease
	if !state.Eligible(now, "0.2.0", optedIn) {
		t.Error("a user who opted in must be offered it")
	}

	// Someone already on a pre-release is following them by definition.
	if !state.Eligible(now, "0.3.0-rc.0", stable) {
		t.Error("a pre-release installation must be offered the next pre-release")
	}
}

func TestCheckDue(t *testing.T) {
	now := at(t, "2026-08-16T12:00:00Z")
	cfg := updatesConfig()

	if (UpdateState{}).CheckDue(now, cfg) != true {
		t.Error("a state that has never checked is due")
	}
	future := UpdateState{NextCheckAfter: timePtr(now.Add(time.Hour))}
	if future.CheckDue(now, cfg) {
		t.Error("a check scheduled for later is not due")
	}
	past := UpdateState{NextCheckAfter: timePtr(now.Add(-time.Second))}
	if !past.CheckDue(now, cfg) {
		t.Error("a check whose time has passed is due")
	}
	if past.CheckDue(now, config.UpdatesConfig{Check: false}) {
		t.Error("no check is ever due when checking is switched off")
	}
}

func TestASuccessfulCheckSchedulesTheNextOne(t *testing.T) {
	now := at(t, "2026-08-16T12:00:00Z")
	cfg := updatesConfig()

	state := UpdateState{CheckFailures: 3, LastCheckError: "timeout"}
	state.RecordCheckOK(now, *knownLatest("0.2.0", now), cfg)

	if !state.LastCheckOK || state.CheckFailures != 0 || state.LastCheckError != "" {
		t.Errorf("a success must clear the failure record: %+v", state)
	}
	if state.NextCheckAfter == nil || !state.NextCheckAfter.Equal(now.Add(24*time.Hour)) {
		t.Errorf("next_check_after = %v, want now + 24h", state.NextCheckAfter)
	}
	if state.LatestKnown == nil || state.LatestKnown.Version != "0.2.0" {
		t.Errorf("latest_known = %+v", state.LatestKnown)
	}
}

func TestASkippedVersionIsForgottenOnceSomethingNewerArrives(t *testing.T) {
	// "Skip this version" is about one release, not about updating forever.
	now := at(t, "2026-08-16T12:00:00Z")
	cfg := updatesConfig()

	state := UpdateState{SkippedVersion: "0.2.0"}
	state.RecordCheckOK(now, *knownLatest("0.2.0", now), cfg)
	if state.SkippedVersion != "0.2.0" {
		t.Error("finding the same version again must not clear the skip")
	}

	state.RecordCheckOK(now, *knownLatest("0.2.1", now), cfg)
	if state.SkippedVersion != "" {
		t.Errorf("skipped_version = %q, want it cleared by a newer release", state.SkippedVersion)
	}
}

func TestFailedChecksBackOff(t *testing.T) {
	// A machine that is offline or behind a captive portal must not retry on
	// every terminal it opens.
	now := at(t, "2026-08-16T12:00:00Z")
	cfg := updatesConfig()
	cfg.CheckInterval = "1m"

	state := UpdateState{}
	for _, want := range []time.Duration{time.Hour, 6 * time.Hour, 24 * time.Hour, 24 * time.Hour} {
		state.RecordCheckFailure(now, "dial tcp: no route to host", cfg)
		if state.NextCheckAfter == nil || !state.NextCheckAfter.Equal(now.Add(want)) {
			t.Fatalf("after %d failures next_check_after = %v, want now + %s",
				state.CheckFailures, state.NextCheckAfter, want)
		}
	}
	if state.LastCheckOK || state.LastCheckError == "" {
		t.Errorf("a failure must be recorded with its reason: %+v", state)
	}

	// A configured interval longer than the back-off wins: the user asked to be
	// bothered less often, not more.
	patient := updatesConfig()
	patient.CheckInterval = "72h"
	state.RecordCheckFailure(now, "timeout", patient)
	if !state.NextCheckAfter.Equal(now.Add(72 * time.Hour)) {
		t.Errorf("next_check_after = %v, want the configured 72h", state.NextCheckAfter)
	}
}
