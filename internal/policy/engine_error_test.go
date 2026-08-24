package policy

import (
	"errors"
	"testing"

	"github.com/Vadym903/Intenter/internal/action"
)

// INVARIANT I-3: any internal error becomes ASK with class ENGINE_ERROR. The
// engine never answers ALLOW when it is unsure, and never fails outright — a
// gate that crashes is a gate that is not there.

// brokenMatcher fails in a chosen way.
type brokenMatcher struct {
	err     error
	panics  bool
	message string
}

func (b *brokenMatcher) Match(Input) (MatchOutcome, error) {
	if b.panics {
		panic(b.message)
	}
	return MatchOutcome{}, b.err
}

// brokenImporter fails in a chosen way.
type brokenImporter struct {
	err    error
	panics bool
}

func (b *brokenImporter) Import(Input, *action.AgentConsent) (MatchOutcome, error) {
	if b.panics {
		panic("import exploded")
	}
	return MatchOutcome{}, b.err
}

func TestEveryFailurePathAsks(t *testing.T) {
	consent := &action.AgentConsent{
		Kind:     action.ConsentKindPersistentRule,
		RuleKeys: []string{"user:Bash(x)"},
	}

	tests := []struct {
		name     string
		matcher  ApprovalMatcher
		importer ConsentImporter
		consent  *action.AgentConsent
	}{
		{"the approval store errors", &brokenMatcher{err: errors.New("database is locked")}, nil, nil},
		{"the approval store panics", &brokenMatcher{panics: true, message: "matcher exploded"}, nil, nil},
		{"the importer errors", &stubMatcher{}, &brokenImporter{err: errors.New("disk full")}, consent},
		{"the importer panics", &stubMatcher{}, &brokenImporter{panics: true}, consent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := NewEngine(tt.matcher, tt.importer, testEngineVersion).
				Evaluate(deleteGeneratedInput(), tt.consent)

			if decision.Outcome != action.OutcomeAsk {
				t.Fatalf("outcome = %s, want ASK (I-3)", decision.Outcome)
			}
			if decision.Class != action.ClassEngineError {
				t.Errorf("class = %s, want ENGINE_ERROR", decision.Class)
			}
			if decision.Reason == "" {
				t.Error("an engine error must still explain itself")
			}
		})
	}
}

func TestAFailureNeverBecomesAnAllow(t *testing.T) {
	// Stated as its own property: across every failure mode and every input,
	// ALLOW must not appear.
	inputs := map[string]Input{
		"a read the baseline would allow": readOnlyWorkspaceInput(),
		"a delete needing approval":       deleteGeneratedInput(),
	}
	failures := map[string]ApprovalMatcher{
		"error": &brokenMatcher{err: errors.New("broken")},
		"panic": &brokenMatcher{panics: true, message: "boom"},
	}

	for inputName, in := range inputs {
		for failureName, matcher := range failures {
			t.Run(inputName+"/"+failureName, func(t *testing.T) {
				decision := NewEngine(matcher, nil, testEngineVersion).Evaluate(in, nil)
				if decision.Outcome == action.OutcomeAllow && decision.Class != action.ClassPolicyReadonlyWorkspace {
					t.Errorf("outcome = ALLOW (%s) despite a failure", decision.Class)
				}
			})
		}
	}
}

func TestTheBaselineStillWorksWhenTheStoreIsBroken(t *testing.T) {
	// B1 is decided before the store is consulted, so a broken store must not
	// stop a plainly safe read. Failing safe should not mean failing useless.
	matcher := &brokenMatcher{err: errors.New("database is locked")}

	decision := NewEngine(matcher, nil, testEngineVersion).Evaluate(readOnlyWorkspaceInput(), nil)
	if decision.Outcome != action.OutcomeAllow {
		t.Fatalf("outcome = %s (%s), want ALLOW from the baseline", decision.Outcome, decision.Reason)
	}
	if decision.Class != action.ClassPolicyReadonlyWorkspace {
		t.Errorf("class = %s, want POLICY_READONLY_WORKSPACE", decision.Class)
	}
}

func TestTheSafetyFloorStillWorksWhenTheStoreIsBroken(t *testing.T) {
	// A hard rule is decided before the store too, so a broken store cannot
	// turn a block into a deferral.
	documents := target("~/Documents", testHome+"/Documents", action.ScopeHome, action.FlagBroad)
	in := ruleInput(effect(action.EffectDelete, documents, action.EffectFlagRecursive))

	matcher := &brokenMatcher{panics: true, message: "boom"}
	decision := NewEngine(matcher, nil, testEngineVersion).Evaluate(in, nil)

	if decision.Outcome != action.OutcomeBlock {
		t.Fatalf("outcome = %s, want BLOCK regardless of the store", decision.Outcome)
	}
	if decision.HardRule != "R2" {
		t.Errorf("hard rule = %s, want R2", decision.HardRule)
	}
}

func TestMalformedActionsDoNotCrashTheEngine(t *testing.T) {
	// Inputs that should not occur, but must not take the gate down if they do.
	tests := map[string]Input{
		"no action":               {},
		"no context":              {Action: &action.ResolvedAction{Status: action.StatusResolved}},
		"effect with no target":   ruleInput(action.Effect{Type: action.EffectDelete}),
		"execute with no program": ruleInput(action.Effect{Type: action.EffectExecute}),
		"network with no target":  ruleInput(action.Effect{Type: action.EffectNetwork}),
		"command with no effects": func() Input {
			in := ruleInput()
			in.Action.Commands = []action.ResolvedCommand{{SemanticOp: action.OpGitPush}}
			return in
		}(),
	}

	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			decision := NewEngine(&stubMatcher{}, nil, testEngineVersion).Evaluate(in, nil)
			if decision.Outcome == action.OutcomeAllow {
				t.Errorf("outcome = ALLOW (%s), want a prompt for a malformed action", decision.Class)
			}
			if decision.Reason == "" {
				t.Error("every decision must carry a reason")
			}
		})
	}
}

func TestHardRulesSurviveMalformedEffects(t *testing.T) {
	// The rules run over whatever was resolved, so they must tolerate the
	// shapes a partially resolved action can produce.
	inputs := []Input{
		ruleInput(action.Effect{Type: action.EffectDelete}),
		ruleInput(action.Effect{Type: action.EffectNetwork}),
		ruleInput(action.Effect{Type: action.EffectExecute}),
		ruleInput(action.Effect{Type: "SOMETHING_ELSE"}),
	}

	for i, in := range inputs {
		findings := HardRules(in)
		for _, finding := range findings {
			if finding.Reason == "" {
				t.Errorf("input %d: rule %s fired without a reason", i, finding.Rule)
			}
		}
	}
}
