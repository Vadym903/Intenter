package updater

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Vadym903/Intenter/internal/config"
)

// Choice is what the user answered at the start-up prompt.
type Choice string

const (
	// ChoiceUpdate: run the update now, in this terminal.
	ChoiceUpdate Choice = "update"
	// ChoiceNotNow: ask again after the reminder interval.
	ChoiceNotNow Choice = "not_now"
	// ChoiceSkip: never offer this particular version again.
	ChoiceSkip Choice = "skip"
	// ChoiceTimeout: nobody answered. It counts as "not now", and is recorded
	// separately so support can tell "declined" from "was not there".
	ChoiceTimeout Choice = "timeout"
)

// Deferred reports whether a choice means "ask me later".
func (c Choice) Deferred() bool { return c == ChoiceNotNow || c == ChoiceTimeout }

// event maps a choice onto its history event name.
func (c Choice) event() string {
	switch c {
	case ChoiceUpdate:
		return EventChoiceUpdate
	case ChoiceSkip:
		return EventChoiceSkip
	case ChoiceTimeout:
		return EventChoiceTimeout
	default:
		return EventChoiceNotNow
	}
}

// Silence is why the start-up path must print nothing. An empty reason means it
// may proceed.
//
// Every one of these is a place where a prompt would be actively harmful rather
// than merely unwanted: a script would see it as output, a Claude hook would see
// it as a delay, and a build machine would see it as a hang.
type Silence string

const (
	// SilenceDisabled: the user switched checking off.
	SilenceDisabled Silence = "update checks are disabled"
	// SilenceCI: a build machine has nobody to answer.
	SilenceCI Silence = "running in CI"
	// SilenceNotATerminal: piped, redirected, or started by a task runner.
	SilenceNotATerminal Silence = "not an interactive terminal"
	// SilenceMachineOutput: the caller asked for JSON, which a prompt would
	// corrupt.
	SilenceMachineOutput Silence = "machine-readable output was requested"
)

// PromptGate decides whether a prompt may be shown at all.
type PromptGate struct {
	Updates config.UpdatesConfig
	// In and Out are the terminal's streams. Both must be terminals: a command
	// whose output is piped is being read by a program, not a person.
	In  *os.File
	Out *os.File
	// JSON is set when the caller asked for machine-readable output.
	JSON bool
}

// Silenced returns the reason no prompt may be shown, or an empty reason.
func (g PromptGate) Silenced() Silence {
	switch {
	case !g.Updates.Check:
		return SilenceDisabled
	case g.JSON:
		return SilenceMachineOutput
	case inContinuousIntegration():
		return SilenceCI
	case !Interactive(g.In, g.Out):
		return SilenceNotATerminal
	}
	return ""
}

// inContinuousIntegration reports whether this looks like a build machine.
// Every CI system in common use sets CI to something; the value is not
// standardized, so its presence is what counts.
func inContinuousIntegration() bool {
	return strings.TrimSpace(os.Getenv("CI")) != ""
}

// Interactive reports whether both streams are terminals.
//
// Character-device detection is used rather than a terminal library because it
// needs no dependency and answers the question that matters: a pipe, a file and
// a socket are all "nobody is going to read this and type back".
func Interactive(in, out *os.File) bool {
	if TestTTY() {
		return true
	}
	return isTerminal(in) && isTerminal(out)
}

func isTerminal(file *os.File) bool {
	if file == nil {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// Prompter shows the three-way start-up prompt and records the answer.
type Prompter struct {
	Store   *Store
	Updates config.UpdatesConfig
	// Installed is the running version, named in the prompt.
	Installed string
	// InstallChannel is recorded with the decision.
	InstallChannel string
	In             io.Reader
	Out            io.Writer
	// Now is injectable so deferral times can be asserted.
	Now func() time.Time
	// Timeout overrides the configured prompt timeout; zero uses the config.
	Timeout time.Duration
}

func (p *Prompter) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return Now()
}

func (p *Prompter) timeout() time.Duration {
	if p.Timeout > 0 {
		return p.Timeout
	}
	return p.Updates.PromptWait()
}

// Ask shows the prompt for a known release and records what the user answered.
//
// The answer is written before it is returned, so a caller that goes on to
// update — and whose update fails, or whose terminal is closed part-way — has
// still recorded the decision. Being asked twice because the update failed is
// exactly the nagging this feature is supposed to avoid.
func (p *Prompter) Ask(latest LatestKnown) (Choice, error) {
	p.render(latest)
	p.record(EventPromptShown, "", latest.Version)

	choice := p.read()
	if err := p.persist(choice, latest); err != nil {
		return choice, err
	}
	p.record(choice.event(), "", latest.Version)
	return choice, nil
}

// render writes the prompt (003 research R-05).
func (p *Prompter) render(latest LatestKnown) {
	fmt.Fprintf(p.Out, "\nIntenter %s is available (you have %s).\n", latest.Version, p.Installed)
	if latest.NotesURL != "" {
		fmt.Fprintf(p.Out, "Release notes: %s\n", latest.NotesURL)
	}
	fmt.Fprintf(p.Out, "Update now? [y]es / [N]ot now / [s]kip this version  (auto \"not now\" in %s): ",
		humanDuration(p.timeout()))
}

// read waits for a line, for as long as the prompt timeout allows.
//
// Enter, an unexpected key, end of input and silence all mean "not now": the
// prompt interrupts whatever the user opened the terminal to do, so the safe
// default is to get out of the way. Only a deliberate "y" installs anything.
func (p *Prompter) read() Choice {
	answers := make(chan string, 1)
	go func() {
		reader := bufio.NewReader(p.In)
		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			answers <- ""
			return
		}
		answers <- line
	}()

	timer := time.NewTimer(p.timeout())
	defer timer.Stop()

	select {
	case <-timer.C:
		fmt.Fprintln(p.Out)
		return ChoiceTimeout
	case line := <-answers:
		fmt.Fprintln(p.Out)
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes":
			return ChoiceUpdate
		case "s", "skip":
			return ChoiceSkip
		default:
			return ChoiceNotNow
		}
	}
}

// persist writes the consequence of a choice into the shared state.
func (p *Prompter) persist(choice Choice, latest LatestKnown) error {
	now := p.now()
	_, err := p.Store.Mutate(func(s *UpdateState) {
		s.LastPromptAt = timePtr(now)
		switch {
		case choice == ChoiceSkip:
			s.SkippedVersion = latest.Version
			s.DeferredUntil = nil
		case choice.Deferred():
			s.DeferredUntil = timePtr(now.Add(p.Updates.RemindEvery()))
		}
	})
	return err
}

func (p *Prompter) record(event, detail, target string) {
	_ = p.Store.Append(UpdateDecision{
		At:               p.now(),
		Event:            event,
		InstalledVersion: p.Installed,
		TargetVersion:    target,
		Channel:          p.InstallChannel,
		Detail:           detail,
	})
}

// humanDuration renders a timeout the way the prompt should read: "30s", "2m".
func humanDuration(d time.Duration) string {
	switch {
	case d >= time.Minute && d%time.Minute == 0:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d >= time.Second:
		return fmt.Sprintf("%ds", int(d/time.Second))
	default:
		return d.String()
	}
}
