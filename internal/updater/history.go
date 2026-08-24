package updater

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"
)

// historyLimit is how many decisions are kept. The log answers "why was I asked
// this" and "what changed when", which are recent questions; keeping every
// check forever would turn a diagnostic into a growing file nobody reads.
const historyLimit = 500

// History event names (003 data-model §3). The "hook" names are kept as they
// are because they are a stored format, even though the user-facing command is
// called `update startup` to avoid confusion with Claude Code hooks.
const (
	EventCheckOK       = "check_ok"
	EventCheckFailed   = "check_failed"
	EventPromptShown   = "prompt_shown"
	EventChoiceUpdate  = "choice_update"
	EventChoiceNotNow  = "choice_not_now"
	EventChoiceSkip    = "choice_skip"
	EventChoiceTimeout = "choice_timeout"
	EventUpdateStarted = "update_started"
	EventUpdateOK      = "update_ok"
	EventUpdateFailed  = "update_failed"
	EventHookInstalled = "hook_installed"
	EventHookRemoved   = "hook_removed"
	EventHookBlocked   = "hook_blocked"
)

// UpdateDecision is one line of the decision log.
type UpdateDecision struct {
	At               time.Time `json:"at"`
	Event            string    `json:"event"`
	InstalledVersion string    `json:"installed_version,omitempty"`
	TargetVersion    string    `json:"target_version,omitempty"`
	Channel          string    `json:"channel,omitempty"`
	Detail           string    `json:"detail,omitempty"`
}

// Append adds one decision to the log, trimming it when it grows past the
// retention limit.
//
// A failure to write history never fails the operation it describes: a user
// updating their tool should not be stopped because a diagnostic file is on a
// full disk. The error is returned so callers that can report it do.
func (s *Store) Append(decision UpdateDecision) error {
	if err := s.ensureDir(); err != nil {
		return err
	}
	if decision.At.IsZero() {
		decision.At = time.Now().UTC()
	} else {
		decision.At = decision.At.UTC()
	}

	line, err := json.Marshal(decision)
	if err != nil {
		return fmt.Errorf("updater: encode history entry: %w", err)
	}

	file, err := os.OpenFile(s.HistoryPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("updater: open %s: %w", s.HistoryPath(), err)
	}
	if _, err := file.Write(append(line, '\n')); err != nil {
		file.Close()
		return fmt.Errorf("updater: write history: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("updater: write history: %w", err)
	}
	return s.trimHistory()
}

// Tail returns the last n decisions, oldest first. An unreadable line is
// skipped rather than fatal: a truncated write must not hide the entries around
// it.
func (s *Store) Tail(n int) []UpdateDecision {
	if n <= 0 {
		return []UpdateDecision{}
	}
	lines, err := readLines(s.HistoryPath())
	if err != nil {
		return []UpdateDecision{}
	}

	out := make([]UpdateDecision, 0, n)
	for _, line := range lines {
		var decision UpdateDecision
		if err := json.Unmarshal([]byte(line), &decision); err != nil {
			continue
		}
		out = append(out, decision)
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

// trimHistory truncates the log to the retention limit, rewriting it only when
// it has actually grown past it.
func (s *Store) trimHistory() error {
	lines, err := readLines(s.HistoryPath())
	if err != nil {
		return err
	}
	if len(lines) <= historyLimit {
		return nil
	}

	kept := strings.Join(lines[len(lines)-historyLimit:], "\n") + "\n"
	temp, err := os.CreateTemp(s.dir, "history-*.jsonl")
	if err != nil {
		return fmt.Errorf("updater: stage history: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)

	if _, err := temp.WriteString(kept); err != nil {
		temp.Close()
		return fmt.Errorf("updater: trim history: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("updater: trim history: %w", err)
	}
	if err := os.Chmod(tempPath, 0o600); err != nil {
		return fmt.Errorf("updater: permissions on history: %w", err)
	}
	if err := os.Rename(tempPath, s.HistoryPath()); err != nil {
		return fmt.Errorf("updater: install history: %w", err)
	}
	return nil
}

// readLines returns the non-empty lines of a file; a missing file has none.
func readLines(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("updater: read %s: %w", path, err)
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			lines = append(lines, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("updater: read %s: %w", path, err)
	}
	return lines, nil
}
