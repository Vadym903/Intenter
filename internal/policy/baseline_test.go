package policy

import (
	"testing"

	"github.com/Vadym903/Intenter/internal/action"
)

// B1 (§18.3) is the only baseline ALLOW in the prototype, and it exists for a
// reason worth stating: an agent that has to ask before reading a file in the
// project it is working on is an agent nobody leaves gated. The rule is
// deliberately narrow — reads only, inside the workspace, nothing sensitive or
// escaping — so that everything it covers is genuinely uninteresting.

// readOf builds a read action on one target.
func readOf(on action.Target) Input { return ruleInput(effect(action.EffectRead, on)) }

func TestBaselineCoversOrdinaryWorkspaceReads(t *testing.T) {
	tests := map[string]action.Target{
		"a source file":  target("./src/main.go", testWorkspace+"/src/main.go", action.ScopeWorkspace),
		"the readme":     target("./README.md", testWorkspace+"/README.md", action.ScopeWorkspace),
		"a build output": target("./dist/bundle.js", testWorkspace+"/dist/bundle.js", action.ScopeWorkspaceGenerated),
		"the git dir":    target("./.git", testWorkspace+"/.git", action.ScopeWorkspace),
		"the root":       target(".", testWorkspace, action.ScopeWorkspace, action.FlagBroad),
	}

	for name, on := range tests {
		t.Run(name, func(t *testing.T) {
			in := readOf(on)
			if !BaselineReadOnlyWorkspace(in) {
				t.Fatal("B1 must cover an ordinary workspace read")
			}
			decision := NewEngine(nil, nil, testEngineVersion).Evaluate(in, nil)
			if decision.Outcome != action.OutcomeAllow {
				t.Errorf("outcome = %s (%s), want ALLOW", decision.Outcome, decision.Reason)
			}
			if decision.Class != action.ClassPolicyReadonlyWorkspace {
				t.Errorf("class = %s, want POLICY_READONLY_WORKSPACE", decision.Class)
			}
		})
	}
}

func TestBaselineCoversAMultiCommandRead(t *testing.T) {
	// `git status && ls src` is two reads, and still just reading.
	readme := target("./README.md", testWorkspace+"/README.md", action.ScopeWorkspace)
	src := target("./src", testWorkspace+"/src", action.ScopeWorkspace)

	in := ruleInput(
		effect(action.EffectRead, readme),
		effect(action.EffectRead, src, action.EffectFlagRecursive),
	)
	if !BaselineReadOnlyWorkspace(in) {
		t.Error("B1 must cover a line of reads")
	}
}

func TestBaselineStopsAtAnythingElse(t *testing.T) {
	// Each of these is a reason the rule is narrow.
	tests := map[string]Input{
		"a write": ruleInput(effect(action.EffectWrite,
			target("./out.txt", testWorkspace+"/out.txt", action.ScopeWorkspace))),

		"a delete": ruleInput(effect(action.EffectDelete,
			target("./dist", testWorkspace+"/dist", action.ScopeWorkspaceGenerated))),

		"a read in the home directory": ruleInput(effect(action.EffectRead,
			target("~/notes.md", testHome+"/notes.md", action.ScopeHome))),

		"a read outside the workspace": ruleInput(effect(action.EffectRead,
			target("/srv/data/x", "/srv/data/x", action.ScopeOutsideWorkspace))),

		"a read of a system file": ruleInput(effect(action.EffectRead,
			target("/etc/hosts", "/etc/hosts", action.ScopeSystem))),

		"a credential file": ruleInput(effect(action.EffectRead,
			target("./.env", testWorkspace+"/.env", action.ScopeWorkspace, action.FlagSensitive))),

		"a path that escapes through a link": ruleInput(effect(action.EffectRead,
			target("./build/link", testHome+"/Documents", action.ScopeHome, action.FlagSymlinkEscape))),

		"a path that traverses out": ruleInput(effect(action.EffectRead,
			target("../../etc/hosts", "/etc/hosts", action.ScopeOutsideWorkspace, action.FlagTraversal))),

		"a UNC path": ruleInput(effect(action.EffectRead,
			target(`\\server\share`, `\\server\share`, action.ScopeOutsideWorkspace, action.FlagNetworkPath))),

		"a network call": ruleInput(networkEffect("api.example.com")),

		"an execution": ruleInput(executeEffect("gradle", false)),

		"one write among reads": ruleInput(
			effect(action.EffectRead, target("./a", testWorkspace+"/a", action.ScopeWorkspace)),
			effect(action.EffectWrite, target("./b", testWorkspace+"/b", action.ScopeWorkspace)),
		),
	}

	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			if BaselineReadOnlyWorkspace(in) {
				t.Error("B1 must not cover this (§18.3)")
			}
		})
	}
}

func TestBaselineNeedsCertainty(t *testing.T) {
	// A read Intenter is not sure about is not an ordinary read.
	statuses := []action.ResolutionStatus{
		action.StatusDeclared, action.StatusUnresolved,
		action.StatusParseFailed, action.StatusContextFailed,
	}
	for _, status := range statuses {
		in := readOf(target("./README.md", testWorkspace+"/README.md", action.ScopeWorkspace))
		in.Action.Status = status
		if BaselineReadOnlyWorkspace(in) {
			t.Errorf("B1 must require a RESOLVED action, got %s", status)
		}
	}

	ambiguous := readOf(target("$TARGET/x", "", action.ScopeWorkspace))
	ambiguous.Action.Effects[0].Target.Status = action.TargetAmbiguous
	if BaselineReadOnlyWorkspace(ambiguous) {
		t.Error("B1 must not cover an ambiguous target")
	}

	noContext := readOf(target("./README.md", testWorkspace+"/README.md", action.ScopeWorkspace))
	noContext.Context.Status = action.ContextWorkspaceUndefined
	if BaselineReadOnlyWorkspace(noContext) {
		t.Error("B1 must require a usable workspace")
	}
}

func TestBaselineCanBeTurnedOff(t *testing.T) {
	// Some users want to be asked about everything; the rule is a default, not
	// a fact (§12.6).
	in := readOf(target("./README.md", testWorkspace+"/README.md", action.ScopeWorkspace))
	in.Config.Policy.AllowReadonlyWorkspace = false

	if BaselineReadOnlyWorkspace(in) {
		t.Fatal("B1 must be disabled by configuration")
	}
	decision := NewEngine(nil, nil, testEngineVersion).Evaluate(in, nil)
	if decision.Outcome != action.OutcomeAsk {
		t.Errorf("outcome = %s, want ASK with the baseline off", decision.Outcome)
	}
}

func TestBaselineCoversCommandsThatDoNothing(t *testing.T) {
	// `echo building` has no effects, and asking about it would be absurd — but
	// only because a recognizer said it does nothing.
	in := ruleInput()
	in.Action.Commands = []action.ResolvedCommand{
		{Executable: "echo", SemanticOp: action.OpNoop, Status: action.StatusResolved},
		{Executable: "pwd", SemanticOp: action.OpNoop, Status: action.StatusResolved},
	}
	if !BaselineReadOnlyWorkspace(in) {
		t.Error("B1 must cover an action whose commands all do nothing")
	}
}

func TestBaselineDoesNotCoverEffectsThatWentMissing(t *testing.T) {
	// The fail-safe half of the rule above: a recognizer that names an
	// operation and then produces no effect is a defect, and a defect must not
	// read as permission (I-3).
	empty := ruleInput()
	empty.Action.Commands = []action.ResolvedCommand{
		{Executable: "cat", SemanticOp: action.OpFSRead, Status: action.StatusResolved},
	}
	if BaselineReadOnlyWorkspace(empty) {
		t.Error("B1 must not cover an operation whose effects are missing")
	}

	mixed := ruleInput()
	mixed.Action.Commands = []action.ResolvedCommand{
		{Executable: "echo", SemanticOp: action.OpNoop, Status: action.StatusResolved},
		{Executable: "rm", SemanticOp: action.OpFSDelete, Status: action.StatusResolved},
	}
	if BaselineReadOnlyWorkspace(mixed) {
		t.Error("B1 must not cover a delete that lost its effects")
	}

	if BaselineReadOnlyWorkspace(ruleInput()) {
		t.Error("B1 must not cover an action with no commands at all")
	}
}
