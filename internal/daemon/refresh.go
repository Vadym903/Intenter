package daemon

import (
	"os"
	"sync"

	"github.com/Vadym903/Intenter/internal/ipc"
	"github.com/Vadym903/Intenter/internal/platform"
	"github.com/Vadym903/Intenter/internal/version"
)

// ExitCodeRefresh is the status the daemon exits with when it has noticed that
// a newer binary is installed and it should be restarted into it. The service
// managers treat it like any other exit and start the daemon again, which is
// the whole mechanism: launchd `KeepAlive`, systemd `Restart=always`, and on
// Windows the hook's lazy start.
const ExitCodeRefresh = 75

// refreshWatch decides whether a newer client means this daemon should step
// aside for a newer binary.
//
// An upgrade replaces the executable while the daemon is still running the
// previous one, so the two halves of the pair disagree until something
// restarts. The CLI and the hook announce their version on every request, which
// is how the daemon learns.
//
// The restart is conditional on the daemon's *own* executable having changed on
// disk since it started. Without that condition a newer binary installed
// somewhere else on PATH — a Homebrew install alongside a `curl | sh` one, say —
// would make every request restart a daemon into the same old code, and the
// gate would spend its life restarting. When the file has not changed there is
// nothing to restart into, so the daemon says so once and keeps working;
// `intenter doctor` reports the mismatch with the command that fixes it.
type refreshWatch struct {
	// version is the release this daemon was built from.
	version string
	// executable identifies the binary file as it was when the daemon started.
	executable os.FileInfo
	// path is where that binary lives.
	path string

	mu       sync.Mutex
	reported bool
	// stat is injectable so the upgrade can be simulated in tests.
	stat func(string) (os.FileInfo, error)
}

// newRefreshWatch records the running binary so a later replacement is visible.
func newRefreshWatch(executablePath string) *refreshWatch {
	watch := &refreshWatch{
		version: version.Version,
		path:    executablePath,
		stat:    os.Stat,
	}
	if executablePath != "" {
		if info, err := os.Stat(executablePath); err == nil {
			watch.executable = info
		}
	}
	return watch
}

// refreshDecision is what the daemon should do about a request's client.
type refreshDecision int

const (
	// refreshNone: the client is not newer, or this build cannot tell.
	refreshNone refreshDecision = iota
	// refreshRestart: a newer binary is in place; finish this request and exit.
	refreshRestart
	// refreshReport: the client is newer but restarting would change nothing.
	refreshReport
)

// decide reports what a request's client version means for this daemon.
func (w *refreshWatch) decide(clientVersion string) refreshDecision {
	if w == nil || clientVersion == "" {
		return refreshNone
	}
	if !version.IsNewer(clientVersion, w.version) {
		return refreshNone
	}
	if !w.executableChanged() {
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.reported {
			return refreshNone
		}
		w.reported = true
		return refreshReport
	}
	return refreshRestart
}

// executableChanged reports whether the binary at the daemon's own path is a
// different file from the one it is running.
//
// When the path cannot be read at all, the answer is "unchanged": restarting
// into something unreadable is not an improvement.
func (w *refreshWatch) executableChanged() bool {
	if w.executable == nil || w.path == "" {
		return false
	}
	current, err := w.stat(w.path)
	if err != nil {
		return false
	}
	return changedFile(w.executable, current)
}

// changedFile reports whether two stats of one path describe different files.
//
// os.SameFile is the primary question. An installer usually replaces the file
// rather than writing through it, so the running process keeps the old inode
// while the path names a new one, and identity is exactly the difference to
// look for.
//
// It is not sufficient on its own, for two reasons:
//
//   - On Windows an os.FileInfo does not carry the file's identity until
//     something asks for it; the comparison reopens the recorded path to read
//     the volume and file index. An upgrade happens while the daemon sits
//     idle, so the first comparison usually happens *after* the replacement —
//     that lazy read then sees the new file, both sides come back identical,
//     and the daemon never notices its own upgrade.
//   - An installer that writes through the path instead of renaming over it
//     keeps the inode on every platform. `install -m 0755 intenter …`, which
//     docs/install.md documents for manual installs, does exactly that.
//
// Size and modification time close both gaps. They are weaker evidence of
// identity, but they are read eagerly at stat time and a replaced binary
// changed at least one of them.
func changedFile(before, after os.FileInfo) bool {
	if !os.SameFile(before, after) {
		return true
	}
	return before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime())
}

// observeClient applies the refresh policy to one served request. It is called
// after the response has been produced, so the client always gets its answer.
func (d *Daemon) observeClient(req *ipc.Request) {
	if req == nil {
		return
	}
	switch d.refresh.decide(req.ClientVersion) {
	case refreshRestart:
		d.logger.Info("newer client detected; restarting",
			"daemon_version", d.refresh.version,
			"client_version", req.ClientVersion)
		d.requestRefresh()
	case refreshReport:
		d.logger.Warn("newer client detected, but this daemon's binary is unchanged; "+
			"run `intenter doctor`",
			"daemon_version", d.refresh.version,
			"client_version", req.ClientVersion,
			"executable", d.refresh.path)
	case refreshNone:
	}
}

// requestRefresh stops the daemon with the refresh exit code, once.
func (d *Daemon) requestRefresh() {
	d.refreshOnce.Do(func() {
		d.exitCode.Store(ExitCodeRefresh)
		d.RequestShutdown()
	})
}

// selfExecutablePath locates the running binary for the refresh watch. A
// failure is not worth reporting: it only means this daemon cannot notice its
// own replacement, and `intenter doctor` still can.
func selfExecutablePath() string {
	path, err := platform.SelfExecutablePath()
	if err != nil {
		return ""
	}
	return path
}
