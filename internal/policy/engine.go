package policy

import (
	"fmt"

	"github.com/Vadym903/Intenter/internal/action"
)

// ApprovalMatcher answers whether a stored approval covers an action (§20). The
// approval package implements it; a nil matcher means nothing is approved yet,
// which is a valid state and not an error.
type ApprovalMatcher interface {
	Match(in Input) (MatchOutcome, error)
}

// MatchOutcome is the result of consulting the approval store.
type MatchOutcome struct {
	// ApprovalID identifies the approval that covered the action.
	ApprovalID int64
	Matched    bool
	// Mismatches explain why related approvals no longer cover it (§20.4).
	Mismatches []action.MismatchReport
}

// ConsentImporter turns the agent's own persistent consent into a validated
// approval, once per (project, rule, command) (§19.5). A nil importer disables
// the path.
type ConsentImporter interface {
	Import(in Input, consent *action.AgentConsent) (MatchOutcome, error)
}

// MismatchExplainer reports why the approvals related to an action no longer
// cover it. It is consulted only after a decision has been made, purely to
// explain it, and its answer can never change the outcome.
//
// It exists because the evaluation order stops at the first step that decides
// (§18.1), so a blocked action never reaches approval matching — yet §21
// requires exactly that event to say which approval stopped applying and why.
type MismatchExplainer interface {
	Mismatches(in Input) ([]action.MismatchReport, error)
}

// Engine applies the deterministic evaluation order of §18.1. It never
// consults a language model and never reaches the network.
type Engine struct {
	approvals     ApprovalMatcher
	consent       ConsentImporter
	explainer     MismatchExplainer
	engineVersion int
}

// NewEngine builds an engine. Any collaborator may be nil, which disables that
// step rather than failing the evaluation. A matcher that can also explain
// mismatches is used for both.
func NewEngine(approvals ApprovalMatcher, consent ConsentImporter, engineVersion int) *Engine {
	engine := &Engine{approvals: approvals, consent: consent, engineVersion: engineVersion}
	if explainer, ok := approvals.(MismatchExplainer); ok {
		engine.explainer = explainer
	}
	return engine
}

// Evaluate decides one action. It follows §18.1 exactly and stops at the first
// step that produces an outcome. Any internal error becomes ASK with class
// ENGINE_ERROR: the engine never answers ALLOW when it is unsure (I-3).
func (e *Engine) Evaluate(in Input, consent *action.AgentConsent) action.Decision {
	decision, _ := e.EvaluateDetailed(in, consent)
	return decision
}

// EvaluateDetailed also returns the hard-rule findings, so a caller that has to
// explain the decision does not run the rules a second time.
func (e *Engine) EvaluateDetailed(in Input, consent *action.AgentConsent) (decision action.Decision, findings Findings) {
	defer func() {
		if recovered := recover(); recovered != nil {
			decision = e.engineError(fmt.Sprintf("policy evaluation failed: %v", recovered))
		}
	}()

	if in.Action == nil {
		return e.engineError("there was nothing to evaluate"), nil
	}

	// Step 1 and 2: the safety floor, which no approval can override (I-4).
	findings = HardRules(in)
	if decided, ok := e.hardOutcome(findings); ok {
		return e.withMismatchExplanation(in, decided), findings
	}

	// Step 3: anything Intenter does not fully understand.
	if decided, ok := e.uncertain(in); ok {
		return e.withMismatchExplanation(in, decided), findings
	}

	// Step 4: the read-only workspace baseline.
	if BaselineReadOnlyWorkspace(in) {
		return action.Decision{
			Outcome:       action.OutcomeAllow,
			Class:         action.ClassPolicyReadonlyWorkspace,
			Reason:        "reads only files inside this project",
			EngineVersion: e.engineVersion,
		}, findings
	}

	// Step 5: a stored approval for the same resolved behavior.
	match, err := e.match(in)
	if err != nil {
		return e.engineError("the approval store could not be read: " + err.Error()), findings
	}
	if match.Matched {
		return action.Decision{
			Outcome:       action.OutcomeAllow,
			Class:         action.ClassApprovalMatch,
			Reason:        fmt.Sprintf("approval %d covers this action", match.ApprovalID),
			ApprovalID:    action.Ref(match.ApprovalID),
			EngineVersion: e.engineVersion,
		}, findings
	}

	// Step 6: consent the agent already holds, validated and imported once.
	if consent.Usable() && e.consent != nil {
		imported, err := e.consent.Import(in, consent)
		if err != nil {
			return e.engineError("the consent import failed: " + err.Error()), findings
		}
		if imported.Matched {
			return action.Decision{
				Outcome:       action.OutcomeAllow,
				Class:         action.ClassRuleImport,
				Reason:        "your agent already holds persistent permission for this action",
				ApprovalID:    action.Ref(imported.ApprovalID),
				EngineVersion: e.engineVersion,
			}, findings
		}
	}

	// Step 7: ask, naming the approval that stopped matching when there is one.
	if len(match.Mismatches) > 0 {
		return action.Decision{
			Outcome:         action.OutcomeAsk,
			Class:           action.ClassApprovalMismatch,
			Reason:          mismatchReason(match.Mismatches),
			MismatchReports: match.Mismatches,
			EngineVersion:   e.engineVersion,
		}, findings
	}
	return action.Decision{
		Outcome:       action.OutcomeAsk,
		Class:         action.ClassNoMatchingApproval,
		Reason:        "this action has not been approved for this project yet",
		EngineVersion: e.engineVersion,
	}, findings
}

// hardOutcome turns the hard-rule pass into a decision. BLOCK stops
// immediately; ASK_ALWAYS forces the prompt without consulting approvals.
func (e *Engine) hardOutcome(findings Findings) (action.Decision, bool) {
	strongest, ok := findings.Strongest()
	if !ok || strongest.Outcome == OutcomePass {
		return action.Decision{}, false
	}

	if strongest.Outcome == OutcomeBlock {
		return action.Decision{
			Outcome:       action.OutcomeBlock,
			Class:         action.HardRuleClass(strongest.Rule),
			Reason:        strongest.Reason,
			HardRule:      strongest.Rule,
			EngineVersion: e.engineVersion,
		}, true
	}
	return action.Decision{
		Outcome:       action.OutcomeAsk,
		Class:         action.ClassPolicyRequiresConfirmation,
		Reason:        strongest.Reason,
		HardRule:      strongest.Rule,
		EngineVersion: e.engineVersion,
	}, true
}

// uncertain covers §18.1 step 3: everything Intenter could not fully model
// asks, with the class naming what was missing.
func (e *Engine) uncertain(in Input) (action.Decision, bool) {
	ask := func(class action.DecisionClass, reason string) (action.Decision, bool) {
		return action.Decision{
			Outcome:       action.OutcomeAsk,
			Class:         class,
			Reason:        reason,
			EngineVersion: e.engineVersion,
		}, true
	}

	switch in.Action.Status {
	case action.StatusParseFailed:
		return ask(action.ClassUnsupportedSyntax, e.statusReason(in,
			"the command uses shell syntax Intenter does not interpret"))
	case action.StatusContextFailed:
		return ask(action.ClassContextUnavailable, e.statusReason(in,
			"no project could be determined for this command"))
	case action.StatusUnresolved:
		return ask(action.ClassUnresolvedCommand, e.statusReason(in,
			"Intenter cannot tell what this command would do"))
	}

	if in.Action.HasAmbiguousTarget() {
		return ask(action.ClassAmbiguousPath,
			"the command's target depends on a variable Intenter cannot expand")
	}
	return action.Decision{}, false
}

// statusReason prefers the resolver's specific reason over the generic one.
func (e *Engine) statusReason(in Input, fallback string) string {
	if in.Action.StatusReason != "" {
		return in.Action.StatusReason
	}
	return fallback
}

// withMismatchExplanation attaches the mismatch reports for an action that was
// decided before approvals were consulted.
//
// A user whose approved command is suddenly blocked needs to know which
// approval stopped applying and what changed (§21). The decision itself is
// already final here: only the explanation is added, and a failure to compute
// it never changes the answer.
func (e *Engine) withMismatchExplanation(in Input, decision action.Decision) action.Decision {
	if e.explainer == nil || decision.Outcome == action.OutcomeAllow || len(decision.MismatchReports) > 0 {
		return decision
	}
	reports, err := e.explainer.Mismatches(in)
	if err != nil || len(reports) == 0 {
		return decision
	}
	decision.MismatchReports = reports
	return decision
}

// match consults the approval store, treating a missing matcher as "nothing is
// approved" rather than an error.
func (e *Engine) match(in Input) (MatchOutcome, error) {
	if e.approvals == nil {
		return MatchOutcome{}, nil
	}
	return e.approvals.Match(in)
}

// engineError is the fail-safe answer: ask, never allow (I-3).
func (e *Engine) engineError(reason string) action.Decision {
	return action.Decision{
		Outcome:       action.OutcomeAsk,
		Class:         action.ClassEngineError,
		Reason:        reason,
		EngineVersion: e.engineVersion,
	}
}

// mismatchReason names the approval that stopped covering the action.
func mismatchReason(reports []action.MismatchReport) string {
	first := reports[0]
	if len(first.Differences) == 0 {
		return fmt.Sprintf("approval %d no longer covers this action", first.ApprovalID)
	}
	return fmt.Sprintf("approval %d no longer covers this action: %s",
		first.ApprovalID, first.Differences[0])
}
