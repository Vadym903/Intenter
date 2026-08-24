package audit

import (
	"context"
	"testing"

	"github.com/Vadym903/Intenter/internal/action"
)

// The invariant index for the audit log: I-17.
// See internal/approval/invariants_test.go for what this index is for.

func TestInvariant_I17_EveryDecisiveOutcomeIsExplainableFromStorage(t *testing.T) {
	// I-17: every ALLOW and BLOCK MUST be explainable from persisted audit data
	// alone.
	//
	// The test that matters is not that the fields exist but that the row can
	// answer the questions a user actually asks afterwards — what ran, what it
	// would have done, where, why that answer, and on what evidence — without
	// re-resolving anything. Re-resolution would answer about the world as it
	// is now, which is exactly the wrong moment.
	//
	// T074 audits field-level completeness; this states the property once.
	decisions := map[string]action.Decision{
		"allowed by an approval": {
			Outcome: action.OutcomeAllow, Class: action.ClassApprovalMatch,
			Reason: "approval 42 covers this action", ApprovalID: action.Ref(42), EngineVersion: 1,
		},
		"allowed by the baseline": {
			Outcome: action.OutcomeAllow, Class: action.ClassPolicyReadonlyWorkspace,
			Reason: "reads only files inside this project", EngineVersion: 1,
		},
		"allowed by an imported rule": {
			Outcome: action.OutcomeAllow, Class: action.ClassRuleImport,
			Reason:     "your agent already holds persistent permission for this action",
			ApprovalID: action.Ref(7), EngineVersion: 1,
		},
		"blocked by a hard rule": {
			Outcome: action.OutcomeBlock, Class: action.HardRuleClass("R2"),
			Reason:   "recursively deleting ~/Documents, which is in your home directory",
			HardRule: "R2", EngineVersion: 1,
		},
		"blocked after an approval stopped applying": {
			Outcome: action.OutcomeBlock, Class: action.HardRuleClass("R2"),
			Reason:   "recursively deleting ~/Documents, which is in your home directory",
			HardRule: "R2", EngineVersion: 1,
			MismatchReports: []action.MismatchReport{{
				ApprovalID: 42,
				Differences: []string{
					"npm-script:package.json#scripts.cleanup changed",
					"target ./dist -> ~/Documents",
					"scope WORKSPACE_GENERATED -> HOME (DELETE)",
				},
			}},
		},
	}

	for name, decision := range decisions {
		t.Run(name, func(t *testing.T) {
			recorder, store := newTestRecorder(t)

			id, err := recorder.RecordEvaluation(context.Background(), Evaluation{
				Request:     testRequest(),
				Context:     testContext(),
				Resolved:    testResolved(),
				Decision:    decision,
				Explanation: []string{"npm run cleanup -> rm -rf ./dist"},
			})
			if err != nil {
				t.Fatalf("record: %v", err)
			}
			if id == nil {
				t.Fatal("a decisive outcome must be recorded")
			}

			// Everything below reads only the stored row.
			stored, err := store.Audit.Get(context.Background(), *id)
			if err != nil {
				t.Fatalf("get: %v", err)
			}

			// What ran, and under what.
			if stored.RawCommand == "" || stored.Tool == "" || stored.Agent == "" {
				t.Error("the row must say what command ran, through which tool, for which agent")
			}
			if stored.Cwd == "" || stored.ProjectID == "" {
				t.Error("the row must say where it ran")
			}

			// What it would have done.
			if stored.Resolved == nil {
				t.Fatal("the resolved action must be persisted; without it nothing else can be answered")
			}
			if len(stored.Resolved.Effects) == 0 {
				t.Error("the row must record the effects the decision was made about")
			}
			for _, target := range stored.Resolved.Targets() {
				if target.Display == "" || target.Scope == "" {
					t.Errorf("target %+v must carry a display path and a scope", target)
				}
			}

			// On what evidence — the mutable inputs resolution depended on.
			if len(stored.Resolved.Fingerprints) == 0 {
				t.Error("the row must record the fingerprints resolution depended on")
			}

			// Why that answer.
			if stored.Reason == "" || stored.DecisionClass == "" {
				t.Error("the row must carry a reason and a class")
			}
			if stored.ResolutionStatus == "" {
				t.Error("the row must say how well the command was understood")
			}
			if stored.EngineVersion == 0 {
				t.Error("the row must record which engine decided, so an old answer can be told apart")
			}

			// The specific evidence for this kind of answer.
			switch {
			case decision.ApprovalID != nil:
				if stored.MatchedApprovalID == nil || *stored.MatchedApprovalID != *decision.ApprovalID {
					t.Errorf("matched approval = %v, want %d", stored.MatchedApprovalID, *decision.ApprovalID)
				}
			case decision.HardRule != "":
				if stored.HardRule != decision.HardRule {
					t.Errorf("hard rule = %q, want %q", stored.HardRule, decision.HardRule)
				}
			}
			if len(decision.MismatchReports) > 0 {
				if len(stored.MismatchReport) != len(decision.MismatchReports) {
					t.Fatalf("mismatch report = %+v, want it stored", stored.MismatchReport)
				}
				if len(stored.MismatchReport[0].Differences) != len(decision.MismatchReports[0].Differences) {
					t.Errorf("differences = %v, want all of them (§21)", stored.MismatchReport[0].Differences)
				}
				if len(stored.RelatedApprovalIDs) == 0 {
					t.Error("a mismatch must name the approval that stopped applying")
				}
			}
		})
	}
}

func TestInvariant_I17_TheRowSurvivesTheObjectsItCameFrom(t *testing.T) {
	// The property only holds if the row is a copy, not a view. Mutating the
	// action after recording must not change what the log says happened.
	recorder, store := newTestRecorder(t)

	resolved := testResolved()
	id, err := recorder.RecordEvaluation(context.Background(), Evaluation{
		Request:  testRequest(),
		Context:  testContext(),
		Resolved: resolved,
		Decision: action.Decision{
			Outcome: action.OutcomeAllow, Class: action.ClassApprovalMatch,
			Reason: "approval 42 covers this action", ApprovalID: action.Ref(42), EngineVersion: 1,
		},
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}

	// The script is rewritten and re-resolved in place, as it would be on a
	// later run.
	resolved.Effects[0].Target.Display = "~/Documents"
	resolved.Effects[0].Target.Scope = action.ScopeHome
	resolved.Fingerprints[0].Value = "hash-documents"

	stored, err := store.Audit.Get(context.Background(), *id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got := stored.Resolved.DisplayTargets(); len(got) != 1 || got[0] != "./dist" {
		t.Errorf("stored targets = %v, want the ones the decision was made about", got)
	}
	if got := stored.Resolved.FingerprintMap()["npm-script:package.json#scripts.cleanup"]; got != "hash-dist" {
		t.Errorf("stored fingerprint = %q, want the value resolution depended on", got)
	}
}
