package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/Vadym903/Intenter/internal/config"
	"github.com/Vadym903/Intenter/internal/ipc"
	"github.com/Vadym903/Intenter/internal/logging"
	"github.com/Vadym903/Intenter/internal/platform"
	"github.com/Vadym903/Intenter/internal/version"
)

// Exit codes (contracts/cli.md).
const (
	ExitOK              = 0
	ExitError           = 1
	ExitDaemonUnreached = 2
	ExitSetupFailed     = 3
)

// ExitError carries the process exit code for a failed command.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

// Fail wraps an error with an explicit exit code.
func Fail(code int, err error) error { return &exitError{code: code, err: err} }

// Failf wraps a formatted message with an explicit exit code.
func Failf(code int, format string, args ...any) error {
	return &exitError{code: code, err: fmt.Errorf(format, args...)}
}

// App is the shared state of every command: the resolved platform, config and
// output streams.
type App struct {
	Platform platform.Platform
	Config   config.Config
	Logger   *slog.Logger
	Out      io.Writer
	Err      io.Writer

	JSON    bool
	Verbose bool

	// Services registers the daemon with the platform. It is injectable so
	// tests can exercise the command wiring without registering a background
	// service on the machine running them.
	Services platform.ServiceManager

	dataDir    string
	configPath string

	// initErr is set when start-up failed for a command that must answer
	// anyway. Only `menu` uses it; every other command treats the same failure
	// as fatal, which is right for a terminal.
	initErr error
}

// ServiceManager returns the injected manager, or the platform's real one.
func (a *App) ServiceManager() platform.ServiceManager {
	if a.Services != nil {
		return a.Services
	}
	return platform.NewServiceManager(a.Platform)
}

// Client builds an IPC client for the discovered daemon endpoint (§10.1).
func (a *App) Client() *ipc.Client {
	endpoint := ipc.DiscoverEndpoint(os.Getenv(platform.EnvEndpoint), a.Platform.DataDir(), a.Platform.IPCEndpoint())
	return ipc.NewClient(endpoint)
}

// Printf writes human-readable output.
func (a *App) Printf(format string, args ...any) {
	fmt.Fprintf(a.Out, format, args...)
}

// Warnf writes a warning to stderr.
func (a *App) Warnf(format string, args ...any) {
	fmt.Fprintf(a.Err, format, args...)
}

// PrintJSON writes a machine-readable result.
func (a *App) PrintJSON(value any) error {
	encoder := json.NewEncoder(a.Out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

// daemonError maps an IPC failure onto the documented exit codes: an
// unreachable daemon is exit 2, anything else exit 1 (contracts/cli.md).
func daemonError(err error) error {
	if err == nil {
		return nil
	}
	if ipc.IsUnavailable(err) {
		return Fail(ExitDaemonUnreached, fmt.Errorf("%w — start it with `intenter daemon start`", err))
	}
	return Fail(ExitError, err)
}

// NewRootCommand builds the command tree. The App is populated in
// PersistentPreRunE, after the global flags are parsed.
func NewRootCommand(out, errOut io.Writer) (*cobra.Command, *App) {
	app := &App{Out: out, Err: errOut, Logger: logging.Discard()}

	root := &cobra.Command{
		Use:   "intenter",
		Short: "Semantic runtime permission layer for AI coding agents",
		Long: "Intenter decides whether an AI coding agent may run a shell command,\n" +
			"based on what the command actually does rather than how it is spelled.",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return app.init()
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	flags := root.PersistentFlags()
	flags.BoolVar(&app.JSON, "json", false, "machine-readable JSON output")
	flags.BoolVarP(&app.Verbose, "verbose", "v", false, "verbose output")
	flags.StringVar(&app.dataDir, "data-dir", "", "override the Intenter data directory")
	flags.StringVar(&app.configPath, "config", "", "override the configuration file path")

	root.AddCommand(
		newVersionCommand(app),
		newDaemonCommand(app),
		newHookCommand(app),
		newMenuCommand(app),
		newApprovalsCommand(app),
		newApprovalCommand(app),
		newApproveCommand(app),
		newHistoryCommand(app),
		newSummaryCommand(app),
		newSetupCommand(app),
		newUninstallCommand(app),
		newStatusCommand(app),
		newDoctorCommand(app),
		newUpdateCommand(app),
	)
	return root, app
}

// init resolves the platform and configuration once per invocation.
func (a *App) init() error {
	p, err := platform.NewWithOverrides(platform.Overrides{DataDir: a.dataDir})
	if err != nil {
		return Fail(ExitError, err)
	}
	a.Platform = p

	configPath := a.configPath
	if configPath == "" {
		configPath = platform.ConfigFilePath(p)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return Fail(ExitError, err)
	}
	a.Config = cfg
	for _, warning := range cfg.Warnings {
		a.Warnf("warning: %s\n", warning)
	}

	level := cfg.Log.Level
	if a.Verbose {
		level = "debug"
	}
	a.Logger = logging.StderrLogger(level)
	return nil
}

// Execute runs the CLI and returns the process exit code.
func Execute(args []string, out, errOut io.Writer) int {
	root, app := NewRootCommand(out, errOut)
	root.SetArgs(args)
	root.SetOut(out)
	root.SetErr(errOut)

	if err := root.Execute(); err != nil {
		var exit *exitError
		if errors.As(err, &exit) {
			fmt.Fprintf(app.Err, "intenter: %v\n", exit.err)
			return exit.code
		}
		fmt.Fprintf(app.Err, "intenter: %v\n", err)
		return ExitError
	}
	return ExitOK
}

// newVersionCommand prints the build and compatibility versions.
func newVersionCommand(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version, engine, protocol and schema versions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			info := version.Current()
			if app.JSON {
				return app.PrintJSON(info)
			}
			app.Printf("intenter %s\n", info.Version)
			app.Printf("  engine   v%d\n", info.EngineVersion)
			app.Printf("  protocol v%d\n", info.ProtocolVersion)
			app.Printf("  schema   v%d\n", info.SchemaVersion)
			app.Printf("  built    %s (%s)\n", info.GoVersion, info.Platform)
			return nil
		},
	}
}
