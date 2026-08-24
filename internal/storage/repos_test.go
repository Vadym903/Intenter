package storage

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Vadym903/Intenter/internal/action"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(openTestDB(t))
}

func seedProject(t *testing.T, store *Store, root string) action.Project {
	t.Helper()
	project := action.Project{
		ID:        action.ProjectID(root),
		RootPath:  root,
		RemoteURL: "git@github.com:acme/demo.git",
	}
	if err := store.Projects.Upsert(context.Background(), project); err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	return project
}

func sampleApproval(projectID string) *action.Approval {
	return &action.Approval{
		ProjectID:   projectID,
		Kind:        action.ApprovalExact,
		SemanticOps: []action.SemanticOp{action.OpRunScript, action.OpFSDelete},
		Envelope: []action.EnvelopeEntry{
			{Type: action.EffectRead, Scope: action.ScopeWorkspace},
			{Type: action.EffectDelete, Scope: action.ScopeWorkspaceGenerated,
				Flags: []action.EffectFlag{action.EffectFlagForce, action.EffectFlagRecursive}},
		},
		Targets: []string{"./dist", "package.json"},
		Network: []action.NetworkTarget{},
		Conditions: []action.ApprovalCondition{
			{Kind: action.ConditionFingerprint, Key: "npm-script:package.json#scripts.cleanup", Value: "aaa"},
			{Kind: action.ConditionFingerprint, Key: "npm-config:.npmrc#script-shell", Value: "bbb"},
		},
		EngineVersion:         1,
		Origin:                action.OriginClaudeRule,
		OriginRef:             "local:Bash(npm run cleanup)",
		CreatedFromRawCommand: "npm run cleanup",
		CreatedByAgent:        "claude",
		State:                 action.ApprovalActive,
		Note:                  "cleanup dist",
	}
}

func TestProjectUpsertRoundTrip(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	project := seedProject(t, store, "/w/demo")

	got, err := store.Projects.Get(ctx, project.ID)
	if err != nil {
		t.Fatalf("get project: %v", err)
	}
	if got.RootPath != "/w/demo" || got.RemoteURL != project.RemoteURL {
		t.Errorf("project = %+v", got)
	}
	if got.FirstSeenAt.IsZero() || got.LastSeenAt.IsZero() {
		t.Error("timestamps must be set")
	}

	firstSeen := got.FirstSeenAt
	later := time.Now().Add(time.Hour)
	if err := store.Projects.Upsert(ctx, action.Project{ID: project.ID, RootPath: "/w/demo", LastSeenAt: later}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	got, err = store.Projects.Get(ctx, project.ID)
	if err != nil {
		t.Fatalf("get project: %v", err)
	}
	if !got.FirstSeenAt.Equal(firstSeen) {
		t.Error("first_seen_at must not move")
	}
	if !got.LastSeenAt.After(firstSeen) {
		t.Error("last_seen_at must advance")
	}
	if got.RemoteURL == "" {
		t.Error("an upsert without a remote must keep the known one")
	}

	if _, err := store.Projects.FindByRoot(ctx, "/w/demo"); err != nil {
		t.Errorf("FindByRoot: %v", err)
	}
	if _, err := store.Projects.Get(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing project error = %v, want ErrNotFound", err)
	}

	projects, err := store.Projects.List(ctx)
	if err != nil || len(projects) != 1 {
		t.Errorf("List = %v, %v", projects, err)
	}
}

func TestApprovalInsertGetRoundTrip(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	project := seedProject(t, store, "/w/demo")

	approval := sampleApproval(project.ID)
	id, err := store.Approvals.Insert(ctx, approval)
	if err != nil {
		t.Fatalf("insert approval: %v", err)
	}
	if id == 0 || approval.ID != id {
		t.Fatalf("insert returned id %d, approval.ID %d", id, approval.ID)
	}

	got, err := store.Approvals.Get(ctx, id)
	if err != nil {
		t.Fatalf("get approval: %v", err)
	}
	if got.Kind != action.ApprovalExact || got.State != action.ApprovalActive {
		t.Errorf("approval = %+v", got)
	}
	if len(got.SemanticOps) != 2 || got.SemanticOps[0] != action.OpRunScript {
		t.Errorf("semantic ops = %v", got.SemanticOps)
	}
	if len(got.Envelope) != 2 {
		t.Fatalf("envelope = %v", got.Envelope)
	}
	if got.Envelope[1].Key() != "DELETE/WORKSPACE_GENERATED{force,recursive}[]" {
		t.Errorf("envelope entry = %q", got.Envelope[1].Key())
	}
	if len(got.Targets) != 2 || got.Targets[0] != "./dist" {
		t.Errorf("targets = %v", got.Targets)
	}
	fingerprints := got.Fingerprints()
	if fingerprints["npm-script:package.json#scripts.cleanup"] != "aaa" || len(fingerprints) != 2 {
		t.Errorf("fingerprints = %v", fingerprints)
	}
	if got.Origin != action.OriginClaudeRule || got.OriginRef != approval.OriginRef {
		t.Errorf("origin = %s / %s", got.Origin, got.OriginRef)
	}
	if got.Note != "cleanup dist" || got.CreatedFromRawCommand != "npm run cleanup" {
		t.Errorf("provenance = %+v", got)
	}
	if got.CreatedAt.IsZero() {
		t.Error("created_at must be set")
	}

	events, err := store.ApprovalEvents.ListByApproval(ctx, id, 10)
	if err != nil {
		t.Fatalf("list approval events: %v", err)
	}
	if len(events) != 1 || events[0].Type != action.ApprovalEventCreated {
		t.Errorf("insert must record a created event, got %v", events)
	}

	if _, err := store.Approvals.Get(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing approval error = %v, want ErrNotFound", err)
	}
}

func TestSemanticApprovalStoresNoTargets(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	project := seedProject(t, store, "/w/demo")

	approval := sampleApproval(project.ID)
	approval.Kind = action.ApprovalSemantic
	approval.Targets = nil
	id, err := store.Approvals.Insert(ctx, approval)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := store.Approvals.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Targets) != 0 {
		t.Errorf("SEMANTIC approvals carry no targets, got %v", got.Targets)
	}
}

func TestApprovalListOrdersExactBeforeSemantic(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	project := seedProject(t, store, "/w/demo")

	semantic := sampleApproval(project.ID)
	semantic.Kind = action.ApprovalSemantic
	semantic.Targets = nil
	if _, err := store.Approvals.Insert(ctx, semantic); err != nil {
		t.Fatalf("insert semantic: %v", err)
	}
	exactOne := sampleApproval(project.ID)
	if _, err := store.Approvals.Insert(ctx, exactOne); err != nil {
		t.Fatalf("insert exact: %v", err)
	}
	exactTwo := sampleApproval(project.ID)
	if _, err := store.Approvals.Insert(ctx, exactTwo); err != nil {
		t.Fatalf("insert exact: %v", err)
	}

	list, err := store.Approvals.List(ctx, ApprovalFilter{ProjectID: project.ID})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("list = %d approvals, want 3", len(list))
	}
	if list[0].Kind != action.ApprovalExact || list[1].Kind != action.ApprovalExact {
		t.Errorf("EXACT approvals must come first: %v %v", list[0].Kind, list[1].Kind)
	}
	if list[0].ID >= list[1].ID {
		t.Error("same-kind approvals must be ordered by ascending id (§20.1)")
	}
	if list[2].Kind != action.ApprovalSemantic {
		t.Errorf("SEMANTIC approval must come last, got %v", list[2].Kind)
	}
	for _, approval := range list {
		if len(approval.Conditions) != 2 {
			t.Errorf("approval %d lost its conditions: %v", approval.ID, approval.Conditions)
		}
	}
}

func TestApprovalListFiltersProjectAndState(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	demo := seedProject(t, store, "/w/demo")
	other := seedProject(t, store, "/w/other")

	active := sampleApproval(demo.ID)
	activeID, err := store.Approvals.Insert(ctx, active)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	disabled := sampleApproval(demo.ID)
	disabledID, err := store.Approvals.Insert(ctx, disabled)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := store.Approvals.SetState(ctx, disabledID, action.ApprovalDisabled, time.Now()); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := store.Approvals.Insert(ctx, sampleApproval(other.ID)); err != nil {
		t.Fatalf("insert other project: %v", err)
	}

	activeOnly, err := store.Approvals.List(ctx, ApprovalFilter{ProjectID: demo.ID})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(activeOnly) != 1 || activeOnly[0].ID != activeID {
		t.Errorf("active list = %v, want only approval %d", activeOnly, activeID)
	}

	withInactive, err := store.Approvals.List(ctx, ApprovalFilter{ProjectID: demo.ID, IncludeInactive: true})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(withInactive) != 2 {
		t.Errorf("inactive list = %d, want 2", len(withInactive))
	}

	all, err := store.Approvals.List(ctx, ApprovalFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("cross-project active list = %d, want 2", len(all))
	}
}

func TestApprovalStateMachine(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	project := seedProject(t, store, "/w/demo")

	id, err := store.Approvals.Insert(ctx, sampleApproval(project.ID))
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	now := time.Now()
	if err := store.Approvals.SetState(ctx, id, action.ApprovalDisabled, now); err != nil {
		t.Fatalf("disable: %v", err)
	}
	got, _ := store.Approvals.Get(ctx, id)
	if got.State != action.ApprovalDisabled || got.DisabledAt == nil {
		t.Errorf("disabled approval = %+v", got)
	}

	if err := store.Approvals.SetState(ctx, id, action.ApprovalActive, now); err != nil {
		t.Fatalf("enable: %v", err)
	}
	got, _ = store.Approvals.Get(ctx, id)
	if got.State != action.ApprovalActive || got.DisabledAt != nil {
		t.Errorf("re-enabled approval = %+v", got)
	}

	if err := store.Approvals.SetState(ctx, id, action.ApprovalRevoked, now); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	got, _ = store.Approvals.Get(ctx, id)
	if got.State != action.ApprovalRevoked || got.RevokedAt == nil {
		t.Errorf("revoked approval = %+v", got)
	}

	if err := store.Approvals.SetState(ctx, id, action.ApprovalActive, now); err == nil {
		t.Error("revocation must be terminal (I-15)")
	}

	// The row itself is never deleted and every transition is audited.
	events, err := store.ApprovalEvents.ListByApproval(ctx, id, 10)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 4 {
		t.Errorf("approval events = %d, want created+disabled+enabled+revoked", len(events))
	}

	if err := store.Approvals.SetState(ctx, 999, action.ApprovalDisabled, now); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown approval error = %v, want ErrNotFound", err)
	}
}

func TestApprovalUsageTracking(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	project := seedProject(t, store, "/w/demo")

	id, err := store.Approvals.Insert(ctx, sampleApproval(project.ID))
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	used := time.Now()
	for range 3 {
		if err := store.Approvals.RecordUse(ctx, id, used); err != nil {
			t.Fatalf("record use: %v", err)
		}
	}
	got, _ := store.Approvals.Get(ctx, id)
	if got.UseCount != 3 {
		t.Errorf("use_count = %d, want 3", got.UseCount)
	}
	if got.LastUsedAt == nil {
		t.Error("last_used_at must be set")
	}

	if err := store.Approvals.RecordUse(ctx, 999, used); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown approval error = %v, want ErrNotFound", err)
	}

	counts, err := store.Approvals.CountByState(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if counts[action.ApprovalActive] != 1 {
		t.Errorf("counts = %v", counts)
	}
}

func TestApprovalEventsCarryDetails(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	project := seedProject(t, store, "/w/demo")
	id, _ := store.Approvals.Insert(ctx, sampleApproval(project.ID))

	if _, err := store.ApprovalEvents.Insert(ctx, action.ApprovalEvent{
		ApprovalID:   id,
		Type:         action.ApprovalEventNotMatched,
		AuditEventID: action.Ref(42),
		Details:      map[string]any{"differences": []any{"fingerprint changed"}},
	}); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	events, err := store.ApprovalEvents.ListByApproval(ctx, id, 10)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	newest := events[0]
	if newest.Type != action.ApprovalEventNotMatched {
		t.Errorf("events must be newest first, got %s", newest.Type)
	}
	if newest.AuditEventID == nil || *newest.AuditEventID != 42 {
		t.Errorf("audit_event_id = %v", newest.AuditEventID)
	}
	if newest.Details["differences"] == nil {
		t.Errorf("details = %v", newest.Details)
	}
}

func sampleAuditEvent(projectID string) *action.AuditEvent {
	dist := action.Target{Raw: "./dist", Canonical: "/w/demo/dist", Display: "./dist",
		Scope: action.ScopeWorkspaceGenerated, Status: action.TargetResolved, IsDir: true}
	return &action.AuditEvent{
		Agent:        "claude",
		AgentVersion: "2.1.233",
		SessionID:    "session-1",
		ToolUseID:    "toolu_01",
		HookEvent:    "PreToolUse",
		ProjectID:    projectID,
		Cwd:          "/w/demo",
		Tool:         "Bash",
		Dialect:      action.DialectPosix,
		RawCommand:   "npm run cleanup",
		Resolved: &action.ResolvedAction{
			RawCommand:  "npm run cleanup",
			Dialect:     action.DialectPosix,
			SemanticOps: []action.SemanticOp{action.OpRunScript, action.OpFSDelete},
			Commands: []action.ResolvedCommand{{
				Executable:   "rm",
				SemanticOp:   action.OpFSDelete,
				Targets:      []action.Target{dist},
				Status:       action.StatusResolved,
				ResolvedFrom: []string{"npm run cleanup", "rm -rf ./dist"},
				RawText:      "rm -rf ./dist",
			}},
			Effects: []action.Effect{{Type: action.EffectDelete, Target: &dist,
				Flags: []action.EffectFlag{action.EffectFlagRecursive, action.EffectFlagForce}}},
			Fingerprints: []action.Fingerprint{{Key: "npm-script:package.json#scripts.cleanup", Value: "aaa"}},
			Status:       action.StatusResolved,
		},
		ResolutionStatus: action.StatusResolved,
		Decision:         action.OutcomeAsk,
		DecisionClass:    action.ClassNoMatchingApproval,
		Reason:           "no approval yet",
		AdapterContext:   map[string]any{"permission_mode": "default"},
		EngineVersion:    1,
		Explanation:      []string{"resolved: npm run cleanup -> rm -rf ./dist", "targets: ./dist [WORKSPACE_GENERATED]"},
	}
}

func TestAuditEventRoundTripPreservesExplanation(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	project := seedProject(t, store, "/w/demo")

	event := sampleAuditEvent(project.ID)
	id, err := store.Audit.Insert(ctx, event)
	if err != nil {
		t.Fatalf("insert audit event: %v", err)
	}

	got, err := store.Audit.Get(ctx, id)
	if err != nil {
		t.Fatalf("get audit event: %v", err)
	}
	if got.Decision != action.OutcomeAsk || got.DecisionClass != action.ClassNoMatchingApproval {
		t.Errorf("decision = %s / %s", got.Decision, got.DecisionClass)
	}
	if got.RawCommand != "npm run cleanup" || got.Dialect != action.DialectPosix {
		t.Errorf("event = %+v", got)
	}
	if got.Resolved == nil || len(got.Resolved.Commands) != 1 {
		t.Fatalf("resolved action lost: %+v", got.Resolved)
	}
	if got.Resolved.Commands[0].Targets[0].Scope != action.ScopeWorkspaceGenerated {
		t.Errorf("target scope lost: %+v", got.Resolved.Commands[0].Targets[0])
	}
	if len(got.Resolved.Fingerprints) != 1 {
		t.Errorf("fingerprints lost: %v", got.Resolved.Fingerprints)
	}
	// INVARIANT I-17: the stored row alone explains the decision.
	if len(got.Explanation) != 2 || got.Explanation[0] != event.Explanation[0] {
		t.Errorf("explanation lost: %v", got.Explanation)
	}
	if got.ResolvedSummary() != "rm -rf ./dist" {
		t.Errorf("resolved summary = %q", got.ResolvedSummary())
	}
	if got.AdapterContext["permission_mode"] != "default" {
		t.Errorf("adapter context lost: %v", got.AdapterContext)
	}
	if got.PromptShown || got.DryRun {
		t.Error("flags must default to false")
	}
}

func TestAuditUpdatesAndCorrelation(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	project := seedProject(t, store, "/w/demo")

	id, err := store.Audit.Insert(ctx, sampleAuditEvent(project.ID))
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	if err := store.Audit.UpdatePrompt(ctx, id, []any{map[string]any{
		"type": "addRules", "behavior": "allow", "destination": "localSettings",
	}}); err != nil {
		t.Fatalf("update prompt: %v", err)
	}
	if err := store.Audit.UpdateExecution(ctx, id, action.ExecutionCompleted, time.Now(), "removed 3 files"); err != nil {
		t.Fatalf("update execution: %v", err)
	}
	if err := store.Audit.SetAdapterAction(ctx, id, action.AdapterDefer); err != nil {
		t.Fatalf("set adapter action: %v", err)
	}

	got, err := store.Audit.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.PromptShown || len(got.PermissionSuggestions) != 1 {
		t.Errorf("prompt fields = %v / %v", got.PromptShown, got.PermissionSuggestions)
	}
	if got.ExecutionStatus != action.ExecutionCompleted || got.ExecutionAt == nil {
		t.Errorf("execution fields = %v / %v", got.ExecutionStatus, got.ExecutionAt)
	}
	if got.ResponseSummary != "removed 3 files" {
		t.Errorf("response summary = %q", got.ResponseSummary)
	}
	if got.AdapterAction != action.AdapterDefer {
		t.Errorf("adapter action = %q", got.AdapterAction)
	}

	byTool, err := store.Audit.FindByToolUseID(ctx, "session-1", "toolu_01")
	if err != nil || byTool.ID != id {
		t.Errorf("FindByToolUseID = %v, %v", byTool, err)
	}
	if _, err := store.Audit.FindByToolUseID(ctx, "session-1", "toolu_missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing tool_use_id error = %v", err)
	}

	recent, err := store.Audit.FindRecentBySessionCommand(ctx, "session-1", "npm run cleanup", time.Now().Add(-60*time.Second))
	if err != nil || recent.ID != id {
		t.Errorf("FindRecentBySessionCommand = %v, %v", recent, err)
	}
	if _, err := store.Audit.FindRecentBySessionCommand(ctx, "session-1", "npm run cleanup", time.Now().Add(time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Errorf("events outside the window must not correlate, got %v", err)
	}

	if err := store.Audit.UpdatePrompt(ctx, 999, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown event error = %v", err)
	}
}

func TestAuditListFilters(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	project := seedProject(t, store, "/w/demo")
	other := seedProject(t, store, "/w/other")

	base := time.Now().Add(-2 * time.Hour)
	blocked := sampleAuditEvent(project.ID)
	blocked.Decision = action.OutcomeBlock
	blocked.DecisionClass = action.HardRuleClass("R2")
	blocked.HardRule = "R2"
	blocked.At = base
	if _, err := store.Audit.Insert(ctx, blocked); err != nil {
		t.Fatalf("insert: %v", err)
	}

	allowed := sampleAuditEvent(project.ID)
	allowed.Decision = action.OutcomeAllow
	allowed.DecisionClass = action.ClassApprovalMatch
	allowed.MatchedApprovalID = action.Ref(7)
	allowed.SessionID = "session-2"
	allowed.At = time.Now()
	if _, err := store.Audit.Insert(ctx, allowed); err != nil {
		t.Fatalf("insert: %v", err)
	}

	dry := sampleAuditEvent(project.ID)
	dry.DryRun = true
	if _, err := store.Audit.Insert(ctx, dry); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Timestamped explicitly, apart from the others: two inserts within one
	// clock tick (Windows ticks at milliseconds) would otherwise share a
	// timestamp and make "newest first" undecidable.
	otherProject := sampleAuditEvent(other.ID)
	otherProject.At = base.Add(90 * time.Minute)
	if _, err := store.Audit.Insert(ctx, otherProject); err != nil {
		t.Fatalf("insert: %v", err)
	}

	all, err := store.Audit.List(ctx, action.AuditFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("default list = %d events, want 3 (dry-run excluded)", len(all))
	}
	if !all[0].At.After(all[1].At) {
		t.Error("history must be newest first")
	}

	blockOutcome := action.OutcomeBlock
	onlyBlocked, err := store.Audit.List(ctx, action.AuditFilter{Decision: &blockOutcome})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(onlyBlocked) != 1 || onlyBlocked[0].Class != action.HardRuleClass("R2") {
		t.Errorf("blocked filter = %v", onlyBlocked)
	}

	byProject, err := store.Audit.List(ctx, action.AuditFilter{ProjectID: other.ID})
	if err != nil || len(byProject) != 1 {
		t.Errorf("project filter = %v, %v", byProject, err)
	}

	bySession, err := store.Audit.List(ctx, action.AuditFilter{SessionID: "session-2"})
	if err != nil || len(bySession) != 1 || bySession[0].ApprovalID == nil {
		t.Errorf("session filter = %v, %v", bySession, err)
	}

	since := time.Now().Add(-time.Hour)
	recent, err := store.Audit.List(ctx, action.AuditFilter{Since: &since})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, event := range recent {
		if event.At.Before(since) {
			t.Errorf("event %d is older than the since filter", event.ID)
		}
	}

	limited, err := store.Audit.List(ctx, action.AuditFilter{Limit: 1})
	if err != nil || len(limited) != 1 {
		t.Errorf("limit = %v, %v", limited, err)
	}

	withDry, err := store.Audit.List(ctx, action.AuditFilter{IncludeDryRun: true})
	if err != nil || len(withDry) != 4 {
		t.Errorf("include dry run = %d events, want 4", len(withDry))
	}

	counts, err := store.Audit.CountByDecisionSince(ctx, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("counts: %v", err)
	}
	if counts[action.OutcomeBlock] != 1 || counts[action.OutcomeAllow] != 1 {
		t.Errorf("counts = %v", counts)
	}
}

func TestRuleImportIsOnceOnly(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	project := seedProject(t, store, "/w/demo")
	approvalID, _ := store.Approvals.Insert(ctx, sampleApproval(project.ID))

	record := action.RuleImport{
		ProjectID:  project.ID,
		Agent:      "claude",
		RuleKey:    "local:Bash(npm run cleanup)",
		RawCommand: "npm run cleanup",
		ApprovalID: approvalID,
	}

	inserted, err := store.Imports.InsertOnce(ctx, record)
	if err != nil || !inserted {
		t.Fatalf("first import = %v, %v", inserted, err)
	}

	inserted, err = store.Imports.InsertOnce(ctx, record)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if inserted {
		t.Error("a rule must import at most once per project and raw command (§19.5)")
	}

	exists, err := store.Imports.Exists(ctx, project.ID, "claude", record.RuleKey, record.RawCommand)
	if err != nil || !exists {
		t.Errorf("Exists = %v, %v", exists, err)
	}
	exists, err = store.Imports.Exists(ctx, project.ID, "claude", record.RuleKey, "npm run build")
	if err != nil || exists {
		t.Errorf("a different raw command must be importable: %v, %v", exists, err)
	}

	any, err := store.Imports.AnyExists(ctx, project.ID, "claude",
		[]string{"user:Bash(npm run *)", record.RuleKey}, record.RawCommand)
	if err != nil || !any {
		t.Errorf("AnyExists = %v, %v", any, err)
	}

	imports, err := store.Imports.ListByApproval(ctx, approvalID)
	if err != nil || len(imports) != 1 {
		t.Errorf("ListByApproval = %v, %v", imports, err)
	}
}

func TestMetaRepo(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if err := store.Meta.SetAll(ctx, map[string]string{
		MetaIntenterVersion: "0.1.0",
		MetaClaudeVersion:   "2.1.233",
	}); err != nil {
		t.Fatalf("SetAll: %v", err)
	}
	if err := store.Meta.Set(ctx, MetaClaudeVersion, "2.1.240"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	value, err := store.Meta.Get(ctx, MetaClaudeVersion)
	if err != nil || value != "2.1.240" {
		t.Errorf("Get = %q, %v", value, err)
	}
	if _, err := store.Meta.Get(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing key error = %v", err)
	}

	value, ok, err := store.Meta.Lookup(ctx, "missing")
	if err != nil || ok || value != "" {
		t.Errorf("Lookup = %q, %v, %v", value, ok, err)
	}

	all, err := store.Meta.All(ctx)
	if err != nil || len(all) != 2 {
		t.Errorf("All = %v, %v", all, err)
	}
}

func TestConcurrentWritersSerializeCorrectly(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	project := seedProject(t, store, "/w/demo")

	const writers = 8
	const perWriter = 5

	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for w := range writers {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := range perWriter {
				event := sampleAuditEvent(project.ID)
				event.SessionID = fmt.Sprintf("session-%d", worker)
				event.ToolUseID = fmt.Sprintf("toolu_%d_%d", worker, i)
				if _, err := store.Audit.Insert(ctx, event); err != nil {
					errs <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent insert: %v", err)
	}

	var count int
	if err := store.DB.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != writers*perWriter {
		t.Errorf("rows = %d, want %d", count, writers*perWriter)
	}
}

func TestApprovalsSurviveReopen(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/intenter.db"
	ctx := context.Background()

	db, err := OpenAndMigrate(ctx, path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	store := NewStore(db)
	project := seedProject(t, store, "/w/demo")
	id, err := store.Approvals.Insert(ctx, sampleApproval(project.ID))
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened, err := OpenAndMigrate(ctx, path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	got, err := NewStore(reopened).Approvals.Get(ctx, id)
	if err != nil {
		t.Fatalf("get after restart: %v", err)
	}
	if len(got.Conditions) != 2 || got.CreatedFromRawCommand != "npm run cleanup" {
		t.Errorf("approval did not survive the restart: %+v", got)
	}
}
