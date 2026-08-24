package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Vadym903/Intenter/internal/config"
	"github.com/Vadym903/Intenter/internal/ipc"
	"github.com/Vadym903/Intenter/internal/platform"
	"github.com/Vadym903/Intenter/internal/updater"
	"github.com/Vadym903/Intenter/internal/version"
)

// daemonPingBudget bounds the one optional daemon contact the start-up path
// makes. It is short because nothing on that path may wait: the answer only
// decides whether to spawn a background check, and guessing wrong costs one
// unnecessary process.
const daemonPingBudget = 100 * time.Millisecond

// updateOptions are the flags of the `update` family.
type updateOptions struct {
	check          bool
	plan           bool
	yes            bool
	target         string
	skip           string
	unskip         bool
	channel        string
	allowDowngrade bool

	// startup is the hidden entry point the shell start-up block calls.
	startup bool
	// backgroundCheck is the hidden detached checker.
	backgroundCheck bool
}

// newUpdateCommand builds `intenter update` (003 contracts/update-cli.md).
func newUpdateCommand(app *App) *cobra.Command {
	options := &updateOptions{}

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Check for a new release and install it",
		Long: "Shows what is available and updates Intenter to it: the release's signed\n" +
			"checksums are verified with the key built into this binary, the download is\n" +
			"verified against them, the executable is replaced atomically and the daemon\n" +
			"is restarted into the new version. Nothing changes on a failed check.\n" +
			"Installations owned by Homebrew or winget are handed to that tool instead.",
		Example: "  intenter update --check\n" +
			"  intenter update\n" +
			"  intenter update --version 0.2.0 --yes\n" +
			"  intenter update --skip 0.2.0",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpdate(orBackground(cmd.Context()), app, options)
		},
	}

	flags := cmd.Flags()
	flags.BoolVar(&options.check, "check", false, "check now and print the status")
	flags.BoolVar(&options.plan, "plan", false, "print what an update would do, and change nothing")
	flags.BoolVarP(&options.yes, "yes", "y", false, "do not ask for confirmation")
	flags.StringVar(&options.target, "version", "", "install a specific version instead of the newest")
	flags.StringVar(&options.skip, "skip", "", "never offer this version again")
	flags.BoolVar(&options.unskip, "unskip", false, "undo a --skip")
	flags.StringVar(&options.channel, "channel", "", "override the release channel (stable or prerelease)")
	flags.BoolVar(&options.allowDowngrade, "allow-downgrade", false,
		"permit --version to install an older release")

	flags.BoolVar(&options.startup, "startup", false, "terminal start-up check (internal)")
	flags.BoolVar(&options.backgroundCheck, "background-check", false, "detached release check (internal)")
	// Hidden rather than absent: they are a documented part of the contract and
	// have to be callable, but they are not commands anyone types.
	_ = flags.MarkHidden("startup")
	_ = flags.MarkHidden("background-check")

	cmd.AddCommand(newUpdateStartupCommand(app))
	return cmd
}

// newUpdateStartupCommand builds `intenter update startup …`.
//
// It is called "startup", never "hook": in this project a hook is a Claude Code
// hook, and two things called the same name in the same CLI would be a support
// question forever.
func newUpdateStartupCommand(app *App) *cobra.Command {
	var shells []string

	cmd := &cobra.Command{
		Use:   "startup",
		Short: "Manage the terminal start-up update check",
		Long: "The start-up check is a small marked block in your shell's start-up file.\n" +
			"It runs when you open an interactive terminal and shows the update prompt\n" +
			"when there is one to show. It does nothing else.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}

	status := &cobra.Command{
		Use:   "status",
		Short: "Show where the start-up check is installed",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStartupStatus(app, shells)
		},
	}
	enable := &cobra.Command{
		Use:   "enable",
		Short: "Add the start-up check to your shell start-up files",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStartupEnable(app, shells)
		},
	}
	disable := &cobra.Command{
		Use:   "disable",
		Short: "Remove the start-up check from your shell start-up files",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStartupDisable(app)
		},
	}

	for _, sub := range []*cobra.Command{status, enable} {
		sub.Flags().StringSliceVar(&shells, "shell", nil,
			"limit to these shells (zsh, bash, fish, powershell)")
	}
	cmd.AddCommand(status, enable, disable)
	return cmd
}

// startupCheck builds the block writer for this installation.
func (a *App) startupCheck() *updater.StartupCheck {
	executable, _ := a.Platform.SelfExecutablePath()
	return &updater.StartupCheck{
		Home:       a.Platform.HomeDir(),
		Executable: executable,
		Store:      updater.NewStore(a.Platform.DataDir()),
		LookPath:   exec.LookPath,
		Policy:     updater.ExecutionPolicyBlocked,
		Now:        updater.Now,
	}
}

func runStartupStatus(app *App, shells []string) error {
	status := app.startupCheck().Status(shells)
	if app.JSON {
		if err := app.PrintJSON(status); err != nil {
			return Fail(ExitError, err)
		}
		return nil
	}
	printStartupStatus(app, status)
	return nil
}

func runStartupEnable(app *App, shells []string) error {
	status, err := app.startupCheck().Install(shells)
	if err != nil {
		return Fail(ExitError, err)
	}
	if app.JSON {
		if err := app.PrintJSON(status); err != nil {
			return Fail(ExitError, err)
		}
		return nil
	}

	app.Printf("The start-up update check is installed in:\n")
	for _, file := range status.Installed {
		app.Printf("  %s\n", file)
	}
	app.Printf("\nIt takes effect in terminals opened from now on.\n")
	if status.BlockedByPolicy {
		app.Warnf("\n! PowerShell will not run profile scripts under the current execution\n"+
			"  policy, so the prompt cannot appear. Fix: %s\n", status.PolicyFix)
	}
	return nil
}

func runStartupDisable(app *App) error {
	check := app.startupCheck()
	before := check.Status(nil).Installed

	if _, err := check.Remove(); err != nil {
		return Fail(ExitError, err)
	}
	if len(before) == 0 {
		app.Printf("The start-up update check was not installed.\n")
		return nil
	}
	app.Printf("Removed the start-up update check from:\n")
	for _, file := range before {
		app.Printf("  %s\n", file)
	}
	app.Printf("\nEverything else in those files is unchanged. " +
		"`intenter update` still works.\n")
	return nil
}

func printStartupStatus(app *App, status updater.StartupStatus) {
	app.Printf("Start-up update check\n\n")
	if len(status.Installed) == 0 {
		Field(app.Out, "installed", "no")
		Field(app.Out, "would go in", "%s", strings.Join(status.Candidates, ", "))
	} else {
		Field(app.Out, "installed", "%s", strings.Join(status.Installed, ", "))
	}
	Field(app.Out, "shells found", "%s", strings.Join(status.Shells, ", "))

	if status.BlockedByPolicy {
		app.Printf("\n! PowerShell will not run profile scripts under the current execution\n"+
			"  policy, so the prompt cannot appear. Fix: %s\n", status.PolicyFix)
		return
	}
	if len(status.Installed) == 0 {
		app.Printf("\nAdd it with: intenter update startup enable\n")
	}
}

// runUpdate dispatches to the mode the flags selected.
func runUpdate(ctx context.Context, app *App, options *updateOptions) error {
	if err := app.applyUpdateChannel(options.channel); err != nil {
		return err
	}

	switch {
	case options.startup:
		return runStartupCheck(ctx, app)
	case options.backgroundCheck:
		return runBackgroundCheck(ctx, app)
	case options.skip != "" || options.unskip:
		return runSkip(app, options)
	case options.check:
		return runCheckNow(ctx, app)
	default:
		return runUpdateNow(ctx, app, options)
	}
}

// applyUpdateChannel lets --channel override the configuration for one command.
func (a *App) applyUpdateChannel(channel string) error {
	channel = strings.ToLower(strings.TrimSpace(channel))
	switch channel {
	case "":
		return nil
	case config.ChannelStable, config.ChannelPrerelease:
		a.Config.Updates.Channel = channel
		return nil
	default:
		return Failf(ExitError, "--channel must be %q or %q", config.ChannelStable, config.ChannelPrerelease)
	}
}

// updateEnv is everything the update commands need from the app, built once so
// each mode reads the same view of the installation.
type updateEnv struct {
	store   *updater.Store
	install updater.Install
	sources updater.Sources
	updates config.UpdatesConfig
}

func (a *App) updateEnv() updateEnv {
	stable, _ := a.Platform.SelfExecutablePath()
	return updateEnv{
		store:   updater.NewStore(a.Platform.DataDir()),
		install: updater.DetectInstall(stable, a.Platform.HomeDir(), a.Platform.DataDir()),
		sources: updater.SourcesFromEnv(),
		updates: a.Config.Updates,
	}
}

func (e updateEnv) checker() *updater.Checker {
	return &updater.Checker{
		Store:          e.store,
		Updates:        e.updates,
		Sources:        e.sources,
		Installed:      version.Version,
		InstallChannel: e.install.Channel,
		Now:            updater.Now,
	}
}

// runStartupCheck is the hidden path the shell start-up block calls.
//
// Its whole job is to be invisible: it must add no perceptible delay, never
// touch the network, never open the database, and print nothing at all unless
// there is a decision for the user to make (003 contracts §4).
func runStartupCheck(ctx context.Context, app *App) error {
	env := app.updateEnv()

	gate := updater.PromptGate{
		Updates: env.updates,
		In:      os.Stdin,
		Out:     os.Stdout,
		JSON:    app.JSON,
	}
	if gate.Silenced() != "" {
		return nil
	}

	now := updater.Now()
	state := env.store.LoadOrZero()

	if state.CheckDue(now, env.updates) {
		spawnBackgroundCheck(ctx, app)
	}
	if !state.Eligible(now, version.Version, env.updates) || state.LatestKnown == nil {
		return nil
	}

	// Whoever takes this lock is the one terminal that prompts; the rest of a
	// dozen tabs opening at once start silently.
	lock, err := updater.AcquirePromptLock(env.store.PromptLockPath())
	if err != nil {
		return nil
	}
	defer lock.Release()

	prompter := &updater.Prompter{
		Store:          env.store,
		Updates:        env.updates,
		Installed:      version.Version,
		InstallChannel: env.install.Channel,
		In:             os.Stdin,
		Out:            app.Out,
		Now:            updater.Now,
	}
	choice, err := prompter.Ask(*state.LatestKnown)
	if err != nil {
		app.Warnf("intenter: could not record your answer: %v\n", err)
	}
	if choice != updater.ChoiceUpdate {
		return nil
	}

	// A failed update must never stop a shell from starting, so the error is
	// printed and swallowed. The user is looking at the reason either way.
	if err := applyUpdate(ctx, app, env, state.LatestKnown.Version, applyOptions{lockHeld: true}); err != nil {
		app.Warnf("intenter: %v\n", err)
	}
	return nil
}

// spawnBackgroundCheck starts a detached check when nothing else will do it.
//
// The daemon is the normal checker; this covers the machine where it is not
// running — unmanaged mode, a stopped service, a fresh install — without the
// terminal ever waiting for the network.
func spawnBackgroundCheck(ctx context.Context, app *App) {
	if daemonAnswers(ctx, app) {
		return
	}
	executable, err := app.Platform.SelfExecutablePath()
	if err != nil {
		return
	}
	logPath := filepath.Join(updater.NewStore(app.Platform.DataDir()).Dir(), "check.log")
	_, _ = platform.SpawnDetached(executable, []string{"update", "--background-check"}, logPath)
}

// daemonAnswers reports whether a daemon is reachable within the ping budget.
// A missing daemon.json answers the question without any I/O at all, which is
// the common case on a machine where the daemon is not running.
func daemonAnswers(ctx context.Context, app *App) bool {
	if _, err := os.Stat(platform.DaemonInfoPath(app.Platform)); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, daemonPingBudget)
	defer cancel()

	client := app.Client().WithTimeouts(daemonPingBudget, daemonPingBudget)
	_, err := client.Ping(ctx)
	return err == nil
}

// runBackgroundCheck performs one check and exits. It is spawned detached, so
// it prints nothing anybody reads and reports failure only through the state
// file it writes.
func runBackgroundCheck(ctx context.Context, app *App) error {
	env := app.updateEnv()
	if !env.updates.Check {
		return nil
	}
	if _, err := env.checker().Check(ctx); err != nil {
		app.Warnf("update check failed: %v\n", err)
	}
	return nil
}

// runCheckNow checks immediately and prints the status.
//
// It ignores the interval and the back-off because the user asked; it also
// works when checking is switched off, for the same reason — a switch that
// governs unprompted behavior should not disable an explicit request.
func runCheckNow(ctx context.Context, app *App) error {
	env := app.updateEnv()

	state, checkErr := env.checker().Check(ctx)
	status := buildStatus(env, state)

	if app.JSON {
		if err := app.PrintJSON(status); err != nil {
			return Fail(ExitError, err)
		}
	} else {
		printUpdateStatus(app, status)
	}

	if checkErr != nil {
		return Failf(updater.ExitDownload, "check failed: %v", checkErr)
	}
	return nil
}

// runSkip sets or clears the skipped version.
func runSkip(app *App, options *updateOptions) error {
	env := app.updateEnv()

	if options.unskip {
		if _, err := env.store.Mutate(func(s *updater.UpdateState) { s.SkippedVersion = "" }); err != nil {
			return Fail(ExitError, err)
		}
		app.Printf("No version is skipped.\n")
		return nil
	}

	target, err := updater.ParseVersion(options.skip)
	if err != nil {
		return Fail(ExitError, err)
	}
	if _, err := env.store.Mutate(func(s *updater.UpdateState) { s.SkippedVersion = target }); err != nil {
		return Fail(ExitError, err)
	}
	app.Printf("Intenter %s will not be offered again. Newer releases still will.\n", target)
	return nil
}

// applyOptions carry the parts of an update decision the caller already made.
type applyOptions struct {
	lockHeld bool
	skipPlan bool
}

// runUpdateNow is the interactive `intenter update`.
func runUpdateNow(ctx context.Context, app *App, options *updateOptions) error {
	env := app.updateEnv()

	applier := app.applier(env)
	plan, err := applier.BuildPlan(options.target)
	if err != nil {
		return exitFor(err)
	}
	applier.PrintPlan(plan)

	if options.plan {
		app.Printf("\nNothing was changed.\n")
		return nil
	}

	confirmed, err := confirmPlan(app, plan, options)
	if err != nil {
		return err
	}
	if !confirmed {
		return nil
	}
	return applyPlan(ctx, app, applier, plan)
}

// confirmPlan asks before anything is installed, unless the caller said not to.
// It reports whether the update may proceed; declining is not an error.
func confirmPlan(app *App, plan updater.Plan, options *updateOptions) (bool, error) {
	if plan.Downgrade && !options.allowDowngrade {
		if options.yes {
			return false, Failf(updater.ExitUsage,
				"%s is older than the installed %s; add --allow-downgrade if you mean it",
				plan.Target, plan.Installed)
		}
		app.Printf("\n%s is OLDER than the installed %s.\n", plan.Target, plan.Installed)
	}

	// An installation this tool cannot place is never updated unattended: it
	// may be a copy something else manages, and replacing it would be someone
	// else's outage.
	if plan.Channel == updater.ChannelUnknown && options.yes {
		return false, Failf(updater.ExitUsage,
			"cannot tell how Intenter was installed (%s); run without --yes to confirm", plan.Path)
	}
	if options.yes {
		return true, nil
	}

	if !updater.Interactive(os.Stdin, os.Stdout) {
		return false, Failf(updater.ExitUsage, "nothing to read an answer from; re-run with --yes")
	}
	app.Printf("\nProceed? [y/N]: ")
	if !readsYes(os.Stdin) {
		app.Printf("Nothing was changed.\n")
		return false, nil
	}
	return true, nil
}

// readsYes reads one line and reports whether it agreed.
func readsYes(in *os.File) bool {
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

// applyUpdate builds a plan for one version and runs it.
func applyUpdate(ctx context.Context, app *App, env updateEnv, target string, options applyOptions) error {
	applier := app.applier(env)
	applier.LockHeld = options.lockHeld

	plan, err := applier.BuildPlan(target)
	if err != nil {
		return exitFor(err)
	}
	if !options.skipPlan {
		applier.PrintPlan(plan)
	}
	return applyPlan(ctx, app, applier, plan)
}

// applyPlan runs an update and then checks the half of the installation only
// the CLI layer may know about: whether Claude's hooks still point at the
// binary. The core updater deliberately cannot ask that question.
func applyPlan(ctx context.Context, app *App, applier *updater.Applier, plan updater.Plan) error {
	started := time.Now()
	if err := applier.Apply(ctx, plan); err != nil {
		return exitFor(err)
	}
	app.Printf("Updated in %.0fs\n", time.Since(started).Seconds())

	if check := checkInstalledPaths(app, readMeta(ctx, app)); !check.OK {
		app.Warnf("\n! %s\n  → %s\n", check.Detail, check.Fix)
	}
	return nil
}

// applier builds the updater with the CLI's own daemon restart wired in.
func (a *App) applier(env updateEnv) *updater.Applier {
	return &updater.Applier{
		Store:     env.store,
		Updates:   env.updates,
		Sources:   env.sources,
		Install:   env.install,
		Installed: version.Version,
		DataDir:   a.Platform.DataDir(),
		Out:       a.Out,
		Services:  a.ServiceManager(),
		Restart:   func(ctx context.Context) error { return restartDaemon(ctx, a) },
		Run:       runInheritingStdio,
		Now:       updater.Now,
	}
}

// runInheritingStdio runs a package manager so the user sees its output as it
// happens — a `brew upgrade` can take a minute, and silence would read as a
// hang.
func runInheritingStdio(ctx context.Context, name string, args ...string) error {
	command := exec.CommandContext(ctx, name, args...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	return command.Run()
}

// exitFor maps an updater failure onto the process exit code it asks for.
func exitFor(err error) error {
	if err == nil {
		return nil
	}
	return Fail(updater.ExitCodeFor(err), err)
}

// UpdateStatus is the `update --check --json` shape.
type UpdateStatus struct {
	Installed      string                   `json:"installed"`
	Latest         string                   `json:"latest,omitempty"`
	Channel        string                   `json:"channel"`
	InstallChannel string                   `json:"install_channel"`
	NotesURL       string                   `json:"notes_url,omitempty"`
	UpdateDue      bool                     `json:"update_available"`
	PromptDue      bool                     `json:"prompt_due"`
	State          updater.UpdateState      `json:"state"`
	History        []updater.UpdateDecision `json:"history"`
	StartupHook    updater.StartupHookState `json:"startup_hook"`
}

// buildStatus assembles what `--check` reports.
func buildStatus(env updateEnv, state updater.UpdateState) UpdateStatus {
	now := updater.Now()
	status := UpdateStatus{
		Installed:      version.Version,
		Channel:        env.updates.Channel,
		InstallChannel: env.install.Channel,
		State:          state,
		History:        env.store.Tail(20),
		StartupHook:    state.StartupHook,
		PromptDue:      state.Eligible(now, version.Version, env.updates),
	}
	if state.LatestKnown != nil {
		status.Latest = state.LatestKnown.Version
		status.NotesURL = state.LatestKnown.NotesURL
		status.UpdateDue = updater.Newer(state.LatestKnown.Version, version.Version)
	}
	return status
}

// printUpdateStatus renders the status the way `doctor` and `status` render
// theirs: one labeled fact per line.
func printUpdateStatus(app *App, status UpdateStatus) {
	app.Printf("Intenter update\n\n")
	Field(app.Out, "installed", "%s", status.Installed)
	Field(app.Out, "latest", "%s", Dash(status.Latest))
	Field(app.Out, "channel", "%s", status.Channel)
	Field(app.Out, "installed via", "%s", status.InstallChannel)

	state := status.State
	if state.SkippedVersion != "" {
		Field(app.Out, "skipped", "%s", state.SkippedVersion)
	}
	if state.DeferredUntil != nil {
		Field(app.Out, "not before", "%s", state.DeferredUntil.Local().Format(time.RFC3339))
	}
	Field(app.Out, "last check", "%s", lastCheckSummary(state))
	if state.NextCheckAfter != nil {
		Field(app.Out, "next check", "%s", state.NextCheckAfter.Local().Format(time.RFC3339))
	}
	Field(app.Out, "start-up check", "%s", startupHookSummary(status.StartupHook))

	app.Printf("\n")
	switch {
	case status.Latest == "":
		app.Printf("No release information yet.\n")
	case status.UpdateDue:
		app.Printf("Intenter %s is available. Update with: intenter update\n", status.Latest)
		if status.NotesURL != "" {
			app.Printf("Release notes: %s\n", status.NotesURL)
		}
	default:
		app.Printf("You are on the newest release.\n")
	}
}

func lastCheckSummary(state updater.UpdateState) string {
	if state.LastCheckAt == nil {
		return "never"
	}
	when := state.LastCheckAt.Local().Format(time.RFC3339)
	if state.LastCheckOK {
		return when + " (ok)"
	}
	reason := state.LastCheckError
	if reason == "" {
		reason = "failed"
	}
	return fmt.Sprintf("%s (%s, %d in a row)", when, reason, state.CheckFailures)
}

func startupHookSummary(hook updater.StartupHookState) string {
	switch {
	case hook.BlockedByPolicy:
		return "blocked by the PowerShell execution policy"
	case len(hook.InstalledFiles) == 0:
		return "not installed (run `intenter update startup enable`)"
	default:
		return strings.Join(hook.InstalledFiles, ", ")
	}
}

// restartDaemon stops the running daemon and starts it again, whichever way it
// is kept running.
func restartDaemon(ctx context.Context, app *App) error {
	client := app.Client()
	if err := client.Call(ctx, ipc.MethodShutdown, nil, nil); err != nil && !ipc.IsUnavailable(err) {
		return err
	}
	waitForDaemonToStop(ctx, app)

	return startDaemon(ctx, app)
}

// waitForDaemonToStop gives the old process time to release the socket, so the
// new one does not fail to bind it.
func waitForDaemonToStop(ctx context.Context, app *App) {
	deadline := time.Now().Add(5 * time.Second)
	client := app.Client().WithTimeouts(daemonPingBudget, daemonPingBudget)
	for time.Now().Before(deadline) {
		if _, err := client.Ping(ctx); err != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}
