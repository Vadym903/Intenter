package cli

import (
	"context"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/adapter/claude"
	"github.com/Vadym903/Intenter/internal/ipc"
)

// menuPermissionLimit bounds how many permissions the menu prints.
//
// The menu is injected into an agent session, where output past the tool's
// inline ceiling arrives as a file path instead of text. A project with fifty
// approvals must still produce a menu, so the list is bounded and says what it
// did not show.
const menuPermissionLimit = 8

// MenuAction is one thing a user can do from `/intenter` or from a terminal.
//
// This slice is the single source of truth for the menu: the `menu` command
// renders it, and `setup claude` renders the skill file's dispatch table from
// the same entries, so the two cannot disagree about what `/intenter allowed`
// runs. Order is fixed, because the skill file is rewritten on every upgrade
// and must not churn.
type MenuAction struct {
	// Name is what the user types after `/intenter`.
	Name string
	// Argument is the placeholder the action takes, empty when it takes none.
	Argument string
	// Summary says what the action does, in one line.
	Summary string
	// Example is a runnable command line.
	Example string
	// Command is the command the action runs, with `%s` where the argument goes.
	Command string
	// Changes marks an action that changes a permission.
	Changes bool
	// Reversible marks a change that can be taken back. An irreversible one
	// asks for a confirmation before it does anything; a reversible one does
	// not, because the confirmation would be friction without a safety gain.
	Reversible bool
	// Undo says how the change is undone, or why it cannot be.
	Undo string
}

// MenuActions is the menu, in the order it is shown.
func MenuActions() []MenuAction {
	return []MenuAction{
		{
			Name:    "allowed",
			Summary: "What this project can run without a prompt — both Intenter's approvals and Claude's own rules.",
			Example: "intenter approvals",
			Command: "intenter approvals",
		},
		{
			Name:    "recent",
			Summary: "The same list, newest first, so a permission granted minutes ago is at the top.",
			Example: "intenter approvals --recent",
			Command: "intenter approvals --recent",
		},
		{
			Name:     "remove",
			Argument: "<id>",
			Summary:  "Stop trusting one permission for good, and remove the agent rule that grants the same command.",
			Example:  "intenter approval revoke 3",
			Command:  "intenter approval revoke %s",
			Changes:  true,
			Undo:     "permanent, and it asks before it changes anything",
		},
		{
			Name:     "pause",
			Argument: "<id>",
			Summary:  "Stop one approval from matching for now, without ending it.",
			Example:  "intenter approval disable 3",
			Command:    "intenter approval disable %s",
			Changes:    true,
			Reversible: true,
			Undo:       "reversible with `intenter approval enable <id>`",
		},
		{
			Name:     "project",
			Argument: "<path>",
			Summary:  "The permissions of another project; `intenter approvals --all` shows every project at once.",
			Example:  "intenter approvals --project ~/src/app",
			Command:  "intenter approvals --project %s",
		},
	}
}

// SkillActions converts the menu for the skill-file renderer, which needs the
// dispatch but not the terminal-facing example.
func SkillActions() []claude.SkillAction {
	actions := MenuActions()
	converted := make([]claude.SkillAction, 0, len(actions))
	for _, act := range actions {
		converted = append(converted, claude.SkillAction{
			Name:     act.Name,
			Argument: act.Argument,
			Summary:  act.Summary,
			Command:  act.Command,
			Changes:  act.Changes,
		})
	}
	return converted
}

// menuResult is the `--json` shape of the menu.
type menuResult struct {
	Project     string            `json:"project"`
	Warnings    []string          `json:"warnings"`
	Permissions []permissionEntry `json:"permissions"`
	Actions     []MenuAction      `json:"actions"`
}

// newMenuCommand builds `intenter menu`.
func newMenuCommand(app *App) *cobra.Command {
	var (
		project string
		all     bool
	)

	cmd := &cobra.Command{
		Use:   "menu",
		Short: "What this project allows, and what you can do about it",
		Long: "One screen for the agent session: what currently runs in this project\n" +
			"without a prompt, and every action you can take, each with an example.\n" +
			"This is the command `/intenter` runs in Claude Code, and it is a normal\n" +
			"terminal command too.\n\n" +
			"It always exits 0. A command injected into an agent skill aborts the whole\n" +
			"invocation when it fails, so an unreachable daemon has to be something the\n" +
			"menu says rather than something it returns.",
		Example: "  intenter menu\n" +
			"  intenter menu --project ~/src/app\n" +
			"  intenter menu --json",
		Args: cobra.NoArgs,
		// Overrides the root hook, which fails the command when the platform or
		// the configuration cannot be read. For every other command that is
		// right. Here it is the one thing that must not happen: a non-zero exit
		// aborts the whole `/intenter` invocation, so a user with a broken
		// config would get a shell error instead of the menu that would have
		// told them about it. Start-up failures become warnings.
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			app.initErr = app.init()
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			result := buildMenu(cmd.Context(), app, project, all)
			if app.JSON {
				// A JSON encoding failure is a broken stdout, not something the
				// menu can report inside its own output.
				return app.PrintJSON(result)
			}
			printMenu(app, result, all)
			return nil
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&project, "project", "", "show another project directory")
	flags.BoolVar(&all, "all", false, "show every project")
	return cmd
}

// buildMenu gathers what the menu shows. Every failure becomes a warning: the
// menu reports problems, it does not fail on them.
func buildMenu(ctx context.Context, app *App, project string, all bool) menuResult {
	result := menuResult{Warnings: []string{}, Permissions: []permissionEntry{}, Actions: MenuActions()}

	// Start-up failed. The actions are still worth printing — one of them is
	// how the user finds out what is wrong — but nothing that needs the
	// platform or the configuration can be read.
	if app.initErr != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"Intenter could not start up: %v. Nothing below reflects your real "+
				"permissions until that is fixed; `intenter doctor` says more.", app.initErr))
		if app.Platform == nil {
			return result
		}
	}

	params := ipc.ListApprovalsParams{}
	if !all {
		id, err := projectIDFor(app, project)
		if err != nil {
			// The working directory could not be read, so there is no project
			// to scope to. The actions are still worth printing.
			result.Warnings = append(result.Warnings,
				"The current directory could not be read, so this is not scoped to a "+
					"project. Name one with --project <path>, or use --all for every project.")
			result.Actions = MenuActions()
			return result
		}
		params.ProjectID = &id

		// The menu always says which project it is about, whether or not that
		// project has anything trusted yet.
		dir := project
		if dir == "" {
			dir = workingDirOrEmpty()
		}
		result.Project = projectRootFor(dir)
	}

	summaries, warning := menuApprovals(ctx, app, params)
	if warning != "" {
		result.Warnings = append(result.Warnings, warning)
	}

	// The rules Claude holds of its own count too: they let a command run
	// without a prompt whether or not Intenter ever imported them.
	var rules []agentRuleEntry
	if !all {
		var unreadable []string
		rules, unreadable = agentRules(app, project)
		for _, path := range unreadable {
			result.Warnings = append(result.Warnings, fmt.Sprintf(
				"%s could not be read, so the rules it holds are unknown — the list "+
					"below may be missing some.", path))
		}
	}
	result.Permissions = permissionEntries(summaries, rules)

	if warning := menuBypassWarning(ctx, app, params.ProjectID); warning != "" {
		result.Warnings = append(result.Warnings, warning)
	}
	return result
}

// menuApprovals reads the approvals, falling back to the database when the
// daemon is not answering. The fallback reads the same committed rows the
// daemon would return, so it is not a stale list — but the user is still told
// the daemon is down, because that is a broken installation either way.
func menuApprovals(ctx context.Context, app *App, params ipc.ListApprovalsParams) ([]ipc.ApprovalSummary, string) {
	var summaries []ipc.ApprovalSummary
	err := app.Client().Call(orBackground(ctx), ipc.MethodListApprovals, params, &summaries)
	if err == nil {
		return summaries, ""
	}
	if !ipc.IsUnavailable(err) {
		return nil, fmt.Sprintf("Intenter could not read its approvals: %v", err)
	}

	stored, readErr := storedApprovals(orBackground(ctx), app, params)
	if readErr != nil {
		return nil, "The Intenter daemon is not answering and its database could not be read, " +
			"so nothing below is certain. Start it with `intenter daemon start`, " +
			"then `intenter doctor`."
	}
	return stored, "The Intenter daemon is not answering, so commands are not being gated " +
		"right now. The list below is read straight from the database. " +
		"Start it with `intenter daemon start`."
}

// menuBypassWarning says when the last command Intenter saw here ran with
// Claude's permission checks bypassed. Intenter records the mode of every
// decision, so this is what it observed rather than a guess about the session.
func menuBypassWarning(ctx context.Context, app *App, projectID *string) string {
	ctx = orBackground(ctx)

	var events []action.AuditEventSummary
	params := ipc.ListHistoryParams{ProjectID: projectID, Limit: 1}
	if err := app.Client().Call(ctx, ipc.MethodListHistory, params, &events); err != nil || len(events) == 0 {
		return ""
	}

	var event action.AuditEvent
	if err := app.Client().Call(ctx, ipc.MethodGetHistoryEvent,
		ipc.GetHistoryEventParams{ID: events[0].ID}, &event); err != nil {
		return ""
	}
	if mode, _ := event.AdapterContext["permission_mode"].(string); mode != claude.ModeBypassPermissions {
		return ""
	}
	return fmt.Sprintf("The last command Intenter saw here (%s) ran with Claude's permission "+
		"checks bypassed. While a session is in that mode nothing below is gated.",
		FormatTime(&events[0].At))
}

// printMenu renders the menu.
func printMenu(app *App, result menuResult, all bool) {
	if all {
		app.Printf("Intenter — every project\n")
	} else if result.Project != "" {
		app.Printf("Intenter — %s\n", result.Project)
	} else {
		app.Printf("Intenter\n")
	}

	for _, warning := range result.Warnings {
		app.Printf("\n! %s\n", warning)
	}

	app.Printf("\nAllowed without asking\n")
	printMenuPermissions(app, result.Permissions, all)

	app.Printf("\nWhat you can do\n")
	for _, act := range result.Actions {
		app.Printf("  /intenter %s\n", menuActionLabel(act))
		app.Printf("      %s\n", act.Summary)
		app.Printf("      e.g. %s\n", act.Example)
		if act.Changes {
			app.Printf("      changes a permission — %s\n", act.Undo)
			if !act.Reversible {
				app.Printf("      you will be shown what changes, and asked, before anything does\n")
			}
		}
	}
}

// printMenuPermissions renders the bounded list, from both sources.
func printMenuPermissions(app *App, permissions []permissionEntry, all bool) {
	if len(permissions) == 0 {
		if all {
			app.Printf("  Nothing is trusted yet.\n")
		} else {
			app.Printf("  Nothing is trusted in this project yet.\n")
		}
		app.Printf("  A permission appears here when you answer \"Yes, and don't ask again\" in\n")
		app.Printf("  Claude, or run `intenter approve <event-id>` (see `intenter history`).\n")
		return
	}

	shown := permissions
	if len(shown) > menuPermissionLimit {
		shown = shown[:menuPermissionLimit]
	}

	table := NewTable("ID", "ACTION", "TRUSTED", "ORIGIN").WithWidths(0, 24, 56, 0)
	for _, entry := range shown {
		switch {
		case entry.Approval != nil:
			table.Add(
				strconv.FormatInt(entry.Approval.ID, 10),
				opsString(entry.Approval.SemanticOps),
				Dash(entry.Approval.Summary),
				string(entry.Approval.Origin),
			)
		case entry.Rule != nil:
			table.Add(entry.Rule.Label, "-", entry.Rule.Text, "claude rule ("+entry.Rule.Scope+")")
		}
	}
	table.Render(app.Out)

	if hidden := len(permissions) - len(shown); hidden > 0 {
		app.Printf("\n  %d more not shown — see them all with `intenter approvals`.\n", hidden)
	}
}

// menuActionLabel is how the action is typed.
func menuActionLabel(act MenuAction) string {
	if act.Argument == "" {
		return act.Name
	}
	return act.Name + " " + act.Argument
}
