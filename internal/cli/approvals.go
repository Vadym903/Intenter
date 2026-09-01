package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/adapter/claude"
	"github.com/Vadym903/Intenter/internal/ipc"
	"github.com/Vadym903/Intenter/internal/storage"
	"github.com/Vadym903/Intenter/internal/updater"
)

// Permission sources, as the `--source` filter and the JSON name them.
const (
	sourceApproval  = "approval"
	sourceAgentRule = "agent-rule"
)

// permissionEntry is one row of the listing in JSON: an approval Intenter
// holds, or a rule the agent holds. Exactly one of the two is set.
type permissionEntry struct {
	Source   string               `json:"source"`
	Approval *ipc.ApprovalSummary `json:"approval,omitempty"`
	Rule     *agentRuleEntry      `json:"rule,omitempty"`
}

// agentRuleEntry is a rule from the agent's own settings.
type agentRuleEntry struct {
	// Key is the stable identity, e.g. "local:Bash(npm run cleanup)". It is
	// what removal takes, because the short label below is only meaningful
	// inside one listing.
	Key string `json:"key"`
	// Label is the short name printed in the table, e.g. "r1".
	Label      string `json:"label"`
	Text       string `json:"text"`
	Scope      string `json:"scope"`
	File       string `json:"file"`
	Changeable bool   `json:"changeable"`
	Reason     string `json:"reason,omitempty"`
	// Broader marks a rule that grants more than the command being removed, so
	// the plan can say what else stops working.
	Broader bool `json:"broader,omitempty"`
}

// newApprovalsCommand builds `intenter approvals`: what is trusted here.
func newApprovalsCommand(app *App) *cobra.Command {
	var (
		project  string
		all      bool
		inactive bool
		recent   bool
		source   string
	)

	cmd := &cobra.Command{
		Use:   "approvals",
		Short: "List what is trusted in this project",
		Long: "Lists everything that lets a command run in the current project without a\n" +
			"prompt. That is two things: the approvals Intenter holds — what each trusts,\n" +
			"how often it was used and where it came from — and the permission rules\n" +
			"Claude itself holds, which allow a command whether or not Intenter ever\n" +
			"imported them. Approvals whose inputs changed are still listed; they simply\n" +
			"stop matching until re-approved.",
		Example: "  intenter approvals\n" +
			"  intenter approvals --recent\n" +
			"  intenter approvals --source approval --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if source != "" && source != sourceApproval && source != sourceAgentRule {
				return Failf(ExitError, "--source must be %q or %q", sourceApproval, sourceAgentRule)
			}

			params := ipc.ListApprovalsParams{IncludeInactive: inactive}
			if !all {
				id, err := projectIDFor(app, project)
				if err != nil {
					return err
				}
				params.ProjectID = &id
			}

			var summaries []ipc.ApprovalSummary
			if source != sourceAgentRule {
				if err := app.Client().Call(cmd.Context(), ipc.MethodListApprovals, params, &summaries); err != nil {
					return daemonError(err)
				}
				if recent {
					sortApprovalsByRecency(summaries)
				}
			}

			// Rules belong to a project's settings files, so `--all` — which
			// spans every project Intenter knows — has none to show.
			var rules []agentRuleEntry
			var unreadable []string
			if source != sourceApproval && !all {
				rules, unreadable = agentRules(app, project)
			}

			if app.JSON {
				// The old array is exactly what `--source approval` returns, so
				// anything parsing it has a one-flag way to keep working.
				if source == sourceApproval {
					return app.PrintJSON(summaries)
				}
				return app.PrintJSON(permissionEntries(summaries, rules))
			}
			printApprovals(app, summaries, rules, unreadable, all, source)
			return nil
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&project, "project", "", "list approvals of another project directory")
	flags.BoolVar(&all, "all", false, "list approvals of every project")
	flags.BoolVar(&inactive, "inactive", false, "include disabled and revoked approvals")
	flags.BoolVar(&recent, "recent", false, "newest first, by when the permission was granted")
	flags.StringVar(&source, "source", "",
		"only one source: `approval` or `agent-rule`")
	return cmd
}

// sortApprovalsByRecency puts the newest grant first. A permission someone
// regrets is nearly always the one they granted a moment ago.
func sortApprovalsByRecency(summaries []ipc.ApprovalSummary) {
	sort.SliceStable(summaries, func(i, j int) bool {
		return summaries[i].CreatedAt.After(summaries[j].CreatedAt)
	})
}

// agentRules reads the allow rules Claude holds for the shell tool in this
// project, and the files whose rules could not be read.
func agentRules(app *App, project string) ([]agentRuleEntry, []string) {
	dir := project
	if dir == "" {
		dir = workingDirOrEmpty()
	}
	reader := claude.NewSettingsReader(app.Platform, app.Config.Claude.SettingsPath)
	rules, unreadable := claude.ProjectRules(reader, dir, claude.ToolBash)

	entries := make([]agentRuleEntry, 0, len(rules))
	for i, rule := range rules {
		entries = append(entries, agentRuleEntry{
			Key:        rule.Key(),
			Label:      fmt.Sprintf("r%d", i+1),
			Text:       rule.Raw,
			Scope:      string(rule.Scope),
			File:       rule.Path,
			Changeable: rule.Changeable,
			Reason:     rule.Reason,
		})
	}
	return entries, unreadable
}

// permissionEntries merges the two sources into one array.
func permissionEntries(summaries []ipc.ApprovalSummary, rules []agentRuleEntry) []permissionEntry {
	entries := make([]permissionEntry, 0, len(summaries)+len(rules))
	for i := range summaries {
		entries = append(entries, permissionEntry{Source: sourceApproval, Approval: &summaries[i]})
	}
	for i := range rules {
		entries = append(entries, permissionEntry{Source: sourceAgentRule, Rule: &rules[i]})
	}
	return entries
}

// printApprovals renders what is trusted here, from both sources.
//
// The two are separate tables rather than one: an agent rule has no kind, no
// use count and no last-used time, and a single table would be half dashes.
func printApprovals(app *App, summaries []ipc.ApprovalSummary, rules []agentRuleEntry,
	unreadable []string, all bool, source string) {

	if len(summaries) == 0 && len(rules) == 0 {
		printNothingTrusted(app, all, source)
		printUnreadable(app, unreadable)
		return
	}

	if len(summaries) > 0 {
		printApprovalTable(app, summaries, all)
	} else if source != sourceAgentRule {
		app.Printf("Intenter holds no approvals here.\n")
	}

	if len(rules) > 0 {
		if len(summaries) > 0 {
			app.Printf("\n")
		}
		printAgentRuleTable(app, rules)
	}

	printUnreadable(app, unreadable)

	if !all && len(summaries) > 0 {
		app.Printf("\nProject: %s\n", projectLabel(summaries[0].ProjectRoot, summaries[0].ProjectID))
	}
}

// printNothingTrusted says the project is empty, and how a permission comes to
// exist — an empty list with no explanation reads as a broken installation.
func printNothingTrusted(app *App, all bool, source string) {
	switch {
	case source == sourceAgentRule && all:
		// Rules belong to a project's settings files, so there is no
		// every-project view of them. Saying "none" would be a claim about
		// something that was never looked at.
		app.Printf("Rules Claude holds are read from a project's settings files, so `--all`\n")
		app.Printf("does not cover them. Run this in a project, or name one with --project.\n")
		return
	case source == sourceAgentRule:
		app.Printf("Claude holds no shell permission rules for this project.\n")
		return
	case all:
		app.Printf("Nothing is approved yet.\n")
	default:
		app.Printf("Nothing is trusted in this project yet.\n")
	}
	app.Printf("A permission appears here when you answer \"Yes, and don't ask again\" in\n")
	app.Printf("Claude, or run `intenter approve <event-id>` (see `intenter history`).\n")
}

// printUnreadable names a settings file whose rules could not be read, so the
// list is never taken for complete when it cannot be.
func printUnreadable(app *App, unreadable []string) {
	for _, path := range unreadable {
		app.Printf("\n! %s could not be read, so the rules it holds are unknown —\n", path)
		app.Printf("  the list above may be missing some.\n")
	}
}

// printApprovalTable renders the approvals of §25: what was approved, where,
// and how much it is used.
func printApprovalTable(app *App, summaries []ipc.ApprovalSummary, all bool) {
	// Scoped to one project, every row would repeat the same path; the space is
	// worth more spent on what is actually trusted.
	headers := []string{"ID", "KIND", "ACTION", "TRUSTED", "USES", "LAST USED", "CREATED", "STATE", "ORIGIN"}
	widths := []int{0, 0, 24, 60, 0, 0, 0, 0, 0}
	if all {
		headers = append(headers[:4], append([]string{"PROJECT"}, headers[4:]...)...)
		widths = []int{0, 0, 20, 44, 24, 0, 0, 0, 0, 0}
	}

	table := NewTable(headers...).WithWidths(widths...)
	for _, summary := range summaries {
		created := summary.CreatedAt
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
			FormatTime(&created),
			string(summary.State),
			string(summary.Origin),
		)
		table.Add(cells...)
	}
	table.Render(app.Out)
}

// printAgentRuleTable renders the rules Claude holds of its own.
func printAgentRuleTable(app *App, rules []agentRuleEntry) {
	app.Printf("Rules Claude holds of its own — these allow a command whether or not\n")
	app.Printf("Intenter ever imported them.\n\n")

	table := NewTable("ID", "RULE", "SCOPE", "FILE").WithWidths(0, 44, 0, 48)
	for _, rule := range rules {
		table.Add(rule.Label, rule.Text, rule.Scope, rule.File)
	}
	table.Render(app.Out)

	for _, rule := range rules {
		if !rule.Changeable {
			app.Printf("\n  %s is %s.\n", rule.Label, rule.Reason)
		}
	}
	app.Printf("\nRemove one with `intenter approval revoke \"%s\"`.\n", rules[0].Key)
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
		newRevokeCommand(app),
	)
	return cmd
}

// removalPlan is everything one revoke would change, gathered before anything
// is written so the user can see it first.
type removalPlan struct {
	// Approval is the approval to revoke, nil when the target was a bare rule.
	Approval *ipc.ApprovalDetail
	// Rules are the agent's own rules that grant the same command.
	Rules []agentRuleEntry
	// AlreadyRevoked is true when there is nothing left to do to the approval.
	AlreadyRevoked bool
}

// removesAnything reports whether the plan would change anything at all.
func (p removalPlan) removesAnything() bool {
	if p.Approval != nil && !p.AlreadyRevoked {
		return true
	}
	for _, rule := range p.Rules {
		if rule.Changeable {
			return true
		}
	}
	return false
}

// blocked returns the rules that cannot be removed here.
func (p removalPlan) blocked() []agentRuleEntry {
	var blocked []agentRuleEntry
	for _, rule := range p.Rules {
		if !rule.Changeable {
			blocked = append(blocked, rule)
		}
	}
	return blocked
}

// newRevokeCommand builds `intenter approval revoke`.
//
// Revoking Intenter's own record is not enough to take a permission away. A
// command with no matching approval is handed back to Claude, which then allows
// it silently through a rule of its own — so the rules go too, after the user
// has seen exactly which files change.
func newRevokeCommand(app *App) *cobra.Command {
	var (
		keepAgentRules bool
		assumeYes      bool
	)

	cmd := &cobra.Command{
		Use:   "revoke <id | rule-key>",
		Short: "Permanently stop a permission from matching",
		Long: "Takes a permission away for good. Give the numeric id of an approval, or the\n" +
			"key of a rule Claude holds (the ID column of `intenter approvals` names both).\n\n" +
			"Because a rule in Claude's own settings allows a command whether or not\n" +
			"Intenter imported it, revoking an approval also removes the rules that grant\n" +
			"the same command — otherwise the command would keep running without a prompt\n" +
			"and the revoke would have changed nothing that matters. Every file that would\n" +
			"change is named first, nothing is written without your confirmation, and each\n" +
			"one is backed up. Rules in a managed policy file, or shared through the\n" +
			"repository, are never edited; they are named instead.\n\n" +
			"Nothing is deleted: a revoked approval stays in the history, and the settings\n" +
			"backup holds the rule that was removed.",
		Example: "  intenter approval revoke 3\n" +
			"  intenter approval revoke 3 --keep-agent-rules\n" +
			"  intenter approval revoke \"local:Bash(npm run cleanup)\"",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := orBackground(cmd.Context())

			// A machine-readable removal has nobody to ask, so it has to be
			// told in advance that it may proceed. Silently going ahead because
			// the output is JSON would be the wrong way to resolve that.
			if app.JSON && !assumeYes {
				return Failf(ExitError, "--json cannot ask for a confirmation; add --yes if you mean it")
			}

			plan, err := buildRemovalPlan(ctx, app, args[0], keepAgentRules, assumeYes)
			if err != nil {
				return err
			}
			if plan.AlreadyRevoked && len(plan.Rules) == 0 {
				if app.JSON {
					id := plan.Approval.Approval.ID
					return app.PrintJSON(revokeResult{
						ApprovalID:   &id,
						RulesRemoved: []agentRuleEntry{},
						RulesKept:    []agentRuleEntry{},
					})
				}
				app.Printf("Approval %d was already revoked. Nothing was changed.\n", plan.Approval.Approval.ID)
				return nil
			}

			if !app.JSON {
				printRemovalPlan(app, plan)
			}
			confirmed, err := confirmRemoval(app, assumeYes)
			if err != nil {
				return err
			}
			if !confirmed {
				return nil
			}

			result, execErr := executeRemoval(ctx, app, plan)
			if app.JSON {
				if err := app.PrintJSON(result); err != nil {
					return Fail(ExitError, err)
				}
			}
			return execErr
		},
	}

	flags := cmd.Flags()
	flags.BoolVar(&keepAgentRules, "keep-agent-rules", false,
		"revoke only Intenter's approval, leaving Claude's own rules in place")
	flags.BoolVar(&assumeYes, "yes", false, "do not ask for confirmation; the plan is still printed")
	return cmd
}

// buildRemovalPlan resolves the argument and collects everything that grants
// the same command.
func buildRemovalPlan(ctx context.Context, app *App, target string, keepAgentRules, assumeYes bool) (removalPlan, error) {
	var plan removalPlan

	// A rule key rather than an approval id.
	if _, err := strconv.ParseInt(strings.TrimSpace(target), 10, 64); err != nil {
		rules, _ := agentRules(app, "")
		for _, rule := range rules {
			if rule.Key == target {
				plan.Rules = []agentRuleEntry{rule}
				return plan, nil
			}
			// A short label is only meaningful inside the listing that printed
			// it: edit the settings and `r1` names a different rule. It is
			// accepted for convenience, but never unattended, because there
			// nobody sees the plan that would show the substitution.
			if rule.Label == target {
				if assumeYes {
					return plan, Failf(ExitError,
						"%q is a label from one listing, not a stable name — with --yes it could "+
							"name a different rule than the one you saw. Use the key: %q",
						target, rule.Key)
				}
				plan.Rules = []agentRuleEntry{rule}
				return plan, nil
			}
		}
		return plan, Failf(ExitError,
			"%q is neither an approval id nor a rule in this project — run `intenter approvals` to see what is here",
			target)
	}

	id, err := parseID(target)
	if err != nil {
		return plan, err
	}
	var detail ipc.ApprovalDetail
	if err := app.Client().Call(ctx, ipc.MethodGetApproval, ipc.GetApprovalParams{ID: id}, &detail); err != nil {
		return plan, daemonError(err)
	}
	plan.Approval = &detail
	plan.AlreadyRevoked = detail.Approval.State == action.ApprovalRevoked

	if keepAgentRules {
		return plan, nil
	}
	// Scoped to the approval's own project, not the directory the command was
	// typed in. `intenter approval revoke 5` from somewhere else must not go
	// looking for rules in whatever project happens to be the working one.
	rules, _ := agentRules(app, detail.ProjectRoot)
	plan.Rules = rulesGranting(app, detail.Approval, rules)
	return plan, nil
}

// rulesGranting finds the agent's rules that permit the command an approval was
// created from. It reuses the same matcher the gate uses to recognize consent,
// so the two agree about which rule covers what.
func rulesGranting(app *App, approval action.Approval, rules []agentRuleEntry) []agentRuleEntry {
	keys := map[string]bool{}
	if approval.OriginRef != "" {
		keys[approval.OriginRef] = true
	}

	if raw := approval.CreatedFromRawCommand; raw != "" {
		reader := claude.NewSettingsReader(app.Platform, app.Config.Claude.SettingsPath)
		files := reader.Discover(workingDirOrEmpty())
		if consent := claude.Consent(claude.ToolBash, raw, files); consent != nil {
			for _, key := range consent.RuleKeys {
				keys[key] = true
			}
		}
	}

	var granting []agentRuleEntry
	for _, rule := range rules {
		if !keys[rule.Key] {
			continue
		}
		// `Bash(npm run *)` grants far more than the one command being revoked,
		// and removing it takes all of that away. The user asked about one
		// command; they have to be told the rule is wider before they agree.
		if content := ruleContent(rule.Text); content != "" && content != approval.CreatedFromRawCommand {
			rule.Broader = true
		}
		granting = append(granting, rule)
	}
	return granting
}

// ruleContent is the text inside a rule's parentheses, e.g. `npm run *` from
// `Bash(npm run *)`.
func ruleContent(raw string) string {
	open := strings.Index(raw, "(")
	if open < 0 || !strings.HasSuffix(raw, ")") {
		return ""
	}
	return raw[open+1 : len(raw)-1]
}

// printRemovalPlan says what will stop being trusted, before anything changes.
func printRemovalPlan(app *App, plan removalPlan) {
	// With nothing removable, announcing what will stop being trusted and then
	// listing nothing would be the one thing this command must never do: claim
	// a change it is not going to make.
	if !plan.removesAnything() {
		app.Printf("Nothing here can be removed:\n")
	} else {
		app.Printf("This will stop being trusted:\n\n")
	}

	if plan.Approval != nil {
		approval := plan.Approval.Approval
		if plan.AlreadyRevoked {
			app.Printf("  approval %d — already revoked, nothing to do\n", approval.ID)
		} else {
			app.Printf("  approval %d — %s\n", approval.ID, approval.Summary())
		}
	}
	for _, rule := range plan.Rules {
		if rule.Changeable {
			app.Printf("  rule %s — %s\n", rule.Text, rule.Scope)
			app.Printf("      removed from %s\n", rule.File)
			if rule.Broader {
				app.Printf("      ! this rule grants more than the command you named —\n")
				app.Printf("        everything matching %s stops being allowed too\n", rule.Text)
			}
		}
	}

	for _, rule := range plan.blocked() {
		app.Printf("\n! %s stays: it is %s.\n", rule.Text, rule.Reason)
		app.Printf("  It is in %s.\n", rule.File)
	}

	if plan.Approval != nil && len(plan.Rules) == 0 {
		app.Printf("\nClaude holds no rule of its own for this command.\n")
	}
	if plan.removesAnything() {
		app.Printf("\nEach file that changes is backed up first, and nothing is deleted from\n")
		app.Printf("the record.\n")
	}
}

// confirmRemoval asks before anything is written.
func confirmRemoval(app *App, assumeYes bool) (bool, error) {
	if assumeYes {
		return true, nil
	}
	if !updater.Interactive(os.Stdin, os.Stdout) {
		return false, Failf(ExitError, "nothing to read an answer from; re-run with --yes")
	}
	app.Printf("\nRemove it? [y/N]: ")
	if !readsYes(os.Stdin) {
		app.Printf("Nothing was changed.\n")
		return false, nil
	}
	return true, nil
}

// revokeResult is the `--json` shape of a removal: what went, what stayed, and
// the answer that matters — whether the command can still run unprompted.
type revokeResult struct {
	ApprovalID   *int64           `json:"approval_id,omitempty"`
	Revoked      bool             `json:"revoked"`
	RulesRemoved []agentRuleEntry `json:"rules_removed"`
	RulesKept    []agentRuleEntry `json:"rules_kept"`
	StillAllowed bool             `json:"still_allowed"`
}

// executeRemoval applies the plan and reports what is true afterwards.
func executeRemoval(ctx context.Context, app *App, plan removalPlan) (revokeResult, error) {
	result := revokeResult{RulesRemoved: []agentRuleEntry{}, RulesKept: []agentRuleEntry{}}
	if !app.JSON {
		app.Printf("\n")
	}

	if plan.Approval != nil {
		id := plan.Approval.Approval.ID
		result.ApprovalID = &id
	}
	if plan.Approval != nil && !plan.AlreadyRevoked {
		var updated ipc.ApprovalDetail
		if err := app.Client().Call(ctx, ipc.MethodSetApprovalState,
			ipc.SetApprovalStateParams{ID: plan.Approval.Approval.ID, State: action.ApprovalRevoked},
			&updated); err != nil {
			return result, daemonError(err)
		}
		result.Revoked = true
		if !app.JSON {
			app.Printf("Approval %d revoked; the record is kept for the history.\n", plan.Approval.Approval.ID)
		}
	}

	settingsChanged := false
	var failed []agentRuleEntry
	for _, rule := range plan.Rules {
		if !rule.Changeable {
			result.RulesKept = append(result.RulesKept, rule)
			continue
		}
		removal, err := claude.RemoveAllowRule(rule.File, app.Platform.DataDir(), rule.Text, time.Now())
		if err != nil {
			// Stopping here would leave the approval revoked, some rules gone
			// and the user with an error instead of an account of what actually
			// happened. Finish the rest and report all of it.
			app.Warnf("warning: %s could not be removed from %s: %v\n", rule.Text, rule.File, err)
			failed = append(failed, rule)
			result.RulesKept = append(result.RulesKept, rule)
			continue
		}
		if !removal.Removed {
			// The file changed since it was listed. Removing something else
			// because the text moved would be worse than doing nothing.
			app.Warnf("warning: %s is no longer in %s; nothing was removed from it\n", rule.Text, rule.File)
			continue
		}
		settingsChanged = true
		result.RulesRemoved = append(result.RulesRemoved, rule)
		if !app.JSON {
			app.Printf("Removed %s from %s\n", rule.Text, rule.File)
			if removal.BackupPath != "" {
				app.Printf("  the previous file is at %s\n", removal.BackupPath)
			}
		}
	}

	result.StillAllowed = len(result.RulesKept) > 0
	if !app.JSON {
		printRemovalOutcome(app, plan, settingsChanged, failed)
	}
	if len(failed) > 0 {
		return result, Failf(ExitError, "%s could not be removed", Plural(len(failed), "rule"))
	}
	return result, nil
}

// printRemovalOutcome says what is true now — including the one answer that
// matters most, whether the command can still run without a prompt.
func printRemovalOutcome(app *App, plan removalPlan, settingsChanged bool, failed []agentRuleEntry) {
	// A rule that survived because the write failed is exactly as permitting as
	// one that survived because it was refused, so it is reported the same way.
	if remaining := append(plan.blocked(), failed...); len(remaining) > 0 {
		app.Printf("\nThe command can still run without a prompt: %s still allows it.\n",
			remaining[0].Text)
		app.Printf("Remove it from %s yourself to finish the job.\n", remaining[0].File)
		return
	}

	app.Printf("\nThe next run of that command will ask again.\n")
	if settingsChanged {
		// Claude reads its own settings when a session starts, so a rule taken
		// out from under a running session may not be noticed until it restarts.
		app.Printf("Claude reads its permission rules when a session starts. If it still runs\n")
		app.Printf("without asking, restart the session.\n")
	}
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

// storedApprovals reads the approvals straight from the database, for the
// commands that keep answering when the daemon is down. It mirrors the daemon's
// own `list_approvals` so the two views cannot describe the same row
// differently; the CLI never migrates, so the schema stays the daemon's.
func storedApprovals(ctx context.Context, app *App, params ipc.ListApprovalsParams) ([]ipc.ApprovalSummary, error) {
	store, closeStore, err := openReadOnlyStore(app)
	if err != nil {
		return nil, err
	}
	defer closeStore()

	filter := storage.ApprovalFilter{IncludeInactive: params.IncludeInactive, Limit: params.Limit}
	if params.ProjectID != nil {
		filter.ProjectID = *params.ProjectID
	}
	approvals, err := store.Approvals.List(ctx, filter)
	if err != nil {
		return nil, err
	}

	roots := map[string]string{}
	if projects, listErr := store.Projects.List(ctx); listErr == nil {
		for _, project := range projects {
			roots[project.ID] = project.RootPath
		}
	}

	summaries := make([]ipc.ApprovalSummary, 0, len(approvals))
	for i := range approvals {
		record := &approvals[i]
		summaries = append(summaries, ipc.ApprovalSummary{
			ID:          record.ID,
			Kind:        record.Kind,
			SemanticOps: record.SemanticOps,
			Summary:     record.Summary(),
			ProjectRoot: roots[record.ProjectID],
			ProjectID:   record.ProjectID,
			UseCount:    record.UseCount,
			LastUsedAt:  record.LastUsedAt,
			State:       record.State,
			Origin:      record.Origin,
			CreatedAt:   record.CreatedAt,
		})
	}
	return summaries, nil
}

// workingDirOrEmpty is the current directory, or "" when it cannot be
// determined. Callers that only need it to look something up prefer an empty
// answer to an error.
func workingDirOrEmpty() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return dir
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

	return action.ProjectID(projectRootFor(dir)), nil
}

// projectRootFor is the canonical workspace root a directory belongs to: the
// readable half of the same answer projectIDFor hashes.
func projectRootFor(dir string) string {
	root := gitRootOrSelf(dir)
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	return filepath.Clean(root)
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
