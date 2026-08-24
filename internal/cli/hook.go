package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/Vadym903/Intenter/internal/adapter"
	"github.com/Vadym903/Intenter/internal/adapter/claude"
	"github.com/Vadym903/Intenter/internal/logging"
	"github.com/Vadym903/Intenter/internal/platform"
)

// newHookCommand builds `intenter hook <agent>`, the entry point Claude Code
// invokes for every gated tool call.
//
// It always exits 0. A hook that failed — bad input, no daemon, a bug — must
// leave the agent's own permission flow exactly as it was, and an error exit
// would instead surface as a broken session (INVARIANT I-12).
func newHookCommand(app *App) *cobra.Command {
	hook := &cobra.Command{
		Use:   "hook",
		Short: "Handle an agent hook invocation (called by the agent, not by you)",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
	hook.AddCommand(newClaudeHookCommand(app))
	return hook
}

func newClaudeHookCommand(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "claude",
		Short: "Handle a Claude Code hook event read from stdin",
		Args:  cobra.NoArgs,
		// The hook speaks JSON on stdout; usage text there would corrupt it.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// stdout is the protocol channel, so diagnostics go to the log file.
			logger := logging.Discard()
			if fileLogger, closer, err := logging.FileLogger(
				platform.LogDir(app.Platform), logging.HookLogFile, app.Config.Log.Level,
			); err == nil {
				logger = fileLogger
				defer closer.Close()
			}

			adapterInstance := claude.New(app.Platform, app.Config, logger)
			if app.Config.Daemon.LazyStart {
				adapterInstance = adapterInstance.WithLazyStart(claude.LazyStart(app.Platform, logger))
			}

			err := adapterInstance.Run(cmd.Context(), adapter.IO{
				Stdin:  cmd.InOrStdin(),
				Stdout: app.Out,
				Stderr: app.Err,
				Env:    os.Getenv,
			})
			if err != nil {
				// Recorded for the operator; never surfaced to the agent.
				logger.Warn("hook finished with an error", "error", err)
			}
			return nil
		},
	}
}
