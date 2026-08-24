package approval

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Vadym903/Intenter/internal/action"
)

// MismatchReports explains why the approvals related to an action no longer
// cover it (§20.4). Only related approvals are reported: those created from the
// same raw command, those sharing a semantic operation, and those sharing a
// fingerprint key. An unrelated approval is not evidence of anything.
func MismatchReports(candidates []action.Approval, act *action.ResolvedAction, engineVersion int) []action.MismatchReport {
	reports := make([]action.MismatchReport, 0, MaxMismatchReports)

	for i := range candidates {
		candidate := &candidates[i]
		if !related(candidate, act) {
			continue
		}
		differences := Differences(candidate, act, engineVersion)
		if len(differences) == 0 {
			continue
		}
		reports = append(reports, action.MismatchReport{ApprovalID: candidate.ID, Differences: differences})
		if len(reports) == MaxMismatchReports {
			break
		}
	}
	return reports
}

// related reports whether an approval is close enough to an action that its
// difference is worth explaining (§20.4).
func related(approval *action.Approval, act *action.ResolvedAction) bool {
	if approval.CreatedFromRawCommand != "" && approval.CreatedFromRawCommand == act.RawCommand {
		return true
	}
	if opsIntersect(approval.SemanticOps, act.SemanticOps) {
		return true
	}
	current := act.FingerprintMap()
	for _, key := range approval.FingerprintKeys() {
		if _, ok := current[key]; ok {
			return true
		}
	}
	return false
}

// Differences lists, in a stable order, everything that stops an approval from
// covering an action. The wording is what `intenter history show` prints, so
// a user can see exactly which change withdrew the trust (§18.4, §21).
func Differences(approval *action.Approval, act *action.ResolvedAction, engineVersion int) []string {
	var differences []string

	if !EngineCompatible(approval.EngineVersion, engineVersion) {
		differences = append(differences, fmt.Sprintf(
			"created by engine version %d, this is version %d", approval.EngineVersion, engineVersion))
	}
	if approval.State != action.ApprovalActive {
		differences = append(differences, fmt.Sprintf("the approval is %s",
			strings.ToLower(string(approval.State))))
	}
	if !opsEqual(approval.SemanticOps, act.SemanticOps) {
		differences = append(differences, fmt.Sprintf("operation %s -> %s",
			approval.OpsString(), opsString(act.SemanticOps)))
	}

	differences = append(differences, fingerprintDifferences(approval, act)...)
	differences = append(differences, targetDifferences(approval, act)...)
	differences = append(differences, scopeDifferences(approval, act)...)
	differences = append(differences, envelopeDifferences(approval, act)...)
	differences = append(differences, networkDifferences(approval, act)...)

	return differences
}

// scopeDifferences names a change in where an effect lands. It is the part of a
// mismatch that decides whether the change is dangerous, so it is stated
// outright rather than left to be inferred from two effect lines (§18.4).
func scopeDifferences(approval *action.Approval, act *action.ResolvedAction) []string {
	before := scopesByEffectType(approval.Envelope)
	after := scopesByEffectType(act.Envelope())

	var differences []string
	for effectType, wasScopes := range before {
		nowScopes, present := after[effectType]
		if !present {
			continue
		}
		// Only an unambiguous one-for-one move reads as a scope change; any
		// wider difference is already covered by the effect lines.
		if len(wasScopes) != 1 || len(nowScopes) != 1 || wasScopes[0] == nowScopes[0] {
			continue
		}
		differences = append(differences, fmt.Sprintf("scope %s -> %s (%s)",
			wasScopes[0], nowScopes[0], effectType))
	}
	sort.Strings(differences)
	return differences
}

// scopesByEffectType indexes the distinct scopes each effect type acts on.
func scopesByEffectType(envelope []action.EnvelopeEntry) map[action.EffectType][]action.Scope {
	out := make(map[action.EffectType][]action.Scope)
	for _, entry := range envelope {
		if entry.Scope == "" {
			continue
		}
		scopes := out[entry.Type]
		if !containsScope(scopes, entry.Scope) {
			out[entry.Type] = append(scopes, entry.Scope)
		}
	}
	return out
}

func containsScope(scopes []action.Scope, scope action.Scope) bool {
	for _, existing := range scopes {
		if existing == scope {
			return true
		}
	}
	return false
}

// fingerprintDifferences names each mutable input that changed or disappeared.
func fingerprintDifferences(approval *action.Approval, act *action.ResolvedAction) []string {
	current := act.FingerprintMap()
	var differences []string

	for _, key := range approval.FingerprintKeys() {
		expected := approval.Fingerprints()[key]
		stored, present := current[key]
		switch {
		case !present:
			differences = append(differences, fmt.Sprintf("%s is no longer part of this action", key))
		case stored != expected:
			differences = append(differences, fmt.Sprintf("%s changed", key))
		}
	}
	return differences
}

// targetDifferences names paths that were added, removed or moved to another
// scope. The scope is included because it is what makes a change dangerous.
func targetDifferences(approval *action.Approval, act *action.ResolvedAction) []string {
	if approval.Kind != action.ApprovalExact {
		return nil
	}

	approved := keySet(approval.Targets)
	scopes := targetScopes(act)

	var added, removed []string
	for _, display := range act.DisplayTargets() {
		if !approved[display] {
			added = append(added, display)
		}
	}
	current := keySet(act.DisplayTargets())
	for _, display := range approval.Targets {
		if !current[display] {
			removed = append(removed, display)
		}
	}
	if len(added) == 0 && len(removed) == 0 {
		return nil
	}

	// One target replaced by one other reads best as a move.
	if len(added) == 1 && len(removed) == 1 {
		return []string{fmt.Sprintf("target %s -> %s (%s)", removed[0], added[0], scopes[added[0]])}
	}

	var differences []string
	for _, display := range added {
		differences = append(differences, fmt.Sprintf("new target %s (%s)", display, scopes[display]))
	}
	for _, display := range removed {
		differences = append(differences, fmt.Sprintf("target %s is no longer touched", display))
	}
	return differences
}

// envelopeDifferences names effects and flags the approval never permitted.
func envelopeDifferences(approval *action.Approval, act *action.ResolvedAction) []string {
	approved := keySet(approval.EnvelopeKeys())

	var differences []string
	for _, entry := range act.Envelope() {
		if approved[entry.Key()] {
			continue
		}
		differences = append(differences, "new effect "+entry.String())
	}

	if approval.Kind == action.ApprovalExact {
		current := keySet(envelopeKeys(act))
		for _, entry := range approval.Envelope {
			if current[entry.Key()] {
				continue
			}
			differences = append(differences, "effect "+entry.String()+" no longer happens")
		}
	}
	sort.Strings(differences)
	return differences
}

// networkDifferences names hosts the approval never permitted.
func networkDifferences(approval *action.Approval, act *action.ResolvedAction) []string {
	approved := keySet(approval.NetworkKeys())

	var differences []string
	for _, target := range act.Network() {
		if approved[target.Key()] {
			continue
		}
		differences = append(differences, "new network target "+target.String())
	}
	sort.Strings(differences)
	return differences
}

// targetScopes indexes an action's target scopes by display path.
func targetScopes(act *action.ResolvedAction) map[string]action.Scope {
	out := make(map[string]action.Scope)
	for _, target := range act.Targets() {
		if target.Display == "" {
			continue
		}
		out[target.Display] = target.Scope
	}
	return out
}

// opsString renders an ordered op list the way Approval.OpsString does.
func opsString(ops []action.SemanticOp) string {
	parts := make([]string, 0, len(ops))
	for _, op := range ops {
		parts = append(parts, string(op))
	}
	return strings.Join(parts, ">")
}
