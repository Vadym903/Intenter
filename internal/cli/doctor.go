package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/Vadym903/Intenter/internal/adapter/claude"
	"github.com/Vadym903/Intenter/internal/ipc"
	"github.com/Vadym903/Intenter/internal/platform"
	"github.com/Vadym903/Intenter/internal/storage"
	"github.com/Vadym903/Intenter/internal/updater"
	"github.com/Vadym903/Intenter/internal/version"
)

// Check is one diagnostic result (contracts/cli.md).
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
	// Fix is what the user can do about a failure.
	Fix string `json:"fix,omitempty"`
}

// DoctorReport is the `doctor --json` shape.
type DoctorReport struct {
	OK     bool    `json:"ok"`
	Checks []Check `json:"checks"`
}

// newDoctorCommand builds `intenter doctor` (§12.5).
//
// Doctor exists for the moment something is wrong and the user cannot tell
// what, so every failing check says what to do about it.
func newDoctorCommand(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check the installation and suggest fixes",
		Long: "Runs every installation check — the binary on PATH, configuration, database,\n" +
			"daemon and service, endpoint permissions, Claude Code and its hooks, leftovers\n" +
			"of a pre-rename install, settings backups, and whether an update is available\n" +
			"or the terminal update check can appear — and prints a fix for each failure.\n" +
			"Exits non-zero when any check fails, so it can gate a script.",
		Example: "  intenter doctor\n" +
			"  intenter doctor --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			report := runDoctor(orBackground(cmd.Context()), app)

			if app.JSON {
				if err := app.PrintJSON(report); err != nil {
					return Fail(ExitError, err)
				}
			} else {
				printDoctor(app, report)
			}
			if !report.OK {
				return Failf(ExitError, "some checks failed")
			}
			return nil
		},
	}
}

// runDoctor performs every check.
func runDoctor(ctx context.Context, app *App) DoctorReport {
	meta := readMeta(ctx, app)

	checks := []Check{
		checkBinaryPath(app, meta),
		checkConfig(app),
		checkDatabase(ctx, app),
		checkDaemon(ctx, app),
		checkService(ctx, app),
		checkEndpointPermissions(app),
		checkClaude(ctx, app, meta),
		checkHooks(app, meta),
		checkSkill(app, meta),
		checkLegacyLeftovers(ctx, app, meta),
		checkInstalledPaths(app, meta),
		checkBackups(app),
		checkUpdates(app),
		checkStartupCheck(app),
	}
	if runtime.GOOS == "windows" {
		checks = append(checks, checkGitBash(app))
	}

	report := DoctorReport{OK: true, Checks: checks}
	for _, check := range checks {
		if !check.OK {
			report.OK = false
		}
	}
	return report
}

// readMeta loads the facts setup recorded. A missing database is not an error
// here: the checks below report it in their own terms.
func readMeta(ctx context.Context, app *App) map[string]string {
	db, err := storage.OpenReadOnly(platform.DatabasePath(app.Platform))
	if err != nil {
		return map[string]string{}
	}
	store := storage.NewStore(db)
	defer store.Close()

	values, err := store.Meta.All(ctx)
	if err != nil {
		return map[string]string{}
	}
	return values
}

// checkBinaryPath catches the case that silently breaks everything: hooks and
// service entries embed an absolute path, so a moved binary leaves them
// pointing at nothing (§12.1).
func checkBinaryPath(app *App, meta map[string]string) Check {
	current, err := app.Platform.SelfExecutablePath()
	if err != nil {
		return Check{Name: "Binary path", Detail: err.Error(),
			Fix: "reinstall Intenter and run `intenter setup claude`"}
	}

	installed := meta[storage.MetaInstalledBinaryPath]
	if installed == "" {
		return Check{Name: "Binary path", OK: true, Detail: current + " (no installation recorded yet)"}
	}
	if !samePathValue(installed, current) {
		return Check{
			Name:   "Binary path",
			Detail: fmt.Sprintf("the hooks point at %s but this binary is %s", installed, current),
			Fix:    "run `intenter setup claude` to point the hooks at the current binary",
		}
	}
	return Check{Name: "Binary path", OK: true, Detail: current}
}

// samePathValue compares two recorded paths.
func samePathValue(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	return a == b || strings.EqualFold(a, b)
}

// checkConfig confirms the configuration file parses.
func checkConfig(app *App) Check {
	path := platform.ConfigFilePath(app.Platform)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return Check{Name: "Configuration", OK: true, Detail: "using the defaults"}
	}
	if len(app.Config.Warnings) > 0 {
		return Check{
			Name:   "Configuration",
			OK:     true,
			Detail: fmt.Sprintf("%s (%s)", path, strings.Join(app.Config.Warnings, "; ")),
		}
	}
	return Check{Name: "Configuration", OK: true, Detail: path}
}

// checkDatabase verifies the store is present, migrated and intact.
func checkDatabase(ctx context.Context, app *App) Check {
	path := platform.DatabasePath(app.Platform)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return Check{Name: "Database", Detail: "not created yet",
			Fix: "run `intenter setup claude`"}
	}

	db, err := storage.OpenReadOnly(path)
	if err != nil {
		return Check{Name: "Database", Detail: err.Error(),
			Fix: "stop the daemon and run `intenter setup claude` again"}
	}
	defer db.Close()

	integrity, err := db.IntegrityCheck(ctx)
	if err != nil {
		return Check{Name: "Database", Detail: err.Error(),
			Fix: "restore from a backup, or remove the database and re-approve"}
	}
	if integrity != "ok" {
		return Check{Name: "Database", Detail: "integrity check reported: " + integrity,
			Fix: "restore from a backup, or remove the database and re-approve"}
	}

	schema, err := storage.SchemaVersion(ctx, db)
	if err != nil {
		return Check{Name: "Database", Detail: err.Error(), Fix: "run `intenter setup claude`"}
	}
	if schema != version.SchemaVersion {
		return Check{
			Name:   "Database",
			Detail: fmt.Sprintf("schema v%d, this build expects v%d", schema, version.SchemaVersion),
			Fix:    "run `intenter setup claude` to migrate",
		}
	}
	return Check{Name: "Database", OK: true, Detail: fmt.Sprintf("%s, schema v%d", path, schema)}
}

// checkDaemon confirms the daemon answers and matches this build.
func checkDaemon(ctx context.Context, app *App) Check {
	client := app.Client()
	ping, err := client.Ping(ctx)
	if err != nil {
		return Check{Name: "Daemon", Detail: "not reachable at " + client.Endpoint(),
			Fix: "run `intenter daemon start`"}
	}
	if ping.Version != version.Version {
		return Check{
			Name:   "Daemon",
			Detail: fmt.Sprintf("running %s, this binary is %s", ping.Version, version.Version),
			Fix:    "run `intenter daemon restart` to pick up the new binary",
		}
	}
	if ping.ProtocolVersion != version.ProtocolVersion {
		return Check{
			Name:   "Daemon",
			Detail: fmt.Sprintf("protocol v%d, this binary speaks v%d", ping.ProtocolVersion, version.ProtocolVersion),
			Fix:    "run `intenter daemon restart`",
		}
	}
	return Check{Name: "Daemon", OK: true,
		Detail: fmt.Sprintf("running (pid %d, %s)", ping.PID, ping.Version)}
}

// checkService reports how the daemon is kept running. Unmanaged is a
// supported mode, not a failure (FR-022).
func checkService(ctx context.Context, app *App) Check {
	manager := app.ServiceManager()
	state, err := manager.Status(ctx)
	if err != nil {
		return Check{Name: "Service", Detail: err.Error(),
			Fix: "run `intenter daemon install`"}
	}

	switch state {
	case platform.ServiceRunning:
		return Check{Name: "Service", OK: true, Detail: manager.Name() + ", running"}
	case platform.ServiceStopped:
		return Check{Name: "Service", OK: true,
			Detail: manager.Name() + ", registered but not running",
			Fix:    "run `intenter daemon start`"}
	case platform.ServiceUnsupported:
		return Check{Name: "Service", OK: true,
			Detail: "unmanaged; the daemon starts on demand from the agent hook"}
	default:
		return Check{Name: "Service", OK: true,
			Detail: "not registered; the daemon starts on demand from the agent hook",
			Fix:    "run `intenter daemon install` to start it with your session"}
	}
}

// checkEndpointPermissions confirms nobody else can reach the daemon socket.
func checkEndpointPermissions(app *App) Check {
	if runtime.GOOS == "windows" {
		return Check{Name: "Endpoint", OK: true, Detail: "named pipe, current user only"}
	}

	runtimeDir := app.Platform.RuntimeDir()
	info, err := os.Stat(runtimeDir)
	if err != nil {
		return Check{Name: "Endpoint", OK: true, Detail: "not created yet"}
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		return Check{
			Name:   "Endpoint",
			Detail: fmt.Sprintf("%s is %#o, which other users can enter", runtimeDir, mode),
			Fix:    fmt.Sprintf("run `chmod 700 %s`", runtimeDir),
		}
	}
	return Check{Name: "Endpoint", OK: true, Detail: runtimeDir + " (owner only)"}
}

// checkClaude confirms the agent is still present.
func checkClaude(ctx context.Context, app *App, meta map[string]string) Check {
	install, err := claude.Detect(ctx, app.Platform, app.Config.Claude.SettingsPath)
	if err != nil {
		return Check{Name: "Claude Code", Detail: err.Error(),
			Fix: "install Claude Code — the CLI, the VS Code extension, or both — then run `intenter setup claude`"}
	}

	// Found without a binary: the VS Code extension does not put `claude` on
	// PATH. Telling that user to install what they already have would send them
	// in a circle, so name what was found instead.
	detail := install.Describe()
	if recorded := meta[storage.MetaClaudeVersion]; recorded != "" && recorded != install.Version {
		detail += fmt.Sprintf(" (was %s at setup)", recorded)
	}
	if len(install.Warnings) > 0 {
		return Check{Name: "Claude Code", OK: true,
			Detail: detail + " — " + strings.Join(install.Warnings, "; ")}
	}
	return Check{Name: "Claude Code", OK: true, Detail: detail}
}

// checkSkill confirms `/intenter` is installed and is what this build writes.
//
// A file left by an older version keeps working but offers the menu that
// version had, so a stale one is reported rather than passed: the gap would
// otherwise be invisible until someone typed an action that no longer exists.
func checkSkill(app *App, meta map[string]string) Check {
	configDir := claudeConfigDir(app, meta)
	path := claude.SkillPath(configDir)

	current, err := claude.SkillUpToDate(configDir, SkillActions())
	if err != nil {
		return Check{Name: "Agent command", Detail: err.Error(),
			Fix: "fix the file, then run `intenter setup claude`"}
	}
	if !current {
		return Check{Name: "Agent command", Detail: path + " is missing or out of date",
			Fix: "run `intenter setup claude` to install `/intenter`"}
	}
	return Check{Name: "Agent command", OK: true, Detail: "/intenter at " + path}
}

// claudeConfigDir is the directory Claude's settings and skills live in,
// preferring what setup recorded over the default location.
func claudeConfigDir(app *App, meta map[string]string) string {
	if path := meta[storage.MetaClaudeSettingsPath]; path != "" {
		return filepath.Dir(path)
	}
	return filepath.Join(app.Platform.HomeDir(), ".claude")
}

// checkHooks confirms the hooks are still installed.
func checkHooks(app *App, meta map[string]string) Check {
	path := meta[storage.MetaClaudeSettingsPath]
	if path == "" {
		path = filepath.Join(app.Platform.HomeDir(), ".claude", "settings.json")
	}

	installed, err := claude.HooksInstalled(path)
	if err != nil {
		return Check{Name: "Hooks", Detail: err.Error(),
			Fix: "fix the settings file, then run `intenter setup claude`"}
	}
	if len(installed) == 0 {
		return Check{Name: "Hooks", Detail: "not installed in " + path,
			Fix: "run `intenter setup claude`"}
	}

	// An installation from before an event was added keeps gating commands
	// correctly, so nothing else reports the gap — only whatever depends on that
	// event quietly never happens.
	missing, err := claude.MissingHookEvents(path)
	if err == nil && len(missing) > 0 {
		return Check{Name: "Hooks",
			Detail: fmt.Sprintf("%s missing in %s", strings.Join(missing, ", "), path),
			Fix:    "run `intenter setup claude` to add the missing hooks"}
	}

	return Check{Name: "Hooks", OK: true,
		Detail: fmt.Sprintf("%s in %s", strings.Join(installed, ", "), path)}
}

// checkInstalledPaths compares the paths written into Claude's hooks and into
// the service registration against the binary that should be running.
//
// Both were written once and are read for months, and an upgrade can move the
// binary underneath them: a package-manager install that was recorded by its
// versioned path stops existing at the next `brew upgrade`. The failure is
// silent — Claude reports a hook error and carries on ungated — so it is worth
// a check of its own rather than being inferred from the recorded path alone.
func checkInstalledPaths(app *App, meta map[string]string) Check {
	const name = "Installed paths"

	stable, err := app.Platform.SelfExecutablePath()
	if err != nil {
		return Check{Name: name, Detail: err.Error(),
			Fix: "reinstall Intenter and run `intenter setup claude`"}
	}

	var stale []string
	settingsPath := meta[storage.MetaClaudeSettingsPath]
	if settingsPath == "" {
		settingsPath = filepath.Join(app.Platform.HomeDir(), ".claude", "settings.json")
	}
	if command, ok := claude.InstalledHookCommand(settingsPath, claude.EventPreToolUse); ok {
		if path := command.ExecutablePath(); path != "" && !samePathValue(path, stable) {
			stale = append(stale, fmt.Sprintf("the Claude hook runs %s", path))
		}
	}
	if registered, ok := platform.RegisteredExecutable(app.ServiceManager()); ok {
		if !samePathValue(registered, stable) {
			stale = append(stale, fmt.Sprintf("the %s service runs %s",
				app.ServiceManager().Name(), registered))
		}
	}

	if len(stale) > 0 {
		return Check{
			Name:   name,
			Detail: fmt.Sprintf("%s, but this binary is %s", strings.Join(stale, "; "), stable),
			Fix:    "run `intenter setup claude` to point them at the current binary",
		}
	}
	return Check{Name: name, OK: true, Detail: stable}
}

// checkBackups confirms a settings backup exists to fall back on.
func checkBackups(app *App) Check {
	dir := filepath.Join(app.Platform.DataDir(), "backups")
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		return Check{Name: "Settings backup", OK: true, Detail: "none yet",
			Fix: "a backup is written every time `intenter setup claude` runs"}
	}
	return Check{Name: "Settings backup", OK: true,
		Detail: fmt.Sprintf("%d in %s", len(entries), dir)}
}

// checkUpdates reports what the last release check found.
//
// It reads the cached state and never touches the network: `doctor` is run when
// something is already wrong, often on a machine that cannot reach anything,
// and a diagnostic that hangs on a network call is worse than no diagnostic.
func checkUpdates(app *App) Check {
	const name = "Updates"

	if !app.Config.Updates.Check {
		return Check{Name: name, OK: true, Detail: "checking is switched off"}
	}

	state := updater.NewStore(app.Platform.DataDir()).LoadOrZero()
	switch {
	case state.LatestKnown == nil && state.LastCheckAt == nil:
		return Check{Name: name, OK: true, Detail: "no check has run yet",
			Fix: "run `intenter update --check`"}
	case state.LatestKnown == nil:
		return Check{Name: name, OK: true,
			Detail: fmt.Sprintf("last check failed: %s", Dash(state.LastCheckError)),
			Fix:    "run `intenter update --check` to see why"}
	case updater.Newer(state.LatestKnown.Version, version.Version):
		return Check{Name: name, OK: true,
			Detail: fmt.Sprintf("%s available (you have %s) — run `intenter update`",
				state.LatestKnown.Version, version.Version)}
	default:
		return Check{Name: name, OK: true, Detail: "up to date (" + version.Version + ")"}
	}
}

// checkStartupCheck reports whether the terminal prompt can appear at all.
//
// This is the check that explains the commonest confusion about the feature:
// everything is installed and working, and no prompt ever appears, because the
// block is missing or PowerShell refuses to run profiles.
func checkStartupCheck(app *App) Check {
	const name = "Start-up check"

	if !app.Config.Updates.Check || !app.Config.Updates.StartupHook {
		return Check{Name: name, OK: true, Detail: "disabled by configuration"}
	}

	status := app.startupCheck().Status(nil)
	if status.BlockedByPolicy {
		return Check{
			Name:   name,
			Detail: "PowerShell will not run profile scripts, so the update prompt cannot appear",
			Fix:    status.PolicyFix,
		}
	}
	if len(status.Installed) == 0 {
		return Check{Name: name, OK: true, Detail: "not installed",
			Fix: "run `intenter update startup enable` to be told about new releases"}
	}
	return Check{Name: name, OK: true, Detail: strings.Join(status.Installed, ", ")}
}

// checkGitBash confirms the shell Claude's Bash tool uses on Windows.
func checkGitBash(app *App) Check {
	if _, err := app.Platform.FindExecutable("bash"); err != nil {
		return Check{
			Name:   "Git Bash",
			Detail: "not found on PATH",
			Fix:    "install Git for Windows; Claude Code's Bash tool needs it",
		}
	}
	return Check{Name: "Git Bash", OK: true, Detail: "found"}
}

// printDoctor renders the report.
func printDoctor(app *App, report DoctorReport) {
	app.Printf("Intenter doctor\n\n")

	// Sized to the checks actually present rather than to a constant, so a
	// longer check name never pushes its detail out of line.
	width := 0
	for _, check := range report.Checks {
		if got := utf8.RuneCountInString(check.Name); got > width {
			width = got
		}
	}

	for _, check := range report.Checks {
		mark := "✓"
		if !check.OK {
			mark = "✗"
		}
		padding := strings.Repeat(" ", width-utf8.RuneCountInString(check.Name))
		app.Printf("%s %s%s  %s\n", mark, check.Name, padding, Dash(check.Detail))
		if check.Fix != "" && !check.OK {
			app.Printf("    → %s\n", check.Fix)
		}
	}

	if report.OK {
		app.Printf("\nEverything looks healthy.\n")
		return
	}
	app.Printf("\nSome checks failed. The suggested fix is shown under each one.\n")
}

// newStatusCommand builds `intenter status` (§25).
func newStatusCommand(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the daemon, integration and recent activity",
		Long: "One screen with the daemon's version, uptime, endpoint and service mode, the\n" +
			"Claude Code integration state, approval counts by state, and how many\n" +
			"decisions of each kind were made in the last 24 hours. For a failing\n" +
			"installation use `intenter doctor`, which says what to fix.",
		Example: "  intenter status\n" +
			"  intenter status --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := orBackground(cmd.Context())

			var result ipc.StatusResult
			if err := app.Client().Call(ctx, ipc.MethodStatus, nil, &result); err != nil {
				return daemonError(err)
			}
			if app.JSON {
				return app.PrintJSON(result)
			}
			printStatus(app, result)
			return nil
		},
	}
}

// printStatus renders the status report.
func printStatus(app *App, result ipc.StatusResult) {
	app.Printf("Intenter %s\n", result.Daemon.Version)
	Summary(app.Out, "daemon", "running (pid %d, %s)", result.Daemon.PID, result.Daemon.ServiceMode)
	Summary(app.Out, "endpoint", "%s", result.Daemon.Endpoint)
	Summary(app.Out, "database", "%s", result.Daemon.DBPath)

	app.Printf("\nTrusted here\n")
	Summary(app.Out, "active", "%d", result.Counts.Approvals["active"])
	if disabled := result.Counts.Approvals["disabled"]; disabled > 0 {
		Summary(app.Out, "disabled", "%d", disabled)
	}
	if revoked := result.Counts.Approvals["revoked"]; revoked > 0 {
		Summary(app.Out, "revoked", "%d", revoked)
	}

	app.Printf("\nLast 24 hours\n")
	Summary(app.Out, "allowed", "%d", result.Counts.Events24h["allow"])
	Summary(app.Out, "asked", "%d", result.Counts.Events24h["ask"])
	Summary(app.Out, "blocked", "%d", result.Counts.Events24h["block"])

	// Sorted, because Go randomizes map iteration: a status that lists agents in
	// a different order on every run is one nobody can diff.
	agents := make([]string, 0, len(result.Integration))
	for agent := range result.Integration {
		agents = append(agents, agent)
	}
	sort.Strings(agents)

	for _, agent := range agents {
		integration := result.Integration[agent]
		app.Printf("\n%s\n", agent)
		if integration.HooksInstalled {
			Summary(app.Out, "hooks", "installed (%s)", Dash(integration.SettingsPath))
		} else {
			Summary(app.Out, "hooks", "not installed — run `intenter setup %s`", agent)
		}
		if integration.AgentVersion != "" {
			Summary(app.Out, "version", "%s", integration.AgentVersion)
		}
	}
}
