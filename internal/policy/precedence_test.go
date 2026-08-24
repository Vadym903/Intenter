package policy

import (
	"testing"

	"github.com/Vadym903/Intenter/internal/action"
)

// INVARIANT I-4: hard-rule BLOCK and ASK_ALWAYS outcomes take precedence over
// stored approvals; no approval, import or baseline rule can override them.
//
// This file states that as a property rather than a set of examples: for every
// rule that can fire, the answer must be the same whether or not an approval
// exists, whether or not the agent holds consent, and whether or not the
// baseline would otherwise have allowed it.

// hardRuleCases is one input per rule that can fire.
func hardRuleCases() map[string]Input {
	documents := target("~/Documents", testHome+"/Documents", action.ScopeHome, action.FlagBroad)
	system := target("/usr/bin", "/usr/bin", action.ScopeSystem)
	key := target("~/.ssh/id_rsa", testHome+"/.ssh/id_rsa", action.ScopeHome, action.FlagSensitive)
	outside := target("/srv/data", "/srv/data", action.ScopeOutsideWorkspace)
	outside.IsDir = true
	traversal := target("../../etc/hosts", "/etc/hosts", action.ScopeOutsideWorkspace, action.FlagTraversal)
	workspaceRoot := target(".", testWorkspace, action.ScopeWorkspace, action.FlagBroad)
	generated := target("./dist", testWorkspace+"/dist", action.ScopeWorkspaceGenerated)

	cases := map[string]Input{
		"R1 system delete":  ruleInput(effect(action.EffectDelete, system)),
		"R2 home delete":    ruleInput(effect(action.EffectDelete, documents, action.EffectFlagRecursive)),
		"R3 outside delete": ruleInput(effect(action.EffectDelete, outside)),
		"R4 single file outside": ruleInput(effect(action.EffectDelete,
			target("/srv/data/x", "/srv/data/x", action.ScopeOutsideWorkspace))),
		"R5 credential read":  ruleInput(effect(action.EffectRead, key)),
		"R5 credential write": ruleInput(effect(action.EffectWrite, key)),
		"R6 traversal":        ruleInput(effect(action.EffectRead, traversal)),
		"R9 workspace root delete": ruleInput(effect(action.EffectDelete, workspaceRoot,
			action.EffectFlagRecursive)),
		"R10 elevated": ruleInput(effect(action.EffectDelete, generated, action.EffectFlagElevated)),
		"R10 credential in the command": ruleInput(
			networkEffect("api.example.com", action.EffectFlagInlineCredential)),
		"R11 insecure tls":       ruleInput(networkEffect("api.example.com", action.EffectFlagInsecureTLS)),
		"R12 streamed execution": ruleInput(executeEffect("sh", true)),
	}

	// R7 and R8 look at whole commands rather than single effects.
	push := networkEffect("github.com", action.EffectFlagBroad)
	pushInput := ruleInput(push)
	pushInput.Action.Commands = []action.ResolvedCommand{{
		SemanticOp: action.OpGitPush,
		Effects:    []action.Effect{push},
		Git:        &action.GitDetail{Remote: "origin", Branch: "main", BranchKnown: true},
	}}
	cases["R7 broad push"] = pushInput

	discard := effect(action.EffectWrite, workspaceRoot, action.EffectFlagDiscardsChanges)
	resetInput := ruleInput(discard)
	resetInput.Action.Commands = []action.ResolvedCommand{{
		SemanticOp: action.OpGitReset,
		Effects:    []action.Effect{discard},
	}}
	cases["R8 discards changes"] = resetInput

	return cases
}

func TestNoApprovalCanOverrideAHardRule(t *testing.T) {
	consent := &action.AgentConsent{
		Kind:     action.ConsentKindPersistentRule,
		RuleKeys: []string{"user:Bash(anything)"},
	}

	for name, in := range hardRuleCases() {
		t.Run(name, func(t *testing.T) {
			// What the engine says with nothing stored at all.
			baseline := NewEngine(nil, nil, testEngineVersion).Evaluate(in, nil)
			if baseline.Outcome == action.OutcomeAllow {
				t.Fatalf("this input is not covered by a hard rule: %s (%s)",
					baseline.Class, baseline.Reason)
			}

			// The same input with every path to an allow made available.
			matcher := &stubMatcher{outcome: MatchOutcome{ApprovalID: 42, Matched: true}}
			importer := &stubImporter{outcome: MatchOutcome{ApprovalID: 43, Matched: true}}
			guarded := NewEngine(matcher, importer, testEngineVersion).Evaluate(in, consent)

			if guarded.Outcome != baseline.Outcome {
				t.Errorf("outcome = %s with an approval, want %s (I-4)",
					guarded.Outcome, baseline.Outcome)
			}
			if guarded.Class != baseline.Class {
				t.Errorf("class = %s with an approval, want %s", guarded.Class, baseline.Class)
			}
			if guarded.Outcome == action.OutcomeAllow {
				t.Error("a hard rule must never resolve to ALLOW")
			}
			if matcher.calls != 0 {
				t.Error("the approval store must not even be consulted")
			}
			if importer.calls != 0 {
				t.Error("consent must not be imported for an action a hard rule stops")
			}
		})
	}
}

func TestTheBaselineCannotOverrideAHardRule(t *testing.T) {
	// B1 allows read-only workspace actions; a sensitive or escaping read
	// inside the workspace must still ask.
	tests := map[string]action.Target{
		"credential in the workspace": target("./.env", testWorkspace+"/.env",
			action.ScopeWorkspace, action.FlagSensitive),
		"symlink escaping the workspace": target("./build/link", testHome+"/Documents",
			action.ScopeHome, action.FlagSymlinkEscape),
		"traversal out of the workspace": target("../../etc/hosts", "/etc/hosts",
			action.ScopeOutsideWorkspace, action.FlagTraversal),
	}

	for name, on := range tests {
		t.Run(name, func(t *testing.T) {
			in := ruleInput(effect(action.EffectRead, on))
			if BaselineReadOnlyWorkspace(in) {
				t.Fatal("B1 must not cover this read (§18.3)")
			}
			decision := NewEngine(nil, nil, testEngineVersion).Evaluate(in, nil)
			if decision.Outcome == action.OutcomeAllow {
				t.Errorf("outcome = ALLOW (%s), want a prompt", decision.Class)
			}
		})
	}
}

func TestHardRulesApplyToEveryCommandOfALine(t *testing.T) {
	// An approved first command never covers a dangerous second one: the rules
	// run over the union of effects (§14.2, §18.1).
	readme := target("./README.md", testWorkspace+"/README.md", action.ScopeWorkspace)
	documents := target("~/Documents", testHome+"/Documents", action.ScopeHome, action.FlagBroad)

	in := ruleInput(
		effect(action.EffectRead, readme),
		effect(action.EffectDelete, documents, action.EffectFlagRecursive),
	)

	matcher := &stubMatcher{outcome: MatchOutcome{ApprovalID: 42, Matched: true}}
	decision := NewEngine(matcher, nil, testEngineVersion).Evaluate(in, nil)

	if decision.Outcome != action.OutcomeBlock {
		t.Fatalf("outcome = %s (%s), want BLOCK", decision.Outcome, decision.Reason)
	}
	if decision.HardRule != "R2" {
		t.Errorf("hard rule = %s, want R2", decision.HardRule)
	}
}

func TestStrongestRuleDecidesWhenSeveralFire(t *testing.T) {
	// BLOCK outranks ASK_ALWAYS, so a command that trips both is blocked.
	key := target("~/.ssh/id_rsa", testHome+"/.ssh/id_rsa", action.ScopeHome,
		action.FlagSensitive, action.FlagTraversal)

	in := ruleInput(effect(action.EffectDelete, key, action.EffectFlagRecursive))
	findings := HardRules(in)

	if findings.Outcome() != OutcomeBlock {
		t.Fatalf("outcome = %s, want BLOCK", findings.Outcome())
	}
	strongest, ok := findings.Strongest()
	if !ok || strongest.Outcome != OutcomeBlock {
		t.Fatalf("strongest = %+v, want a blocking rule", strongest)
	}

	decision := NewEngine(nil, nil, testEngineVersion).Evaluate(in, nil)
	if decision.Outcome != action.OutcomeBlock {
		t.Errorf("outcome = %s, want BLOCK", decision.Outcome)
	}
	// Only R1 to R5 may block (§18.5).
	if !blockingRule(decision.HardRule) {
		t.Errorf("hard rule = %s, want one of R1..R5", decision.HardRule)
	}
}

// blockingRule reports whether a rule id is one of the rules allowed to BLOCK.
func blockingRule(rule string) bool {
	switch rule {
	case "R1", "R2", "R3", "R4", "R5":
		return true
	}
	return false
}

func TestOnlyTheDocumentedRulesCanBlock(t *testing.T) {
	// §18.5 lists HARD_RULE_R1..R5 as the only BLOCK classes; every other rule
	// asks. A rule that blocked unexpectedly would deny work with no way for
	// the user to proceed.
	for name, in := range hardRuleCases() {
		t.Run(name, func(t *testing.T) {
			findings := HardRules(in)
			for _, finding := range findings {
				if finding.Outcome == OutcomeBlock && !blockingRule(finding.Rule) {
					t.Errorf("%s must not BLOCK (§18.5): %s", finding.Rule, finding.Reason)
				}
			}
		})
	}
}
