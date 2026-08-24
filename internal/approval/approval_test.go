package approval

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/config"
	"github.com/Vadym903/Intenter/internal/platform"
	"github.com/Vadym903/Intenter/internal/policy"
	"github.com/Vadym903/Intenter/internal/storage"
)

const (
	testEngineVersion = 1
	testWorkspace     = "/w/demo"
	testHome          = "/Users/u"
)

func newTestStore(t *testing.T) *storage.Store {
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
	return store
}

func target(display, canonical string, scope action.Scope, flags ...action.TargetFlag) action.Target {
	out := action.Target{
		Raw: display, Display: display, Canonical: canonical,
		Scope: scope, Status: action.TargetResolved,
	}
	out.AddFlags(flags...)
	return out
}

func effect(kind action.EffectType, on action.Target, flags ...action.EffectFlag) action.Effect {
	pinned := on
	out := action.Effect{Type: kind, Target: &pinned}
	out.AddFlags(flags...)
	return out
}

// cleanupAction is the demo action: `npm run cleanup` resolving to
// `rm -rf ./dist` inside the workspace.
func cleanupAction() *action.ResolvedAction {
	dist := target("./dist", testWorkspace+"/dist", action.ScopeWorkspaceGenerated)
	deletion := effect(action.EffectDelete, dist, action.EffectFlagRecursive, action.EffectFlagForce)

	return &action.ResolvedAction{
		RawCommand:  "npm run cleanup",
		ProjectID:   action.ProjectID(testWorkspace),
		Status:      action.StatusResolved,
		SemanticOps: []action.SemanticOp{action.OpRunScript},
		Effects:     []action.Effect{deletion},
		Commands: []action.ResolvedCommand{{
			SemanticOp: action.OpRunScript,
			Status:     action.StatusResolved,
			Targets:    []action.Target{dist},
			Effects:    []action.Effect{deletion},
		}},
		Fingerprints: []action.Fingerprint{
			{Key: "npm-script:package.json#scripts.cleanup", Value: "hash-dist"},
			{Key: "npm-config:.npmrc#script-shell", Value: "unset"},
		},
	}
}

// changedCleanupAction is the same command after the script was rewritten to
// delete a home directory.
func changedCleanupAction() *action.ResolvedAction {
	documents := target("~/Documents", testHome+"/Documents", action.ScopeHome, action.FlagBroad)
	deletion := effect(action.EffectDelete, documents, action.EffectFlagRecursive, action.EffectFlagForce)

	act := cleanupAction()
	act.Effects = []action.Effect{deletion}
	act.Commands = []action.ResolvedCommand{{
		SemanticOp: action.OpRunScript,
		Status:     action.StatusResolved,
		Targets:    []action.Target{documents},
		Effects:    []action.Effect{deletion},
	}}
	act.Fingerprints = []action.Fingerprint{
		{Key: "npm-script:package.json#scripts.cleanup", Value: "hash-documents"},
		{Key: "npm-config:.npmrc#script-shell", Value: "unset"},
	}
	return act
}

func policyInput(act *action.ResolvedAction) policy.Input {
	return policy.Input{
		Action: act,
		Context: &action.Context{
			WorkspaceRoot: testWorkspace,
			ProjectID:     action.ProjectID(testWorkspace),
			HomeDir:       testHome,
			Status:        action.ContextOK,
		},
		Config: config.Default(),
		Rules:  platform.PathRules{},
		Agent:  "claude",
	}
}

func createRequest(act *action.ResolvedAction) CreateRequest {
	return CreateRequest{
		Action:        act,
		Policy:        policyInput(act),
		Kind:          action.ApprovalExact,
		Origin:        action.OriginCLI,
		Agent:         "claude",
		EngineVersion: testEngineVersion,
		Now:           time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC),
	}
}

func mustCreate(t *testing.T, store *storage.Store, request CreateRequest) *action.Approval {
	t.Helper()
	created, err := NewCreator(store).Create(context.Background(), request)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}
	return created
}

func TestApprovalRecordsEveryFingerprintResolutionDependedOn(t *testing.T) {
	// INVARIANT I-16.
	store := newTestStore(t)
	created := mustCreate(t, store, createRequest(cleanupAction()))

	fingerprints := created.Fingerprints()
	for _, key := range []string{"npm-script:package.json#scripts.cleanup", "npm-config:.npmrc#script-shell"} {
		if _, ok := fingerprints[key]; !ok {
			t.Errorf("approval is missing the %s condition", key)
		}
	}
	if created.Kind != action.ApprovalExact {
		t.Errorf("kind = %s, want EXACT by default (§19.2)", created.Kind)
	}
	if len(created.Targets) != 1 || created.Targets[0] != "./dist" {
		t.Errorf("targets = %v, want [./dist]", created.Targets)
	}
	if created.CreatedFromRawCommand != "npm run cleanup" {
		t.Errorf("provenance = %q, want the raw command", created.CreatedFromRawCommand)
	}
}

func TestApprovalCreationRefusesUnapprovableActions(t *testing.T) {
	// INVARIANT I-11 and §19.3.
	tests := []struct {
		name   string
		mutate func(act *action.ResolvedAction)
	}{
		{"unresolved", func(act *action.ResolvedAction) { act.Status = action.StatusUnresolved }},
		{"parse failed", func(act *action.ResolvedAction) { act.Status = action.StatusParseFailed }},
		{"context failed", func(act *action.ResolvedAction) { act.Status = action.StatusContextFailed }},
		{"ambiguous target", func(act *action.ResolvedAction) {
			act.Commands[0].Targets[0].Status = action.TargetAmbiguous
		}},
		{"no project", func(act *action.ResolvedAction) { act.ProjectID = "" }},
		{"no effects", func(act *action.ResolvedAction) { act.Effects = nil }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			act := cleanupAction()
			tt.mutate(act)
			if _, err := Build(createRequest(act)); err == nil {
				t.Error("want a refusal")
			}
		})
	}
}

func TestApprovalCreationRefusesWhatTheSafetyFloorStops(t *testing.T) {
	// §19.3: an action a hard rule blocks or always asks about is never
	// approvable, not even through an explicit CLI request.
	store := newTestStore(t)

	if _, err := NewCreator(store).Create(context.Background(), createRequest(changedCleanupAction())); err == nil {
		t.Fatal("deleting ~/Documents must never become an approval")
	}

	key := target("~/.ssh/id_rsa", testHome+"/.ssh/id_rsa", action.ScopeHome, action.FlagSensitive)
	reading := cleanupAction()
	reading.Effects = []action.Effect{effect(action.EffectRead, key)}
	reading.Commands[0].Effects = reading.Effects
	reading.Commands[0].Targets = []action.Target{key}
	if _, err := Build(createRequest(reading)); err == nil {
		t.Error("reading a credential always asks and must not be approvable")
	}
}

func TestApprovalMatchesTheSameResolvedAction(t *testing.T) {
	store := newTestStore(t)
	created := mustCreate(t, store, createRequest(cleanupAction()))

	outcome, err := NewMatcher(store, testEngineVersion).Match(policyInput(cleanupAction()))
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if !outcome.Matched || outcome.ApprovalID != created.ID {
		t.Fatalf("outcome = %+v, want a match on approval %d", outcome, created.ID)
	}
}

func TestApprovalMatchesADifferentCommandThatResolvesIdentically(t *testing.T) {
	// §19.2: two different raw commands that resolve identically match the same
	// approval, because what was approved is the effect.
	store := newTestStore(t)
	mustCreate(t, store, createRequest(cleanupAction()))

	direct := cleanupAction()
	direct.RawCommand = "npm run clean"

	outcome, err := NewMatcher(store, testEngineVersion).Match(policyInput(direct))
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if !outcome.Matched {
		t.Error("an identically resolving command must match the same approval")
	}
}

func TestApprovalStopsMatchingWhenTheScriptChanges(t *testing.T) {
	// INVARIANT I-1 and I-5: this is the behavior the prototype exists to show.
	store := newTestStore(t)
	created := mustCreate(t, store, createRequest(cleanupAction()))

	outcome, err := NewMatcher(store, testEngineVersion).Match(policyInput(changedCleanupAction()))
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if outcome.Matched {
		t.Fatal("a changed script must not match the old approval")
	}
	if len(outcome.Mismatches) == 0 {
		t.Fatal("want a mismatch report naming the approval")
	}
	report := outcome.Mismatches[0]
	if report.ApprovalID != created.ID {
		t.Errorf("mismatch names approval %d, want %d", report.ApprovalID, created.ID)
	}

	joined := ""
	for _, difference := range report.Differences {
		joined += difference + "\n"
	}
	for _, want := range []string{
		"npm-script:package.json#scripts.cleanup changed",
		"./dist -> ~/Documents",
		"HOME",
	} {
		if !contains(joined, want) {
			t.Errorf("differences must mention %q, got:\n%s", want, joined)
		}
	}
}

func TestApprovalIsScopedToItsProject(t *testing.T) {
	// A moved or cloned checkout has a different project id and is not even a
	// candidate (§21).
	store := newTestStore(t)
	mustCreate(t, store, createRequest(cleanupAction()))

	elsewhere := cleanupAction()
	elsewhere.ProjectID = action.ProjectID("/w/other")

	outcome, err := NewMatcher(store, testEngineVersion).Match(policyInput(elsewhere))
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if outcome.Matched {
		t.Error("an approval must not cross projects")
	}
	if len(outcome.Mismatches) != 0 {
		t.Error("an approval in another project is not a related approval")
	}
}

func TestApprovalEngineVersionMismatch(t *testing.T) {
	store := newTestStore(t)
	mustCreate(t, store, createRequest(cleanupAction()))

	outcome, err := NewMatcher(store, testEngineVersion+1).Match(policyInput(cleanupAction()))
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if outcome.Matched {
		t.Error("an approval from another engine version must not match (§20.3 rule 1)")
	}
}

func TestDisabledAndRevokedApprovalsDoNotMatch(t *testing.T) {
	for _, state := range []action.ApprovalState{action.ApprovalDisabled, action.ApprovalRevoked} {
		t.Run(string(state), func(t *testing.T) {
			store := newTestStore(t)
			created := mustCreate(t, store, createRequest(cleanupAction()))
			if err := store.Approvals.SetState(context.Background(), created.ID, state, time.Now()); err != nil {
				t.Fatalf("set state: %v", err)
			}

			outcome, err := NewMatcher(store, testEngineVersion).Match(policyInput(cleanupAction()))
			if err != nil {
				t.Fatalf("match: %v", err)
			}
			if outcome.Matched {
				t.Errorf("a %s approval must not match", state)
			}
		})
	}
}

func TestSemanticApprovalCoversOtherTargetsInTheSameScope(t *testing.T) {
	store := newTestStore(t)
	request := createRequest(cleanupAction())
	request.Kind = action.ApprovalSemantic
	created := mustCreate(t, store, request)

	if len(created.Targets) != 0 {
		t.Errorf("a SEMANTIC approval pins no targets, got %v", created.Targets)
	}

	// Another generated directory, same envelope.
	other := cleanupAction()
	build := target("./build", testWorkspace+"/build", action.ScopeWorkspaceGenerated)
	other.Effects = []action.Effect{effect(action.EffectDelete, build,
		action.EffectFlagRecursive, action.EffectFlagForce)}
	other.Commands[0].Targets = []action.Target{build}
	other.Commands[0].Effects = other.Effects

	outcome, err := NewMatcher(store, testEngineVersion).Match(policyInput(other))
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if !outcome.Matched {
		t.Error("a SEMANTIC approval covers another target in the same scope (§19.2)")
	}
}

func TestSemanticApprovalStillRespectsScopeAndFingerprints(t *testing.T) {
	store := newTestStore(t)
	request := createRequest(cleanupAction())
	request.Kind = action.ApprovalSemantic
	mustCreate(t, store, request)

	// A workspace-source delete is a different scope, so outside the envelope.
	broader := cleanupAction()
	src := target("./src", testWorkspace+"/src", action.ScopeWorkspace)
	broader.Effects = []action.Effect{effect(action.EffectDelete, src,
		action.EffectFlagRecursive, action.EffectFlagForce)}
	broader.Commands[0].Targets = []action.Target{src}
	broader.Commands[0].Effects = broader.Effects

	outcome, err := NewMatcher(store, testEngineVersion).Match(policyInput(broader))
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if outcome.Matched {
		t.Error("a SEMANTIC approval must not cover a broader scope (I-1)")
	}

	// A changed fingerprint still invalidates a SEMANTIC approval.
	changed := cleanupAction()
	changed.Fingerprints[0].Value = "different"
	outcome, err = NewMatcher(store, testEngineVersion).Match(policyInput(changed))
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if outcome.Matched {
		t.Error("a SEMANTIC approval must still honor its fingerprints (§20.3 rule 3)")
	}
}

func TestMatchRecordsUsage(t *testing.T) {
	store := newTestStore(t)
	created := mustCreate(t, store, createRequest(cleanupAction()))

	matcher := NewMatcher(store, testEngineVersion)
	for i := 0; i < 2; i++ {
		if _, err := matcher.Match(policyInput(cleanupAction())); err != nil {
			t.Fatalf("match: %v", err)
		}
	}

	stored, err := store.Approvals.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("get approval: %v", err)
	}
	if stored.UseCount != 2 {
		t.Errorf("use count = %d, want 2 (§19.4)", stored.UseCount)
	}
	if stored.LastUsedAt == nil {
		t.Error("last used must be recorded")
	}

	events, err := store.ApprovalEvents.ListByApproval(context.Background(), created.ID, 10)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	matched := 0
	for _, event := range events {
		if event.Type == action.ApprovalEventMatched {
			matched++
		}
	}
	if matched != 2 {
		t.Errorf("matched events = %d, want 2", matched)
	}
}

func TestMatchingIsAPureFunctionOfItsInputs(t *testing.T) {
	// INVARIANT I-6.
	store := newTestStore(t)
	created := mustCreate(t, store, createRequest(cleanupAction()))
	stored, err := store.Approvals.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	stored.Conditions, err = store.Conditions.ListByApproval(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("conditions: %v", err)
	}

	for i := 0; i < 5; i++ {
		if _, ok := Matches(stored, cleanupAction(), testEngineVersion); !ok {
			t.Fatal("matching must give the same answer every time")
		}
	}
}

func TestUnresolvedActionsNeverMatch(t *testing.T) {
	// §20.3 rule 7 / INVARIANT I-11.
	store := newTestStore(t)
	mustCreate(t, store, createRequest(cleanupAction()))

	for _, status := range []action.ResolutionStatus{
		action.StatusUnresolved, action.StatusParseFailed, action.StatusContextFailed,
	} {
		act := cleanupAction()
		act.Status = status
		outcome, err := NewMatcher(store, testEngineVersion).Match(policyInput(act))
		if err != nil {
			t.Fatalf("match: %v", err)
		}
		if outcome.Matched {
			t.Errorf("a %s action must never match an approval", status)
		}
	}

	ambiguous := cleanupAction()
	ambiguous.Commands[0].Targets[0].Status = action.TargetAmbiguous
	outcome, err := NewMatcher(store, testEngineVersion).Match(policyInput(ambiguous))
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if outcome.Matched {
		t.Error("an ambiguous target must never match an approval")
	}
}

func TestConsentImportCreatesOneValidatedApproval(t *testing.T) {
	store := newTestStore(t)
	importer := NewImporter(store, testEngineVersion)
	consent := &action.AgentConsent{
		Kind:     action.ConsentKindPersistentRule,
		RuleKeys: []string{"local:Bash(npm run cleanup)"},
	}

	outcome, err := importer.Import(policyInput(cleanupAction()), consent)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if !outcome.Matched {
		t.Fatal("want an imported approval")
	}

	stored, err := store.Approvals.Get(context.Background(), outcome.ApprovalID)
	if err != nil {
		t.Fatalf("get approval: %v", err)
	}
	if stored.Origin != action.OriginClaudeRule {
		t.Errorf("origin = %s, want claude_rule", stored.Origin)
	}
	if stored.OriginRef != "local:Bash(npm run cleanup)" {
		t.Errorf("origin ref = %q, want the rule key", stored.OriginRef)
	}
	if stored.Kind != action.ApprovalExact {
		t.Errorf("kind = %s, want EXACT (§19.5)", stored.Kind)
	}

	imports, err := store.Imports.ListByApproval(context.Background(), outcome.ApprovalID)
	if err != nil {
		t.Fatalf("list imports: %v", err)
	}
	if len(imports) != 1 {
		t.Errorf("imports = %d, want one row per consenting rule", len(imports))
	}
}

func TestConsentImportHappensOnlyOnce(t *testing.T) {
	// §19.5 and I-5: this is what makes a changed script ask again rather than
	// silently re-import the agent's still-present string rule.
	store := newTestStore(t)
	importer := NewImporter(store, testEngineVersion)
	consent := &action.AgentConsent{
		Kind:     action.ConsentKindPersistentRule,
		RuleKeys: []string{"local:Bash(npm run cleanup)"},
	}

	first, err := importer.Import(policyInput(cleanupAction()), consent)
	if err != nil || !first.Matched {
		t.Fatalf("first import = %+v, %v", first, err)
	}

	second, err := importer.Import(policyInput(cleanupAction()), consent)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if second.Matched {
		t.Error("the same rule must not be imported twice")
	}

	// Even after the approval is revoked, the rule is not re-imported.
	if err := store.Approvals.SetState(context.Background(), first.ApprovalID, action.ApprovalRevoked, time.Now()); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	third, err := importer.Import(policyInput(cleanupAction()), consent)
	if err != nil {
		t.Fatalf("third import: %v", err)
	}
	if third.Matched {
		t.Error("a revoked import must not be redone (§19.5)")
	}
}

func TestConsentImportRefusesWhatIsNotApprovable(t *testing.T) {
	store := newTestStore(t)
	importer := NewImporter(store, testEngineVersion)
	consent := &action.AgentConsent{
		Kind:     action.ConsentKindPersistentRule,
		RuleKeys: []string{"local:Bash(npm run cleanup)"},
	}

	// The safety floor stops it, so the agent's own rule cannot import it.
	outcome, err := importer.Import(policyInput(changedCleanupAction()), consent)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if outcome.Matched {
		t.Error("a rule must never import an action a hard rule stops (I-5)")
	}

	unresolved := cleanupAction()
	unresolved.Status = action.StatusUnresolved
	outcome, err = importer.Import(policyInput(unresolved), consent)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if outcome.Matched {
		t.Error("an unresolved action must never be imported (I-11)")
	}
}

func TestConsentImportIgnoresUnusableConsent(t *testing.T) {
	store := newTestStore(t)
	importer := NewImporter(store, testEngineVersion)

	for _, consent := range []*action.AgentConsent{
		nil,
		{Kind: "something-else", RuleKeys: []string{"x"}},
		{Kind: action.ConsentKindPersistentRule},
	} {
		outcome, err := importer.Import(policyInput(cleanupAction()), consent)
		if err != nil {
			t.Fatalf("import: %v", err)
		}
		if outcome.Matched {
			t.Errorf("consent %+v must not import anything (I-8)", consent)
		}
	}
}

func TestConsentImportForExecutionOnlyAfterADeferral(t *testing.T) {
	// Path (b) of §19.5: the user answered "don't ask again" in the agent's own
	// dialog after Intenter deferred.
	store := newTestStore(t)
	importer := NewImporter(store, testEngineVersion)
	consent := &action.AgentConsent{
		Kind:     action.ConsentKindPersistentRule,
		RuleKeys: []string{"local:Bash(npm run cleanup)"},
	}

	deferred := action.Decision{Outcome: action.OutcomeAsk, Class: action.ClassNoMatchingApproval}
	outcome, err := importer.ImportForExecution(policyInput(cleanupAction()), consent, deferred)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if !outcome.Matched {
		t.Error("consent reported with the execution must import after a deferral")
	}

	for _, decision := range []action.Decision{
		{Outcome: action.OutcomeAllow, Class: action.ClassApprovalMatch},
		{Outcome: action.OutcomeBlock, Class: action.HardRuleClass("R2")},
		{Outcome: action.OutcomeAsk, Class: action.ClassApprovalMismatch},
		{Outcome: action.OutcomeAsk, Class: action.ClassUnresolvedCommand},
	} {
		store := newTestStore(t)
		importer := NewImporter(store, testEngineVersion)
		outcome, err := importer.ImportForExecution(policyInput(cleanupAction()), consent, decision)
		if err != nil {
			t.Fatalf("import: %v", err)
		}
		if outcome.Matched {
			t.Errorf("decision %s/%s must not trigger an import", decision.Outcome, decision.Class)
		}
	}
}

func TestConsentImportRecordsEveryConsentingRule(t *testing.T) {
	store := newTestStore(t)
	importer := NewImporter(store, testEngineVersion)
	consent := &action.AgentConsent{
		Kind:     action.ConsentKindPersistentRule,
		RuleKeys: []string{"user:Bash(npm run:*)", "local:Bash(npm run cleanup)", ""},
	}

	outcome, err := importer.Import(policyInput(cleanupAction()), consent)
	if err != nil || !outcome.Matched {
		t.Fatalf("import = %+v, %v", outcome, err)
	}

	imports, err := store.Imports.ListByApproval(context.Background(), outcome.ApprovalID)
	if err != nil {
		t.Fatalf("list imports: %v", err)
	}
	if len(imports) != 2 {
		t.Fatalf("imports = %d, want one per non-empty rule", len(imports))
	}

	// Either rule alone is then enough to block a re-import.
	single := &action.AgentConsent{
		Kind:     action.ConsentKindPersistentRule,
		RuleKeys: []string{"user:Bash(npm run:*)"},
	}
	again, err := importer.Import(policyInput(cleanupAction()), single)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if again.Matched {
		t.Error("a rule already imported must not import again")
	}
}

func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }
