package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/ipc"
)

// defaultSummarySince is the window `summary` reports on when none is given.
// A day is the span a person can still remember working through, which is what
// makes the counts mean anything to them.
const defaultSummarySince = 24 * time.Hour

// newSummaryCommand builds `intenter summary`.
func newSummaryCommand(app *App) *cobra.Command {
	var (
		project string
		session string
		since   time.Duration
		all     bool
	)

	cmd := &cobra.Command{
		Use:   "summary",
		Short: "Count what Intenter decided, and how many prompts it answered for you",
		Long: "Counts the decisions of the last 24 hours: how many commands ran without a\n" +
			"prompt, how many of those an approval you gave once was responsible for, and\n" +
			"how many were asked about or refused. The approval count is the number of\n" +
			"dialogs that did not appear because the same question had already been\n" +
			"answered. Narrow it with --since, --project or --session, or widen it to\n" +
			"everything on record with --all.",
		Example: "  intenter summary\n" +
			"  intenter summary --since 72h --project ~/src/app\n" +
			"  intenter summary --all --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			params := ipc.SummarizeParams{}

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

			// --all and --since are the same knob from opposite ends, so the
			// explicit "everything" wins over the implicit default rather than
			// the two silently fighting.
			window := since
			if window == 0 && !all {
				window = defaultSummarySince
			}
			if window > 0 {
				at := time.Now().Add(-window)
				params.Since = &at
			}

			summary, err := summarize(cmd.Context(), app, params)
			if err != nil {
				return err
			}
			if app.JSON {
				return app.PrintJSON(summary)
			}
			printSummary(app, summary, window)
			return nil
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&project, "project", "", "only this project directory")
	flags.StringVar(&session, "session", "", "only this agent session")
	flags.DurationVar(&since, "since", 0, "count events newer than this duration, e.g. 24h")
	flags.BoolVar(&all, "all", false, "count everything on record instead of the last 24 hours")

	return cmd
}

// summarize asks the daemon for the counts.
func summarize(ctx context.Context, app *App, params ipc.SummarizeParams) (action.ActivitySummary, error) {
	var summary ipc.SummarizeResult
	if err := app.Client().Call(orBackground(ctx), ipc.MethodSummarize, params, &summary); err != nil {
		return action.ActivitySummary{}, daemonError(err)
	}
	return summary, nil
}

// printSummary renders the counts.
//
// Every line is a count of recorded decisions. Nothing here is extrapolated
// into minutes saved: the log knows how many prompts did not happen, and it
// does not know what any of them would have cost.
func printSummary(app *App, summary action.ActivitySummary, window time.Duration) {
	app.Printf("Intenter — %s\n", summaryPeriod(summary, window))

	if summary.Empty() {
		app.Printf("\nNothing decided in this period.\n")
		app.Printf("Intenter records a decision for every command Claude Code runs; if this\n")
		app.Printf("stays empty while you are working, run `intenter doctor`.\n")
		return
	}

	app.Printf("\n")
	Summary(app.Out, "commands", "%d", summary.Total)
	Summary(app.Out, "allowed", "%d%s", summary.Allowed, allowedBreakdown(summary))
	Summary(app.Out, "asked", "%d", summary.Asked)
	Summary(app.Out, "blocked", "%d", summary.Blocked)

	if avoided := summary.PromptsAvoided(); avoided > 0 {
		app.Printf("\n%s you did not have to answer, because an approval had already\n",
			Plural(avoided, "prompt"))
		app.Printf("answered the same question. See what they trust: `intenter approvals`.\n")
	}
}

// allowedBreakdown splits the allow total into its two halves, inline, because
// the halves only mean anything next to the number they add up to.
func allowedBreakdown(summary action.ActivitySummary) string {
	switch {
	case summary.Allowed == 0:
		return ""
	case summary.AllowedBaseline == 0:
		return "  (all by approval)"
	case summary.AllowedByApproval == 0:
		return "  (all reads allowed by the baseline)"
	default:
		return fmt.Sprintf("  (%d by approval, %d %s allowed by the baseline)",
			summary.AllowedByApproval, summary.AllowedBaseline,
			pluralNoun(summary.AllowedBaseline, "read"))
	}
}

// pluralNoun is Plural without the count, for a sentence that already has one.
func pluralNoun(count int, noun string) string {
	if count == 1 {
		return noun
	}
	return noun + "s"
}

// summaryPeriod names the span the counts cover, preferring what was actually
// recorded over the requested window: a 24-hour window holding one event at
// noon is better described by that event than by the window.
func summaryPeriod(summary action.ActivitySummary, window time.Duration) string {
	if summary.FirstAt == nil || summary.LastAt == nil {
		if window == 0 {
			return "everything on record"
		}
		return "last " + window.String()
	}
	first := FormatTime(summary.FirstAt)
	last := FormatTime(summary.LastAt)
	if first == last {
		return first
	}
	return first + " to " + last
}
