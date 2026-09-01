package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/config"
	"github.com/Vadym903/Intenter/internal/ipc"
	"github.com/Vadym903/Intenter/internal/logging"
	"github.com/Vadym903/Intenter/internal/platform"
	"github.com/Vadym903/Intenter/internal/storage"
	"github.com/Vadym903/Intenter/internal/updater"
	"github.com/Vadym903/Intenter/internal/version"
)

// EnvSelfTest forces a hook invocation into dry-run mode, so the self-test can
// run the real hook command line without recording anything (§12.2 step 7).
const EnvSelfTest = "INTENTER_SELFTEST"

// SelfTestTimeout bounds the whole self-test.
const SelfTestTimeout = 30 * time.Second

// daemonReadyTimeout is how long setup waits for the daemon to answer.
const daemonReadyTimeout = 5 * time.Second

// SetupOptions configure `intenter setup claude` (§12.2).
type SetupOptions struct {
	// DryRun prints what would change without changing anything.
	DryRun bool
	// SettingsPath overrides the Claude settings file.
	SettingsPath string
	// NoService skips service registration; the daemon is started manually.
	NoService bool
	// NoStartupCheck leaves the user's shell start-up files untouched. The
	// installers pass it when the user chose not to have their PATH modified:
	// somebody who declined one edit to their shell files did not ask for
	// another.
	NoStartupCheck bool
	// SkillActions is the `/intenter` menu the skill file dispatches. The CLI
	// supplies it, because the CLI owns the command surface it names.
	SkillActions []SkillAction
}

// Step is one line of the setup report.
type Step struct {
	Name string
	// Detail is the parenthetical after the step name.
	Detail string
	// Duration is how long the step took, shown so a slow step is visible.
	Duration time.Duration
	// Err is set when the step failed.
	Err error
	// Warning marks a step that succeeded with something worth saying.
	Warning string
}

// OK reports whether the step succeeded.
func (s Step) OK() bool { return s.Err == nil }

// String renders the step as it appears in the report.
func (s Step) String() string {
	mark := "✓"
	text := s.Name
	if s.Err != nil {
		mark = "✗"
		text = fmt.Sprintf("%s: %v", s.Name, s.Err)
	} else if s.Detail != "" {
		text = fmt.Sprintf("%s (%s)", s.Name, s.Detail)
	}
	return fmt.Sprintf("%s %s (%.1fs)", mark, text, s.Duration.Seconds())
}

// SetupResult is everything setup did.
type SetupResult struct {
	Steps []Step
	// Installation is what was detected.
	Installation *Installation
	// BackupPath is where the previous settings were saved.
	BackupPath string
	// ServiceMode is "managed" or "unmanaged".
	ServiceMode string
	// RuleInventory summarizes the agent's existing permission rules.
	RuleInventory RuleInventory
	// StartupCheckFiles are the shell start-up files the update check was
	// written into.
	StartupCheckFiles []string
	// SkillInstall is what installing the `/intenter` command did.
	SkillInstall SkillInstall
}

// Failed reports whether any step failed.
func (r *SetupResult) Failed() bool {
	for _, step := range r.Steps {
		if !step.OK() {
			return true
		}
	}
	return false
}

// RuleInventory counts the agent's existing allow rules (§12.2 step 6).
type RuleInventory struct {
	Exact  int
	Prefix int
	Files  []string
}

// Total is how many rules were found.
func (r RuleInventory) Total() int { return r.Exact + r.Prefix }

// Setup runs the installation steps of §12.2.
//
// Each step is idempotent and re-runnable, and setup stops at the first
// required failure so the system is never left half-configured.
type Setup struct {
	platform platform.Platform
	config   config.Config
	services platform.ServiceManager
	options  SetupOptions
	now      func() time.Time
}

// NewSetup builds the setup runner.
func NewSetup(p platform.Platform, cfg config.Config, services platform.ServiceManager, options SetupOptions) *Setup {
	return &Setup{platform: p, config: cfg, services: services, options: options, now: time.Now}
}

// Run executes the steps, returning what happened. The error is the first
// required failure; the result always holds every step attempted.
func (s *Setup) Run(ctx context.Context) (*SetupResult, error) {
	result := &SetupResult{ServiceMode: platform.ModeUnmanaged}

	steps := []func(context.Context, *SetupResult) Step{
		s.detect,
		s.backup,
		s.installHooks,
		s.installSkill,
		s.installStartupCheck,
		s.initializeStorage,
		s.installService,
		s.inventoryRules,
		s.selfTest,
		s.recordMeta,
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
func (s *Setup) timed(name string, body func() (string, string, error)) Step {
	started := s.now()
	detail, warning, err := body()
	return Step{
		Name:     name,
		Detail:   detail,
		Warning:  warning,
		Err:      err,
		Duration: s.now().Sub(started),
	}
}

// detect locates Claude Code (§12.2 step 1).
func (s *Setup) detect(ctx context.Context, result *SetupResult) Step {
	return s.timed("Claude Code detected", func() (string, string, error) {
		install, err := Detect(ctx, s.platform, s.options.SettingsPath)
		result.Installation = install
		if err != nil {
			return "", "", err
		}
		if !s.options.DryRun {
			if err := EnsureSettingsFile(install.SettingsPath); err != nil {
				return "", "", err
			}
		}

		return install.Describe(), strings.Join(install.Warnings, "; "), nil
	})
}

// backup copies the settings before anything is changed (§12.2 step 2).
func (s *Setup) backup(_ context.Context, result *SetupResult) Step {
	return s.timed("Settings backed up", func() (string, string, error) {
		if s.options.DryRun {
			return "skipped for a dry run", "", nil
		}
		path, err := BackupSettings(result.Installation.SettingsPath, s.platform.DataDir(), s.now())
		if err != nil {
			return "", "", err
		}
		result.BackupPath = path
		if path == "" {
			return "no existing settings to back up", "", nil
		}
		return path, "", nil
	})
}

// installHooks writes the hook entries (§12.2 step 3).
func (s *Setup) installHooks(_ context.Context, result *SetupResult) Step {
	return s.timed("Permission hooks installed", func() (string, string, error) {
		executable, err := s.platform.SelfExecutablePath()
		if err != nil {
			return "", "", err
		}
		if s.options.DryRun {
			return fmt.Sprintf("would write %s", result.Installation.SettingsPath), "", nil
		}
		if err := InstallHooks(result.Installation.SettingsPath, executable,
			s.platform.OS(), s.config.Claude.HookConfigChange); err != nil {
			return "", "", err
		}
		return result.Installation.SettingsPath, "", nil
	})
}

// installSkill writes the `/intenter` command into Claude's skills directory.
//
// It is not a required step. A machine where the file cannot be written still
// has a working gate; it just has no menu inside the session, and the CLI does
// everything the menu does. Failing setup over it would trade the whole
// integration for one convenience.
func (s *Setup) installSkill(_ context.Context, result *SetupResult) Step {
	return s.timed("Agent command /intenter", func() (string, string, error) {
		configDir := result.Installation.ConfigDir
		if s.options.DryRun {
			return fmt.Sprintf("would write %s", SkillPath(configDir)), "", nil
		}

		install, err := InstallSkill(configDir, s.platform.DataDir(), s.options.SkillActions, s.now())
		if err != nil {
			return "not installed", fmt.Sprintf("%v — `/intenter` will not be available", err), nil
		}
		result.SkillInstall = install

		warning := ""
		if install.BackupPath != "" {
			warning = "a file Intenter did not write was already at that path; " +
				"it was saved to " + install.BackupPath
		}
		if install.CreatedSkillsDir {
			// Claude Code watches skill directories for changes, but only ones
			// that existed when the session started. The first time this
			// directory is created, a running session cannot see it.
			warning = strings.TrimSpace(warning + " restart Claude Code so it picks up the new skills directory.")
		}

		detail := install.Path
		if install.Unchanged {
			detail += " (unchanged)"
		}
		return detail, warning, nil
	})
}

// installStartupCheck adds the managed block that shows the update prompt when
// a terminal opens (003 FR-009).
//
// It is not a required step: a machine whose shell files cannot be written is
// one that never gets an update prompt, which is a smaller problem than a setup
// that refuses to finish. The step says so and moves on.
func (s *Setup) installStartupCheck(_ context.Context, result *SetupResult) Step {
	return s.timed("Start-up update check", func() (string, string, error) {
		switch {
		case s.options.NoStartupCheck:
			return "skipped (--no-startup-check)", "", nil
		case !s.config.Updates.StartupHook:
			return "skipped (updates.startup_hook = false)", "", nil
		case s.options.DryRun:
			return "would add the check to your shell start-up files", "", nil
		}

		check := s.startupCheck()
		status, err := check.Install(nil)
		if err != nil {
			return "not installed", fmt.Sprintf("%v — run `intenter update startup enable` to retry", err), nil
		}
		result.StartupCheckFiles = status.Installed

		warning := ""
		if status.BlockedByPolicy {
			warning = "PowerShell will not run profile scripts under the current execution " +
				"policy, so the update prompt cannot appear. Fix: " + status.PolicyFix
		}
		// The check runs when a shell starts. Claude Code's VS Code panel is not
		// a shell and never sources a profile, so for someone who works there
		// this is installed and will still never fire. Saying it is installed
		// and stopping would be true and useless.
		if result.Installation != nil && result.Installation.Executable == "" {
			warning = strings.TrimSpace(warning + " This runs when a terminal opens, so it " +
				"cannot appear in Claude Code's VS Code panel — check for updates with " +
				"`intenter update --check`.")
		}
		return strings.Join(status.Installed, ", "), warning, nil
	})
}

// startupCheck builds the block writer for this installation.
func (s *Setup) startupCheck() *updater.StartupCheck {
	executable, _ := s.platform.SelfExecutablePath()
	return &updater.StartupCheck{
		Home:       s.platform.HomeDir(),
		Executable: executable,
		Store:      updater.NewStore(s.platform.DataDir()),
		LookPath:   exec.LookPath,
		Policy:     updater.ExecutionPolicyBlocked,
	}
}

// initializeStorage creates the database (§12.2 step 4).
func (s *Setup) initializeStorage(ctx context.Context, _ *SetupResult) Step {
	return s.timed("Database initialized", func() (string, string, error) {
		if s.options.DryRun {
			return fmt.Sprintf("would create %s", platform.DatabasePath(s.platform)), "", nil
		}

		warning := ""
		// A prior installation under the product's old name is moved into
		// place before anything new is created, so its approvals and history
		// survive the rename (contracts/identity-and-rename.md). A failure
		// here must not block setup: it just leaves the old directory for
		// `doctor` to report.
		if _, err := platform.MigrateLegacyDataDir(s.platform, logging.Discard()); err != nil {
			warning = fmt.Sprintf("could not migrate the previous installation's data directory (%v)", err)
		}

		if err := platform.EnsureDirs(s.platform); err != nil {
			return "", "", err
		}
		db, err := storage.OpenAndMigrate(ctx, platform.DatabasePath(s.platform))
		if err != nil {
			return "", "", err
		}
		defer db.Close()

		return fmt.Sprintf("%s, schema v%d", db.Path(), version.SchemaVersion), warning, nil
	})
}

// installService registers and starts the daemon (§12.2 step 5).
func (s *Setup) installService(ctx context.Context, result *SetupResult) Step {
	return s.timed("Daemon running", func() (string, string, error) {
		if s.options.DryRun {
			return "skipped for a dry run", "", nil
		}

		executable, err := s.platform.SelfExecutablePath()
		if err != nil {
			return "", "", err
		}

		warning := ""
		// A registration left by the product's old name is removed before
		// this one is installed, so an upgrade never leaves two daemons
		// competing for the same job (contracts/identity-and-rename.md).
		if err := platform.RemoveLegacyService(ctx, s.platform); err != nil {
			warning = fmt.Sprintf("could not remove the previous installation's service (%v); ", err)
		}

		if s.options.NoService {
			warning += "service registration skipped; start the daemon with `intenter daemon start`"
		} else if s.services != nil && s.services.Available(ctx) {
			if err := s.services.Install(ctx, executable); err != nil {
				// A machine that cannot register a service still works: the
				// hook starts the daemon on demand (FR-022).
				warning += fmt.Sprintf("could not register the service (%v); running unmanaged", err)
			} else {
				result.ServiceMode = platform.ModeManaged
			}
		} else {
			warning += "no per-user service manager is available; running unmanaged"
		}

		if err := s.ensureDaemonRunning(ctx, executable); err != nil {
			return "", "", err
		}

		detail := result.ServiceMode
		if result.ServiceMode == platform.ModeManaged && s.services != nil {
			detail = s.services.Name() + ", managed"
		}
		return detail, warning, nil
	})
}

// ensureDaemonRunning starts the daemon if it is not already answering.
func (s *Setup) ensureDaemonRunning(ctx context.Context, executable string) error {
	if s.daemonReady(ctx) {
		return nil
	}

	logPath := filepath.Join(platform.LogDir(s.platform), "daemon-stdio.log")
	if _, err := platform.SpawnDetached(executable, []string{"daemon", "run"}, logPath); err != nil {
		return fmt.Errorf("could not start the daemon: %w", err)
	}

	deadline := s.now().Add(daemonReadyTimeout)
	for s.now().Before(deadline) {
		if s.daemonReady(ctx) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("the daemon did not answer within %s (see %s)", daemonReadyTimeout, logPath)
}

// daemonReady reports whether the daemon answers.
func (s *Setup) daemonReady(ctx context.Context) bool {
	_, err := s.client().Ping(ctx)
	return err == nil
}

// client connects to the daemon the same way the CLI does.
func (s *Setup) client() *ipc.Client {
	endpoint := ipc.DiscoverEndpoint("", s.platform.DataDir(), s.platform.IPCEndpoint())
	return ipc.NewClient(endpoint)
}

// inventoryRules summarizes the agent's existing permission rules (§12.2 step
// 6). No approval is created here: a rule is only ever converted at first use,
// after full resolution and policy (I-8).
func (s *Setup) inventoryRules(_ context.Context, result *SetupResult) Step {
	return s.timed("Existing permissions inventoried", func() (string, string, error) {
		reader := NewSettingsReader(s.platform, s.options.SettingsPath)
		files := reader.Discover("")

		inventory := RuleInventory{}
		for _, file := range files {
			if !file.Exists {
				continue
			}
			inventory.Files = append(inventory.Files, file.Path)
			for _, tool := range []string{ToolBash, ToolPowerShell} {
				for _, rule := range AllowRules([]SettingsFile{file}, tool) {
					if strings.Contains(rule.Content, "*") {
						inventory.Prefix++
					} else {
						inventory.Exact++
					}
				}
			}
		}
		result.RuleInventory = inventory

		if inventory.Total() == 0 {
			return "no existing shell permissions found", "", nil
		}
		return fmt.Sprintf("%d exact and %d pattern rules; each is validated and imported at first use",
			inventory.Exact, inventory.Prefix), "", nil
	})
}

// selfTest proves the whole path works before setup claims success
// (§12.2 step 7).
//
// It checks the daemon's own judgement and then runs the exact hook command
// line that was written to the settings file, because a hook that was installed
// but cannot execute is the failure a user would otherwise discover mid-session.
func (s *Setup) selfTest(ctx context.Context, result *SetupResult) Step {
	return s.timed("Integration test passed", func() (string, string, error) {
		if s.options.DryRun {
			return "skipped for a dry run", "", nil
		}

		ctx, cancel := context.WithTimeout(ctx, SelfTestTimeout)
		defer cancel()

		workspace, err := os.MkdirTemp("", "intenter-selftest")
		if err != nil {
			return "", "", fmt.Errorf("could not create a temporary workspace: %w", err)
		}
		defer os.RemoveAll(workspace)

		if err := s.expectDecision(ctx, workspace, "rm -rf ~/Documents",
			action.OutcomeBlock, ""); err != nil {
			return "", "", err
		}
		if err := s.expectDecision(ctx, workspace, "some-unknown-tool --x",
			action.OutcomeAsk, action.ClassUnresolvedCommand); err != nil {
			return "", "", err
		}
		if err := s.runInstalledHook(ctx, result, workspace); err != nil {
			return "", "", err
		}
		return "daemon, policy and hook verified", "", nil
	})
}

// expectDecision runs one dry-run evaluation and checks the answer.
func (s *Setup) expectDecision(ctx context.Context, workspace, command string,
	outcome action.DecisionOutcome, class action.DecisionClass) error {

	var result action.EvaluationResult
	err := s.client().Call(ctx, ipc.MethodEvaluate, ipc.EvaluateParams{
		DryRun: true,
		Request: action.ActionRequest{
			Agent:      Agent,
			SessionID:  "intenter-selftest",
			Tool:       ToolBash,
			Dialect:    action.DialectPosix,
			RawCommand: command,
			Cwd:        workspace,
		},
	}, &result)
	if err != nil {
		return fmt.Errorf("the daemon could not evaluate %q: %w", command, err)
	}
	if result.Decision != outcome {
		return fmt.Errorf("%q was answered %s, expected %s", command, result.Decision, outcome)
	}
	if class != "" && result.Class != class {
		return fmt.Errorf("%q was classified %s, expected %s", command, result.Class, class)
	}
	return nil
}

// runInstalledHook executes the exact command line written to the settings
// file, with a synthetic payload, and checks it answers with a valid denial.
func (s *Setup) runInstalledHook(ctx context.Context, result *SetupResult, workspace string) error {
	installed, ok := InstalledHookCommand(result.Installation.SettingsPath, EventPreToolUse)
	if !ok {
		return fmt.Errorf("no Intenter hook was found in %s", result.Installation.SettingsPath)
	}

	name, args, err := hookInvocation(installed)
	if err != nil {
		return err
	}

	payload, err := json.Marshal(map[string]any{
		"hook_event_name": EventPreToolUse,
		"session_id":      "intenter-selftest",
		"cwd":             workspace,
		"permission_mode": ModeDefault,
		"tool_name":       ToolBash,
		"tool_use_id":     "intenter-selftest",
		"tool_input":      map[string]any{"command": "rm -rf ~/Documents"},
	})
	if err != nil {
		return err
	}

	command := exec.CommandContext(ctx, name, args...)
	command.Stdin = strings.NewReader(string(payload))
	command.Env = append(os.Environ(), EnvSelfTest+"=1")

	output, err := command.Output()
	if err != nil {
		return fmt.Errorf("the installed hook command failed: %w", err)
	}

	var response struct {
		HookSpecificOutput struct {
			PermissionDecision string `json:"permissionDecision"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		return fmt.Errorf("the installed hook did not answer with JSON: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	if response.HookSpecificOutput.PermissionDecision != "deny" {
		return fmt.Errorf("the installed hook answered %q for a blocked command, expected deny",
			response.HookSpecificOutput.PermissionDecision)
	}
	return nil
}

// hookInvocation turns a stored hook entry back into an executable and
// arguments. The shell form is the one setup wrote, so its quoting is known.
func hookInvocation(command HookCommand) (string, []string, error) {
	if len(command.Args) > 0 {
		return command.Command, command.Args, nil
	}

	text := strings.TrimSpace(command.Command)
	if !strings.HasSuffix(text, "hook claude") {
		return "", nil, fmt.Errorf("the installed hook command is not recognizable: %q", text)
	}
	executable := strings.TrimSpace(strings.TrimSuffix(text, "hook claude"))
	executable = strings.Trim(executable, `"'`)
	if executable == "" {
		return "", nil, fmt.Errorf("the installed hook command names no executable: %q", text)
	}
	return executable, []string{"hook", "claude"}, nil
}

// recordMeta stores the installation facts `doctor` reports on (§12.4).
func (s *Setup) recordMeta(ctx context.Context, result *SetupResult) Step {
	return s.timed("Installation recorded", func() (string, string, error) {
		if s.options.DryRun {
			return "skipped for a dry run", "", nil
		}

		executable, err := s.platform.SelfExecutablePath()
		if err != nil {
			return "", "", err
		}
		db, err := storage.OpenAndMigrate(ctx, platform.DatabasePath(s.platform))
		if err != nil {
			return "", "", err
		}
		store := storage.NewStore(db)
		defer store.Close()

		values := map[string]string{
			storage.MetaIntenterVersion:     version.Version,
			storage.MetaHooksVersion:        fmt.Sprintf("%d", version.EngineVersion),
			storage.MetaClaudeSettingsPath:  result.Installation.SettingsPath,
			storage.MetaServiceMode:         result.ServiceMode,
			storage.MetaEngineVersion:       fmt.Sprintf("%d", version.EngineVersion),
			storage.MetaInstalledBinaryPath: executable,
			storage.MetaSetupAt:             s.now().UTC().Format(time.RFC3339),
		}
		if result.Installation.Version != "" {
			values[storage.MetaClaudeVersion] = result.Installation.Version
		}
		if result.BackupPath != "" {
			values[storage.MetaLastBackupPath] = result.BackupPath
		}

		if err := store.Meta.SetAll(ctx, values); err != nil {
			return "", "", err
		}
		return "", "", nil
	})
}
