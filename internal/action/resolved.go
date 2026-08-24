package action

import "sort"

// Fingerprint pins a mutable input that resolution depended on. An approval
// records every fingerprint it was created with; a changed value stops the
// approval from matching (PROTOTYPE_SPEC.md §15.6, §20.3 rule 3, I-16).
type Fingerprint struct {
	// Key is a stable identifier such as
	// "npm-script:package.json#scripts.cleanup" or "gradle-config".
	Key string `json:"key"`
	// Value is a hex SHA-256 of the normalized content.
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
}

// ResolvedCommand is one recognized simple command together with everything it
// was resolved from (PROTOTYPE_SPEC.md §13.6).
type ResolvedCommand struct {
	Executable string           `json:"executable"`
	SemanticOp SemanticOp       `json:"semantic_op"`
	Targets    []Target         `json:"targets,omitempty"`
	Effects    []Effect         `json:"effects,omitempty"`
	Status     ResolutionStatus `json:"status"`
	// StatusReason explains a non-RESOLVED status in the audit log.
	StatusReason string        `json:"status_reason,omitempty"`
	Fingerprints []Fingerprint `json:"fingerprints,omitempty"`
	// ResolvedFrom is the wrapper chain, e.g.
	// ["npm run cleanup", "rm -rf ./dist"].
	ResolvedFrom []string          `json:"resolved_from,omitempty"`
	Children     []ResolvedCommand `json:"children,omitempty"`
	// RawText is the command as written, for explanations.
	RawText string `json:"raw_text,omitempty"`
	// Git carries the ref a git operation targets. Hard rule R7 needs it, and
	// R7 runs before approvals are consulted, so it is deliberately outside
	// action_key: two pushes differing only in branch share an identity.
	Git *GitDetail `json:"git,omitempty"`
}

// GitDetail is the remote and ref a git operation acts on (§15.4, R7).
type GitDetail struct {
	Remote string `json:"remote,omitempty"`
	// Branch is the ref the operation targets, resolved from the refspec or
	// from HEAD.
	Branch string `json:"branch,omitempty"`
	// BranchKnown is false when the ref could not be determined, which R7
	// treats like pushing to a protected branch.
	BranchKnown bool `json:"branch_known,omitempty"`
}

// ResolvedAction is the aggregate view of one agent tool invocation: every
// command it runs, the union of their effects and fingerprints, and the weakest
// resolution status (PROTOTYPE_SPEC.md §13.6).
type ResolvedAction struct {
	RawCommand  string            `json:"raw_command"`
	Dialect     Dialect           `json:"dialect"`
	ProjectID   string            `json:"project_id,omitempty"`
	Commands    []ResolvedCommand `json:"commands,omitempty"`
	SemanticOps []SemanticOp      `json:"semantic_ops,omitempty"`
	// Effects is the union over all commands, including declared envelopes.
	Effects []Effect `json:"effects,omitempty"`
	// Fingerprints is the union over all commands, unique by key.
	Fingerprints []Fingerprint    `json:"fingerprints,omitempty"`
	Status       ResolutionStatus `json:"status"`
	StatusReason string           `json:"status_reason,omitempty"`
	ActionKey    string           `json:"action_key,omitempty"`
	Explanation  []string         `json:"explanation,omitempty"`
	// Unsupported lists parser constructs Intenter refuses to interpret.
	Unsupported []string `json:"unsupported,omitempty"`
	// Incomplete records that part of the command line was never examined at
	// all — it was longer than Intenter evaluates, or resolution ran out of
	// time before reaching every command. That is different from a command
	// Intenter looked at and could not model: an unexamined tail may hold
	// anything, so hard rule R13 forces a prompt rather than deferring (§18.2).
	Incomplete       bool   `json:"incomplete,omitempty"`
	IncompleteReason string `json:"incomplete_reason,omitempty"`
}

// MarkIncomplete records that the action was not fully examined, keeping the
// first reason.
func (a *ResolvedAction) MarkIncomplete(reason string) {
	if !a.Incomplete {
		a.IncompleteReason = reason
	}
	a.Incomplete = true
}

// Targets returns every target of every command, in command order.
func (a *ResolvedAction) Targets() []Target {
	var out []Target
	for i := range a.Commands {
		out = append(out, a.Commands[i].Targets...)
	}
	return out
}

// HasAmbiguousTarget reports whether any target could not be determined exactly
// (PROTOTYPE_SPEC.md §18.1 step 3).
//
// It scans the aggregated effects as well as the per-command ones. Aggregation
// keeps the two in step, so this is belt and braces — but the cost of missing an
// ambiguous target here is an ALLOW for a path Intenter could not read (I-2),
// which is too high a price for relying on that.
func (a *ResolvedAction) HasAmbiguousTarget() bool {
	for i := range a.Effects {
		if a.Effects[i].Target.Ambiguous() {
			return true
		}
	}
	for i := range a.Commands {
		for j := range a.Commands[i].Targets {
			if a.Commands[i].Targets[j].Ambiguous() {
				return true
			}
		}
		for j := range a.Commands[i].Effects {
			if a.Commands[i].Effects[j].Target.Ambiguous() {
				return true
			}
		}
	}
	return false
}

// DisplayTargets returns the deduplicated, sorted display paths of all targets.
// This is the form EXACT approvals compare (§20.3 rule 6).
func (a *ResolvedAction) DisplayTargets() []string {
	seen := make(map[string]bool)
	out := make([]string, 0)
	for _, t := range a.Targets() {
		if t.Display == "" || seen[t.Display] {
			continue
		}
		seen[t.Display] = true
		out = append(out, t.Display)
	}
	sort.Strings(out)
	return out
}

// Envelope projects the action's effects onto approval envelope entries.
func (a *ResolvedAction) Envelope() []EnvelopeEntry { return Envelope(a.Effects) }

// Network returns the action's deduplicated network targets.
func (a *ResolvedAction) Network() []NetworkTarget { return NetworkTargets(a.Effects) }

// FingerprintMap indexes fingerprints by key.
func (a *ResolvedAction) FingerprintMap() map[string]string {
	out := make(map[string]string, len(a.Fingerprints))
	for _, f := range a.Fingerprints {
		out[f.Key] = f.Value
	}
	return out
}

// MergeFingerprints appends fingerprints that are not already present by key
// and keeps the slice sorted by key for deterministic hashing.
func MergeFingerprints(into []Fingerprint, add ...Fingerprint) []Fingerprint {
	index := make(map[string]bool, len(into))
	for _, f := range into {
		index[f.Key] = true
	}
	for _, f := range add {
		if f.Key == "" || index[f.Key] {
			continue
		}
		index[f.Key] = true
		into = append(into, f)
	}
	sort.Slice(into, func(i, j int) bool { return into[i].Key < into[j].Key })
	return into
}
