package storage

import (
	"context"
	"testing"
	"time"

	"github.com/Vadym903/Intenter/internal/action"
)

// insertDecision stores one audit row with the decision and class under test.
// Everything else is the sample event, because none of it changes a count.
func insertDecision(t *testing.T, store *Store, projectID, sessionID string,
	decision action.DecisionOutcome, class action.DecisionClass, at time.Time,
) {
	t.Helper()

	event := sampleAuditEvent(projectID)
	event.SessionID = sessionID
	event.Decision = decision
	event.DecisionClass = class
	event.At = at

	if _, err := store.Audit.Insert(context.Background(), event); err != nil {
		t.Fatalf("insert audit event: %v", err)
	}
}

func TestSummarizeSplitsAllowsByWhatDecidedThem(t *testing.T) {
	// The split is the point of the summary: a baseline read was never going to
	// prompt, so counting it as a prompt avoided would inflate the one number a
	// user is meant to trust.
	store := newTestStore(t)
	project := seedProject(t, store, "/w/demo")
	now := time.Now().UTC()

	insertDecision(t, store, project.ID, "s1", action.OutcomeAllow, action.ClassApprovalMatch, now)
	insertDecision(t, store, project.ID, "s1", action.OutcomeAllow, action.ClassRuleImport, now)
	insertDecision(t, store, project.ID, "s1", action.OutcomeAllow, action.ClassPolicyReadonlyWorkspace, now)
	insertDecision(t, store, project.ID, "s1", action.OutcomeAsk, action.ClassNoMatchingApproval, now)
	insertDecision(t, store, project.ID, "s1", action.OutcomeBlock, action.HardRuleClass("R2"), now)

	summary, err := store.Audit.Summarize(context.Background(), action.AuditFilter{})
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}

	if summary.Total != 5 {
		t.Errorf("total = %d, want 5", summary.Total)
	}
	if summary.Allowed != 3 {
		t.Errorf("allowed = %d, want 3", summary.Allowed)
	}
	if summary.AllowedByApproval != 2 {
		t.Errorf("allowed by approval = %d, want 2 (match + import)", summary.AllowedByApproval)
	}
	if summary.AllowedBaseline != 1 {
		t.Errorf("baseline = %d, want 1", summary.AllowedBaseline)
	}
	if summary.Asked != 1 || summary.Blocked != 1 {
		t.Errorf("asked = %d, blocked = %d, want 1 and 1", summary.Asked, summary.Blocked)
	}
	if summary.PromptsAvoided() != 2 {
		t.Errorf("prompts avoided = %d, want 2", summary.PromptsAvoided())
	}
}

func TestSummarizeScopesToOneSession(t *testing.T) {
	store := newTestStore(t)
	project := seedProject(t, store, "/w/demo")
	now := time.Now().UTC()

	insertDecision(t, store, project.ID, "s1", action.OutcomeAllow, action.ClassApprovalMatch, now)
	insertDecision(t, store, project.ID, "s2", action.OutcomeAllow, action.ClassApprovalMatch, now)
	insertDecision(t, store, project.ID, "s2", action.OutcomeBlock, action.HardRuleClass("R1"), now)

	summary, err := store.Audit.Summarize(context.Background(),
		action.AuditFilter{SessionID: "s2"})
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if summary.Total != 2 || summary.AllowedByApproval != 1 || summary.Blocked != 1 {
		t.Errorf("summary = %+v, want only session s2's two events", summary)
	}
}

func TestSummarizeHonoursTheTimeWindow(t *testing.T) {
	store := newTestStore(t)
	project := seedProject(t, store, "/w/demo")
	now := time.Now().UTC()

	insertDecision(t, store, project.ID, "s1", action.OutcomeAllow, action.ClassApprovalMatch, now.Add(-48*time.Hour))
	insertDecision(t, store, project.ID, "s1", action.OutcomeAllow, action.ClassApprovalMatch, now)

	since := now.Add(-24 * time.Hour)
	summary, err := store.Audit.Summarize(context.Background(),
		action.AuditFilter{Since: &since})
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if summary.Total != 1 {
		t.Errorf("total = %d, want only the recent event", summary.Total)
	}
	if summary.FirstAt == nil || summary.LastAt == nil {
		t.Fatalf("a non-empty summary must carry its bounds: %+v", summary)
	}
	if summary.FirstAt.Before(since) {
		t.Errorf("first = %s, want it inside the window starting %s", summary.FirstAt, since)
	}
}

func TestSummarizeOfNothingIsEmptyRatherThanAnError(t *testing.T) {
	// The session-end notice asks for this on every session, including ones in
	// which Intenter was never consulted.
	store := newTestStore(t)

	summary, err := store.Audit.Summarize(context.Background(),
		action.AuditFilter{SessionID: "never-seen"})
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if !summary.Empty() {
		t.Errorf("summary = %+v, want empty", summary)
	}
	if summary.FirstAt != nil || summary.LastAt != nil {
		t.Errorf("an empty summary must carry no bounds: %+v", summary)
	}
}

func TestSummarizeIgnoresDryRuns(t *testing.T) {
	// Setup's self-test runs the real hook path. Counting it would mean every
	// installation starts with a decision nobody made.
	store := newTestStore(t)
	project := seedProject(t, store, "/w/demo")

	event := sampleAuditEvent(project.ID)
	event.SessionID = "s1"
	event.Decision = action.OutcomeAllow
	event.DecisionClass = action.ClassApprovalMatch
	event.At = time.Now().UTC()
	event.DryRun = true
	if _, err := store.Audit.Insert(context.Background(), event); err != nil {
		t.Fatalf("insert audit event: %v", err)
	}

	summary, err := store.Audit.Summarize(context.Background(), action.AuditFilter{})
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if !summary.Empty() {
		t.Errorf("summary = %+v, want dry runs excluded", summary)
	}
}
