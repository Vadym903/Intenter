package updater

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Markers delimiting the block Intenter manages in a start-up file.
//
// They are deliberately different from the installer's `# >>> intenter >>>`
// PATH block: the two are written by different commands at different times, and
// removing one must never disturb the other.
const (
	MarkerBegin = "# >>> intenter:update-check >>>"
	MarkerEnd   = "# <<< intenter:update-check <<<"
)

// Shell names the start-up check knows how to install itself into.
const (
	ShellZsh        = "zsh"
	ShellBash       = "bash"
	ShellFish       = "fish"
	ShellPowerShell = "powershell"
)

// AllShells is every shell in the order they are reported.
var AllShells = []string{ShellZsh, ShellBash, ShellFish, ShellPowerShell}

// fishBlockFile is a drop-in of its own rather than a block inside a shared
// file, because that is how fish is meant to be extended.
const fishBlockFile = "intenter-update.fish"

// StartupCheck writes and removes the managed block that runs the start-up
// check when a terminal opens.
type StartupCheck struct {
	// Home is the user's home directory.
	Home string
	// Executable is the stable path the block invokes. It is the path that
	// survives an upgrade, not the one this process happens to be running from.
	Executable string
	// Store records what was installed, and why the user was or was not asked.
	Store *Store
	// Now is injectable so recorded times can be asserted.
	Now func() time.Time
	// GOOS overrides the host for tests; empty means the running one.
	GOOS string
	// LookPath finds a program, for detecting which PowerShell hosts exist.
	LookPath func(string) (string, error)
	// Policy reports whether PowerShell will run profile scripts at all.
	Policy func() (blocked bool, fix string)
}

func (s *StartupCheck) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return Now()
}

func (s *StartupCheck) goos() string {
	if s.GOOS != "" {
		return s.GOOS
	}
	return runtime.GOOS
}

// StartupStatus is what `update startup status` reports.
type StartupStatus struct {
	// Installed lists the files that currently contain the block.
	Installed []string `json:"installed"`
	// Candidates lists the files the block would be written to.
	Candidates []string `json:"candidates"`
	// Shells lists the shells detected on this machine.
	Shells []string `json:"shells"`
	// BlockedByPolicy is true when PowerShell will not run profile scripts.
	BlockedByPolicy bool `json:"blocked_by_policy,omitempty"`
	// PolicyFix is the command that lifts the block.
	PolicyFix string `json:"policy_fix,omitempty"`
}

// Install writes the block for the named shells, or for every shell detected on
// this machine when none are named. It is idempotent: a file that already has
// the block is rewritten rather than given a second one.
func (s *StartupCheck) Install(shells []string) (StartupStatus, error) {
	targets := s.filesFor(shells)
	if len(targets) == 0 {
		return s.Status(shells), fmt.Errorf("updater: no shell start-up files to write")
	}

	// A machine set up under the old name gets its legacy block cleaned out
	// before the current one goes in, so it ends with exactly one block.
	if _, err := s.removeLegacyBlock(); err != nil {
		return s.Status(shells), err
	}

	written := make([]string, 0, len(targets))
	for _, target := range targets {
		if err := s.writeBlock(target); err != nil {
			return s.Status(shells), err
		}
		written = append(written, target.path)
	}

	status := s.Status(shells)
	s.recordHook(EventHookInstalled, written, status)
	return status, nil
}

// Remove takes the block out of every file that has it.
//
// A start-up file belongs to the user: everything outside the block — comments,
// spacing, the order of their own lines — comes back exactly as it was. The
// fish drop-in is deleted rather than left empty, because a file that exists
// only to hold our block is ours to remove.
func (s *StartupCheck) Remove() (StartupStatus, error) {
	removed := make([]string, 0, 4)
	for _, target := range s.allCandidates() {
		gone, err := s.removeBlock(target)
		if err != nil {
			return s.Status(nil), err
		}
		if gone {
			removed = append(removed, target.path)
		}
	}

	legacyRemoved, err := s.removeLegacyBlock()
	if err != nil {
		return s.Status(nil), err
	}
	removed = append(removed, legacyRemoved...)

	status := s.Status(nil)
	if len(removed) > 0 {
		s.recordHook(EventHookRemoved, removed, status)
	}
	return status, nil
}

// Status reports where the block is and where it would go.
func (s *StartupCheck) Status(shells []string) StartupStatus {
	status := StartupStatus{
		Installed:  []string{},
		Candidates: []string{},
		Shells:     s.detectShells(),
	}
	for _, target := range s.allCandidates() {
		if hasBlock(target.path) {
			status.Installed = append(status.Installed, target.path)
		}
	}
	for _, target := range s.filesFor(shells) {
		status.Candidates = append(status.Candidates, target.path)
	}

	if s.wantsPowerShell(shells) {
		if blocked, fix := s.policy(); blocked {
			status.BlockedByPolicy = true
			status.PolicyFix = fix
		}
	}
	return status
}

// recordHook stores what changed and appends a history entry, so `doctor` and
// support can answer "why does this machine not prompt".
func (s *StartupCheck) recordHook(event string, files []string, status StartupStatus) {
	if s.Store == nil {
		return
	}
	now := s.now()
	_, _ = s.Store.Mutate(func(state *UpdateState) {
		state.StartupHook.InstalledFiles = status.Installed
		state.StartupHook.BlockedByPolicy = status.BlockedByPolicy
		if len(status.Installed) > 0 {
			state.StartupHook.InstalledAt = timePtr(now)
		} else {
			state.StartupHook.InstalledAt = nil
		}
	})
	_ = s.Store.Append(UpdateDecision{At: now, Event: event, Detail: strings.Join(files, ", ")})

	if status.BlockedByPolicy {
		_ = s.Store.Append(UpdateDecision{At: now, Event: EventHookBlocked, Detail: status.PolicyFix})
	}
}

// target is one start-up file and the block dialect it takes.
type target struct {
	shell string
	path  string
	// standalone means the file exists only for our block, so removing the
	// block removes the file.
	standalone bool
}

// filesFor resolves the requested shells to files, defaulting to what is
// actually on the machine.
func (s *StartupCheck) filesFor(shells []string) []target {
	if len(shells) == 0 {
		shells = s.detectShells()
	}
	wanted := map[string]bool{}
	for _, shell := range shells {
		wanted[strings.ToLower(strings.TrimSpace(shell))] = true
	}

	out := make([]target, 0, 4)
	for _, candidate := range s.allCandidates() {
		if wanted[candidate.shell] {
			out = append(out, candidate)
		}
	}
	return out
}

// allCandidates is every file the block could live in, on any shell.
//
// Removal walks all of them regardless of what is installed today, because the
// shell a user had when they installed is not necessarily the one they have
// when they uninstall.
func (s *StartupCheck) allCandidates() []target {
	if s.goos() == "windows" {
		return s.powerShellTargets()
	}

	out := []target{
		{shell: ShellZsh, path: filepath.Join(s.Home, ".zshrc")},
		{shell: ShellBash, path: filepath.Join(s.Home, ".bashrc")},
	}
	// macOS Terminal starts login shells, which read .bash_profile and not
	// .bashrc unless the user wired them together. Both get the block; the
	// once-per-session guard stops it running twice.
	if s.goos() == "darwin" && !sourcesBashrc(filepath.Join(s.Home, ".bash_profile")) {
		out = append(out, target{shell: ShellBash, path: filepath.Join(s.Home, ".bash_profile")})
	}
	out = append(out, target{
		shell:      ShellFish,
		path:       filepath.Join(s.Home, ".config", "fish", "conf.d", fishBlockFile),
		standalone: true,
	})
	return out
}

// powerShellTargets are the profiles the two Windows hosts read. Both are
// written: a machine commonly has 5.1 and 7 side by side, and a user opening
// either expects the same behavior.
func (s *StartupCheck) powerShellTargets() []target {
	documents := filepath.Join(s.Home, "Documents")
	return []target{
		{shell: ShellPowerShell, path: filepath.Join(documents, "WindowsPowerShell", "profile.ps1")},
		{shell: ShellPowerShell, path: filepath.Join(documents, "PowerShell", "profile.ps1")},
	}
}

// sourcesBashrc reports whether a bash profile already pulls in .bashrc, in
// which case one block is enough.
func sourcesBashrc(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), ".bashrc")
}

// detectShells reports which shells this machine actually has, so a user who
// has never opened fish does not get a fish file.
func (s *StartupCheck) detectShells() []string {
	found := map[string]bool{}

	if s.goos() == "windows" {
		if s.hasPowerShell() {
			found[ShellPowerShell] = true
		}
	} else {
		switch filepath.Base(strings.TrimSpace(os.Getenv("SHELL"))) {
		case "zsh":
			found[ShellZsh] = true
		case "bash":
			found[ShellBash] = true
		case "fish":
			found[ShellFish] = true
		}
		for shell, evidence := range map[string]string{
			ShellZsh:  filepath.Join(s.Home, ".zshrc"),
			ShellBash: filepath.Join(s.Home, ".bashrc"),
			ShellFish: filepath.Join(s.Home, ".config", "fish"),
		} {
			if _, err := os.Stat(evidence); err == nil {
				found[shell] = true
			}
		}
		// A machine with no evidence at all still needs somewhere to prompt
		// from; the login shell's own file is the safest guess.
		if len(found) == 0 {
			found[ShellBash] = true
		}
	}

	out := make([]string, 0, len(found))
	for shell := range found {
		out = append(out, shell)
	}
	sort.Strings(out)
	return out
}

func (s *StartupCheck) hasPowerShell() bool {
	lookPath := s.LookPath
	if lookPath == nil {
		lookPath = func(string) (string, error) { return "", os.ErrNotExist }
	}
	for _, name := range []string{"pwsh", "powershell"} {
		if _, err := lookPath(name); err == nil {
			return true
		}
	}
	// The Windows host always has Windows PowerShell, whether or not it is
	// findable through the test's injected lookup.
	return s.goos() == "windows"
}

func (s *StartupCheck) wantsPowerShell(shells []string) bool {
	for _, t := range s.filesFor(shells) {
		if t.shell == ShellPowerShell {
			return true
		}
	}
	return false
}

// policy asks whether PowerShell will run profile scripts at all.
func (s *StartupCheck) policy() (bool, string) {
	if s.Policy == nil {
		return false, ""
	}
	return s.Policy()
}

// writeBlock installs or refreshes the block in one file.
func (s *StartupCheck) writeBlock(t target) error {
	if err := os.MkdirAll(filepath.Dir(t.path), 0o755); err != nil {
		return fmt.Errorf("updater: create %s: %w", filepath.Dir(t.path), err)
	}

	existing, err := os.ReadFile(t.path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("updater: read %s: %w", t.path, err)
	}

	before, after, had := splitBlock(string(existing))
	block := s.blockFor(t.shell)

	var rebuilt string
	switch {
	case had:
		// Refreshing in place: everything around the block is untouched, so the
		// file survives an executable that moved without being rewritten.
		rebuilt = before + block + after
	case len(existing) == 0:
		rebuilt = block
	default:
		// Exactly one newline, always — see splitBlock for why the count
		// matters more than how it looks.
		rebuilt = string(existing) + "\n" + block
	}

	if err := os.WriteFile(t.path, []byte(rebuilt), 0o644); err != nil {
		return fmt.Errorf("updater: write %s: %w", t.path, err)
	}
	return nil
}

// removeBlock takes the block out of one file, reporting whether it was there.
func (s *StartupCheck) removeBlock(t target) (bool, error) {
	return s.removeBlockMarked(t, MarkerBegin, MarkerEnd)
}

// removeBlockMarked is removeBlock generalized over the marker pair, so the
// same byte-exact technique strips a block delimited by any markers — in
// particular the legacy ones from before the rename (see legacy.go) —
// without duplicating the logic.
func (s *StartupCheck) removeBlockMarked(t target, begin, end string) (bool, error) {
	existing, err := os.ReadFile(t.path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("updater: read %s: %w", t.path, err)
	}

	before, after, had := splitBlockMarked(string(existing), begin, end)
	if !had {
		return false, nil
	}

	rest := trimOneNewline(before) + after
	// A file that held nothing but our block is one we created.
	if t.standalone && strings.TrimSpace(rest) == "" {
		if err := os.Remove(t.path); err != nil {
			return false, fmt.Errorf("updater: remove %s: %w", t.path, err)
		}
		return true, nil
	}
	if err := os.WriteFile(t.path, []byte(rest), 0o644); err != nil {
		return false, fmt.Errorf("updater: write %s: %w", t.path, err)
	}
	return true, nil
}

// splitBlock separates a file into the raw text before our block and the raw
// text after it. `before` still carries the newline that separates them.
//
// It works on raw text rather than lines so removal can restore the file
// byte-for-byte: a user's trailing whitespace, CRLF line endings and missing
// final newline all survive.
func splitBlock(content string) (before, after string, found bool) {
	return splitBlockMarked(content, MarkerBegin, MarkerEnd)
}

// splitBlockMarked is splitBlock generalized over the marker pair.
func splitBlockMarked(content, begin, end string) (before, after string, found bool) {
	start := strings.Index(content, begin)
	if start < 0 {
		return content, "", false
	}
	stop := strings.Index(content[start:], end)
	if stop < 0 {
		return content, "", false
	}
	stop += start + len(end)

	// The newline that terminates the closing marker's line belongs to the
	// block, not to whatever follows.
	if strings.HasPrefix(content[stop:], "\r\n") {
		stop += 2
	} else if strings.HasPrefix(content[stop:], "\n") {
		stop++
	}
	return content[:start], content[stop:], true
}

// trimOneNewline removes exactly the separator writeBlock inserted.
//
// Exactly one, always: a file that ended with a newline gets a blank line
// before the block, one that did not gets its last line terminated. Both are a
// single newline, so removal is the same operation either way — which is what
// makes install → uninstall byte-identical rather than approximately so.
func trimOneNewline(s string) string {
	switch {
	case strings.HasSuffix(s, "\r\n"):
		return s[:len(s)-2]
	case strings.HasSuffix(s, "\n"):
		return s[:len(s)-1]
	}
	return s
}

func hasBlock(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), MarkerBegin)
}

// blockFor renders the managed block for a shell.
//
// Every guard in it exists to make the block do nothing in the cases where it
// would do harm: a non-interactive shell, a pipe, a shell started twice per
// session, a machine where the user opted out, and an executable that an
// uninstall removed.
func (s *StartupCheck) blockFor(shell string) string {
	executable := s.Executable

	switch shell {
	case ShellFish:
		return MarkerBegin + "\n" +
			"# Added by Intenter. Remove with: intenter update startup disable\n" +
			"if status is-interactive; and isatty stdin; and isatty stdout\n" +
			"    if not set -q INTENTER_NO_UPDATE_CHECK; and not set -q INTENTER_STARTUP_CHECKED\n" +
			"        if test -x " + fishQuote(executable) + "\n" +
			"            set -gx INTENTER_STARTUP_CHECKED 1\n" +
			"            " + fishQuote(executable) + " update --startup\n" +
			"        end\n" +
			"    end\n" +
			"end\n" +
			MarkerEnd + "\n"

	case ShellPowerShell:
		return MarkerBegin + "\n" +
			"# Added by Intenter. Remove with: intenter update startup disable\n" +
			"if ([Environment]::UserInteractive -and -not $env:INTENTER_NO_UPDATE_CHECK -and " +
			"-not $env:INTENTER_STARTUP_CHECKED -and (Test-Path " + powerShellQuote(executable) + ")) {\n" +
			"    $env:INTENTER_STARTUP_CHECKED = '1'\n" +
			"    & " + powerShellQuote(executable) + " update --startup\n" +
			"}\n" +
			MarkerEnd + "\n"

	default:
		// POSIX: `$-` contains `i` in an interactive shell, and both streams
		// must be terminals — a shell whose output is captured is a script.
		return MarkerBegin + "\n" +
			"# Added by Intenter. Remove with: intenter update startup disable\n" +
			"case $- in *i*)\n" +
			"    if [ -t 0 ] && [ -t 1 ] && [ -z \"${INTENTER_NO_UPDATE_CHECK:-}\" ] && " +
			"[ -z \"${INTENTER_STARTUP_CHECKED:-}\" ] && [ -x " + shellQuote(executable) + " ]; then\n" +
			"        INTENTER_STARTUP_CHECKED=1; export INTENTER_STARTUP_CHECKED\n" +
			"        " + shellQuote(executable) + " update --startup\n" +
			"    fi\n" +
			"    ;;\n" +
			"esac\n" +
			MarkerEnd + "\n"
	}
}

// shellQuote makes a path safe inside single quotes in a POSIX shell.
func shellQuote(path string) string {
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}

// fishQuote does the same for fish, where a backslash inside single quotes is
// an escape character.
func fishQuote(path string) string {
	escaped := strings.ReplaceAll(path, `\`, `\\`)
	return "'" + strings.ReplaceAll(escaped, "'", `\'`) + "'"
}

// powerShellQuote makes a path safe inside a single-quoted PowerShell string.
func powerShellQuote(path string) string {
	return "'" + strings.ReplaceAll(path, "'", "''") + "'"
}
