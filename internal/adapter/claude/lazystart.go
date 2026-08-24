package claude

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/Vadym903/Intenter/internal/ipc"
	"github.com/Vadym903/Intenter/internal/logging"
	"github.com/Vadym903/Intenter/internal/platform"
)

// LazyStartTimeout bounds how long a hook waits for a daemon it just started.
// A hook that waits longer than this is worse for the user than deferring
// (§9.5: retry for at most 2 s).
const LazyStartTimeout = 2 * time.Second

// lazyStartPoll is how often the hook re-checks while waiting.
const lazyStartPoll = 50 * time.Millisecond

// LazyStart returns the hook-side daemon starter (§9.5).
//
// On Windows and on Linux without `systemd --user` there is no service manager
// to keep the daemon running, so the first gated command of a session starts
// it. Starting is best effort: if it does not come up in time the hook defers,
// which is the same outcome as having no daemon at all.
func LazyStart(p platform.Platform, logger *slog.Logger) func(context.Context) error {
	if logger == nil {
		logger = logging.Discard()
	}
	return func(ctx context.Context) error {
		executable, err := p.SelfExecutablePath()
		if err != nil {
			return fmt.Errorf("claude: locate the intenter executable: %w", err)
		}

		logPath := filepath.Join(platform.LogDir(p), logging.DaemonLogFile)
		pid, err := platform.SpawnDetached(executable, []string{"daemon", "run"}, logPath)
		if err != nil {
			return fmt.Errorf("claude: start the daemon: %w", err)
		}
		logger.Info("claude: started the daemon lazily", "pid", pid)

		return waitForDaemon(ctx, p, LazyStartTimeout)
	}
}

// waitForDaemon polls until the daemon answers or the deadline passes.
func waitForDaemon(ctx context.Context, p platform.Platform, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		endpoint := ipc.DiscoverEndpoint("", p.DataDir(), p.IPCEndpoint())
		if endpoint != "" {
			if _, err := ipc.NewClient(endpoint).Ping(ctx); err == nil {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("claude: the daemon did not answer within %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(lazyStartPoll):
		}
	}
}
