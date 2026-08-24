package policy

import (
	"errors"
	"strings"
	"testing"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/config"
	"github.com/Vadym903/Intenter/internal/platform"
)

const testEngineVersion = 1

// stubMatcher is an approval store with a fixed answer.
type stubMatcher struct {
	outcome MatchOutcome
	err     error
	calls   int
}

func (s *stubMatcher) Match(Input) (MatchOutcome, error) {
	s.calls++
	return s.outcome, s.err
}

// stubImporter is a consent importer with a fixed answer.
type stubImporter struct {
	outcome MatchOutcome
	err     error
	calls   int
}

func (s *stubImporter) Import(Input, *action.AgentConsent) (MatchOutcome, error) {
	s.calls++
	return s.outcome, s.err
}

// readOnlyWorkspaceInput is an action B1 covers.
func readOnlyWorkspaceInput() Input {
	readme := target("./README.md", testWorkspace+"/README.md", action.ScopeWorkspace)
	return ruleInput(effect(action.EffectRead, readme))
}

// deleteGeneratedInput is a resolved delete inside the workspace: not covered
// by B1, not caught by any hard rule.
func deleteGeneratedInput() Input {
	dist := target("./dist", testWorkspace+"/dist", action.ScopeWorkspaceGenerated)
	return ruleInput(effect(action.EffectDelete, dist, action.EffectFlagRecursive, action.EffectFlagForce))
}

func TestEngineBaselineAllowsReadOnlyWorkspace(t *testing.T) {
	engine := NewEngine(nil, nil, testEngineVersion)

	decision := engine.Evaluate(readOnlyWorkspaceInput(), nil)
	if decision.Outcome != action.OutcomeAllow {
		t.Fatalf("outcome = %s (%s), want ALLOW", decision.Outcome, decision.Reason)
	}
	if decision.Class != action.ClassPolicyReadonlyWorkspace {
		t.Errorf("class = %s, want POLICY_READONLY_WORKSPACE", decision.Class)
	}
	if decision.EngineVersion != testEngineVersion {
		t.Errorf("engine version = %d, want %d", decision.EngineVersion, testEngineVersion)
	}
}

func TestEngineBaselineIsConfigurable(t *testing.T) {
	in := readOnlyWorkspaceInput()
	in.Config.Policy.AllowReadonlyWorkspace = false

	decision := NewEngine(nil, nil, testEngineVersion).Evaluate(in, nil)
	if decision.Outcome != action.OutcomeAsk {
		t.Errorf("outcome = %s, want ASK with the baseline disabled", decision.Outcome)
	}
	if decision.Class != action.ClassNoMatchingApproval {
		t.Errorf("class = %s, want NO_MATCHING_APPROVAL", decision.Class)
	}
}

func TestEngineBaselineExclusions(t *testing.T) {
	tests := []struct {
		name string
		in   Input
	}{
		{"a write", ruleInput(effect(action.EffectWrite,
			target("./out.txt", testWorkspace+"/out.txt", action.ScopeWorkspace)))},
		{"a read outside the workspace", ruleInput(effect(action.EffectRead,
			target("~/notes.md", testHome+"/notes.md", action.ScopeHome)))},
		{"a network call", ruleInput(networkEffect("api.example.com"))},
		{"an execution", ruleInput(executeEffect("gradle", false))},
		{"no effects at all", ruleInput()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if BaselineReadOnlyWorkspace(tt.in) {
				t.Error("B1 must not cover this action (§18.3)")
			}
		})
	}
}

func TestEngineBaselineNeedsAResolvedActionAndContext(t *testing.T) {
	unresolved := readOnlyWorkspaceInput()
	unresolved.Action.Status = action.StatusUnresolved
	if BaselineReadOnlyWorkspace(unresolved) {
		t.Error("B1 requires a RESOLVED action")
	}

	noContext := readOnlyWorkspaceInput()
	noContext.Context.Status = action.ContextWorkspaceUndefined
	if BaselineReadOnlyWorkspace(noContext) {
		t.Error("B1 requires context_status = OK")
	}
}

func TestEngineHardBlockBeatsAMatchingApproval(t *testing.T) {
	// INVARIANT I-4: no approval can override the safety floor.
	documents := target("~/Documents", testHome+"/Documents", action.ScopeHome, action.FlagBroad)
	in := ruleInput(effect(action.EffectDelete, documents, action.EffectFlagRecursive))

	matcher := &stubMatcher{outcome: MatchOutcome{ApprovalID: 42, Matched: true}}
	decision := NewEngine(matcher, nil, testEngineVersion).Evaluate(in, nil)

	if decision.Outcome != action.OutcomeBlock {
		t.Fatalf("outcome = %s (%s), want BLOCK", decision.Outcome, decision.Reason)
	}
	if decision.Class != action.HardRuleClass("R2") {
		t.Errorf("class = %s, want HARD_RULE_R2", decision.Class)
	}
	if decision.HardRule != "R2" {
		t.Errorf("hard rule = %s, want R2", decision.HardRule)
	}
	if matcher.calls != 0 {
		t.Error("a blocked action must not consult the approval store")
	}
}

func TestEngineHardAskAlwaysBeatsAMatchingApproval(t *testing.T) {
	key := target("~/.ssh/id_rsa", testHome+"/.ssh/id_rsa", action.ScopeHome, action.FlagSensitive)
	in := ruleInput(effect(action.EffectRead, key))

	matcher := &stubMatcher{outcome: MatchOutcome{ApprovalID: 42, Matched: true}}
	decision := NewEngine(matcher, nil, testEngineVersion).Evaluate(in, nil)

	if decision.Outcome != action.OutcomeAsk {
		t.Fatalf("outcome = %s, want ASK", decision.Outcome)
	}
	if decision.Class != action.ClassPolicyRequiresConfirmation {
		t.Errorf("class = %s, want POLICY_REQUIRES_CONFIRMATION", decision.Class)
	}
	if matcher.calls != 0 {
		t.Error("an ASK_ALWAYS action must not consult the approval store (§18.1 step 2)")
	}
}

func TestEngineUncertaintyAsksWithTheRightClass(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(in *Input)
		class  action.DecisionClass
	}{
		{"parse failure", func(in *Input) {
			in.Action.Status = action.StatusParseFailed
			in.Action.StatusReason = "the command uses a loop"
		}, action.ClassUnsupportedSyntax},
		{"context failure", func(in *Input) {
			in.Action.Status = action.StatusContextFailed
		}, action.ClassContextUnavailable},
		{"unresolved", func(in *Input) {
			in.Action.Status = action.StatusUnresolved
			in.Action.StatusReason = "chmod is not a program Intenter models"
		}, action.ClassUnresolvedCommand},
		{"ambiguous target", func(in *Input) {
			in.Action.Commands = []action.ResolvedCommand{{
				Targets: []action.Target{{Display: "$TARGET/x", Status: action.TargetAmbiguous}},
			}}
		}, action.ClassAmbiguousPath},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := deleteGeneratedInput()
			tt.mutate(&in)

			matcher := &stubMatcher{outcome: MatchOutcome{ApprovalID: 7, Matched: true}}
			decision := NewEngine(matcher, nil, testEngineVersion).Evaluate(in, nil)

			if decision.Outcome != action.OutcomeAsk {
				t.Fatalf("outcome = %s, want ASK", decision.Outcome)
			}
			if decision.Class != tt.class {
				t.Errorf("class = %s, want %s", decision.Class, tt.class)
			}
			if matcher.calls != 0 {
				t.Error("an action Intenter cannot model must never be auto-allowed (I-3)")
			}
		})
	}
}

func TestEngineUncertaintyKeepsTheResolverReason(t *testing.T) {
	in := deleteGeneratedInput()
	in.Action.Status = action.StatusUnresolved
	in.Action.StatusReason = "chmod is not a program Intenter models"

	decision := NewEngine(nil, nil, testEngineVersion).Evaluate(in, nil)
	if decision.Reason != "chmod is not a program Intenter models" {
		t.Errorf("reason = %q, want the resolver's specific reason", decision.Reason)
	}
}

func TestEngineApprovalMatchAllows(t *testing.T) {
	matcher := &stubMatcher{outcome: MatchOutcome{ApprovalID: 42, Matched: true}}

	decision := NewEngine(matcher, nil, testEngineVersion).Evaluate(deleteGeneratedInput(), nil)
	if decision.Outcome != action.OutcomeAllow {
		t.Fatalf("outcome = %s (%s), want ALLOW", decision.Outcome, decision.Reason)
	}
	if decision.Class != action.ClassApprovalMatch {
		t.Errorf("class = %s, want APPROVAL_MATCH", decision.Class)
	}
	if decision.ApprovalID == nil || *decision.ApprovalID != 42 {
		t.Errorf("approval id = %v, want 42", decision.ApprovalID)
	}
}

func TestEngineWithoutAnApprovalStoreAsks(t *testing.T) {
	decision := NewEngine(nil, nil, testEngineVersion).Evaluate(deleteGeneratedInput(), nil)
	if decision.Outcome != action.OutcomeAsk {
		t.Fatalf("outcome = %s, want ASK", decision.Outcome)
	}
	if decision.Class != action.ClassNoMatchingApproval {
		t.Errorf("class = %s, want NO_MATCHING_APPROVAL", decision.Class)
	}
}

func TestEngineMismatchIsReportedAsSuch(t *testing.T) {
	matcher := &stubMatcher{outcome: MatchOutcome{
		Mismatches: []action.MismatchReport{{
			ApprovalID:  42,
			Differences: []string{"fingerprint npm-script:package.json#scripts.cleanup changed"},
		}},
	}}

	decision := NewEngine(matcher, nil, testEngineVersion).Evaluate(deleteGeneratedInput(), nil)
	if decision.Outcome != action.OutcomeAsk {
		t.Fatalf("outcome = %s, want ASK", decision.Outcome)
	}
	if decision.Class != action.ClassApprovalMismatch {
		t.Errorf("class = %s, want APPROVAL_MISMATCH", decision.Class)
	}
	if len(decision.MismatchReports) != 1 {
		t.Fatalf("mismatch reports = %+v, want one", decision.MismatchReports)
	}
	if !strings.Contains(decision.Reason, "42") || !strings.Contains(decision.Reason, "fingerprint") {
		t.Errorf("reason = %q, want it to name the approval and what changed", decision.Reason)
	}
}

func TestEngineConsentImportIsOnlyTriedAfterApprovals(t *testing.T) {
	consent := &action.AgentConsent{Kind: action.ConsentKindPersistentRule, RuleKeys: []string{"Bash(npm run cleanup)"}}

	matched := &stubMatcher{outcome: MatchOutcome{ApprovalID: 42, Matched: true}}
	importer := &stubImporter{outcome: MatchOutcome{ApprovalID: 43, Matched: true}}
	decision := NewEngine(matched, importer, testEngineVersion).Evaluate(deleteGeneratedInput(), consent)
	if decision.Class != action.ClassApprovalMatch {
		t.Errorf("class = %s, want the existing approval to win", decision.Class)
	}
	if importer.calls != 0 {
		t.Error("consent import must not run when an approval already matched")
	}

	unmatched := &stubMatcher{}
	importer = &stubImporter{outcome: MatchOutcome{ApprovalID: 43, Matched: true}}
	decision = NewEngine(unmatched, importer, testEngineVersion).Evaluate(deleteGeneratedInput(), consent)
	if decision.Outcome != action.OutcomeAllow || decision.Class != action.ClassRuleImport {
		t.Fatalf("outcome = %s class = %s, want ALLOW/RULE_IMPORT", decision.Outcome, decision.Class)
	}
	if decision.ApprovalID == nil || *decision.ApprovalID != 43 {
		t.Errorf("approval id = %v, want the imported approval", decision.ApprovalID)
	}
}

func TestEngineUnusableConsentIsIgnored(t *testing.T) {
	importer := &stubImporter{outcome: MatchOutcome{ApprovalID: 43, Matched: true}}

	for _, consent := range []*action.AgentConsent{
		nil,
		{Kind: "something-else", RuleKeys: []string{"x"}},
		{Kind: action.ConsentKindPersistentRule},
	} {
		importer.calls = 0
		decision := NewEngine(nil, importer, testEngineVersion).Evaluate(deleteGeneratedInput(), consent)
		if decision.Outcome != action.OutcomeAsk {
			t.Errorf("outcome = %s, want ASK for unusable consent (I-8)", decision.Outcome)
		}
		if importer.calls != 0 {
			t.Error("unusable consent must not reach the importer")
		}
	}
}

// explainingMatcher reports mismatches without ever matching.
type explainingMatcher struct {
	reports []action.MismatchReport
	err     error
	calls   int
}

func (e *explainingMatcher) Match(Input) (MatchOutcome, error) { return MatchOutcome{}, nil }

func (e *explainingMatcher) Mismatches(Input) ([]action.MismatchReport, error) {
	e.calls++
	return e.reports, e.err
}

func TestBlockedActionStillExplainsTheApprovalThatStoppedApplying(t *testing.T) {
	// §21: the evaluation order stops at the hard rule, but the event must
	// still name the approval that no longer covers the action.
	documents := target("~/Documents", testHome+"/Documents", action.ScopeHome, action.FlagBroad)
	in := ruleInput(effect(action.EffectDelete, documents, action.EffectFlagRecursive))

	matcher := &explainingMatcher{reports: []action.MismatchReport{{
		ApprovalID:  42,
		Differences: []string{"npm-script:package.json#scripts.cleanup changed"},
	}}}

	decision := NewEngine(matcher, nil, testEngineVersion).Evaluate(in, nil)
	if decision.Outcome != action.OutcomeBlock {
		t.Fatalf("outcome = %s, want BLOCK", decision.Outcome)
	}
	if decision.Class != action.HardRuleClass("R2") {
		t.Errorf("class = %s, want the hard rule to still decide", decision.Class)
	}
	if len(decision.MismatchReports) != 1 || decision.MismatchReports[0].ApprovalID != 42 {
		t.Errorf("mismatch reports = %+v, want approval 42 explained", decision.MismatchReports)
	}
	if matcher.calls != 1 {
		t.Errorf("the explainer ran %d times, want once", matcher.calls)
	}
}

func TestMismatchExplanationNeverChangesTheOutcome(t *testing.T) {
	// The explainer runs after the decision, so neither its answer nor its
	// failure may affect what the user gets.
	key := target("~/.ssh/id_rsa", testHome+"/.ssh/id_rsa", action.ScopeHome, action.FlagSensitive)
	sensitive := ruleInput(effect(action.EffectRead, key))

	quiet := NewEngine(&stubMatcher{}, nil, testEngineVersion).Evaluate(sensitive, nil)
	loud := NewEngine(&explainingMatcher{reports: []action.MismatchReport{
		{ApprovalID: 42, Differences: []string{"something changed"}},
	}}, nil, testEngineVersion).Evaluate(sensitive, nil)
	failing := NewEngine(&explainingMatcher{err: errors.New("database is locked")},
		nil, testEngineVersion).Evaluate(sensitive, nil)

	for name, decision := range map[string]action.Decision{"loud": loud, "failing": failing} {
		if decision.Outcome != quiet.Outcome || decision.Class != quiet.Class || decision.Reason != quiet.Reason {
			t.Errorf("%s: decision = %s/%s (%s), want the same as without an explainer %s/%s (%s)",
				name, decision.Outcome, decision.Class, decision.Reason,
				quiet.Outcome, quiet.Class, quiet.Reason)
		}
	}
	if len(failing.MismatchReports) != 0 {
		t.Errorf("a failed explanation adds nothing, got %+v", failing.MismatchReports)
	}
}

func TestAllowIsNeverAnnotatedWithMismatches(t *testing.T) {
	// An allowed action has nothing to explain away.
	matcher := &explainingMatcher{reports: []action.MismatchReport{{ApprovalID: 42}}}

	decision := NewEngine(matcher, nil, testEngineVersion).Evaluate(readOnlyWorkspaceInput(), nil)
	if decision.Outcome != action.OutcomeAllow {
		t.Fatalf("outcome = %s, want ALLOW", decision.Outcome)
	}
	if len(decision.MismatchReports) != 0 {
		t.Errorf("mismatch reports = %+v, want none on an allow", decision.MismatchReports)
	}
	if matcher.calls != 0 {
		t.Error("the explainer must not run for an allowed action")
	}
}

func TestEngineErrorsAskAndNeverAllow(t *testing.T) {
	// INVARIANT I-3: any internal failure asks.
	failing := &stubMatcher{err: errors.New("database is locked")}
	decision := NewEngine(failing, nil, testEngineVersion).Evaluate(deleteGeneratedInput(), nil)
	if decision.Outcome != action.OutcomeAsk || decision.Class != action.ClassEngineError {
		t.Errorf("outcome = %s class = %s, want ASK/ENGINE_ERROR", decision.Outcome, decision.Class)
	}
	if !strings.Contains(decision.Reason, "database is locked") {
		t.Errorf("reason = %q, want the underlying error", decision.Reason)
	}

	consent := &action.AgentConsent{Kind: action.ConsentKindPersistentRule, RuleKeys: []string{"x"}}
	failingImport := &stubImporter{err: errors.New("import failed")}
	decision = NewEngine(nil, failingImport, testEngineVersion).Evaluate(deleteGeneratedInput(), consent)
	if decision.Outcome != action.OutcomeAsk || decision.Class != action.ClassEngineError {
		t.Errorf("outcome = %s class = %s, want ASK/ENGINE_ERROR", decision.Outcome, decision.Class)
	}
}

func TestEngineWithoutAnActionAsks(t *testing.T) {
	decision := NewEngine(nil, nil, testEngineVersion).Evaluate(Input{Config: config.Default()}, nil)
	if decision.Outcome != action.OutcomeAsk || decision.Class != action.ClassEngineError {
		t.Errorf("outcome = %s class = %s, want ASK/ENGINE_ERROR", decision.Outcome, decision.Class)
	}
}

// panickingMatcher exercises the engine's recovery path.
type panickingMatcher struct{}

func (panickingMatcher) Match(Input) (MatchOutcome, error) { panic("boom") }

func TestEnginePanicBecomesAnAsk(t *testing.T) {
	decision := NewEngine(panickingMatcher{}, nil, testEngineVersion).Evaluate(deleteGeneratedInput(), nil)
	if decision.Outcome != action.OutcomeAsk || decision.Class != action.ClassEngineError {
		t.Fatalf("outcome = %s class = %s, want ASK/ENGINE_ERROR", decision.Outcome, decision.Class)
	}
	if !strings.Contains(decision.Reason, "boom") {
		t.Errorf("reason = %q, want the recovered panic", decision.Reason)
	}
}

func TestEngineDecisionsAreDeterministic(t *testing.T) {
	in := deleteGeneratedInput()
	engine := NewEngine(&stubMatcher{}, nil, testEngineVersion)

	first := engine.Evaluate(in, nil)
	second := engine.Evaluate(in, nil)
	if first.Outcome != second.Outcome || first.Class != second.Class || first.Reason != second.Reason {
		t.Errorf("decisions differ between runs:\n%+v\n%+v", first, second)
	}
}

func TestExplainRendersTheWholeDecision(t *testing.T) {
	in := deleteGeneratedInput()
	in.Action.Explanation = []string{"npm run cleanup -> rm -rf ./dist"}
	in.Action.Unsupported = []string{"eval"}
	in.Rules = platform.PathRules{}

	decision := action.Decision{
		Outcome:  action.OutcomeBlock,
		Class:    action.HardRuleClass("R2"),
		Reason:   "recursively deleting ~/Documents, which is in your home directory",
		HardRule: "R2",
		MismatchReports: []action.MismatchReport{{
			ApprovalID:  42,
			Differences: []string{"target ./dist -> ~/Documents"},
		}},
	}
	findings := Findings{{Rule: "R2", Outcome: OutcomeBlock, Reason: "recursively deleting ~/Documents"}}

	explanation := Explain(in, decision, findings)
	joined := strings.Join(explanation, "\n")
	for _, want := range []string{
		"npm run cleanup -> rm -rf ./dist",
		"not interpreted: eval",
		"R2: recursively deleting ~/Documents",
		"blocked by safety rule R2",
		"approval 42: target ./dist -> ~/Documents",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("explanation is missing %q:\n%s", want, joined)
		}
	}
}

func TestUserMessageOnlyRepeatsTheReason(t *testing.T) {
	blocked := UserMessage(action.Decision{Outcome: action.OutcomeBlock, Reason: "deleting ~/Documents"})
	if !strings.Contains(blocked, "deleting ~/Documents") {
		t.Errorf("message = %q, want the reason", blocked)
	}
	if allowed := UserMessage(action.Decision{Outcome: action.OutcomeAllow, Reason: "x"}); allowed != "" {
		t.Errorf("an allowed action needs no message, got %q", allowed)
	}
	if asked := UserMessage(action.Decision{Outcome: action.OutcomeAsk, Reason: "not approved yet"}); !strings.Contains(asked, "not approved yet") {
		t.Errorf("message = %q, want the reason", asked)
	}
}
