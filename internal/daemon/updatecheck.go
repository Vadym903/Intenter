package daemon

import (
	"context"
	"log/slog"
	"time"

	"github.com/Vadym903/Intenter/internal/config"
	"github.com/Vadym903/Intenter/internal/platform"
	"github.com/Vadym903/Intenter/internal/updater"
	"github.com/Vadym903/Intenter/internal/version"
)

// updateCheckEvery is how often the daemon looks at whether a check is due. It
// is not how often a check happens: that is `updates.check_interval`, a day by
// default. The ticker is short so a machine that was asleep at the moment a
// check came due does not wait another whole day.
const updateCheckEvery = time.Hour

// updateCheckDelay keeps the first check away from login, when a user's machine
// is busiest and the network least likely to be up.
const updateCheckDelay = 2 * time.Minute

// updateChecker keeps the cached release information fresh while the daemon
// runs, so the terminal start-up path only ever has to read a file.
//
// It is deliberately separate from everything that answers requests: a check
// runs on its own goroutine, never blocks, and its failure has no effect on any
// decision. A daemon that cannot reach the network still gates commands exactly
// as before.
type updateChecker struct {
	updates config.UpdatesConfig
	logger  *slog.Logger
	// check performs one check; injectable so tests need no network.
	check func(context.Context) error
	// due reports whether a check should run now.
	due func(time.Time) bool
	// now, every and delay are injectable so the schedule can be tested
	// without waiting for it.
	now   func() time.Time
	every time.Duration
	delay time.Duration
}

// newUpdateChecker builds the daemon's checker from its platform and config.
func newUpdateChecker(p platform.Platform, cfg config.Config, logger *slog.Logger) *updateChecker {
	store := updater.NewStore(p.DataDir())
	updates := cfg.Updates

	stable, _ := p.SelfExecutablePath()
	install := updater.DetectInstall(stable, p.HomeDir(), p.DataDir())

	checker := &updater.Checker{
		Store:          store,
		Updates:        updates,
		Sources:        updater.SourcesFromEnv(),
		Installed:      version.Version,
		InstallChannel: install.Channel,
	}

	return &updateChecker{
		updates: updates,
		logger:  logger,
		check: func(ctx context.Context) error {
			_, err := checker.Check(ctx)
			return err
		},
		due: func(now time.Time) bool {
			return store.LoadOrZero().CheckDue(now, updates)
		},
		now:   time.Now,
		every: updateCheckEvery,
		delay: updateCheckDelay,
	}
}

// run performs checks until the context is canceled. It returns immediately
// when checking is switched off, which is what makes "no network requests at
// all" observable rather than merely intended (SC-008).
func (u *updateChecker) run(ctx context.Context) {
	if u == nil || !u.updates.Check {
		return
	}

	first := time.NewTimer(u.delay)
	defer first.Stop()
	select {
	case <-ctx.Done():
		return
	case <-first.C:
	}
	u.runOnce(ctx)

	ticker := time.NewTicker(u.every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			u.runOnce(ctx)
		}
	}
}

// runOnce checks if one is due, and logs the outcome either way.
func (u *updateChecker) runOnce(ctx context.Context) {
	if !u.due(u.now()) {
		return
	}
	if err := u.check(ctx); err != nil {
		// A failed check is ordinary — laptops close, networks go away — so it
		// is not a warning. The reason is stored in the state file for
		// `intenter update --check` to report.
		u.logger.Debug("update check failed", "error", err)
		return
	}
	u.logger.Debug("update check complete")
}
