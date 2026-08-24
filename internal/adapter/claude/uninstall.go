package claude

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Vadym903/Intenter/internal/config"
	"github.com/Vadym903/Intenter/internal/ipc"
	"github.com/Vadym903/Intenter/internal/platform"
	"github.com/Vadym903/Intenter/internal/updater"
)

// UninstallOptions configure `intenter uninstall claude` (§12.3).
type UninstallOptions struct {
	// SettingsPath overrides the Claude settings file.
	SettingsPath string
	// KeepDaemon leaves the service registered and running.
	KeepDaemon bool
	// Purge removes the database and configuration as well.
	Purge bool
}

// UninstallResult is everything uninstall did.
type UninstallResult struct {
	Steps      []Step
	BackupPath string
	// StartupCheckFiles are the shell start-up files the update check was
	// removed from.
	StartupCheckFiles []string
}

// Failed reports whether any step failed.
func (r *UninstallResult) Failed() bool {
	for _, step := range r.Steps {
		if !step.OK() {
			return true
		}
	}
	return false
}

// Uninstall removes Intenter's integration (§12.3).
//
// It is deliberately conservative: only Intenter's own hook entries are
// removed, the settings file is backed up first, and the user's data is kept
// unless they ask for it to go (INVARIANT I-9).
type Uninstall struct {
	platform platform.Platform
	config   config.Config
	services platform.ServiceManager
	options  UninstallOptions
	now      func() time.Time
}

// NewUninstall builds the uninstall runner.
func NewUninstall(p platform.Platform, cfg config.Config, services platform.ServiceManager, options UninstallOptions) *Uninstall {
	return &Uninstall{platform: p, config: cfg, services: services, options: options, now: time.Now}
}

// Run executes the removal steps.
func (u *Uninstall) Run(ctx context.Context) (*UninstallResult, error) {
	result := &UninstallResult{}

	steps := []func(context.Context, *UninstallResult) Step{
		u.backup,
		u.removeHooks,
		u.removeStartupCheck,
		u.stopDaemon,
		u.purge,
	}
	for _, step := range steps {
		executed := step(ctx, result)
		result.Steps = append(result.Steps, executed)
		if !executed.OK() {
			return result, executed.Err
		}
	}
	return result, nil
}

// timed runs a step body and stamps how long it took.
func (u *Uninstall) timed(name string, body func() (string, string, error)) Step {
	started := u.now()
	detail, warning, err := body()
	return Step{
		Name:     name,
		Detail:   detail,
		Warning:  warning,
		Err:      err,
		Duration: u.now().Sub(started),
	}
}

// removeStartupCheck takes the managed block back out of the shell start-up
// files (003 FR-009).
//
// Uninstalling has to leave nothing behind that runs at terminal start: a
// leftover block calling a binary that is gone would print an error in every
// new terminal, forever, from a tool the user believes they removed.
func (u *Uninstall) removeStartupCheck(_ context.Context, result *UninstallResult) Step {
	return u.timed("Start-up update check removed", func() (string, string, error) {
		check := &updater.StartupCheck{
			Home:  u.platform.HomeDir(),
			Store: updater.NewStore(u.platform.DataDir()),
		}
		before := check.Status(nil).Installed
		if _, err := check.Remove(); err != nil {
			return "", "", err
		}
		result.StartupCheckFiles = before

		if len(before) == 0 {
			return "none installed", "", nil
		}
		return strings.Join(before, ", "), "", nil
	})
}

// settingsPath is the file hooks are removed from.
func (u *Uninstall) settingsPath() string {
	if u.options.SettingsPath != "" {
		return u.options.SettingsPath
	}
	return filepath.Join(u.platform.HomeDir(), ".claude", "settings.json")
}

// backup copies the settings before they are edited (§12.3 step 1).
func (u *Uninstall) backup(_ context.Context, result *UninstallResult) Step {
	return u.timed("Settings backed up", func() (string, string, error) {
		path, err := BackupSettings(u.settingsPath(), u.platform.DataDir(), u.now())
		if err != nil {
			return "", "", err
		}
		result.BackupPath = path
		if path == "" {
			return "no settings file to back up", "", nil
		}
		return path, "", nil
	})
}

// removeHooks deletes only Intenter's own entries (§12.3 step 2).
func (u *Uninstall) removeHooks(_ context.Context, _ *UninstallResult) Step {
	return u.timed("Permission hooks removed", func() (string, string, error) {
		path := u.settingsPath()
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return "no settings file to edit", "", nil
		}
		if err := RemoveHooks(path); err != nil {
			return "", "", err
		}
		return path, "", nil
	})
}

// stopDaemon stops and unregisters the service (§12.3 step 3).
func (u *Uninstall) stopDaemon(ctx context.Context, _ *UninstallResult) Step {
	return u.timed("Daemon stopped", func() (string, string, error) {
		if u.options.KeepDaemon {
			return "kept running at your request", "", nil
		}

		// Ask the daemon to exit first, so it releases the database before the
		// service registration goes.
		client := ipc.NewClient(ipc.DiscoverEndpoint("", u.platform.DataDir(), u.platform.IPCEndpoint()))
		if _, err := client.Ping(ctx); err == nil {
			if err := client.Call(ctx, ipc.MethodShutdown, nil, nil); err != nil {
				return "", "", fmt.Errorf("could not stop the daemon: %w", err)
			}
			waitForShutdown(ctx, client, platform.PidFilePath(u.platform))
		}

		if u.services == nil {
			return "unregistered", "", nil
		}
		if err := u.services.Uninstall(ctx); err != nil {
			return "", fmt.Sprintf("could not unregister the service: %v", err), nil
		}
		return "unregistered", "", nil
	})
}

// waitForShutdown gives the daemon a moment to exit: first the endpoint stops
// answering, then the pid file goes. The daemon removes the pid file last,
// after closing the database, and purge relies on that order — on Windows an
// open database file cannot be deleted.
func waitForShutdown(ctx context.Context, client *ipc.Client, pidPath string) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := client.Ping(ctx); err != nil {
			if _, statErr := os.Stat(pidPath); os.IsNotExist(statErr) {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// purge removes Intenter's own data, only when asked (§12.3 step 4).
//
// The database holds the user's approval history, which is the record of what
// they consented to. It survives an ordinary uninstall so reinstalling does not
// silently lose it.
//
// The settings backups live under the data directory and go with it. That is
// safe because the steps run in order and stop on the first failure: purge is
// only reached once the hooks were removed successfully, so the backup has
// already served its purpose.
func (u *Uninstall) purge(_ context.Context, _ *UninstallResult) Step {
	return u.timed("Data removed", func() (string, string, error) {
		if !u.options.Purge {
			return fmt.Sprintf("kept in %s (use --purge to remove)", u.platform.DataDir()), "", nil
		}

		for _, dir := range []string{u.platform.DataDir(), u.platform.ConfigDir(), u.platform.RuntimeDir()} {
			if dir == "" {
				continue
			}
			if err := removeAllRetrying(dir); err != nil {
				return "", "", fmt.Errorf("could not remove %s: %w", dir, err)
			}
		}
		return "approvals, history, configuration and settings backups removed", "", nil
	})
}

// removeAllRetrying removes a directory tree, retrying for a moment when it
// cannot: a daemon that was just asked to stop closes its log file as the very
// last thing it does, and on Windows a file that is still open cannot be
// deleted.
func removeAllRetrying(dir string) error {
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := os.RemoveAll(dir)
		if err == nil || !time.Now().Before(deadline) {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
}
