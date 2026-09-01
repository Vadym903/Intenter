package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/Vadym903/Intenter/internal/adapter/claude"
)

// newSetupCommand builds `intenter setup claude` (§12.2).
func newSetupCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Install the Intenter integration for an agent",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newSetupClaudeCommand(app))
	return cmd
}

func newSetupClaudeCommand(app *App) *cobra.Command {
	var options claude.SetupOptions

	cmd := &cobra.Command{
		Use:   "claude",
		Short: "Detect Claude Code, install the hooks and start the daemon",
		Long: "Installs Intenter's permission hooks into Claude Code, initializes the\n" +
			"local database, registers and starts the daemon, adds the terminal update\n" +
			"check, and verifies the whole path end to end with a dry-run evaluation.\n" +
			"Your Claude settings file is backed up first, hooks you already have are\n" +
			"kept, and entries left by a pre-rename development install are replaced.\n" +
			"Claude Code reads its hooks at start-up, so restart running sessions after.",
		Example: "  intenter setup claude\n" +
			"  intenter setup claude --dry-run\n" +
			"  intenter setup claude --no-startup-check",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSetup(cmd.Context(), app, options)
		},
	}

	flags := cmd.Flags()
	flags.BoolVar(&options.DryRun, "dry-run", false, "print the planned changes without making any")
	flags.StringVar(&options.SettingsPath, "settings", "", "override the Claude settings file")
	flags.BoolVar(&options.NoService, "no-service", false,
		"skip service registration and start the daemon manually")
	flags.BoolVar(&options.NoStartupCheck, "no-startup-check", false,
		"do not add the update check to your shell start-up files")
	return cmd
}

func runSetup(ctx context.Context, app *App, options claude.SetupOptions) error {
	ctx = orBackground(ctx)

	// The menu is defined next to the commands it names, so the skill file and
	// the CLI cannot disagree about what `/intenter allowed` runs.
	options.SkillActions = SkillActions()

	setup := claude.NewSetup(app.Platform, app.Config, app.ServiceManager(), options)
	result, runErr := setup.Run(ctx)

	if options.DryRun {
		app.Printf("Intenter setup (dry run)\n\n")
	} else {
		app.Printf("Intenter setup\n\n")
	}
	printSteps(app, result.Steps)

	if runErr != nil {
		// Exit 3 marks a setup step failing, so a script can tell it apart from
		// an ordinary error (contracts/cli.md).
		return Fail(ExitSetupFailed, runErr)
	}

	if result.RuleInventory.Total() > 0 {
		app.Printf("\nClaude already permits %d shell command(s) of its own. Intenter will\n",
			result.RuleInventory.Total())
		app.Printf("validate each one against what the command actually does, the first time\n")
		app.Printf("it runs. No approval was created now.\n")
	}

	if options.DryRun {
		app.Printf("\nNothing was changed. Run without --dry-run to apply.\n")
		return nil
	}
	app.Printf("\nIntenter is ready. Restart any running Claude Code sessions to activate\n")
	app.Printf("the hooks — Claude reads them once, when a session starts.\n")
	if result.SkillInstall.Path != "" {
		app.Printf("Then type `/intenter` in a session to see what this project is allowed\n")
		app.Printf("to run without asking, and to take a permission back.\n")
	}
	return nil
}

// newUninstallCommand builds `intenter uninstall claude` (§12.3).
func newUninstallCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the Intenter integration for an agent",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newUninstallClaudeCommand(app))
	return cmd
}

func newUninstallClaudeCommand(app *App) *cobra.Command {
	var options claude.UninstallOptions

	cmd := &cobra.Command{
		Use:   "claude",
		Short: "Remove Intenter's hooks and stop the daemon",
		Long: "Removes only the hook entries Intenter installed (including any left by a\n" +
			"pre-rename development install), backing up the settings first, then stops\n" +
			"and unregisters the daemon and the terminal update check. Your approvals,\n" +
			"history and configuration are kept unless you pass --purge. Claude Code\n" +
			"keeps working exactly as before Intenter was installed.",
		Example: "  intenter uninstall claude\n" +
			"  intenter uninstall claude --purge",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUninstall(cmd.Context(), app, options)
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&options.SettingsPath, "settings", "", "override the Claude settings file")
	flags.BoolVar(&options.KeepDaemon, "keep-daemon", false, "leave the daemon running and registered")
	flags.BoolVar(&options.Purge, "purge", false,
		"also remove the approvals, history and configuration")
	return cmd
}

func runUninstall(ctx context.Context, app *App, options claude.UninstallOptions) error {
	ctx = orBackground(ctx)

	uninstall := claude.NewUninstall(app.Platform, app.Config, app.ServiceManager(), options)
	result, runErr := uninstall.Run(ctx)

	app.Printf("Intenter uninstall\n\n")
	printSteps(app, result.Steps)

	if runErr != nil {
		return Fail(ExitSetupFailed, runErr)
	}

	app.Printf("\nClaude Code settings that Intenter did not create were left untouched.\n")
	if !options.Purge {
		app.Printf("Your approvals and history are still there if you reinstall.\n")
	}
	return nil
}

// printSteps renders the ✓/✗ report both commands share.
func printSteps(app *App, steps []claude.Step) {
	for _, step := range steps {
		app.Printf("%s\n", step.String())
		if step.Warning != "" {
			app.Printf("  ! %s\n", step.Warning)
		}
	}
}
