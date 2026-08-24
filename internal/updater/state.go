package updater

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/Vadym903/Intenter/internal/config"
	"github.com/Vadym903/Intenter/internal/platform"
)

// StateSchema is the version of the state file's shape. A file written by a
// future build is read on a best-effort basis rather than deleted: the worst
// case is one unnecessary check, and losing a user's "skip this version" would
// be a worse outcome than that.
const StateSchema = 1

// inProgressStale is how long an `update_in_progress` marker is believed. A
// machine that lost power mid-update must not be locked out of updating
// forever, and no update this tool performs takes half an hour.
const inProgressStale = 30 * time.Minute

// backoffSchedule is how long to wait after consecutive failed checks. A
// laptop that is offline, behind a captive portal or on a rate-limited network
// would otherwise retry on every terminal it opens.
var backoffSchedule = []time.Duration{time.Hour, 6 * time.Hour, 24 * time.Hour}

// UpdateState is everything the update feature remembers between processes
// (003 data-model §1). It is shared by every terminal and by the daemon, so it
// is small, atomically written and readable without a lock.
type UpdateState struct {
	Schema int `json:"schema"`
	// InstalledVersion is the build that last wrote this file. It is
	// informational: eligibility always compares against the running binary,
	// which may be older or newer than whatever wrote the file last.
	InstalledVersion string `json:"installed_version,omitempty"`
	// InstallChannel is how this copy was installed, which decides whether the
	// updater may replace the executable itself.
	InstallChannel string `json:"install_channel,omitempty"`
	// Channel is the release channel the last check followed.
	Channel string `json:"channel,omitempty"`

	LatestKnown *LatestKnown `json:"latest_known,omitempty"`

	LastCheckAt    *time.Time `json:"last_check_at,omitempty"`
	LastCheckOK    bool       `json:"last_check_ok"`
	LastCheckError string     `json:"last_check_error,omitempty"`
	CheckFailures  int        `json:"check_failures"`
	NextCheckAfter *time.Time `json:"next_check_after,omitempty"`

	// SkippedVersion is the one version the user asked never to be offered.
	SkippedVersion string `json:"skipped_version,omitempty"`
	// DeferredUntil is when prompting may resume after a "not now".
	DeferredUntil *time.Time `json:"deferred_until,omitempty"`
	LastPromptAt  *time.Time `json:"last_prompt_at,omitempty"`

	UpdateInProgress *InProgress `json:"update_in_progress,omitempty"`
	LastUpdate       *LastUpdate `json:"last_update,omitempty"`

	StartupHook StartupHookState `json:"startup_hook"`
}

// LatestKnown is the newest release the last successful check found.
type LatestKnown struct {
	Version  string    `json:"version"`
	NotesURL string    `json:"notes_url,omitempty"`
	FoundAt  time.Time `json:"found_at"`
}

// InProgress marks an update that started and has not reported back.
type InProgress struct {
	PID           int       `json:"pid"`
	StartedAt     time.Time `json:"started_at"`
	TargetVersion string    `json:"target_version,omitempty"`
}

// LastUpdate is the outcome of the most recent attempt.
type LastUpdate struct {
	From   string    `json:"from,omitempty"`
	To     string    `json:"to,omitempty"`
	At     time.Time `json:"at"`
	Result string    `json:"result"`
	Error  string    `json:"error,omitempty"`
}

// Update results recorded in LastUpdate.
const (
	UpdateResultOK     = "ok"
	UpdateResultFailed = "failed"
)

// StartupHookState is where the managed start-up block currently lives.
type StartupHookState struct {
	InstalledFiles  []string   `json:"installed_files"`
	InstalledAt     *time.Time `json:"installed_at,omitempty"`
	BlockedByPolicy bool       `json:"blocked_by_policy,omitempty"`
}

// Store owns the files under <DataDir>/update.
type Store struct{ dir string }

// NewStore returns the store for a data directory.
func NewStore(dataDir string) *Store {
	return &Store{dir: filepath.Join(dataDir, "update")}
}

// Dir is <DataDir>/update.
func (s *Store) Dir() string { return s.dir }

// StatePath is the state file.
func (s *Store) StatePath() string { return filepath.Join(s.dir, "state.json") }

// HistoryPath is the append-only decision log.
func (s *Store) HistoryPath() string { return filepath.Join(s.dir, "history.jsonl") }

// StateLockPath serializes writers of the state file.
func (s *Store) StateLockPath() string { return filepath.Join(s.dir, "state.lock") }

// PromptLockPath admits one prompt or update at a time across terminals.
func (s *Store) PromptLockPath() string { return filepath.Join(s.dir, "prompt.lock") }

// TempDir is where downloads are staged.
func (s *Store) TempDir() string { return filepath.Join(s.dir, "tmp") }

// ensureDir creates <DataDir>/update on demand. The start-up path reads before
// it ever writes, so the directory is not created just by looking.
func (s *Store) ensureDir() error { return platform.EnsureDir(s.dir) }

// Load reads the state. A missing or unreadable file yields the zero state:
// the update feature is an assistant, and it must not turn one corrupt file
// into a broken terminal.
func (s *Store) Load() (UpdateState, error) {
	data, err := os.ReadFile(s.StatePath())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return UpdateState{Schema: StateSchema}, nil
		}
		return UpdateState{Schema: StateSchema}, fmt.Errorf("updater: read %s: %w", s.StatePath(), err)
	}

	var state UpdateState
	if err := json.Unmarshal(data, &state); err != nil {
		return UpdateState{Schema: StateSchema}, fmt.Errorf("updater: parse %s: %w", s.StatePath(), err)
	}
	if state.Schema == 0 {
		state.Schema = StateSchema
	}
	return state, nil
}

// LoadOrZero reads the state, ignoring the error. The start-up path uses it:
// there is nothing useful to say to a user opening a terminal about a state
// file that could not be parsed, and `intenter doctor` reports it properly.
func (s *Store) LoadOrZero() UpdateState {
	state, err := s.Load()
	if err != nil {
		return UpdateState{Schema: StateSchema}
	}
	return state
}

// Save writes the state atomically, so a reader never sees half a file and a
// crash mid-write leaves the previous state intact.
func (s *Store) Save(state UpdateState) error {
	if err := s.ensureDir(); err != nil {
		return err
	}
	state.Schema = StateSchema

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("updater: encode state: %w", err)
	}
	data = append(data, '\n')

	temp, err := os.CreateTemp(s.dir, "state-*.json")
	if err != nil {
		return fmt.Errorf("updater: stage state: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)

	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("updater: write state: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("updater: write state: %w", err)
	}
	if err := os.Chmod(tempPath, 0o600); err != nil {
		return fmt.Errorf("updater: permissions on state: %w", err)
	}
	if err := os.Rename(tempPath, s.StatePath()); err != nil {
		return fmt.Errorf("updater: install state: %w", err)
	}
	return nil
}

// Mutate applies a change to the state under the writer lock, so two terminals
// answering a prompt at the same moment cannot lose each other's answer.
func (s *Store) Mutate(change func(*UpdateState)) (UpdateState, error) {
	if err := s.ensureDir(); err != nil {
		return UpdateState{}, err
	}
	release, err := acquireLock(s.StateLockPath())
	if err != nil {
		return UpdateState{}, err
	}
	defer release()

	state, err := s.Load()
	if err != nil {
		// A file we cannot read is replaced rather than left to block every
		// future write; the alternative is an installation that can never
		// record another decision.
		state = UpdateState{Schema: StateSchema}
	}
	change(&state)
	if err := s.Save(state); err != nil {
		return state, err
	}
	return state, nil
}

// CheckDue reports whether a background check should run now.
func (s UpdateState) CheckDue(now time.Time, cfg config.UpdatesConfig) bool {
	if !cfg.Check {
		return false
	}
	if s.NextCheckAfter == nil {
		return true
	}
	return !now.Before(*s.NextCheckAfter)
}

// Eligible reports whether the user should be prompted about the known latest
// version, given the version actually running (003 data-model §1).
//
// The installed version is a parameter rather than the recorded one because the
// file may have been written by a different build — after a package-manager
// upgrade, for instance — and offering an update to a version already installed
// is exactly the noise that gets a prompt disabled.
func (s UpdateState) Eligible(now time.Time, installed string, cfg config.UpdatesConfig) bool {
	if !cfg.Check || s.LatestKnown == nil {
		return false
	}
	latest := s.LatestKnown.Version
	if !Newer(latest, installed) {
		return false
	}
	if Prerelease(latest) && !cfg.Prerelease() && !Prerelease(installed) {
		return false
	}
	if s.SkippedVersion != "" && Same(latest, s.SkippedVersion) {
		return false
	}
	if s.DeferredUntil != nil && now.Before(*s.DeferredUntil) {
		return false
	}
	if s.LastPromptAt != nil && now.Sub(*s.LastPromptAt) < cfg.RemindEvery() {
		return false
	}
	return !s.UpdateRunning(now)
}

// UpdateRunning reports whether another process is part-way through an update.
func (s UpdateState) UpdateRunning(now time.Time) bool {
	if s.UpdateInProgress == nil {
		return false
	}
	return now.Sub(s.UpdateInProgress.StartedAt) < inProgressStale
}

// RecordCheckOK stores the result of a successful check.
//
// A skipped version is forgotten once something newer than it appears: the user
// declined that release, not every release after it.
func (s *UpdateState) RecordCheckOK(now time.Time, latest LatestKnown, cfg config.UpdatesConfig) {
	s.LastCheckAt = timePtr(now)
	s.LastCheckOK = true
	s.LastCheckError = ""
	s.CheckFailures = 0
	s.NextCheckAfter = timePtr(now.Add(cfg.CheckEvery()))
	s.Channel = channelName(cfg)

	if latest.Version != "" {
		if latest.FoundAt.IsZero() {
			latest.FoundAt = now
		}
		s.LatestKnown = &latest
		if s.SkippedVersion != "" && Newer(latest.Version, s.SkippedVersion) {
			s.SkippedVersion = ""
		}
	}
}

// RecordCheckFailure stores a failed check and backs off.
func (s *UpdateState) RecordCheckFailure(now time.Time, reason string, cfg config.UpdatesConfig) {
	s.LastCheckAt = timePtr(now)
	s.LastCheckOK = false
	s.LastCheckError = reason
	s.CheckFailures++
	s.NextCheckAfter = timePtr(now.Add(backoffFor(s.CheckFailures, cfg)))
	s.Channel = channelName(cfg)
}

// backoffFor is how long to wait after n consecutive failures, never shorter
// than the configured interval when that is already longer.
func backoffFor(failures int, cfg config.UpdatesConfig) time.Duration {
	if failures < 1 {
		failures = 1
	}
	index := failures - 1
	if index >= len(backoffSchedule) {
		index = len(backoffSchedule) - 1
	}
	wait := backoffSchedule[index]
	if interval := cfg.CheckEvery(); interval > wait {
		return interval
	}
	return wait
}

func channelName(cfg config.UpdatesConfig) string {
	if cfg.Prerelease() {
		return config.ChannelPrerelease
	}
	return config.ChannelStable
}

func timePtr(t time.Time) *time.Time {
	value := t.UTC()
	return &value
}
