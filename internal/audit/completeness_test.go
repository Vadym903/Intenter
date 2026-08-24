package audit

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/Vadym903/Intenter/internal/action"
)

// §24 names two questions the audit log has to answer without re-evaluating
// anything: "why did Intenter auto-approve this?" and "why did it block this?"
// Answering them later, from a row alone, only works if nothing was dropped on
// the way into storage — and the way a field gets dropped is never dramatic. It
// is added to the model, populated by the resolver, and quietly not copied by
// one of the two projections between the evaluation and the row.
//
// These tests walk that path field by field.

// completeEvaluation is an evaluation with every field the evaluate path is
// responsible for populated, each with a value distinct enough that a
// projection copying the wrong one is visible.
func completeEvaluation() Evaluation {
	dist := action.Target{
		Raw: "./dist", Display: "./dist", Canonical: testWorkspace + "/dist",
		Scope: action.ScopeWorkspaceGenerated, Status: action.TargetResolved, IsDir: true,
	}
	dist.AddFlags(action.FlagBroad)

	documents := action.Target{
		Raw: "~/Documents", Display: "~/Documents", Canonical: "/Users/u/Documents",
		Scope: action.ScopeHome, Status: action.TargetResolved, IsDir: true,
	}
	documents.AddFlags(action.FlagBroad)

	deletion := action.Effect{Type: action.EffectDelete, Target: &dist}
	deletion.AddFlags(action.EffectFlagRecursive, action.EffectFlagForce)

	fetch := action.Effect{
		Type:    action.EffectNetwork,
		Network: &action.NetworkTarget{Host: "registry.example.com", Scheme: "https"},
	}

	resolved := &action.ResolvedAction{
		RawCommand:  "npm run cleanup",
		Dialect:     action.DialectPosix,
		ProjectID:   action.ProjectID(testWorkspace),
		Status:      action.StatusResolved,
		SemanticOps: []action.SemanticOp{action.OpRunScript, action.OpFSDelete},
		Effects:     []action.Effect{deletion, fetch},
		Commands: []action.ResolvedCommand{{
			Executable:   "npm",
			SemanticOp:   action.OpRunScript,
			Status:       action.StatusResolved,
			Targets:      []action.Target{dist},
			Effects:      []action.Effect{deletion},
			ResolvedFrom: []string{"npm run cleanup", "rm -rf ./dist"},
			RawText:      "npm run cleanup",
			Fingerprints: []action.Fingerprint{
				{Key: "npm-script:package.json#scripts.cleanup", Value: "hash-dist",
					Description: "the cleanup script"},
			},
		}},
		Fingerprints: []action.Fingerprint{
			{Key: "npm-config:.npmrc#script-shell", Value: "unset"},
			{Key: "npm-script:package.json#scripts.cleanup", Value: "hash-dist",
				Description: "the cleanup script"},
		},
		ActionKey:   "sha256-of-the-canonical-form",
		Explanation: []string{"npm run cleanup -> rm -rf ./dist"},
		Unsupported: []string{"process substitution"},
	}

	request := action.ActionRequest{
		Agent:        "claude",
		AgentVersion: "2.1.233",
		SessionID:    "session-1",
		ToolUseID:    "toolu_1",
		Tool:         "Bash",
		Dialect:      action.DialectPosix,
		RawCommand:   "npm run cleanup",
		Cwd:          testWorkspace + "/packages/api",
		ReceivedAt:   testNow,
		AdapterContext: map[string]any{
			"hook_event":      "PreToolUse",
			"permission_mode": "default",
		},
	}

	decision := action.Decision{
		Outcome:       action.OutcomeBlock,
		Class:         action.HardRuleClass("R2"),
		Reason:        "recursively deleting ~/Documents, which is in your home directory",
		HardRule:      "R2",
		EngineVersion: 3,
		MismatchReports: []action.MismatchReport{{
			ApprovalID: 42,
			Differences: []string{
				"npm-script:package.json#scripts.cleanup changed",
				"target ./dist -> ~/Documents",
				"scope WORKSPACE_GENERATED -> HOME (DELETE)",
			},
		}},
	}

	return Evaluation{
		Request:     request,
		Context:     testContext(),
		Resolved:    resolved,
		Decision:    decision,
		Explanation: []string{"npm run cleanup -> rm -rf ~/Documents", "blocked by R2"},
	}
}

func TestEveryFieldOfAnEvaluationReachesTheRow(t *testing.T) {
	recorder, store := newTestRecorder(t)
	evaluation := completeEvaluation()

	id, err := recorder.RecordEvaluation(context.Background(), evaluation)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	stored, err := store.Audit.Get(context.Background(), *id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	request := evaluation.Request
	checks := map[string]struct{ got, want any }{
		"agent":             {stored.Agent, request.Agent},
		"agent version":     {stored.AgentVersion, request.AgentVersion},
		"session":           {stored.SessionID, request.SessionID},
		"tool use id":       {stored.ToolUseID, request.ToolUseID},
		"hook event":        {stored.HookEvent, "PreToolUse"},
		"cwd":               {stored.Cwd, request.Cwd},
		"tool":              {stored.Tool, request.Tool},
		"dialect":           {stored.Dialect, request.Dialect},
		"raw command":       {stored.RawCommand, request.RawCommand},
		"project":           {stored.ProjectID, evaluation.Context.ProjectID},
		"resolution status": {stored.ResolutionStatus, evaluation.Resolved.Status},
		"decision":          {stored.Decision, evaluation.Decision.Outcome},
		"class":             {stored.DecisionClass, evaluation.Decision.Class},
		"reason":            {stored.Reason, evaluation.Decision.Reason},
		"hard rule":         {stored.HardRule, evaluation.Decision.HardRule},
		"engine version":    {stored.EngineVersion, evaluation.Decision.EngineVersion},
		"explanation":       {stored.Explanation, evaluation.Explanation},
		"adapter context":   {stored.AdapterContext, request.AdapterContext},
		"at":                {stored.At.UTC(), request.ReceivedAt.UTC()},
	}

	for name, check := range checks {
		if !reflect.DeepEqual(check.got, check.want) {
			t.Errorf("%s = %#v, want %#v", name, check.got, check.want)
		}
	}
}

func TestTheResolvedActionSurvivesStorageIntact(t *testing.T) {
	// The resolved action carries everything the explanation is built from, and
	// it goes through storage as JSON. A field that does not round-trip is one
	// the user can never be shown again.
	recorder, store := newTestRecorder(t)
	evaluation := completeEvaluation()

	id, err := recorder.RecordEvaluation(context.Background(), evaluation)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	stored, err := store.Audit.Get(context.Background(), *id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.Resolved == nil {
		t.Fatal("the resolved action must be persisted")
	}

	// Explanation is compared separately: see the test below for why it is the
	// one field that deliberately does not round-trip.
	got, want := *stored.Resolved, *evaluation.Resolved
	got.Explanation, want.Explanation = nil, nil

	if !reflect.DeepEqual(got, want) {
		t.Errorf("the resolved action changed in storage:\n got %#v\nwant %#v", got, want)
	}
}

func TestTheDecisionExplanationSharesTheResolvedBlob(t *testing.T) {
	// The schema §23.2 contracts has no explanation column, so the decision
	// explanation is stored inside the `resolved` JSON, over the resolver's own
	// lines. That is safe only because policy.Explain begins with exactly those
	// lines — the event explanation is a superset, never a replacement.
	//
	// Stating it here means a future explanation that stops opening with the
	// resolution chain fails a test rather than silently losing it from every
	// audit row.
	recorder, store := newTestRecorder(t)
	evaluation := completeEvaluation()
	evaluation.Resolved.Explanation = []string{"npm run cleanup -> rm -rf ./dist"}
	evaluation.Explanation = append(
		append([]string{}, evaluation.Resolved.Explanation...),
		"blocked by safety rule R2: recursively deleting ~/Documents")

	id, err := recorder.RecordEvaluation(context.Background(), evaluation)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	stored, err := store.Audit.Get(context.Background(), *id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if !reflect.DeepEqual(stored.Explanation, evaluation.Explanation) {
		t.Errorf("explanation = %v, want %v", stored.Explanation, evaluation.Explanation)
	}
	for i, line := range evaluation.Resolved.Explanation {
		if i >= len(stored.Explanation) || stored.Explanation[i] != line {
			t.Fatalf("the stored explanation must begin with the resolution chain;\n"+
				"got %v\nwant it to start with %v", stored.Explanation, evaluation.Resolved.Explanation)
		}
	}
}

func TestABlockRowCarriesItsWholeCase(t *testing.T) {
	// "Why did Intenter block this?" — the hard rule, the targets and their
	// scopes, the effects, the fingerprints, and the approval that stopped
	// applying (§24, §21).
	recorder, store := newTestRecorder(t)

	id, err := recorder.RecordEvaluation(context.Background(), completeEvaluation())
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	stored, err := store.Audit.Get(context.Background(), *id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if stored.HardRule != "R2" {
		t.Errorf("hard rule = %q, want R2", stored.HardRule)
	}

	targets := stored.Resolved.Targets()
	if len(targets) == 0 {
		t.Fatal("a block must record what it was about")
	}
	for _, target := range targets {
		if target.Display == "" {
			t.Error("a target with no display path cannot be shown to the user")
		}
		if target.Scope == "" {
			t.Errorf("target %q has no scope; the scope is half the reason", target.Display)
		}
		if target.Canonical == "" {
			t.Errorf("target %q has no canonical path, so the classification cannot be checked",
				target.Display)
		}
	}

	if len(stored.Resolved.Effects) != 2 {
		t.Fatalf("effects = %d, want both of them", len(stored.Resolved.Effects))
	}
	deletion := stored.Resolved.Effects[0]
	if !deletion.HasFlag(action.EffectFlagRecursive) || !deletion.HasFlag(action.EffectFlagForce) {
		t.Errorf("effect flags = %v, want them all: they are what made this a block", deletion.Flags)
	}
	if network := stored.Resolved.Network(); len(network) != 1 || network[0].Host == "" {
		t.Errorf("network targets = %+v, want the host recorded", network)
	}

	fingerprints := stored.Resolved.FingerprintMap()
	if len(fingerprints) != 2 {
		t.Errorf("fingerprints = %v, want every one resolution depended on", fingerprints)
	}
	for _, fingerprint := range stored.Resolved.Fingerprints {
		if fingerprint.Key == "" || fingerprint.Value == "" {
			t.Errorf("fingerprint %+v is not usable for a later comparison", fingerprint)
		}
	}

	if len(stored.MismatchReport) != 1 {
		t.Fatalf("mismatch report = %+v, want it stored", stored.MismatchReport)
	}
	if len(stored.MismatchReport[0].Differences) != 3 {
		t.Errorf("differences = %v, want all three (§21)", stored.MismatchReport[0].Differences)
	}
	if len(stored.RelatedApprovalIDs) != 1 || stored.RelatedApprovalIDs[0] != 42 {
		t.Errorf("related approvals = %v, want [42]", stored.RelatedApprovalIDs)
	}
	if stored.MatchedApprovalID != nil {
		t.Error("a blocked action matched no approval; recording one would be a false record")
	}
}

func TestAnAllowRowCarriesTheTrustItRestedOn(t *testing.T) {
	// "Why did Intenter auto-approve this?" — the class, the approval, and
	// the fingerprints that were valid at the time, so the same question can be
	// asked again later against the approval's own conditions.
	tests := map[string]struct {
		decision       action.Decision
		wantApprovalID bool
	}{
		"an approval matched": {
			decision: action.Decision{
				Outcome: action.OutcomeAllow, Class: action.ClassApprovalMatch,
				Reason: "approval 42 covers this action", ApprovalID: action.Ref(42), EngineVersion: 3,
			},
			wantApprovalID: true,
		},
		"an imported rule": {
			decision: action.Decision{
				Outcome: action.OutcomeAllow, Class: action.ClassRuleImport,
				Reason:     "your agent already holds persistent permission for this action",
				ApprovalID: action.Ref(43), EngineVersion: 3,
			},
			wantApprovalID: true,
		},
		"the read-only baseline": {
			decision: action.Decision{
				Outcome: action.OutcomeAllow, Class: action.ClassPolicyReadonlyWorkspace,
				Reason: "reads only files inside this project", EngineVersion: 3,
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			recorder, store := newTestRecorder(t)

			evaluation := completeEvaluation()
			evaluation.Decision = tc.decision

			id, err := recorder.RecordEvaluation(context.Background(), evaluation)
			if err != nil {
				t.Fatalf("record: %v", err)
			}
			stored, err := store.Audit.Get(context.Background(), *id)
			if err != nil {
				t.Fatalf("get: %v", err)
			}

			if stored.DecisionClass != tc.decision.Class {
				t.Errorf("class = %s, want %s", stored.DecisionClass, tc.decision.Class)
			}
			if tc.wantApprovalID {
				if stored.MatchedApprovalID == nil || *stored.MatchedApprovalID != *tc.decision.ApprovalID {
					t.Errorf("matched approval = %v, want %d", stored.MatchedApprovalID, *tc.decision.ApprovalID)
				}
			} else if stored.MatchedApprovalID != nil {
				t.Errorf("matched approval = %v, want none for a baseline allow", stored.MatchedApprovalID)
			}
			if len(stored.Resolved.Fingerprints) == 0 {
				t.Error("an allow must record the fingerprints it rested on, so they can be re-checked")
			}
			if len(stored.Resolved.Envelope()) == 0 {
				t.Error("an allow must record the envelope it permitted")
			}
		})
	}
}

func TestTheLaterAnnotationsFindTheirRow(t *testing.T) {
	// The row is completed by three separate hook invocations. Each writes to
	// the same row, and none may overwrite another's work.
	recorder, store := newTestRecorder(t)

	id, err := recorder.RecordEvaluation(context.Background(), completeEvaluation())
	if err != nil {
		t.Fatalf("record: %v", err)
	}

	suggestions := []any{map[string]any{"type": "addRules"}}
	if _, err := recorder.RecordPrompt(context.Background(), completeEvaluation().Request, suggestions); err != nil {
		t.Fatalf("record prompt: %v", err)
	}

	executedAt := time.Date(2026, 8, 16, 11, 31, 0, 0, time.UTC)
	if _, err := recorder.RecordExecution(context.Background(), Execution{
		SessionID: "session-1", ToolUseID: "toolu_1",
		Status: action.ExecutionFailed, Summary: "exit 1", At: executedAt,
	}); err != nil {
		t.Fatalf("record execution: %v", err)
	}

	if err := recorder.RecordAdapterAction(context.Background(), *id, action.AdapterDeny); err != nil {
		t.Fatalf("record adapter action: %v", err)
	}

	stored, err := store.Audit.Get(context.Background(), *id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	// Everything the evaluation wrote is still there.
	if stored.Decision != action.OutcomeBlock || stored.HardRule != "R2" {
		t.Errorf("a later annotation overwrote the decision: %s / %q", stored.Decision, stored.HardRule)
	}
	if stored.Resolved == nil || len(stored.Resolved.Effects) != 2 {
		t.Error("a later annotation dropped the resolved action")
	}

	// And everything the annotations added is too.
	if !stored.PromptShown || len(stored.PermissionSuggestions) != 1 {
		t.Errorf("prompt = %v / %v", stored.PromptShown, stored.PermissionSuggestions)
	}
	if stored.ExecutionStatus != action.ExecutionFailed {
		t.Errorf("execution status = %q, want failed", stored.ExecutionStatus)
	}
	if stored.ExecutionAt == nil || !stored.ExecutionAt.UTC().Equal(executedAt) {
		t.Errorf("execution at = %v, want %v", stored.ExecutionAt, executedAt)
	}
	if stored.ResponseSummary != "exit 1" {
		t.Errorf("response summary = %q", stored.ResponseSummary)
	}
	if stored.AdapterAction != action.AdapterDeny {
		t.Errorf("adapter action = %q, want deny", stored.AdapterAction)
	}
}

func TestAnEvaluationLeavesTheDeliveryUnrecordedUntilItHappens(t *testing.T) {
	// `adapter_action` is written by a second call, after the hook has mapped
	// the decision and told the agent (§11.3). The evaluation itself cannot
	// know it — the same ASK becomes a forced prompt or a deferral depending on
	// the decision class and the agent's permission mode — so the column stays
	// empty rather than being guessed at.
	recorder, store := newTestRecorder(t)

	id, err := recorder.RecordEvaluation(context.Background(), completeEvaluation())
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	stored, err := store.Audit.Get(context.Background(), *id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if stored.AdapterAction != "" {
		t.Errorf("adapter action = %q, want it unset until the hook reports it",
			stored.AdapterAction)
	}
}
