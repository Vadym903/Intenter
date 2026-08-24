package policy

import (
	"fmt"
	"net"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/config"
	"github.com/Vadym903/Intenter/internal/platform"
)

// Outcome is what the hard-rule pass concludes about an action (§18.2).
type Outcome string

const (
	OutcomePass      Outcome = "PASS"
	OutcomeAskAlways Outcome = "ASK_ALWAYS"
	OutcomeBlock     Outcome = "BLOCK"
)

// outcomeRank orders outcomes from weakest to strongest.
var outcomeRank = map[Outcome]int{OutcomePass: 0, OutcomeAskAlways: 1, OutcomeBlock: 2}

// Stronger returns the stronger of two outcomes: BLOCK > ASK_ALWAYS > PASS.
func Stronger(a, b Outcome) Outcome {
	if outcomeRank[b] > outcomeRank[a] {
		return b
	}
	return a
}

// Finding is one hard rule firing, with the reason a user is shown.
type Finding struct {
	// Rule is the identifier from §18.2, e.g. "R2".
	Rule    string
	Outcome Outcome
	Reason  string
}

// Findings is the ordered result of the hard-rule pass.
type Findings []Finding

// Outcome returns the strongest outcome among the findings.
func (f Findings) Outcome() Outcome {
	strongest := OutcomePass
	for _, finding := range f {
		strongest = Stronger(strongest, finding.Outcome)
	}
	return strongest
}

// Strongest returns the finding that decided the outcome. Ties are broken by
// rule id so the same action always produces the same explanation.
func (f Findings) Strongest() (Finding, bool) {
	best, found := Finding{}, false
	for _, finding := range f {
		switch {
		case !found:
			best, found = finding, true
		case outcomeRank[finding.Outcome] > outcomeRank[best.Outcome]:
			best = finding
		case outcomeRank[finding.Outcome] == outcomeRank[best.Outcome] && finding.Rule < best.Rule:
			best = finding
		}
	}
	return best, found
}

// Reasons renders every finding for the explanation list.
func (f Findings) Reasons() []string {
	out := make([]string, 0, len(f))
	for _, finding := range f {
		out = append(out, fmt.Sprintf("%s: %s", finding.Rule, finding.Reason))
	}
	return out
}

// Input is everything a policy pass runs over. The path rules come from the
// platform layer so scope comparisons use the same case rule as classification.
type Input struct {
	Action  *action.ResolvedAction
	Context *action.Context
	Config  config.Config
	Rules   platform.PathRules
	// Agent names the adapter the request came from. The rules never branch on
	// it; approvals record it as provenance (§19.1).
	Agent string
}

// HardRules evaluates R1–R12 over everything that was parsed and resolved, even
// when the action's overall status is UNRESOLVED or PARSE_FAILED (§18.1 step 1).
// No approval, import or baseline rule can override what it returns (I-4).
func HardRules(in Input) Findings {
	if in.Action == nil {
		return nil
	}
	var findings Findings

	for i := range in.Action.Effects {
		findings = append(findings, effectRules(&in.Action.Effects[i])...)
	}
	for i := range in.Action.Commands {
		findings = append(findings, commandRules(&in.Action.Commands[i], in)...)
	}
	findings = append(findings, workspaceDeleteRules(in)...)
	if finding, ok := ruleR13(in); ok {
		findings = append(findings, finding)
	}

	return dedupeFindings(findings)
}

// ruleR13 forces a prompt for a command Intenter could not examine in full:
// a line longer than it evaluates, or a resolution that ran out of time before
// reaching every command. Unlike an unresolved command it looked at — which is
// deferred to the agent's native flow — an unexamined tail may hold anything, so
// it must not be handed to a string rule or run under bypassPermissions. This is
// ASK_ALWAYS, which forces Claude's own prompt.
func ruleR13(in Input) (Finding, bool) {
	if in.Action == nil || !in.Action.Incomplete {
		return Finding{}, false
	}
	reason := in.Action.IncompleteReason
	if reason == "" {
		reason = "part of the command line could not be examined"
	}
	return Finding{"R13", OutcomeAskAlways, reason}, true
}

// effectRules applies the rules that look at one effect: R1–R6, R10–R12.
func effectRules(effect *action.Effect) Findings {
	var findings Findings

	if finding, ok := ruleR10(effect); ok {
		findings = append(findings, finding)
	}
	if finding, ok := ruleR11(effect); ok {
		findings = append(findings, finding)
	}
	if finding, ok := ruleR12(effect); ok {
		findings = append(findings, finding)
	}

	target := effect.Target
	if target == nil {
		return findings
	}

	if finding, ok := ruleR1(effect, target); ok {
		findings = append(findings, finding)
	}
	if finding, ok := ruleR5(effect, target); ok {
		findings = append(findings, finding)
	}
	if finding, ok := ruleR6(target); ok {
		findings = append(findings, finding)
	}

	if effect.Type == action.EffectDelete {
		switch target.Scope {
		case action.ScopeHome:
			if finding, ok := ruleR2(effect, target); ok {
				findings = append(findings, finding)
			} else {
				findings = append(findings, ruleR4(target))
			}
		case action.ScopeOutsideWorkspace:
			if finding, ok := ruleR3(effect, target); ok {
				findings = append(findings, finding)
			} else {
				findings = append(findings, ruleR4(target))
			}
		}
	}
	return findings
}

// ruleR1 blocks destructive effects on system locations.
func ruleR1(effect *action.Effect, target *action.Target) (Finding, bool) {
	if target.Scope != action.ScopeSystem {
		return Finding{}, false
	}
	switch effect.Type {
	case action.EffectDelete:
		return Finding{"R1", OutcomeBlock, fmt.Sprintf(
			"deleting %s, which is part of the operating system", target.Display)}, true
	case action.EffectWrite, action.EffectCreate:
		return Finding{"R1", OutcomeBlock, fmt.Sprintf(
			"writing to %s, which is part of the operating system", target.Display)}, true
	}
	return Finding{}, false
}

// ruleR2 blocks a broad delete inside the home directory.
func ruleR2(effect *action.Effect, target *action.Target) (Finding, bool) {
	reason := ""
	switch {
	case effect.HasFlag(action.EffectFlagRecursive):
		reason = "recursively deleting"
	case effect.HasFlag(action.EffectFlagWildcard), target.HasFlag(action.FlagWildcard):
		reason = "deleting everything matching"
	case target.HasFlag(action.FlagBroad):
		reason = "deleting"
	case target.HasFlag(action.FlagSensitive):
		reason = "deleting"
	default:
		return Finding{}, false
	}
	return Finding{"R2", OutcomeBlock, fmt.Sprintf(
		"%s %s, which is in your home directory", reason, target.Display)}, true
}

// ruleR3 blocks a broad delete outside the workspace, with a carve-out for the
// temp directory that does not extend to the temp root itself.
func ruleR3(effect *action.Effect, target *action.Target) (Finding, bool) {
	broad := effect.HasFlag(action.EffectFlagRecursive) ||
		effect.HasFlag(action.EffectFlagWildcard) ||
		target.HasFlag(action.FlagWildcard) ||
		target.IsDir
	if !broad {
		return Finding{}, false
	}
	if target.HasFlag(action.FlagTemp) && !target.HasFlag(action.FlagBroad) {
		return Finding{}, false
	}
	if target.HasFlag(action.FlagTemp) {
		return Finding{"R3", OutcomeBlock, fmt.Sprintf(
			"deleting %s, the temporary directory itself", target.Display)}, true
	}
	return Finding{"R3", OutcomeBlock, fmt.Sprintf(
		"deleting %s, which is outside this project", target.Display)}, true
}

// ruleR4 asks about the narrower deletes R2 and R3 let through.
func ruleR4(target *action.Target) Finding {
	return Finding{"R4", OutcomeAskAlways, fmt.Sprintf(
		"deleting %s, which is outside this project", target.Display)}
}

// ruleR5 protects credential files: reading one asks, changing one is blocked.
func ruleR5(effect *action.Effect, target *action.Target) (Finding, bool) {
	if !target.HasFlag(action.FlagSensitive) {
		return Finding{}, false
	}
	switch effect.Type {
	case action.EffectRead:
		return Finding{"R5", OutcomeAskAlways, fmt.Sprintf(
			"reading %s, which holds credentials or Intenter's own configuration", target.Display)}, true
	case action.EffectWrite, action.EffectCreate, action.EffectDelete:
		return Finding{"R5", OutcomeBlock, fmt.Sprintf(
			"changing %s, which holds credentials or Intenter's own configuration", target.Display)}, true
	}
	return Finding{}, false
}

// ruleR6 asks about a path that leaves the workspace through `..` or a symlink.
func ruleR6(target *action.Target) (Finding, bool) {
	switch {
	case target.HasFlag(action.FlagSymlinkEscape):
		return Finding{"R6", OutcomeAskAlways, fmt.Sprintf(
			"%s is a link that points outside this project, to %s", target.Raw, target.Display)}, true
	case target.HasFlag(action.FlagTraversal):
		return Finding{"R6", OutcomeAskAlways, fmt.Sprintf(
			"%s leaves this project and resolves to %s", target.Raw, target.Display)}, true
	}
	return Finding{}, false
}

// ruleR10 asks about elevated privileges and credentials written into a command.
func ruleR10(effect *action.Effect) (Finding, bool) {
	switch {
	case effect.HasFlag(action.EffectFlagElevated):
		return Finding{"R10", OutcomeAskAlways, "the command runs with elevated privileges"}, true
	case effect.HasFlag(action.EffectFlagInlineCredential):
		return Finding{"R10", OutcomeAskAlways, "the command carries a credential in its arguments"}, true
	}
	return Finding{}, false
}

// ruleR11 asks about disabled certificate verification against a remote host.
func ruleR11(effect *action.Effect) (Finding, bool) {
	if effect.Type != action.EffectNetwork || !effect.HasFlag(action.EffectFlagInsecureTLS) {
		return Finding{}, false
	}
	if effect.Network == nil || isLocalhost(effect.Network.Host) {
		return Finding{}, false
	}
	return Finding{"R11", OutcomeAskAlways, fmt.Sprintf(
		"contacting %s with certificate verification disabled", effect.Network.Host)}, true
}

// ruleR12 asks about a command that executes whatever another command streams
// into it, such as `curl … | sh`.
func ruleR12(effect *action.Effect) (Finding, bool) {
	if effect.Type != action.EffectExecute || effect.Program == nil {
		return Finding{}, false
	}
	if !effect.Program.Streamed || effect.Program.Resolution != action.ProgramUnresolved {
		return Finding{}, false
	}
	return Finding{"R12", OutcomeAskAlways, fmt.Sprintf(
		"%s executes content piped into it from another command", effect.Program.Name)}, true
}

// commandRules applies the rules that look at a whole command: R7 and R8.
func commandRules(command *action.ResolvedCommand, in Input) Findings {
	var findings Findings

	if finding, ok := ruleR7(command, in); ok {
		findings = append(findings, finding)
	}
	if finding, ok := ruleR8(command); ok {
		findings = append(findings, finding)
	}
	for i := range command.Children {
		findings = append(findings, commandRules(&command.Children[i], in)...)
	}
	return findings
}

// ruleR7 asks about a push that rewrites or removes history on a branch nobody
// should lose, and about pushes that cover many refs at once.
func ruleR7(command *action.ResolvedCommand, in Input) (Finding, bool) {
	if command.SemanticOp != action.OpGitPush {
		return Finding{}, false
	}

	forced, deletes, broad := false, false, false
	for i := range command.Effects {
		effect := &command.Effects[i]
		forced = forced || effect.HasFlag(action.EffectFlagForce)
		deletes = deletes || effect.HasFlag(action.EffectFlagDelete)
		broad = broad || effect.HasFlag(action.EffectFlagBroad)
	}

	switch {
	case deletes:
		return Finding{"R7", OutcomeAskAlways, "the push deletes a branch on the remote"}, true
	case broad:
		return Finding{"R7", OutcomeAskAlways, "the push updates every branch or tag at once"}, true
	case !forced:
		return Finding{}, false
	}

	branch, known := "", false
	if command.Git != nil {
		branch, known = command.Git.Branch, command.Git.BranchKnown
	}
	if !known {
		return Finding{"R7", OutcomeAskAlways,
			"the push rewrites history on a branch Intenter could not determine"}, true
	}
	if isProtectedBranch(branch, in) {
		return Finding{"R7", OutcomeAskAlways, fmt.Sprintf(
			"the push rewrites history on %s, a protected branch", branch)}, true
	}
	return Finding{}, false
}

// ruleR8 asks before uncommitted work is overwritten.
func ruleR8(command *action.ResolvedCommand) (Finding, bool) {
	if command.SemanticOp != action.OpGitCheckout && command.SemanticOp != action.OpGitReset {
		return Finding{}, false
	}
	for i := range command.Effects {
		if command.Effects[i].HasFlag(action.EffectFlagDiscardsChanges) {
			return Finding{"R8", OutcomeAskAlways, "the command discards uncommitted changes"}, true
		}
	}
	return Finding{}, false
}

// workspaceDeleteRules applies R9: deleting the workspace root, everything at
// its root, or its git directory.
func workspaceDeleteRules(in Input) Findings {
	if in.Context == nil || in.Context.WorkspaceRoot == "" {
		return nil
	}
	gitDir := filepath.Join(in.Context.WorkspaceRoot, ".git")

	var findings Findings
	for i := range in.Action.Effects {
		effect := &in.Action.Effects[i]
		if effect.Type != action.EffectDelete || effect.Target == nil {
			continue
		}
		target := effect.Target
		switch {
		case samePath(in, target.Canonical, in.Context.WorkspaceRoot):
			findings = append(findings, Finding{"R9", OutcomeAskAlways, fmt.Sprintf(
				"deleting %s, the project root itself", target.Display)})
		case samePath(in, target.Canonical, gitDir):
			findings = append(findings, Finding{"R9", OutcomeAskAlways, fmt.Sprintf(
				"deleting %s, the project's git directory", target.Display)})
		}
	}
	return findings
}

// isProtectedBranch reports whether a branch is configured as protected or is
// the repository's detected default branch (§18.2 R7).
func isProtectedBranch(branch string, in Input) bool {
	if branch == "" {
		return true
	}
	if in.Config.ProtectedBranchSet()[branch] {
		return true
	}
	return in.Context != nil && in.Context.Git != nil && in.Context.Git.DefaultBranch == branch
}

// isLocalhost reports whether a host is the local machine, where a self-signed
// certificate is ordinary.
//
// An empty host is treated as local so a request whose host could not be parsed
// does not become an insecure-remote finding on its own. Every loopback address
// is covered, not just 127.0.0.1: `127.0.0.2`, `127.1` and the bracketed IPv6
// forms are loopback too, and an approval-free R11 that missed them would ask
// about a self-signed certificate on a local service.
func isLocalhost(host string) bool {
	host = strings.ToLower(strings.Trim(host, "[]"))
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsUnspecified()
	}
	return false
}

// samePath compares two canonical paths using the platform's case rule, the
// same one scope classification used to produce them (I-14).
func samePath(in Input, a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return in.Rules.EqualPath(a, b)
}

// dedupeFindings removes identical findings and orders them by rule id so the
// same action always explains itself the same way (§18.4).
func dedupeFindings(findings Findings) Findings {
	if len(findings) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(findings))
	out := make(Findings, 0, len(findings))
	for _, finding := range findings {
		key := finding.Rule + "|" + string(finding.Outcome) + "|" + finding.Reason
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, finding)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Rule != out[j].Rule {
			return ruleOrder(out[i].Rule) < ruleOrder(out[j].Rule)
		}
		return out[i].Reason < out[j].Reason
	})
	return out
}

// ruleOrder sorts R2 before R10 rather than lexically.
func ruleOrder(rule string) int {
	number := 0
	for i := 1; i < len(rule); i++ {
		if rule[i] < '0' || rule[i] > '9' {
			return number
		}
		number = number*10 + int(rule[i]-'0')
	}
	return number
}
