package updater

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Vadym903/Intenter/internal/config"
	"github.com/Vadym903/Intenter/internal/platform"
)

// promptFor builds a prompter reading a fixed answer.
func promptFor(t *testing.T, store *Store, answer string) (*Prompter, *bytes.Buffer) {
	t.Helper()
	out := &bytes.Buffer{}
	return &Prompter{
		Store:          store,
		Updates:        updatesConfig(),
		Installed:      "0.1.0",
		InstallChannel: ChannelScript,
		In:             strings.NewReader(answer),
		Out:            out,
		Now:            func() time.Time { return at(t, "2026-08-16T12:00:00Z") },
		Timeout:        2 * time.Second,
	}, out
}

func availableRelease() LatestKnown {
	return LatestKnown{
		Version:  "0.2.0",
		NotesURL: "https://github.com/Vadym903/Intenter/releases/tag/v0.2.0",
		FoundAt:  time.Now(),
	}
}

func TestThePromptSaysWhatIsAvailableAndWhatTheChoicesAre(t *testing.T) {
	prompter, out := promptFor(t, newStore(t), "\n")
	if _, err := prompter.Ask(availableRelease()); err != nil {
		t.Fatalf("Ask: %v", err)
	}

	text := out.String()
	for _, want := range []string{
		"Intenter 0.2.0 is available (you have 0.1.0).",
		"Release notes: https://github.com/Vadym903/Intenter/releases/tag/v0.2.0",
		"[y]es", "[N]ot now", "[s]kip this version",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the prompt is missing %q:\n%s", want, text)
		}
	}
}

func TestEveryAnswerMapsToAChoice(t *testing.T) {
	tests := map[string]Choice{
		"y\n":        ChoiceUpdate,
		"Y\n":        ChoiceUpdate,
		"yes\n":      ChoiceUpdate,
		" yes \n":    ChoiceUpdate,
		"s\n":        ChoiceSkip,
		"SKIP\n":     ChoiceSkip,
		"\n":         ChoiceNotNow,
		"n\n":        ChoiceNotNow,
		"whatever\n": ChoiceNotNow,
		// End of input: a terminal closed, or something piped in that ran out.
		"": ChoiceNotNow,
	}

	for answer, want := range tests {
		t.Run(strings.TrimSpace(answer)+"→"+string(want), func(t *testing.T) {
			prompter, _ := promptFor(t, newStore(t), answer)
			got, err := prompter.Ask(availableRelease())
			if err != nil {
				t.Fatalf("Ask: %v", err)
			}
			if got != want {
				t.Errorf("answer %q = %v, want %v", answer, got, want)
			}
		})
	}
}

func TestAnythingButYesInstallsNothing(t *testing.T) {
	// The prompt interrupts whatever the user opened the terminal to do, so the
	// only answer that installs software is a deliberate one.
	for _, answer := range []string{"\n", "n\n", "no\n", "Y E S\n", "", "s\n"} {
		prompter, _ := promptFor(t, newStore(t), answer)
		choice, err := prompter.Ask(availableRelease())
		if err != nil {
			t.Fatalf("Ask: %v", err)
		}
		if choice == ChoiceUpdate {
			t.Errorf("answer %q was taken as consent to install", answer)
		}
	}
}

func TestNotNowIsQuietForTheReminderInterval(t *testing.T) {
	store := newStore(t)
	prompter, _ := promptFor(t, store, "\n")
	now := at(t, "2026-08-16T12:00:00Z")

	if _, err := prompter.Ask(availableRelease()); err != nil {
		t.Fatalf("Ask: %v", err)
	}

	state := store.LoadOrZero()
	if state.DeferredUntil == nil || !state.DeferredUntil.Equal(now.Add(24*time.Hour)) {
		t.Fatalf("deferred_until = %v, want now + 24h", state.DeferredUntil)
	}
	if state.LastPromptAt == nil {
		t.Error("the prompt must record that it was shown")
	}
	if state.SkippedVersion != "" {
		t.Error("\"not now\" must not skip the version")
	}

	// And the consequence: no prompt in the next terminal. The release itself
	// is put back because only a check writes it, and none ran here.
	state.LatestKnown = knownLatest("0.2.0", now)
	if state.Eligible(now.Add(time.Hour), "0.1.0", updatesConfig()) {
		t.Error("a deferred version must not be offered again within the interval")
	}
	if !state.Eligible(now.Add(25*time.Hour), "0.1.0", updatesConfig()) {
		t.Error("it must be offered again once the interval has passed")
	}
}

func TestSkipIsForeverForThatVersionOnly(t *testing.T) {
	store := newStore(t)
	prompter, _ := promptFor(t, store, "s\n")
	now := at(t, "2026-08-16T12:00:00Z")

	if _, err := prompter.Ask(availableRelease()); err != nil {
		t.Fatalf("Ask: %v", err)
	}

	state := store.LoadOrZero()
	if state.SkippedVersion != "0.2.0" {
		t.Fatalf("skipped_version = %q, want 0.2.0", state.SkippedVersion)
	}
	state.LatestKnown = knownLatest("0.2.0", now)
	if state.Eligible(now.Add(30*24*time.Hour), "0.1.0", updatesConfig()) {
		t.Error("a skipped version must never be offered again")
	}

	// The next release still is — after the reminder interval, because at most
	// one prompt per interval holds for every version (FR-008), not just the
	// one that was answered.
	state.LatestKnown = knownLatest("0.2.1", now)
	if state.Eligible(now.Add(time.Hour), "0.1.0", updatesConfig()) {
		t.Error("a newer release must still respect the one-prompt-per-interval rule")
	}
	if !state.Eligible(now.Add(25*time.Hour), "0.1.0", updatesConfig()) {
		t.Error("skipping one version must not suppress the next")
	}
}

func TestAnUnansweredPromptCountsAsNotNow(t *testing.T) {
	// A prompt in a terminal somebody walked away from must hand the shell
	// back rather than sit there.
	store := newStore(t)
	prompter, out := promptFor(t, store, "")
	prompter.In = neverReads{}
	prompter.Timeout = 50 * time.Millisecond

	start := time.Now()
	choice, err := prompter.Ask(availableRelease())
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if choice != ChoiceTimeout {
		t.Errorf("choice = %v, want a timeout", choice)
	}
	if elapsed > time.Second {
		t.Errorf("the prompt waited %s for a 50ms timeout", elapsed)
	}
	if state := store.LoadOrZero(); state.DeferredUntil == nil {
		t.Error("a timeout must defer like \"not now\"")
	}
	if !strings.Contains(out.String(), "auto \"not now\" in 50ms") {
		t.Errorf("the prompt must say how long it will wait:\n%s", out.String())
	}
}

func TestEveryChoiceIsRecorded(t *testing.T) {
	// FR-020: support has to be able to answer "why was I asked" and "what did
	// I answer".
	tests := map[string]struct {
		answer string
		event  string
	}{
		"update":  {"y\n", EventChoiceUpdate},
		"not now": {"\n", EventChoiceNotNow},
		"skip":    {"s\n", EventChoiceSkip},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			store := newStore(t)
			prompter, _ := promptFor(t, store, test.answer)
			if _, err := prompter.Ask(availableRelease()); err != nil {
				t.Fatalf("Ask: %v", err)
			}

			entries := store.Tail(10)
			if len(entries) != 2 {
				t.Fatalf("history = %+v, want the prompt and the answer", entries)
			}
			if entries[0].Event != EventPromptShown {
				t.Errorf("first entry = %q, want %q", entries[0].Event, EventPromptShown)
			}
			if entries[1].Event != test.event {
				t.Errorf("second entry = %q, want %q", entries[1].Event, test.event)
			}
			if entries[1].TargetVersion != "0.2.0" || entries[1].InstalledVersion != "0.1.0" {
				t.Errorf("the entry must name both versions: %+v", entries[1])
			}
		})
	}
}

func TestThePromptIsSilencedWhereItWouldDoHarm(t *testing.T) {
	// Each of these is a context where a prompt is not merely unwanted: it
	// would be read as output, as a hang, or as a broken hook.
	//
	// The gate reads CI from the environment, and a build machine sets it;
	// TestCISilencesThePrompt covers that reason, so here it is cleared to let
	// the other reasons be observed.
	t.Setenv("CI", "")
	enabled := updatesConfig()
	disabled := enabled
	disabled.Check = false

	tests := map[string]struct {
		gate PromptGate
		want Silence
	}{
		"checking switched off": {PromptGate{Updates: disabled}, SilenceDisabled},
		"machine-readable output": {
			PromptGate{Updates: enabled, JSON: true}, SilenceMachineOutput,
		},
		"no terminal": {PromptGate{Updates: enabled}, SilenceNotATerminal},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if got := test.gate.Silenced(); got != test.want {
				t.Errorf("Silenced = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCISilencesThePrompt(t *testing.T) {
	t.Setenv("CI", "true")
	t.Setenv(platform.EnvTestMode, "1")
	t.Setenv(EnvTestTTY, "1")

	gate := PromptGate{Updates: updatesConfig()}
	if got := gate.Silenced(); got != SilenceCI {
		t.Errorf("Silenced = %q, want %q — a build machine has nobody to answer", got, SilenceCI)
	}
}

func TestAPipeIsNotATerminal(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer reader.Close()
	defer writer.Close()

	if Interactive(reader, writer) {
		t.Error("a pipe must not be treated as a terminal")
	}
	if Interactive(nil, nil) {
		t.Error("absent streams must not be treated as a terminal")
	}
}

func TestHumanDuration(t *testing.T) {
	for input, want := range map[time.Duration]string{
		30 * time.Second: "30s",
		time.Minute:      "1m",
		2 * time.Minute:  "2m",
		90 * time.Second: "90s",
	} {
		if got := humanDuration(input); got != want {
			t.Errorf("humanDuration(%s) = %q, want %q", input, got, want)
		}
	}
}

// neverReads stands in for a terminal nobody is typing at.
type neverReads struct{}

func (neverReads) Read([]byte) (int, error) {
	select {}
}

var _ io.Reader = neverReads{}

var _ = config.ChannelStable
