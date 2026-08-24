// Package approval creates, matches and invalidates the semantic approvals
// Intenter remembers. An approval records resolved effects, not command
// strings, so it stops covering an action as soon as the behavior it was
// granted for changes (PROTOTYPE_SPEC.md §19–§21).
package approval

import (
	"sort"

	"github.com/Vadym903/Intenter/internal/action"
)

// EngineCompatible reports whether an approval created by one engine may still
// be matched by another (§20.3 rule 1). The prototype has a single major
// version, so compatibility is equality.
func EngineCompatible(approvalVersion, engineVersion int) bool {
	return approvalVersion == engineVersion
}

// conditionsFor turns an action's fingerprints into approval conditions, sorted
// by key so a stored approval is byte-identical for the same inputs.
func conditionsFor(fingerprints []action.Fingerprint) []action.ApprovalCondition {
	conditions := make([]action.ApprovalCondition, 0, len(fingerprints))
	for _, fingerprint := range fingerprints {
		conditions = append(conditions, action.ApprovalCondition{
			Kind:  action.ConditionFingerprint,
			Key:   fingerprint.Key,
			Value: fingerprint.Value,
		})
	}
	sort.Slice(conditions, func(i, j int) bool { return conditions[i].Key < conditions[j].Key })
	return conditions
}

// sortedOps copies an action's semantic ops, preserving order: the ordering is
// part of the identity (§20.3 rule 2).
func sortedOps(ops []action.SemanticOp) []action.SemanticOp {
	return append([]action.SemanticOp(nil), ops...)
}

// keySet turns a key list into a set for subset and equality comparisons.
func keySet(keys []string) map[string]bool {
	out := make(map[string]bool, len(keys))
	for _, key := range keys {
		out[key] = true
	}
	return out
}

// equalSets reports whether two key lists describe the same set.
func equalSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	setB := keySet(b)
	for _, key := range a {
		if !setB[key] {
			return false
		}
	}
	return true
}

// subsetOf reports whether every key of a is present in b.
func subsetOf(a, b []string) bool {
	setB := keySet(b)
	for _, key := range a {
		if !setB[key] {
			return false
		}
	}
	return true
}

// envelopeKeys renders an action's effect envelope as comparison keys.
func envelopeKeys(act *action.ResolvedAction) []string {
	envelope := act.Envelope()
	keys := make([]string, 0, len(envelope))
	for _, entry := range envelope {
		keys = append(keys, entry.Key())
	}
	sort.Strings(keys)
	return keys
}

// networkKeys renders an action's network targets as comparison keys.
func networkKeys(act *action.ResolvedAction) []string {
	targets := act.Network()
	keys := make([]string, 0, len(targets))
	for _, target := range targets {
		keys = append(keys, target.Key())
	}
	sort.Strings(keys)
	return keys
}

// opsEqual compares two ordered semantic-op lists.
func opsEqual(a, b []action.SemanticOp) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// opsIntersect reports whether two op lists share at least one operation, which
// is one of the ways an approval is "related" to an action (§20.4).
func opsIntersect(a, b []action.SemanticOp) bool {
	seen := make(map[action.SemanticOp]bool, len(a))
	for _, op := range a {
		seen[op] = true
	}
	for _, op := range b {
		if seen[op] {
			return true
		}
	}
	return false
}
