package audit

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/storage"
)

const testWorkspace = "/w/demo"

// testNow is the recorder's clock in these tests.
var testNow = time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)

func newTestRecorder(t *testing.T) (*Recorder, *storage.Store) {
	t.Helper()
	db, err := storage.OpenAndMigrate(context.Background(), filepath.Join(t.TempDir(), "intenter.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	store := storage.NewStore(db)
	t.Cleanup(func() { _ = store.Close() })

	project := action.Project{ID: action.ProjectID(testWorkspace), RootPath: testWorkspace}
	if err := store.Projects.Upsert(context.Background(), project); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	recorder := NewRecorder(store)
	recorder.now = func() time.Time { return testNow }
	return recorder, store
}

func testRequest() action.ActionRequest {
	return action.ActionRequest{
		Agent:      "claude",
		SessionID:  "session-1",
		ToolUseID:  "toolu_1",
		Tool:       "Bash",
		Dialect:    action.DialectPosix,
		RawCommand: "npm run cleanup",
		Cwd:        testWorkspace,
		AdapterContext: map[string]any{
			"hook_event": "PreToolUse",
		},
	}
}

func testResolved() *action.ResolvedAction {
	dist := action.Target{
		Raw: "./dist", Display: "./dist", Canonical: testWorkspace + "/dist",
		Scope: action.ScopeWorkspaceGenerated, Status: action.TargetResolved,
	}
	deletion := action.Effect{Type: action.EffectDelete, Target: &dist}
	deletion.AddFlags(action.EffectFlagRecursive, action.EffectFlagForce)

	return &action.ResolvedAction{
		RawCommand:  "npm run cleanup",
		Dialect:     action.DialectPosix,
		ProjectID:   action.ProjectID(testWorkspace),
		Status:      action.StatusResolved,
		SemanticOps: []action.SemanticOp{action.OpRunScript},
		Effects:     []action.Effect{deletion},
		Commands: []action.ResolvedCommand{{
			SemanticOp: action.OpRunScript,
			Status:     action.StatusResolved,
			Targets:    []action.Target{dist},
			Effects:    []action.Effect{deletion},
			RawText:    "npm run cleanup",
		}},
		Fingerprints: []action.Fingerprint{
			{Key: "npm-script:package.json#scripts.cleanup", Value: "hash-dist"},
		},
		Explanation: []string{"npm run cleanup -> rm -rf ./dist"},
	}
}

func testContext() *action.Context {
	return &action.Context{
		WorkspaceRoot: testWorkspace,
		ProjectID:     action.ProjectID(testWorkspace),
		Status:        action.ContextOK,
	}
}

func TestRecordEvaluationStoresEverythingTheExplanationNeeds(t *testing.T) {
	// INVARIANT I-17: the stored row alone must explain the decision.
	recorder, store := newTestRecorder(t)

	decision := action.Decision{
		Outcome:       action.OutcomeAllow,
		Class:         action.ClassApprovalMatch,
		Reason:        "approval 42 covers this action",
		ApprovalID:    action.Ref(42),
		EngineVersion: 1,
	}
	id, err := recorder.RecordEvaluation(context.Background(), Evaluation{
		Request:     testRequest(),
		Context:     testContext(),
		Resolved:    testResolved(),
		Decision:    decision,
		Explanation: []string{"npm run cleanup -> rm -rf ./dist", "allowed by approval 42"},
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if id == nil {
		t.Fatal("want an audit event id")
	}

	stored, err := store.Audit.Get(context.Background(), *id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if stored.Decision != action.OutcomeAllow || stored.DecisionClass != action.ClassApprovalMatch {
		t.Errorf("decision = %s/%s", stored.Decision, stored.DecisionClass)
	}
	if stored.MatchedApprovalID == nil || *stored.MatchedApprovalID != 42 {
		t.Errorf("matched approval = %v, want 42", stored.MatchedApprovalID)
	}
	if stored.ProjectID != action.ProjectID(testWorkspace) {
		t.Errorf("project = %q", stored.ProjectID)
	}
	if stored.ResolutionStatus != action.StatusResolved {
		t.Errorf("resolution status = %s", stored.ResolutionStatus)
	}
	if stored.HookEvent != "PreToolUse" {
		t.Errorf("hook event = %q, want PreToolUse", stored.HookEvent)
	}
	if len(stored.Explanation) != 2 {
		t.Errorf("explanation = %v, want the stored lines", stored.Explanation)
	}

	// The resolved action itself is stored, so targets, effects and
	// fingerprints are all answerable without re-resolving.
	if stored.Resolved == nil {
		t.Fatal("the resolved action must be persisted")
	}
	if got := stored.Resolved.DisplayTargets(); len(got) != 1 || got[0] != "./dist" {
		t.Errorf("stored targets = %v", got)
	}
	if _, ok := stored.Resolved.FingerprintMap()["npm-script:package.json#scripts.cleanup"]; !ok {
		t.Errorf("stored fingerprints = %+v", stored.Resolved.Fingerprints)
	}
	if len(stored.Resolved.Envelope()) == 0 {
		t.Error("the stored effects must reproduce the envelope")
	}
}

func TestRecordEvaluationStoresBlockEvidence(t *testing.T) {
	// The other half of I-17: "why was this blocked?"
	recorder, store := newTestRecorder(t)

	decision := action.Decision{
		Outcome:       action.OutcomeBlock,
		Class:         action.HardRuleClass("R2"),
		Reason:        "recursively deleting ~/Documents, which is in your home directory",
		HardRule:      "R2",
		EngineVersion: 1,
		MismatchReports: []action.MismatchReport{{
			ApprovalID:  42,
			Differences: []string{"npm-script:package.json#scripts.cleanup changed"},
		}},
	}
	id, err := recorder.RecordEvaluation(context.Background(), Evaluation{
		Request:  testRequest(),
		Context:  testContext(),
		Resolved: testResolved(),
		Decision: decision,
	})
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
	if len(stored.MismatchReport) != 1 {
		t.Fatalf("mismatch report = %+v, want one", stored.MismatchReport)
	}
	if len(stored.RelatedApprovalIDs) != 1 || stored.RelatedApprovalIDs[0] != 42 {
		t.Errorf("related approvals = %v, want [42] (§21)", stored.RelatedApprovalIDs)
	}
	if stored.MatchedApprovalID != nil {
		t.Error("a blocked action has no matched approval")
	}
}

func TestDryRunLeavesNoTrace(t *testing.T) {
	// The setup self-test must not pollute the history a user reads (§12.2).
	recorder, store := newTestRecorder(t)

	id, err := recorder.RecordEvaluation(context.Background(), Evaluation{
		Request:  testRequest(),
		Context:  testContext(),
		Resolved: testResolved(),
		Decision: action.Decision{Outcome: action.OutcomeBlock, Class: action.HardRuleClass("R2")},
		DryRun:   true,
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if id != nil {
		t.Errorf("a dry run must not produce an event id, got %d", *id)
	}

	events, err := store.Audit.List(context.Background(), action.AuditFilter{Limit: 10, IncludeDryRun: true})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("a dry run must write nothing, found %d events", len(events))
	}
}

func TestRecordPromptCorrelatesWithTheEvaluation(t *testing.T) {
	recorder, store := newTestRecorder(t)

	id, err := recorder.RecordEvaluation(context.Background(), Evaluation{
		Request:  testRequest(),
		Context:  testContext(),
		Resolved: testResolved(),
		Decision: action.Decision{Outcome: action.OutcomeAsk, Class: action.ClassNoMatchingApproval},
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}

	suggestions := []any{map[string]any{"type": "addRules", "rules": []any{"Bash(npm run cleanup)"}}}
	promptID, err := recorder.RecordPrompt(context.Background(), testRequest(), suggestions)
	if err != nil {
		t.Fatalf("record prompt: %v", err)
	}
	if promptID != *id {
		t.Fatalf("prompt id = %d, want the evaluate row %d", promptID, *id)
	}

	stored, err := store.Audit.Get(context.Background(), promptID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !stored.PromptShown {
		t.Error("prompt_shown must be set")
	}
	if len(stored.PermissionSuggestions) != 1 {
		t.Errorf("permission suggestions = %+v, want them stored verbatim", stored.PermissionSuggestions)
	}
}

func TestRecordPromptOutsideTheWindowCreatesItsOwnRow(t *testing.T) {
	recorder, store := newTestRecorder(t)

	// An evaluation from well before the correlation window.
	old := testRequest()
	old.ReceivedAt = time.Date(2026, 8, 16, 11, 0, 0, 0, time.UTC)
	if _, err := recorder.RecordEvaluation(context.Background(), Evaluation{
		Request:  old,
		Context:  testContext(),
		Resolved: testResolved(),
		Decision: action.Decision{Outcome: action.OutcomeAsk, Class: action.ClassNoMatchingApproval},
	}); err != nil {
		t.Fatalf("record: %v", err)
	}

	promptID, err := recorder.RecordPrompt(context.Background(), testRequest(), nil)
	if err != nil {
		t.Fatalf("record prompt: %v", err)
	}

	stored, err := store.Audit.Get(context.Background(), promptID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.HookEvent != "PermissionRequest" {
		t.Errorf("hook event = %q, want a PermissionRequest row of its own (§24)", stored.HookEvent)
	}

	events, err := store.Audit.List(context.Background(), action.AuditFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("events = %d, want the old evaluation plus a new prompt row", len(events))
	}
}

func TestRecordPromptWithNoEvaluationAtAll(t *testing.T) {
	recorder, store := newTestRecorder(t)

	promptID, err := recorder.RecordPrompt(context.Background(), testRequest(), nil)
	if err != nil {
		t.Fatalf("record prompt: %v", err)
	}
	stored, err := store.Audit.Get(context.Background(), promptID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !stored.PromptShown || stored.HookEvent != "PermissionRequest" {
		t.Errorf("stored = %+v, want a standalone PermissionRequest row", stored)
	}
}

func TestRecordExecutionAttachesToTheDecidingRow(t *testing.T) {
	recorder, store := newTestRecorder(t)

	id, err := recorder.RecordEvaluation(context.Background(), Evaluation{
		Request:  testRequest(),
		Context:  testContext(),
		Resolved: testResolved(),
		Decision: action.Decision{Outcome: action.OutcomeAllow, Class: action.ClassApprovalMatch},
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}

	event, err := recorder.RecordExecution(context.Background(), Execution{
		SessionID: "session-1",
		ToolUseID: "toolu_1",
		Status:    action.ExecutionCompleted,
		Summary:   "removed 12 files",
	})
	if err != nil {
		t.Fatalf("record execution: %v", err)
	}
	if event == nil || event.ID != *id {
		t.Fatalf("execution attached to %+v, want the evaluate row %d", event, *id)
	}

	stored, err := store.Audit.Get(context.Background(), *id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.ExecutionStatus != action.ExecutionCompleted {
		t.Errorf("execution status = %s", stored.ExecutionStatus)
	}
	if stored.ExecutionAt == nil {
		t.Error("execution time must be recorded")
	}
	if stored.ResponseSummary != "removed 12 files" {
		t.Errorf("summary = %q", stored.ResponseSummary)
	}
}

func TestRecordExecutionForAnUnknownToolUseIsNotAnError(t *testing.T) {
	// The daemon may have restarted between the decision and the execution.
	recorder, _ := newTestRecorder(t)

	event, err := recorder.RecordExecution(context.Background(), Execution{
		SessionID: "session-1",
		ToolUseID: "toolu_unknown",
		Status:    action.ExecutionCompleted,
	})
	if err != nil {
		t.Fatalf("record execution: %v", err)
	}
	if event != nil {
		t.Errorf("event = %+v, want none", event)
	}
}

func TestRecordAdapterActionAndImportedApproval(t *testing.T) {
	recorder, store := newTestRecorder(t)

	id, err := recorder.RecordEvaluation(context.Background(), Evaluation{
		Request:  testRequest(),
		Context:  testContext(),
		Resolved: testResolved(),
		Decision: action.Decision{Outcome: action.OutcomeAsk, Class: action.ClassNoMatchingApproval},
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}

	if err := recorder.RecordAdapterAction(context.Background(), *id, action.AdapterDefer); err != nil {
		t.Fatalf("record adapter action: %v", err)
	}
	if err := recorder.RecordImportedApproval(context.Background(), *id, 7); err != nil {
		t.Fatalf("record imported approval: %v", err)
	}

	stored, err := store.Audit.Get(context.Background(), *id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.AdapterAction != action.AdapterDefer {
		t.Errorf("adapter action = %q, want defer", stored.AdapterAction)
	}
	if stored.ImportedApprovalID == nil || *stored.ImportedApprovalID != 7 {
		t.Errorf("imported approval = %v, want 7", stored.ImportedApprovalID)
	}
}

func TestExplanationIsReproducibleFromTheStoredRow(t *testing.T) {
	// INVARIANT I-17 stated directly: reading the row back must answer both
	// "why was this allowed?" and "what would it have done?".
	recorder, store := newTestRecorder(t)

	id, err := recorder.RecordEvaluation(context.Background(), Evaluation{
		Request:  testRequest(),
		Context:  testContext(),
		Resolved: testResolved(),
		Decision: action.Decision{
			Outcome:    action.OutcomeAllow,
			Class:      action.ClassApprovalMatch,
			Reason:     "approval 42 covers this action",
			ApprovalID: action.Ref(42),
		},
		Explanation: []string{
			"npm run cleanup -> rm -rf ./dist",
			"targets: ./dist (WORKSPACE_GENERATED)",
			"effects: DELETE(force,recursive) WORKSPACE_GENERATED",
			"allowed by approval 42",
		},
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}

	stored, err := store.Audit.Get(context.Background(), *id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	joined := strings.Join(stored.Explanation, "\n")
	for _, want := range []string{
		"npm run cleanup -> rm -rf ./dist",
		"WORKSPACE_GENERATED",
		"approval 42",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("stored explanation is missing %q:\n%s", want, joined)
		}
	}
	if summary := stored.ResolvedSummary(); summary == "" {
		t.Error("the stored row must render a resolved summary without re-evaluating")
	}
}

func TestRecordEvaluationUsesTheRequestTimestamp(t *testing.T) {
	recorder, store := newTestRecorder(t)

	received := time.Date(2026, 8, 16, 9, 30, 0, 0, time.UTC)
	request := testRequest()
	request.ReceivedAt = received

	id, err := recorder.RecordEvaluation(context.Background(), Evaluation{
		Request:  request,
		Context:  testContext(),
		Resolved: testResolved(),
		Decision: action.Decision{Outcome: action.OutcomeAsk, Class: action.ClassNoMatchingApproval},
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	stored, err := store.Audit.Get(context.Background(), *id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !stored.At.Equal(received) {
		t.Errorf("at = %s, want the request timestamp %s", stored.At, received)
	}
}
