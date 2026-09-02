package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Vadym903/Intenter/internal/daemon"
	"github.com/Vadym903/Intenter/internal/ipc"
	"github.com/Vadym903/Intenter/internal/logging"
	"github.com/Vadym903/Intenter/internal/platform"
	"github.com/Vadym903/Intenter/internal/storage"
)

// startTimeout is how long `daemon start` waits for the daemon to answer (§9.2).
const startTimeout = 5 * time.Second

// newDaemonCommand builds `intenter daemon [run|start|stop|restart|status|
// install|uninstall]` (§9.2).
func newDaemonCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Run and manage the Intenter daemon",
		Long: "The daemon is the per-user process that evaluates every command and owns\n" +
			"the approvals and history. `intenter setup claude` registers it with launchd,\n" +
			"systemd (user) or the Windows Run key and starts it; the hook also starts it\n" +
			"lazily when it is not running. These subcommands are for looking at it and\n" +
			"for the rare manual restart — a stopped daemon never means \"allow\": Claude's\n" +
			"own permission prompt takes over until it is back.",
		Example: "  intenter daemon status\n" +
			"  intenter daemon restart\n" +
			"  intenter daemon run        # foreground, for debugging",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Bare `daemon` runs in the foreground and hints at `daemon start`.
			app.Warnf("Running in the foreground. Use `intenter daemon start` to run it in the background.\n")
			return runDaemon(cmd.Context(), app)
		},
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "run",
			Short: "Run the daemon in the foreground",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				return runDaemon(cmd.Context(), app)
			},
		},
		&cobra.Command{
			Use:   "start",
			Short: "Start the daemon in the background",
			Args:  cobra.NoArgs,
			RunE:  func(cmd *cobra.Command, args []string) error { return startDaemon(cmd.Context(), app) },
		},
		&cobra.Command{
			Use:   "stop",
			Short: "Stop the running daemon",
			Args:  cobra.NoArgs,
			RunE:  func(cmd *cobra.Command, args []string) error { return stopDaemon(cmd.Context(), app) },
		},
		&cobra.Command{
			Use:   "restart",
			Short: "Restart the daemon",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				if err := stopDaemon(cmd.Context(), app); err != nil {
					var exit *exitError
					// Stopping a daemon that is not running is not an error here.
					if !errors.As(err, &exit) || exit.code != ExitDaemonUnreached {
						return err
					}
				}
				return startDaemon(cmd.Context(), app)
			},
		},
		&cobra.Command{
			Use:   "status",
			Short: "Show daemon state",
			Args:  cobra.NoArgs,
			RunE:  func(cmd *cobra.Command, args []string) error { return daemonStatus(cmd.Context(), app) },
		},
		&cobra.Command{
			Use:   "install",
			Short: "Register the per-user daemon service",
			Args:  cobra.NoArgs,
			RunE:  func(cmd *cobra.Command, args []string) error { return installService(cmd.Context(), app) },
		},
		&cobra.Command{
			Use:   "uninstall",
			Short: "Unregister the per-user daemon service",
			Args:  cobra.NoArgs,
			RunE:  func(cmd *cobra.Command, args []string) error { return uninstallService(cmd.Context(), app) },
		},
	)
	return cmd
}

// runDaemon executes the daemon in the foreground until a termination signal
// arrives (§9.3 step 6).
func runDaemon(ctx context.Context, app *App) error {
	if ctx == nil {
		ctx = context.Background()
	}
	signalCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger, closer, err := logging.FileLogger(platform.LogDir(app.Platform), logging.DaemonLogFile, app.Config.Log.Level)
	if err != nil {
		return Fail(ExitError, err)
	}
	defer closer.Close()

	instance, err := daemon.New(daemon.Options{
		Platform: app.Platform,
		Config:   app.Config,
		Logger:   logger,
	})
	if err != nil {
		return Fail(ExitError, err)
	}

	if err := instance.Run(signalCtx); err != nil {
		var alreadyRunning *daemon.ErrAlreadyRunning
		if errors.As(err, &alreadyRunning) {
			return Fail(ExitError, err)
		}
		return Fail(ExitError, err)
	}

	// A clean stop that asks to be restarted: the binary was replaced under a
	// running daemon, and the service manager brings the new one up.
	if code := instance.ExitCode(); code == daemon.ExitCodeRefresh {
		return Failf(code, "stopping to restart into the newly installed binary")
	}
	return nil
}

// startDaemon starts the daemon in the background and waits for it to answer.
//
// A registered service is asked to start it, so the platform keeps supervising
// it afterwards; otherwise the daemon is spawned directly, which is the
// unmanaged mode a machine without a per-user service manager uses.
func startDaemon(ctx context.Context, app *App) error {
	ctx = orBackground(ctx)

	if _, err := daemon.Ping(ctx, app.Platform, ""); err == nil {
		app.Printf("Intenter daemon is already running.\n")
		return nil
	}
	if err := platform.EnsureDirs(app.Platform); err != nil {
		return Fail(ExitError, err)
	}

	manager := app.ServiceManager()
	if state, err := manager.Status(ctx); err == nil && state != platform.ServiceNotInstalled &&
		state != platform.ServiceUnsupported {
		if err := manager.Start(ctx); err != nil {
			return Fail(ExitError, err)
		}
		if _, err := daemon.WaitForPing(ctx, app.Platform, "", startTimeout); err != nil {
			return Fail(ExitError, fmt.Errorf(
				"the %s service did not bring the daemon up within %s: %w", manager.Name(), startTimeout, err))
		}
		app.Printf("Intenter daemon started (%s, managed).\n", manager.Name())
		return nil
	}

	executable, err := app.Platform.SelfExecutablePath()
	if err != nil {
		return Fail(ExitError, err)
	}

	logPath := daemonStdioLog(app)
	pid, err := platform.SpawnDetached(executable, []string{"daemon", "run"}, logPath)
	if err != nil {
		return Fail(ExitError, err)
	}

	if _, err := daemon.WaitForPing(ctx, app.Platform, "", startTimeout); err != nil {
		return Fail(ExitError, fmt.Errorf("daemon did not become ready within %s (pid %d, see %s): %w",
			startTimeout, pid, logPath, err))
	}
	app.Printf("Intenter daemon started (pid %d, unmanaged).\n", pid)
	return nil
}

// stopDaemon asks the running daemon to shut down and waits for it to exit.
func stopDaemon(ctx context.Context, app *App) error {
	ctx = orBackground(ctx)

	pid := daemonPID(app)
	client := app.Client()
	if err := client.Call(ctx, ipc.MethodShutdown, nil, nil); err != nil {
		if ipc.IsUnavailable(err) {
			return Failf(ExitDaemonUnreached, "daemon is not running")
		}
		return daemonError(err)
	}

	deadline := time.Now().Add(startTimeout)
	for time.Now().Before(deadline) {
		if _, err := daemon.Ping(ctx, app.Platform, ""); err != nil {
			waitForDaemonExit(ctx, platform.PidFilePath(app.Platform), pid, deadline)
			app.Printf("Intenter daemon stopped.\n")
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return Failf(ExitError, "daemon did not stop within %s", startTimeout)
}

// daemonPID is the process the single-instance pid file names, or zero.
func daemonPID(app *App) int {
	return daemon.ReadPidFile(platform.PidFilePath(app.Platform))
}

// waitForDaemonExit gives a daemon whose endpoint is already gone time to
// finish shutting down. The listener closes first and the single-instance
// lock is released last, after the database; a daemon started in between
// finds the lock held and exits, so a restart then looks like a daemon that
// never came up. The pid file goes only after the lock (Lock.Release), so the
// old process is out of the way once the file is missing or names another
// process — a supervisor may have started the next one already. A pid file
// left behind by a crash names nothing to wait for. Best effort: past the
// deadline the caller's own timeout takes over.
func waitForDaemonExit(ctx context.Context, pidPath string, pid int, deadline time.Time) {
	if pid == 0 || !processAlive(pid) {
		return
	}
	for time.Now().Before(deadline) {
		if daemon.ReadPidFile(pidPath) != pid {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// processAlive reports whether pid names a running process. On Windows
// FindProcess opens the process and fails when there is none; on unix it
// always succeeds and the null signal asks the kernel instead.
func processAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return process.Signal(syscall.Signal(0)) == nil
}

// DaemonStatus is the `daemon status --json` shape.
type DaemonStatus struct {
	Running     bool   `json:"running"`
	PID         int    `json:"pid,omitempty"`
	Version     string `json:"version,omitempty"`
	Endpoint    string `json:"endpoint"`
	UptimeS     int64  `json:"uptime_s,omitempty"`
	DBPath      string `json:"db_path"`
	ServiceMode string `json:"service_mode"`
}

// daemonStatus prints running state, pid, version, endpoint, uptime, database
// path and service mode (§9.2).
func daemonStatus(ctx context.Context, app *App) error {
	ctx = orBackground(ctx)

	client := app.Client()
	status := DaemonStatus{
		Endpoint:    client.Endpoint(),
		DBPath:      platform.DatabasePath(app.Platform),
		ServiceMode: "unmanaged",
	}

	ping, err := client.Ping(ctx)
	if err == nil {
		status.Running = true
		status.PID = ping.PID
		status.Version = ping.Version
		status.UptimeS = ping.UptimeS

		var full ipc.StatusResult
		if err := client.Call(ctx, ipc.MethodStatus, nil, &full); err == nil {
			status.ServiceMode = full.Daemon.ServiceMode
			status.DBPath = full.Daemon.DBPath
		}
	} else if !ipc.IsUnavailable(err) {
		return daemonError(err)
	}

	if app.JSON {
		if err := app.PrintJSON(status); err != nil {
			return Fail(ExitError, err)
		}
		// Scripts rely on the exit code, so keep it consistent with the human
		// form: 2 means the daemon is unreachable (contracts/cli.md).
		if !status.Running {
			return Failf(ExitDaemonUnreached, "daemon is not running")
		}
		return nil
	}

	if !status.Running {
		app.Printf("Intenter daemon: not running\n")
		app.Printf("  endpoint %s\n", status.Endpoint)
		app.Printf("  database %s\n", status.DBPath)
		app.Printf("\nStart it with `intenter daemon start`.\n")
		return Failf(ExitDaemonUnreached, "daemon is not running")
	}

	app.Printf("Intenter daemon: running\n")
	app.Printf("  pid      %d\n", status.PID)
	app.Printf("  version  %s\n", status.Version)
	app.Printf("  uptime   %s\n", (time.Duration(status.UptimeS) * time.Second).String())
	app.Printf("  endpoint %s\n", status.Endpoint)
	app.Printf("  database %s\n", status.DBPath)
	app.Printf("  mode     %s\n", status.ServiceMode)
	return nil
}

// installService registers the daemon with the platform's per-user service
// manager (§9.4).
//
// A machine with no such manager is a supported configuration, not a failure:
// the daemon runs unmanaged and the hook's lazy start covers it (FR-022).
func installService(ctx context.Context, app *App) error {
	ctx = orBackground(ctx)

	manager := app.ServiceManager()
	if !manager.Available(ctx) {
		app.Printf("No per-user service manager is available; the daemon will run unmanaged.\n")
		app.Printf("It starts on demand from the agent hook, or with `intenter daemon start`.\n")
		return recordServiceMode(ctx, app, platform.ModeUnmanaged)
	}

	executable, err := app.Platform.SelfExecutablePath()
	if err != nil {
		return Fail(ExitError, err)
	}
	if err := platform.EnsureDirs(app.Platform); err != nil {
		return Fail(ExitError, err)
	}
	// A registration left by the product's old name is removed first, so an
	// upgrade never leaves two daemons competing for the same job
	// (contracts/identity-and-rename.md).
	if err := platform.RemoveLegacyService(ctx, app.Platform); err != nil {
		app.Warnf("warning: could not remove the previous installation's service: %v\n", err)
	}
	if err := manager.Install(ctx, executable); err != nil {
		return Fail(ExitError, err)
	}

	app.Printf("Intenter daemon service registered (%s).\n", manager.Name())
	return recordServiceMode(ctx, app, platform.ModeManaged)
}

// uninstallService removes the registration, leaving the database and
// configuration alone.
func uninstallService(ctx context.Context, app *App) error {
	ctx = orBackground(ctx)

	manager := app.ServiceManager()
	if err := manager.Uninstall(ctx); err != nil {
		return Fail(ExitError, err)
	}

	app.Printf("Intenter daemon service unregistered.\n")
	return recordServiceMode(ctx, app, platform.ModeUnmanaged)
}

// recordServiceMode stores how the daemon is kept running, so `status` and
// `doctor` can report it without asking the platform again (§12.4).
//
// The meta table is written directly rather than through the daemon: setup has
// to record installation facts before a daemon exists, and the database is
// WAL-mode so a concurrent writer is safe. A failure here is not worth failing
// the registration over — the service is already installed.
func recordServiceMode(ctx context.Context, app *App, mode string) error {
	if err := writeMeta(ctx, app, map[string]string{storage.MetaServiceMode: mode}); err != nil {
		app.Warnf("warning: could not record the service mode: %v\n", err)
	}
	return nil
}

// writeMeta stores installation facts in the database.
func writeMeta(ctx context.Context, app *App, values map[string]string) error {
	// A prior installation under the product's old name is migrated into
	// place before the database opens (contracts/identity-and-rename.md); a
	// failure here is not worth failing this call over, matching the comment
	// above about the service mode itself.
	if _, err := platform.MigrateLegacyDataDir(app.Platform, app.Logger); err != nil {
		app.Warnf("warning: could not migrate the previous installation's data directory: %v\n", err)
	}

	db, err := storage.OpenAndMigrate(ctx, platform.DatabasePath(app.Platform))
	if err != nil {
		return err
	}
	store := storage.NewStore(db)
	defer store.Close()

	return store.Meta.SetAll(ctx, values)
}

// daemonStdioLog is where a detached daemon's stdout and stderr go; the daemon
// itself logs through slog to daemon.log.
func daemonStdioLog(app *App) string {
	return filepath.Join(platform.LogDir(app.Platform), "daemon-stdio.log")
}

func orBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
