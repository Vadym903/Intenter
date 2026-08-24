package policy

import "github.com/Vadym903/Intenter/internal/action"

// baselineDisqualifyingFlags are the target flags that take an otherwise
// read-only action out of the baseline (§18.3 B1).
var baselineDisqualifyingFlags = []action.TargetFlag{
	action.FlagSensitive, action.FlagTraversal, action.FlagSymlinkEscape, action.FlagNetworkPath,
}

// BaselineReadOnlyWorkspace reports whether B1 allows the action: a fully
// resolved, purely read-only action confined to the workspace (§18.3).
//
// It is the only baseline ALLOW in the prototype. Everything it does not cover
// falls through to approvals.
func BaselineReadOnlyWorkspace(in Input) bool {
	if !in.Config.Policy.AllowReadonlyWorkspace {
		return false
	}
	if in.Action == nil || in.Action.Status != action.StatusResolved {
		return false
	}
	if in.Context == nil || in.Context.Status != action.ContextOK {
		return false
	}
	if len(in.Action.Effects) == 0 {
		return allNoop(in.Action)
	}

	for i := range in.Action.Effects {
		effect := &in.Action.Effects[i]
		if effect.Type != action.EffectRead {
			return false
		}
		target := effect.Target
		if target == nil || target.Ambiguous() {
			return false
		}
		if target.Scope != action.ScopeWorkspace && target.Scope != action.ScopeWorkspaceGenerated {
			return false
		}
		if target.HasAnyFlag(baselineDisqualifyingFlags...) {
			return false
		}
	}
	return true
}

// allNoop reports whether every command was recognized as doing nothing at all,
// which is what makes `echo building && pwd` an ordinary read rather than a
// prompt.
//
// The distinction that matters is between "no effects because a recognizer said
// so" and "no effects because none were filled in". Only the first is safe: a
// recognizer that names an operation but produces no effect is a defect, and
// treating that as an ALLOW would turn one bug into a silent permission (I-3).
func allNoop(resolved *action.ResolvedAction) bool {
	if len(resolved.Commands) == 0 {
		return false
	}
	for i := range resolved.Commands {
		if resolved.Commands[i].SemanticOp != action.OpNoop {
			return false
		}
	}
	return true
}
