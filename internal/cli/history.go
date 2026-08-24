package cli

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/ipc"
	"github.com/Vadym903/Intenter/internal/platform"
	"github.com/Vadym903/Intenter/internal/storage"
)

// defaultHistoryLimit is how much of the decision log `history` shows.
const defaultHistoryLimit = 50

// newHistoryCommand builds `intenter history [show <id>]`.
func newHistoryCommand(app *App) *cobra.Command {
	var (
		blocked bool
		asked   bool
		allowed bool
		project string
		session string
		since   time.Duration
		limit   int
	)

	cmd := &cobra.Command{
		Use:   "history",
		Short: "Show what Intenter decided, and why",
		Long: "Lists the most recent decisions across every project: the command, what it\n" +
			"resolved to, the decision (allow, ask, block) and the rule or approval that\n" +
			"decided it. Filters narrow by decision, project, agent session or age. Use\n" +
			"`intenter history show <event-id>` for the full explanation of one event.",
		Example: "  intenter history\n" +
			"  intenter history --blocked --since 24h\n" +
			"  intenter history --project ~/src/app --limit 50 --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			params := ipc.ListHistoryParams{Limit: limit}

			decision, err := singleDecisionFilter(blocked, asked, allowed)
			if err != nil {
				return err
			}
			if decision != "" {
				params.Decision = &decision
			}
			if project != "" {
				id, err := projectIDFor(app, project)
				if err != nil {
					return err
				}
				params.ProjectID = &id
			}
			if session != "" {
				params.SessionID = &session
			}
			if since > 0 {
				at := time.Now().Add(-since)
				params.Since = &at
			}

			events, err := listHistory(cmd.Context(), app, params)
			if err != nil {
				return err
			}
			if app.JSON {
				return app.PrintJSON(events)
			}
			printHistory(app, events)
			return nil
		},
	}

	flags := cmd.Flags()
	flags.BoolVar(&blocked, "blocked", false, "only blocked commands")
	flags.BoolVar(&asked, "asked", false, "only commands that needed confirmation")
	flags.BoolVar(&allowed, "allowed", false, "only allowed commands")
	flags.StringVar(&project, "project", "", "only this project directory")
	flags.StringVar(&session, "session", "", "only this agent session")
	flags.DurationVar(&since, "since", 0, "only events newer than this duration, e.g. 24h")
	flags.IntVar(&limit, "limit", defaultHistoryLimit, "how many events to show")

	cmd.AddCommand(newHistoryShowCommand(app))
	return cmd
}

// newHistoryShowCommand builds `intenter history show <id>`: the full answer
// to "why did Intenter do that?".
func newHistoryShowCommand(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "show <event-id>",
		Short: "Explain one decision in full",
		Long: "Prints everything recorded for one event: the raw command and how it was\n" +
			"resolved (wrappers, scripts, the final commands), every target with its scope,\n" +
			"the effects, the fingerprints read, the hard-rule and approval outcome, what\n" +
			"the agent was told, and — for an approval that stopped matching — exactly what\n" +
			"changed. The event id is printed by the hook message and by `intenter history`.",
		Example: "  intenter history show 42\n" +
			"  intenter history show 42 --json",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			event, err := getHistoryEvent(cmd.Context(), app, id)
			if err != nil {
				return err
			}
			if app.JSON {
				return app.PrintJSON(event)
			}
			printHistoryEvent(app, event)
			return nil
		},
	}
}

// listHistory asks the daemon, falling back to a read-only look at the database
// when it is not running.
//
// The history is the one thing a user needs precisely when something is wrong,
// which is also when the daemon may be down. Reading it directly is safe: the
// fallback never writes (§25).
func listHistory(ctx context.Context, app *App, params ipc.ListHistoryParams) ([]action.AuditEventSummary, error) {
	var events []action.AuditEventSummary
	err := app.Client().Call(ctx, ipc.MethodListHistory, params, &events)
	if err == nil {
		return events, nil
	}
	if !ipc.IsUnavailable(err) {
		return nil, daemonError(err)
	}

	store, closeStore, openErr := openReadOnlyStore(app)
	if openErr != nil {
		return nil, daemonError(err)
	}
	defer closeStore()

	app.Warnf("warning: the daemon is not running; showing the stored history read-only\n")
	events, readErr := store.Audit.List(ctx, historyFilter(params))
	if readErr != nil {
		return nil, Fail(ExitError, readErr)
	}
	return events, nil
}

// getHistoryEvent reads one event, with the same read-only fallback.
func getHistoryEvent(ctx context.Context, app *App, id int64) (*action.AuditEvent, error) {
	var event action.AuditEvent
	err := app.Client().Call(ctx, ipc.MethodGetHistoryEvent, ipc.GetHistoryEventParams{ID: id}, &event)
	if err == nil {
		return &event, nil
	}
	if !ipc.IsUnavailable(err) {
		return nil, daemonError(err)
	}

	store, closeStore, openErr := openReadOnlyStore(app)
	if openErr != nil {
		return nil, daemonError(err)
	}
	defer closeStore()

	app.Warnf("warning: the daemon is not running; showing the stored event read-only\n")
	stored, readErr := store.Audit.Get(ctx, id)
	if readErr != nil {
		return nil, Fail(ExitError, readErr)
	}
	return stored, nil
}

// openReadOnlyStore opens the database without migrating it: a CLI must never
// change the schema the daemon owns.
func openReadOnlyStore(app *App) (*storage.Store, func(), error) {
	db, err := storage.OpenReadOnly(platform.DatabasePath(app.Platform))
	if err != nil {
		return nil, nil, err
	}
	store := storage.NewStore(db)
	return store, func() { _ = store.Close() }, nil
}

// historyFilter converts the IPC params into the repository filter.
func historyFilter(params ipc.ListHistoryParams) action.AuditFilter {
	filter := action.AuditFilter{Limit: params.Limit, Since: params.Since}
	if params.ProjectID != nil {
		filter.ProjectID = *params.ProjectID
	}
	if params.SessionID != nil {
		filter.SessionID = *params.SessionID
	}
	if params.Decision != nil {
		if outcome, ok := action.ParseOutcome(*params.Decision); ok {
			filter.Decision = &outcome
		}
	}
	return filter
}

// singleDecisionFilter turns the three convenience flags into one filter.
func singleDecisionFilter(blocked, asked, allowed bool) (string, error) {
	selected := make([]string, 0, 3)
	if blocked {
		selected = append(selected, action.OutcomeBlock.Wire())
	}
	if asked {
		selected = append(selected, action.OutcomeAsk.Wire())
	}
	if allowed {
		selected = append(selected, action.OutcomeAllow.Wire())
	}

	switch len(selected) {
	case 0:
		return "", nil
	case 1:
		return selected[0], nil
	default:
		return "", Failf(ExitError,
			"choose at most one of --blocked, --asked and --allowed")
	}
}

// printHistory renders the decision log.
func printHistory(app *App, events []action.AuditEventSummary) {
	if len(events) == 0 {
		app.Printf("No decisions recorded yet.\n")
		return
	}

	table := NewTable("ID", "TIME", "DECISION", "CLASS", "COMMAND", "RESOLVED", "REASON", "APPROVAL").
		WithWidths(0, 0, 0, 26, 32, 36, 44, 0)

	for _, event := range events {
		at := event.At
		approval := "-"
		if event.ApprovalID != nil {
			approval = strconv.FormatInt(*event.ApprovalID, 10)
		}
		table.Add(
			strconv.FormatInt(event.ID, 10),
			FormatTime(&at),
			strings.ToUpper(string(event.Decision)),
			string(event.Class),
			Dash(event.RawCommand),
			Dash(event.ResolvedSummary),
			Dash(event.Reason),
			approval,
		)
	}
	table.Render(app.Out)
}

// printHistoryEvent explains one decision from the stored row alone: no
// re-evaluation happens here (INVARIANT I-17).
func printHistoryEvent(app *App, event *action.AuditEvent) {
	at := event.At
	app.Printf("Event %d  %s  %s (%s)\n", event.ID, FormatTime(&at),
		strings.ToUpper(string(event.Decision)), event.DecisionClass)
	Field(app.Out, "command", "%s", event.RawCommand)
	Field(app.Out, "cwd", "%s", Dash(event.Cwd))
	if event.SessionID != "" {
		Field(app.Out, "agent", "%s (session %s)", Dash(event.Agent), event.SessionID)
	} else {
		Field(app.Out, "agent", "%s", Dash(event.Agent))
	}

	if event.Resolved != nil {
		printResolvedAction(app, event.Resolved)
	}

	Field(app.Out, "reason", "%s", Dash(event.Reason))
	if event.HardRule != "" {
		Field(app.Out, "rule", "%s", event.HardRule)
	}
	if event.MatchedApprovalID != nil {
		Field(app.Out, "approval", "%d", *event.MatchedApprovalID)
	}
	if event.ImportedApprovalID != nil {
		Field(app.Out, "imported", "approval %d", *event.ImportedApprovalID)
	}

	for _, report := range event.MismatchReport {
		app.Printf("  approval %d no longer matches:\n", report.ApprovalID)
		for _, difference := range report.Differences {
			app.Printf("    %s\n", difference)
		}
	}

	if len(event.Explanation) > 0 {
		FieldHeading(app.Out, "explanation")
		for _, line := range event.Explanation {
			app.Printf("    %s\n", line)
		}
	}

	if event.AdapterAction != "" {
		Field(app.Out, "delivered", "%s", event.AdapterAction)
	}
	if event.PromptShown {
		Field(app.Out, "prompted", "the agent showed its own permission dialog")
	}
	if event.ExecutionStatus != "" {
		if event.ExecutionAt != nil {
			Field(app.Out, "executed", "%s at %s", event.ExecutionStatus, FormatTime(event.ExecutionAt))
		} else {
			Field(app.Out, "executed", "%s", event.ExecutionStatus)
		}
	}
}

// printResolvedAction renders what the command was understood to do.
func printResolvedAction(app *App, resolved *action.ResolvedAction) {
	Field(app.Out, "resolved", "%s", resolved.Status)
	if resolved.StatusReason != "" {
		app.Printf("%s%s\n", strings.Repeat(" ", FieldWidth), resolved.StatusReason)
	}
	if len(resolved.Unsupported) > 0 {
		Field(app.Out, "refused", "%s", strings.Join(resolved.Unsupported, ", "))
	}

	targets := NewPairs(PathWidth)
	seen := make(map[string]bool)
	for _, target := range resolved.Targets() {
		if target.Display == "" || seen[target.Display] {
			continue
		}
		seen[target.Display] = true

		scope := string(target.Scope)
		if len(target.Flags) > 0 {
			flags := make([]string, 0, len(target.Flags))
			for _, flag := range target.Flags {
				flags = append(flags, string(flag))
			}
			scope += " [" + strings.Join(flags, ",") + "]"
		}
		targets.Add(target.Display, scope)
	}
	if !targets.Empty() {
		FieldHeading(app.Out, "targets")
		targets.Render(app.Out)
	}

	if envelope := resolved.Envelope(); len(envelope) > 0 {
		FieldHeading(app.Out, "effects")
		for _, entry := range envelope {
			app.Printf("    %s\n", entry.String())
		}
	}
	if len(resolved.Fingerprints) > 0 {
		FieldHeading(app.Out, "depends on")
		fingerprints := NewPairs(KeyWidth)
		for _, fingerprint := range resolved.Fingerprints {
			fingerprints.Add(fingerprint.Key, FingerprintShort(fingerprint.Value))
		}
		fingerprints.Render(app.Out)
	}
}
