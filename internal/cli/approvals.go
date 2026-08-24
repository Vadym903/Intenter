package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/ipc"
)

// newApprovalsCommand builds `intenter approvals`: what is trusted here.
func newApprovalsCommand(app *App) *cobra.Command {
	var (
		project  string
		all      bool
		inactive bool
	)

	cmd := &cobra.Command{
		Use:   "approvals",
		Short: "List what is trusted in this project",
		Long: "Lists the approvals that apply to the current project directory: what each\n" +
			"one trusts (the resolved effects, targets and scopes), how often it was used,\n" +
			"and where it came from (the CLI or an imported Claude rule). Approvals whose\n" +
			"inputs changed are still listed; they simply stop matching until re-approved.",
		Example: "  intenter approvals\n" +
			"  intenter approvals --inactive\n" +
			"  intenter approvals --all --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			params := ipc.ListApprovalsParams{IncludeInactive: inactive}
			if !all {
				id, err := projectIDFor(app, project)
				if err != nil {
					return err
				}
				params.ProjectID = &id
			}

			var summaries []ipc.ApprovalSummary
			if err := app.Client().Call(cmd.Context(), ipc.MethodListApprovals, params, &summaries); err != nil {
				return daemonError(err)
			}
			if app.JSON {
				return app.PrintJSON(summaries)
			}
			printApprovals(app, summaries, all)
			return nil
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&project, "project", "", "list approvals of another project directory")
	flags.BoolVar(&all, "all", false, "list approvals of every project")
	flags.BoolVar(&inactive, "inactive", false, "include disabled and revoked approvals")
	return cmd
}

// printApprovals renders the table of §25: what was approved, where, and how
// much it is used.
func printApprovals(app *App, summaries []ipc.ApprovalSummary, all bool) {
	if len(summaries) == 0 {
		if all {
			app.Printf("Nothing is approved yet.\n")
		} else {
			app.Printf("Nothing is approved in this project yet.\n")
		}
		app.Printf("Approve an evaluated command with `intenter approve <event-id>` (see `intenter history`).\n")
		return
	}

	// Scoped to one project, every row would repeat the same path; the space is
	// worth more spent on what is actually trusted.
	headers := []string{"ID", "KIND", "ACTION", "TRUSTED", "USES", "LAST USED", "STATE", "ORIGIN"}
	widths := []int{0, 0, 24, 72, 0, 0, 0, 0}
	if all {
		headers = append(headers[:4], append([]string{"PROJECT"}, headers[4:]...)...)
		widths = []int{0, 0, 20, 52, 28, 0, 0, 0, 0}
	}

	table := NewTable(headers...).WithWidths(widths...)
	for _, summary := range summaries {
		cells := []string{
			strconv.FormatInt(summary.ID, 10),
			string(summary.Kind),
			opsString(summary.SemanticOps),
			Dash(summary.Summary),
		}
		if all {
			cells = append(cells, Dash(projectLabel(summary.ProjectRoot, summary.ProjectID)))
		}
		cells = append(cells,
			strconv.FormatInt(summary.UseCount, 10),
			FormatTime(summary.LastUsedAt),
			string(summary.State),
			string(summary.Origin),
		)
		table.Add(cells...)
	}
	table.Render(app.Out)

	if !all && len(summaries) > 0 {
		app.Printf("\nProject: %s\n", projectLabel(summaries[0].ProjectRoot, summaries[0].ProjectID))
	}
}

// newApprovalCommand builds `intenter approval show|disable|enable|revoke`.
func newApprovalCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "approval",
		Short: "Inspect and change one approval",
		Long: "Works on a single approval by its id (the ID column of `intenter approvals`):\n" +
			"show what it covers and what would stop it matching, or disable, enable or\n" +
			"revoke it. Nothing is ever deleted; a revoked approval stays in the history.",
		Example: "  intenter approval show 3\n" +
			"  intenter approval disable 3\n" +
			"  intenter approval revoke 3",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "show <id>",
			Short: "Show everything one approval covers",
			Long: "Prints the approval's kind, the effects, targets and scopes it trusts, the\n" +
				"fingerprints it depends on (with their current status), when it was created\n" +
				"and last used, and how many times it matched.",
			Example: "  intenter approval show 3\n" +
				"  intenter approval show 3 --json",
			Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				id, err := parseID(args[0])
				if err != nil {
					return err
				}
				var detail ipc.ApprovalDetail
				if err := app.Client().Call(cmd.Context(), ipc.MethodGetApproval,
					ipc.GetApprovalParams{ID: id}, &detail); err != nil {
					return daemonError(err)
				}
				if app.JSON {
					return app.PrintJSON(detail)
				}
				printApprovalDetail(app, detail)
				return nil
			},
		},
		approvalStateCommand(app, "disable", "Temporarily stop an approval from matching",
			action.ApprovalDisabled, "disabled"),
		approvalStateCommand(app, "enable", "Let a disabled approval match again",
			action.ApprovalActive, "enabled"),
		approvalStateCommand(app, "revoke", "Permanently stop an approval from matching",
			action.ApprovalRevoked, "revoked"),
	)
	return cmd
}

// approvalStateCommand builds one state transition. The record itself is never
// deleted, so a revoked approval stays in the history (I-15).
func approvalStateCommand(app *App, use, short string, state action.ApprovalState, verb string) *cobra.Command {
	return &cobra.Command{
		Use:   use + " <id>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			var detail ipc.ApprovalDetail
			if err := app.Client().Call(cmd.Context(), ipc.MethodSetApprovalState,
				ipc.SetApprovalStateParams{ID: id, State: state}, &detail); err != nil {
				return daemonError(err)
			}
			if app.JSON {
				return app.PrintJSON(detail)
			}
			app.Printf("Approval %d %s: %s\n", id, verb, detail.Approval.Summary())
			if state == action.ApprovalRevoked {
				app.Printf("Revocation is permanent; the record is kept for the history.\n")
			}
			return nil
		},
	}
}

// newApproveCommand builds `intenter approve <event-id>`: the explicit path
// that turns an evaluated command into remembered trust (§19.3 path 1).
func newApproveCommand(app *App) *cobra.Command {
	var (
		semantic bool
		note     string
	)

	cmd := &cobra.Command{
		Use:   "approve <event-id>",
		Short: "Remember the effects of an evaluated command",
		Long: "Creates an approval from an audit event (the event id printed by the hook or\n" +
			"shown by `intenter history`), recording the effects that command resolved to\n" +
			"and a fingerprint of every mutable input the resolution read. By default the\n" +
			"approval is exact (these targets); --semantic approves the effect envelope so\n" +
			"an equivalent command with different spelling matches too. Either kind stops\n" +
			"matching as soon as the resolved effects or a fingerprint change. Only a fully\n" +
			"resolved command that no safety rule objects to can be approved: a blocked\n" +
			"command, one a rule always asks about, or one Intenter could not resolve\n" +
			"is refused.",
		Example: "  intenter approve 12\n" +
			"  intenter approve 12 --semantic --note \"test runs may write build/\"",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eventID, err := parseID(args[0])
			if err != nil {
				return err
			}

			kind := action.ApprovalExact
			if semantic {
				kind = action.ApprovalSemantic
			}

			var detail ipc.ApprovalDetail
			callErr := app.Client().Call(cmd.Context(), ipc.MethodCreateApproval,
				ipc.CreateApprovalParams{AuditEventID: eventID, Kind: kind, Note: note}, &detail)
			if callErr != nil {
				return daemonError(callErr)
			}
			if app.JSON {
				return app.PrintJSON(detail)
			}

			app.Printf("Approved as %s approval %d.\n", strings.ToLower(string(detail.Approval.Kind)), detail.Approval.ID)
			app.Printf("  trusted: %s\n", detail.Approval.Summary())
			if len(detail.Approval.Conditions) > 0 {
				app.Printf("  valid while these stay unchanged:\n")
				for _, condition := range detail.Approval.Conditions {
					app.Printf("    %s\n", condition.Key)
				}
			}
			return nil
		},
	}

	flags := cmd.Flags()
	flags.BoolVar(&semantic, "semantic", false,
		"approve the effect envelope rather than these exact targets")
	flags.StringVar(&note, "note", "", "a note to remember why this was approved")
	return cmd
}

// printApprovalDetail shows everything an approval covers and what would
// withdraw it.
func printApprovalDetail(app *App, detail ipc.ApprovalDetail) {
	approval := detail.Approval

	app.Printf("Approval %d (%s, %s)\n", approval.ID, approval.Kind, approval.State)
	Field(app.Out, "project", "%s", Dash(projectLabel(detail.ProjectRoot, approval.ProjectID)))
	Field(app.Out, "action", "%s", opsString(approval.SemanticOps))

	FieldHeading(app.Out, "effects")
	for _, entry := range approval.Envelope {
		app.Printf("    %s\n", entry.String())
	}
	if len(approval.Targets) > 0 {
		FieldHeading(app.Out, "targets")
		for _, target := range approval.Targets {
			app.Printf("    %s\n", target)
		}
	}
	for _, network := range approval.Network {
		Field(app.Out, "network", "%s", network.String())
	}

	if len(approval.Conditions) > 0 {
		FieldHeading(app.Out, "valid while unchanged")
		conditions := NewPairs(KeyWidth)
		for _, condition := range approval.Conditions {
			conditions.Add(condition.Key, FingerprintShort(condition.Value))
		}
		conditions.Render(app.Out)
	}

	if approval.OriginRef != "" {
		Field(app.Out, "origin", "%s (%s)", approval.Origin, approval.OriginRef)
	} else {
		Field(app.Out, "origin", "%s", approval.Origin)
	}
	if approval.CreatedFromRawCommand != "" {
		Field(app.Out, "created by", "%s", approval.CreatedFromRawCommand)
	}
	if approval.CreatedFromEventID != nil {
		Field(app.Out, "from event", "%d", *approval.CreatedFromEventID)
	}
	Field(app.Out, "created", "%s", FormatTime(&approval.CreatedAt))
	Field(app.Out, "used", "%d time(s), last %s", approval.UseCount, FormatTime(approval.LastUsedAt))
	if approval.Note != "" {
		Field(app.Out, "note", "%s", approval.Note)
	}

	if len(detail.RecentEvents) > 0 {
		FieldHeading(app.Out, "history")
		events := NewPairs(0)
		for _, event := range detail.RecentEvents {
			events.Add(FormatTime(&event.At), string(event.Type))
		}
		events.Render(app.Out)
	}
}

// projectIDFor resolves a project directory to the identity approvals are
// scoped by. It mirrors the daemon's own rule: the identity is the hash of the
// canonical workspace root (§16.2).
func projectIDFor(app *App, override string) (string, error) {
	dir := override
	if dir == "" {
		working, err := os.Getwd()
		if err != nil {
			return "", Fail(ExitError, fmt.Errorf("could not determine the working directory: %w", err))
		}
		dir = working
	}

	root := gitRootOrSelf(dir)
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	return action.ProjectID(filepath.Clean(root)), nil
}

// gitRootOrSelf walks up looking for `.git`, falling back to the directory.
func gitRootOrSelf(dir string) string {
	current, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	for {
		if _, statErr := os.Stat(filepath.Join(current, ".git")); statErr == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return dir
		}
		current = parent
	}
}

// projectLabel prefers the readable root over the identity hash.
func projectLabel(root, id string) string {
	if root != "" {
		return root
	}
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// opsString renders an ordered operation list, e.g. "RUN_SCRIPT>FS_DELETE".
func opsString(ops []action.SemanticOp) string {
	parts := make([]string, 0, len(ops))
	for _, op := range ops {
		parts = append(parts, string(op))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ">")
}

// parseID reads a positive integer argument.
func parseID(text string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	if err != nil || id <= 0 {
		return 0, Failf(ExitError, "%q is not a valid id", text)
	}
	return id, nil
}
