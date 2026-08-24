package approval

import (
	"context"
	"strings"
	"testing"

	"github.com/Vadym903/Intenter/internal/action"
)

// This file is the table of PROTOTYPE_SPEC.md §21: every signal that must stop
// an approval from covering an action, and the difference a user is shown.
//
// "Invalidation" is not a state change — the record stays (I-15). It is the
// consequence of §20.3 matching resolved effects rather than command strings,
// so each case here proves one field-wise rule does its job.

// invalidationCase is one §21 signal.
type invalidationCase struct {
	name string
	// mutate changes the action the way the signal would.
	mutate func(act *action.ResolvedAction)
	// want is a phrase the difference list must contain.
	want string
}

func runInvalidationCases(t *testing.T, cases []invalidationCase) {
	t.Helper()
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			store := newTestStore(t)
			created := mustCreate(t, store, createRequest(cleanupAction()))

			changed := cleanupAction()
			tt.mutate(changed)

			outcome, err := NewMatcher(store, testEngineVersion).Match(policyInput(changed))
			if err != nil {
				t.Fatalf("match: %v", err)
			}
			if outcome.Matched {
				t.Fatal("the approval must stop covering the changed action (§21)")
			}
			if len(outcome.Mismatches) == 0 {
				t.Fatal("want a mismatch report explaining why")
			}
			if outcome.Mismatches[0].ApprovalID != created.ID {
				t.Errorf("mismatch names approval %d, want %d",
					outcome.Mismatches[0].ApprovalID, created.ID)
			}

			joined := strings.Join(outcome.Mismatches[0].Differences, "\n")
			if !strings.Contains(joined, tt.want) {
				t.Errorf("differences must mention %q, got:\n%s", tt.want, joined)
			}

			// The record itself is untouched: invalidation is not revocation.
			stored, err := store.Approvals.Get(context.Background(), created.ID)
			if err != nil {
				t.Fatalf("get approval: %v", err)
			}
			if stored.State != action.ApprovalActive {
				t.Errorf("state = %s, want the record to survive (I-15)", stored.State)
			}
		})
	}
}

func TestInvalidationByChangedInputs(t *testing.T) {
	// Rule 3: every mutable input resolution depended on is a condition.
	runInvalidationCases(t, []invalidationCase{
		{
			name:   "resolved script text changed",
			mutate: func(act *action.ResolvedAction) { act.Fingerprints[0].Value = "different" },
			want:   "npm-script:package.json#scripts.cleanup changed",
		},
		{
			name:   "script shell configured",
			mutate: func(act *action.ResolvedAction) { act.Fingerprints[1].Value = "bash" },
			want:   "npm-config:.npmrc#script-shell changed",
		},
		{
			name:   "an input disappeared from the action",
			mutate: func(act *action.ResolvedAction) { act.Fingerprints = act.Fingerprints[1:] },
			want:   "no longer part of this action",
		},
		{
			name: "gradle build files changed",
			mutate: func(act *action.ResolvedAction) {
				act.Fingerprints = []action.Fingerprint{{Key: "gradle-config", Value: "different"}}
			},
			want: "no longer part of this action",
		},
	})
}

func TestInvalidationByChangedTargets(t *testing.T) {
	// Rules 4 and 6: what it touches, and where.
	runInvalidationCases(t, []invalidationCase{
		{
			name: "target moved inside the same scope",
			mutate: func(act *action.ResolvedAction) {
				build := target("./build", testWorkspace+"/build", action.ScopeWorkspaceGenerated)
				replaceEffect(act, effect(action.EffectDelete, build,
					action.EffectFlagRecursive, action.EffectFlagForce), build)
			},
			want: "./dist -> ./build",
		},
		{
			name: "scope broadened to workspace source",
			mutate: func(act *action.ResolvedAction) {
				src := target("./src", testWorkspace+"/src", action.ScopeWorkspace)
				replaceEffect(act, effect(action.EffectDelete, src,
					action.EffectFlagRecursive, action.EffectFlagForce), src)
			},
			want: "new effect",
		},
		{
			name: "scope broadened to the home directory",
			mutate: func(act *action.ResolvedAction) {
				documents := target("~/Documents", testHome+"/Documents", action.ScopeHome, action.FlagBroad)
				replaceEffect(act, effect(action.EffectDelete, documents,
					action.EffectFlagRecursive, action.EffectFlagForce), documents)
			},
			// The scope move is what makes this dangerous, so it is stated.
			want: "scope WORKSPACE_GENERATED -> HOME (DELETE)",
		},
		{
			name: "an extra target was added",
			mutate: func(act *action.ResolvedAction) {
				src := target("./src", testWorkspace+"/src", action.ScopeWorkspace)
				act.Effects = append(act.Effects, effect(action.EffectDelete, src,
					action.EffectFlagRecursive, action.EffectFlagForce))
				act.Commands[0].Targets = append(act.Commands[0].Targets, src)
				act.Commands[0].Effects = act.Effects
			},
			want: "new target ./src",
		},
	})
}

func TestInvalidationByChangedEffects(t *testing.T) {
	// Rule 4 again: flags are part of the identity, so a widening flag is a
	// different effect (I-1).
	runInvalidationCases(t, []invalidationCase{
		{
			name: "a wildcard flag was added",
			mutate: func(act *action.ResolvedAction) {
				act.Effects[0].AddFlags(action.EffectFlagWildcard)
				act.Commands[0].Effects = act.Effects
			},
			want: "new effect",
		},
		{
			name: "the recursive flag was added",
			mutate: func(act *action.ResolvedAction) {
				dist := target("./dist", testWorkspace+"/dist", action.ScopeWorkspaceGenerated)
				replaceEffect(act, effect(action.EffectDelete, dist), dist)
			},
			want: "new effect",
		},
		{
			name: "a write became a delete",
			mutate: func(act *action.ResolvedAction) {
				act.Effects[0].Type = action.EffectWrite
				act.Commands[0].Effects = act.Effects
			},
			want: "new effect",
		},
		{
			name: "an execution was added",
			mutate: func(act *action.ResolvedAction) {
				act.Effects = append(act.Effects, action.Effect{
					Type: action.EffectExecute,
					Program: &action.ProgramRef{
						Name: "sh", Resolution: action.ProgramUnresolved, Streamed: true,
					},
				})
				act.Commands[0].Effects = act.Effects
			},
			want: "new effect EXECUTE",
		},
	})
}

func TestInvalidationByChangedNetwork(t *testing.T) {
	// Rule 5: an endpoint the approval never permitted.
	runInvalidationCases(t, []invalidationCase{
		{
			name: "a network call was added",
			mutate: func(act *action.ResolvedAction) {
				act.Effects = append(act.Effects, action.Effect{
					Type:    action.EffectNetwork,
					Network: &action.NetworkTarget{Host: "evil.example.com", Scheme: "https"},
				})
				act.Commands[0].Effects = act.Effects
			},
			want: "new network target",
		},
		{
			name: "a dependency registry was added",
			mutate: func(act *action.ResolvedAction) {
				act.Effects = append(act.Effects, action.Effect{
					Type:    action.EffectNetwork,
					Network: &action.NetworkTarget{DeclaredKind: "dependency-registry"},
				})
				act.Commands[0].Effects = act.Effects
			},
			want: "new network target",
		},
	})
}

func TestInvalidationByChangedOperation(t *testing.T) {
	// Rule 2: the ordered operations are part of the identity.
	runInvalidationCases(t, []invalidationCase{
		{
			name: "a different operation",
			mutate: func(act *action.ResolvedAction) {
				act.SemanticOps = []action.SemanticOp{action.OpFSDelete}
			},
			want: "operation RUN_SCRIPT -> FS_DELETE",
		},
		{
			name: "an extra operation",
			mutate: func(act *action.ResolvedAction) {
				act.SemanticOps = []action.SemanticOp{action.OpRunScript, action.OpGitPush}
			},
			want: "operation RUN_SCRIPT -> RUN_SCRIPT>GIT_PUSH",
		},
	})
}

func TestInvalidationByEngineVersion(t *testing.T) {
	// Rule 1: an engine that interprets envelopes differently must not reuse an
	// approval created by the previous one.
	store := newTestStore(t)
	created := mustCreate(t, store, createRequest(cleanupAction()))

	outcome, err := NewMatcher(store, testEngineVersion+1).Match(policyInput(cleanupAction()))
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if outcome.Matched {
		t.Fatal("an approval from another engine version must not match")
	}
	if len(outcome.Mismatches) == 0 {
		t.Fatal("want a mismatch report")
	}
	joined := strings.Join(outcome.Mismatches[0].Differences, "\n")
	if !strings.Contains(joined, "engine version") {
		t.Errorf("differences must explain the engine change:\n%s", joined)
	}
	_ = created
}

func TestInvalidationByProjectIdentity(t *testing.T) {
	// A moved or cloned checkout is a different project, so the approval is not
	// even a candidate — and reporting a mismatch would be misleading.
	store := newTestStore(t)
	mustCreate(t, store, createRequest(cleanupAction()))

	moved := cleanupAction()
	moved.ProjectID = action.ProjectID("/w/moved")

	outcome, err := NewMatcher(store, testEngineVersion).Match(policyInput(moved))
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if outcome.Matched {
		t.Error("an approval must not follow a project to another location")
	}
	if len(outcome.Mismatches) != 0 {
		t.Errorf("mismatches = %+v, want none: another project's approval is unrelated", outcome.Mismatches)
	}
}

func TestUnchangedActionStillMatches(t *testing.T) {
	// The control: without a signal, the approval keeps working. Without this
	// every test above would pass for the wrong reason.
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

// replaceEffect swaps the action's single effect and its target.
func replaceEffect(act *action.ResolvedAction, replacement action.Effect, on action.Target) {
	act.Effects = []action.Effect{replacement}
	act.Commands[0].Effects = act.Effects
	act.Commands[0].Targets = []action.Target{on}
}
