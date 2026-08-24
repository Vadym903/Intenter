package approval

import (
	"context"
	"testing"
	"time"

	"github.com/Vadym903/Intenter/internal/action"
)

// The invariants in Appendix A are the promises Intenter makes about its own
// behavior — the things that must hold no matter what else changes. They are
// tested throughout these packages, but scattered across names like
// "TestApprovalStopsMatchingWhenTheScriptChanges" they are hard to audit as a
// set.
//
// These files give each invariant one test named after it, so
// `go test ./... -run TestInvariant_` is a runnable reading of the safety
// contract. Each states the invariant in its most essential form; the fuller
// coverage stays in the topical tests, which the doc comments point at.
//
// This file covers the invariants about approvals: I-1, I-5, I-6, I-11, I-15,
// I-16.

func TestInvariant_I1_ApprovalNeverCoversLargerEffects(t *testing.T) {
	// I-1: an approval MUST never match an action whose resolved effects exceed
	// the effects originally permitted by that approval.
	//
	// See also TestApprovalStopsMatchingWhenTheScriptChanges.
	store := newTestStore(t)
	mustCreate(t, store, createRequest(cleanupAction()))
	matcher := NewMatcher(store, testEngineVersion)

	// Each of these is the approved action plus something it did not permit.
	tests := map[string]func(*action.ResolvedAction){
		"a wider scope": func(act *action.ResolvedAction) {
			documents := target("~/Documents", testHome+"/Documents", action.ScopeHome)
			act.Effects = []action.Effect{effect(action.EffectDelete, documents,
				action.EffectFlagRecursive, action.EffectFlagForce)}
		},
		"an extra effect type": func(act *action.ResolvedAction) {
			act.Effects = append(act.Effects, effect(action.EffectWrite,
				target("./dist/out", testWorkspace+"/dist/out", action.ScopeWorkspaceGenerated)))
		},
		"an extra flag": func(act *action.ResolvedAction) {
			act.Effects[0].AddFlags(action.EffectFlagBroad)
		},
		"a network call": func(act *action.ResolvedAction) {
			act.Effects = append(act.Effects, action.Effect{
				Type:    action.EffectNetwork,
				Network: &action.NetworkTarget{Host: "example.com", Scheme: "https"},
			})
		},
		"an execution": func(act *action.ResolvedAction) {
			act.Effects = append(act.Effects, action.Effect{
				Type:    action.EffectExecute,
				Program: &action.ProgramRef{Name: "sh", Resolution: action.ProgramUnresolved},
			})
		},
	}

	for name, exceed := range tests {
		t.Run(name, func(t *testing.T) {
			act := cleanupAction()
			exceed(act)
			act.ActionKey = ""

			outcome, err := matcher.Match(policyInput(act))
			if err != nil {
				t.Fatalf("match: %v", err)
			}
			if outcome.Matched {
				t.Error("the approval covered an action that exceeds what it permitted")
			}
		})
	}
}

func TestInvariant_I5_RawCommandEqualityIsNotEvidence(t *testing.T) {
	// I-5: raw command equality alone MUST never be sufficient evidence for
	// semantic approval reuse when the command references a mutable script.
	//
	// This is the invariant the whole product rests on: `npm run cleanup` is a
	// name, not a behavior.
	store := newTestStore(t)
	mustCreate(t, store, createRequest(cleanupAction()))
	matcher := NewMatcher(store, testEngineVersion)

	rewritten := changedCleanupAction()
	if rewritten.RawCommand != "npm run cleanup" {
		t.Fatalf("this test needs the same raw command, got %q", rewritten.RawCommand)
	}

	outcome, err := matcher.Match(policyInput(rewritten))
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if outcome.Matched {
		t.Fatal("an identical command string matched after its script was rewritten")
	}

	// And the converse: a different string that resolves to the same behavior
	// does match, because the effect is what was approved.
	renamed := cleanupAction()
	renamed.RawCommand = "npm run clean"
	same, err := matcher.Match(policyInput(renamed))
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if !same.Matched {
		t.Error("a different command with identical effects must still match")
	}
}

func TestInvariant_I6_MatchingIsPureAndDeterministic(t *testing.T) {
	// I-6: approval matching MUST be a pure, deterministic function; no runtime
	// LLM. The same input decided differently on two calls would make every
	// other guarantee unverifiable.
	//
	// See also TestMatchingIsAPureFunctionOfItsInputs.
	store := newTestStore(t)
	created := mustCreate(t, store, createRequest(cleanupAction()))
	matcher := NewMatcher(store, testEngineVersion)

	for i := 0; i < 20; i++ {
		outcome, err := matcher.Match(policyInput(cleanupAction()))
		if err != nil {
			t.Fatalf("match %d: %v", i, err)
		}
		if !outcome.Matched || outcome.ApprovalID != created.ID {
			t.Fatalf("call %d returned %+v, want a stable match on %d", i, outcome, created.ID)
		}
	}

	// The rewritten script is equally stable in the other direction.
	for i := 0; i < 20; i++ {
		outcome, err := matcher.Match(policyInput(changedCleanupAction()))
		if err != nil {
			t.Fatalf("match %d: %v", i, err)
		}
		if outcome.Matched {
			t.Fatalf("call %d matched a rewritten script", i)
		}
	}
}

func TestInvariant_I11_UnresolvedActionsAreNotApprovable(t *testing.T) {
	// I-11: UNRESOLVED / PARSE_FAILED / CONTEXT_FAILED actions MUST NOT be
	// auto-allowed nor approvable. Approving what Intenter could not read
	// would record permission for a behavior nobody established.
	//
	// See also TestApprovalCreationRefusesUnapprovableActions,
	// TestUnresolvedActionsNeverMatch.
	statuses := []action.ResolutionStatus{
		action.StatusUnresolved, action.StatusParseFailed, action.StatusContextFailed,
	}

	for _, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			store := newTestStore(t)

			unapprovable := cleanupAction()
			unapprovable.Status = status
			if _, err := Build(createRequest(unapprovable)); err == nil {
				t.Error("an unresolved action must not be approvable")
			}

			// Nor may an existing approval be stretched to cover one.
			mustCreate(t, store, createRequest(cleanupAction()))
			outcome, err := NewMatcher(store, testEngineVersion).Match(policyInput(unapprovable))
			if err != nil {
				t.Fatalf("match: %v", err)
			}
			if outcome.Matched {
				t.Error("an unresolved action must never match an approval")
			}
		})
	}
}

func TestInvariant_I15_ApprovalsAreNeverPhysicallyDeleted(t *testing.T) {
	// I-15: approval records are never physically deleted by normal operation;
	// state changes are audited. Revoking is a decision worth being able to
	// look back on, which a deleted row cannot support.
	store := newTestStore(t)
	created := mustCreate(t, store, createRequest(cleanupAction()))
	ctx := context.Background()

	revokedAt := time.Date(2026, 8, 16, 13, 0, 0, 0, time.UTC)
	if err := store.Approvals.SetState(ctx, created.ID, action.ApprovalRevoked, revokedAt); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	stored, err := store.Approvals.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("the revoked approval must still be readable: %v", err)
	}
	if stored.State != action.ApprovalRevoked {
		t.Errorf("state = %s, want revoked", stored.State)
	}
	if stored.RevokedAt == nil {
		t.Error("a revoked approval must record when it was revoked")
	}
	if len(stored.Envelope) == 0 || len(stored.SemanticOps) == 0 {
		t.Error("a revoked approval keeps what it permitted, so the record still explains itself")
	}

	// It no longer matches, which is the point of revoking rather than deleting.
	outcome, err := NewMatcher(store, testEngineVersion).Match(policyInput(cleanupAction()))
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if outcome.Matched {
		t.Error("a revoked approval must not match")
	}
}

func TestInvariant_I16_ApprovalsRecordWhatResolutionDependedOn(t *testing.T) {
	// I-16: approvals are created only from fully evaluated resolved actions and
	// record every fingerprint resolution depended on. A missing fingerprint is
	// a mutable input nothing is watching.
	//
	// See also TestApprovalRecordsEveryFingerprintResolutionDependedOn.
	store := newTestStore(t)
	act := cleanupAction()
	created := mustCreate(t, store, createRequest(act))

	recorded := created.Fingerprints()
	if len(recorded) != len(act.Fingerprints) {
		t.Fatalf("fingerprints = %d, want all %d resolution depended on",
			len(recorded), len(act.Fingerprints))
	}
	for _, want := range act.Fingerprints {
		if recorded[want.Key] != want.Value {
			t.Errorf("fingerprint %q = %q, want %q", want.Key, recorded[want.Key], want.Value)
		}
	}
	if len(created.SemanticOps) == 0 || len(created.Envelope) == 0 {
		t.Error("an approval must record the operation and effects it permits")
	}
	if created.CreatedFromRawCommand == "" {
		t.Error("an approval must record the command it was created from")
	}

	// The fingerprints are conditions, not notes: changing one stops the match.
	stale := cleanupAction()
	stale.Fingerprints[0].Value = "hash-something-else"
	outcome, err := NewMatcher(store, testEngineVersion).Match(policyInput(stale))
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if outcome.Matched {
		t.Error("a changed fingerprint must stop the approval from matching")
	}
}
