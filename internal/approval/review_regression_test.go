package approval

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Vadym903/Intenter/internal/action"
)

func networkEffect(host, scheme, method string) action.Effect {
	return action.Effect{
		Type:    action.EffectNetwork,
		Network: &action.NetworkTarget{Host: host, Scheme: scheme, Method: method},
	}
}

func curlGetAction() *action.ResolvedAction {
	act := cleanupAction()
	act.SemanticOps = []action.SemanticOp{action.OpHTTPRequest}
	act.RawCommand = "curl https://api.example.com/data"
	eff := networkEffect("api.example.com", "https", "GET")
	act.Effects = []action.Effect{eff}
	act.Commands = []action.ResolvedCommand{{
		SemanticOp: action.OpHTTPRequest, Status: action.StatusResolved,
		Effects: []action.Effect{eff},
	}}
	act.Fingerprints = nil
	return act
}

func TestProbe_NetworkMethodChangeDoesNotMatch(t *testing.T) {
	for _, kind := range []action.ApprovalKind{action.ApprovalExact, action.ApprovalSemantic} {
		t.Run(string(kind), func(t *testing.T) {
			store := newTestStore(t)
			req := createRequest(curlGetAction())
			req.Kind = kind
			mustCreate(t, store, req)

			post := curlGetAction()
			post.Effects[0].Network.Method = "POST"
			post.Commands[0].Effects = post.Effects

			outcome, err := NewMatcher(store, testEngineVersion).Match(policyInput(post))
			if err != nil {
				t.Fatalf("match: %v", err)
			}
			if outcome.Matched {
				t.Errorf("%s approval for GET must not cover POST to the same host", kind)
			}
		})
	}
}

func TestProbe_NetworkSchemeChangeDoesNotMatch(t *testing.T) {
	for _, kind := range []action.ApprovalKind{action.ApprovalExact, action.ApprovalSemantic} {
		t.Run(string(kind), func(t *testing.T) {
			store := newTestStore(t)
			req := createRequest(curlGetAction())
			req.Kind = kind
			mustCreate(t, store, req)

			plain := curlGetAction()
			plain.Effects[0].Network.Scheme = "http"
			plain.Commands[0].Effects = plain.Effects

			outcome, err := NewMatcher(store, testEngineVersion).Match(policyInput(plain))
			if err != nil {
				t.Fatalf("match: %v", err)
			}
			if outcome.Matched {
				t.Errorf("%s approval for https must not cover http to the same host", kind)
			}
		})
	}
}

func TestProbe_NetworkHostChangeDoesNotMatch(t *testing.T) {
	for _, kind := range []action.ApprovalKind{action.ApprovalExact, action.ApprovalSemantic} {
		t.Run(string(kind), func(t *testing.T) {
			store := newTestStore(t)
			req := createRequest(curlGetAction())
			req.Kind = kind
			mustCreate(t, store, req)

			evil := curlGetAction()
			evil.Effects[0].Network.Host = "attacker.example.net"
			evil.Commands[0].Effects = evil.Effects

			outcome, err := NewMatcher(store, testEngineVersion).Match(policyInput(evil))
			if err != nil {
				t.Fatalf("match: %v", err)
			}
			if outcome.Matched {
				t.Errorf("%s approval must not cover a different host", kind)
			}
		})
	}
}

// TestProbe_SemanticNonRecursiveDoesNotCoverRecursive checks a SEMANTIC
// approval created from a non-recursive delete never covers a recursive one
// on a target in the same scope (I-1: flags are part of the envelope key).
func TestProbe_SemanticNonRecursiveDoesNotCoverRecursive(t *testing.T) {
	store := newTestStore(t)
	nonRecursive := cleanupAction()
	nonRecursive.Effects[0].Flags = nil // strip recursive/force
	nonRecursive.Commands[0].Effects = nonRecursive.Effects
	req := createRequest(nonRecursive)
	req.Kind = action.ApprovalSemantic
	mustCreate(t, store, req)

	recursive := cleanupAction() // has recursive+force flags
	outcome, err := NewMatcher(store, testEngineVersion).Match(policyInput(recursive))
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if outcome.Matched {
		t.Error("a SEMANTIC approval for a non-recursive delete must not cover a recursive one")
	}
}

// TestProbe_SemanticWriteDoesNotCoverDelete checks a SEMANTIC approval for a
// WRITE envelope never covers a DELETE on the same scope.
func TestProbe_SemanticWriteDoesNotCoverDelete(t *testing.T) {
	store := newTestStore(t)
	writeAction := cleanupAction()
	writeAction.Effects[0].Type = action.EffectWrite
	writeAction.Effects[0].Flags = nil
	writeAction.Commands[0].Effects = writeAction.Effects
	req := createRequest(writeAction)
	req.Kind = action.ApprovalSemantic
	mustCreate(t, store, req)

	deleteAction := cleanupAction()
	outcome, err := NewMatcher(store, testEngineVersion).Match(policyInput(deleteAction))
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if outcome.Matched {
		t.Error("a SEMANTIC approval for WRITE must not cover DELETE, even in the same scope")
	}
}

// TestProbe_MultiCommandPartialMatchDoesNotAllowTheDangerousHalf builds a
// two-command action (approved command && dangerous command) and checks an
// approval for the first command alone never covers the combined line.
func TestProbe_MultiCommandPartialMatchDoesNotAllowTheDangerousHalf(t *testing.T) {
	store := newTestStore(t)
	// Approve just `npm test` in isolation.
	testOnly := cleanupAction()
	testOnly.RawCommand = "npm test"
	testOnly.SemanticOps = []action.SemanticOp{action.OpRunTests}
	safeTarget := target("./src", testWorkspace+"/src", action.ScopeWorkspace)
	readEffect := effect(action.EffectRead, safeTarget)
	testOnly.Effects = []action.Effect{readEffect}
	testOnly.Commands = []action.ResolvedCommand{{
		SemanticOp: action.OpRunTests, Status: action.StatusResolved,
		Effects: []action.Effect{readEffect}, Targets: []action.Target{safeTarget},
	}}
	testOnly.Fingerprints = nil
	mustCreate(t, store, createRequest(testOnly))

	// The combined line: `npm test && rm -rf ~/Documents`.
	combined := cleanupAction()
	combined.RawCommand = "npm test && rm -rf ~/Documents"
	documents := target("~/Documents", testHome+"/Documents", action.ScopeHome, action.FlagBroad)
	deleteEffect := effect(action.EffectDelete, documents, action.EffectFlagRecursive, action.EffectFlagForce)
	combined.SemanticOps = []action.SemanticOp{action.OpRunTests, action.OpFSDelete}
	combined.Effects = []action.Effect{readEffect, deleteEffect}
	combined.Commands = []action.ResolvedCommand{
		{SemanticOp: action.OpRunTests, Status: action.StatusResolved, Effects: []action.Effect{readEffect}, Targets: []action.Target{safeTarget}},
		{SemanticOp: action.OpFSDelete, Status: action.StatusResolved, Effects: []action.Effect{deleteEffect}, Targets: []action.Target{documents}},
	}
	combined.Fingerprints = nil

	outcome, err := NewMatcher(store, testEngineVersion).Match(policyInput(combined))
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if outcome.Matched {
		t.Error("an approval for the safe half of a command line must not cover the whole line")
	}
}

// The tests below continue the area-3 review (T048): the approval matcher.
//
// AG-160 (fixed) — candidates() fetched only ACTIVE approvals, so once a
// DISABLED or REVOKED approval correctly stopped matching, it also vanished
// from the mismatch report. The user saw the generic "not approved for this
// project yet" instead of "approval 42 no longer covers this action: the
// approval is revoked" (§21), even though mismatch.go's Differences already
// had the code to say so. Fix: internal/approval/match.go candidates() now
// asks storage for every approval of the project, including inactive ones;
// Matches() still excludes them from actually matching via its own
// Active() check, so no match outcome changes — only the explanation does.
//
// AG-161 (fixed) — EnvelopeEntry, the (type, scope, flags) triple approval
// matching compares, only ever looked at a target's Scope, never its own
// classification flags (sensitive, traversal, symlink_escape, tool_cache,
// broad, wildcard, network_path, temp — internal/action/enums.go TargetFlag).
// R2/R3/R5/R6 give sensitive/traversal/symlink_escape/broad/wildcard hard-rule
// coverage, but only for specific effect types and scopes (R5 covers every
// effect type but only for FlagSensitive; R2/R3 only fire for EffectDelete in
// HOME/OUTSIDE_WORKSPACE). Nothing covered FlagToolCache, FlagNetworkPath or
// FlagTemp, or FlagBroad/FlagWildcard outside a delete. Concretely: an
// approval for `cp ./notes.txt ~/notes.txt` (SEMANTIC, an ordinary HOME
// write) matched `cp ./payload ~/.npm/_cacache/evil` (a write into npm's
// package cache — the classic cache-poisoning target for a later `npm
// install`) because both actions produce the exact same CREATE/HOME/{} and
// WRITE/HOME/{} envelope entries; the tool-cache classification was invisible
// to the comparison. Fix: EnvelopeEntry now carries the target's flags as
// part of its identity (internal/action/effect.go), so a target that gains
// any of these after an approval was granted is a different envelope entry.

// TestProbe_ToolCacheWriteDoesNotMatchAPlainHomeWriteApproval is the AG-161
// regression: it fails before the fix (outcome.Matched == true).
func TestProbe_ToolCacheWriteDoesNotMatchAPlainHomeWriteApproval(t *testing.T) {
	store := newTestStore(t)
	plain := target("~/notes.txt", testHome+"/notes.txt", action.ScopeHome)
	request := createRequest(copyAction(plain))
	request.Kind = action.ApprovalSemantic
	mustCreate(t, store, request)

	cache := target("~/.npm/_cacache/evil", testHome+"/.npm/_cacache/evil",
		action.ScopeHome, action.FlagToolCache)
	outcome, err := NewMatcher(store, testEngineVersion).Match(policyInput(copyAction(cache)))
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if outcome.Matched {
		t.Error("a plain HOME write approval must not cover a write into a tool cache (AG-161)")
	}
}

// TestProbe_SymlinkEscapeAndSensitiveTargetFlagsAreAlsoPartOfTheEnvelope
// covers the rest of the AG-161 target-flag family for a SEMANTIC approval,
// as belt-and-braces alongside the hard rules that also stop these (R5, R6).
func TestProbe_SymlinkEscapeAndSensitiveTargetFlagsAreAlsoPartOfTheEnvelope(t *testing.T) {
	store := newTestStore(t)
	plain := target("~/notes.txt", testHome+"/notes.txt", action.ScopeHome)
	request := createRequest(copyAction(plain))
	request.Kind = action.ApprovalSemantic
	mustCreate(t, store, request)

	tests := []struct {
		name string
		flag action.TargetFlag
	}{
		{"sensitive", action.FlagSensitive},
		{"symlink escape", action.FlagSymlinkEscape},
		{"traversal", action.FlagTraversal},
		{"network path", action.FlagNetworkPath},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagged := target("~/notes.txt", testHome+"/notes.txt", action.ScopeHome, tt.flag)
			outcome, err := NewMatcher(store, testEngineVersion).Match(policyInput(copyAction(flagged)))
			if err != nil {
				t.Fatalf("match: %v", err)
			}
			if outcome.Matched {
				t.Errorf("a %s target must not be covered by an approval granted over a clean one", tt.name)
			}
		})
	}
}

// copyAction models `cp ./notes.txt <dest>`: a READ on the workspace source
// plus CREATE+WRITE on dest, the way internal/resolver/fs.go's copyRecognizer
// resolves it.
func copyAction(dest action.Target) *action.ResolvedAction {
	src := target("./notes.txt", testWorkspace+"/notes.txt", action.ScopeWorkspace)
	read := effect(action.EffectRead, src)
	create := effect(action.EffectCreate, dest)
	write := effect(action.EffectWrite, dest)
	return &action.ResolvedAction{
		RawCommand:  "cp ./notes.txt " + dest.Raw,
		ProjectID:   action.ProjectID(testWorkspace),
		Status:      action.StatusResolved,
		SemanticOps: []action.SemanticOp{action.OpFSCopy},
		Effects:     []action.Effect{read, create, write},
		Commands: []action.ResolvedCommand{{
			SemanticOp: action.OpFSCopy, Status: action.StatusResolved,
			Targets: []action.Target{src, dest}, Effects: []action.Effect{read, create, write},
		}},
	}
}

// TestProbe_RevokedApprovalStillExplainsItsMismatch is the AG-160 regression.
func TestProbe_RevokedApprovalStillExplainsItsMismatch(t *testing.T) {
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
				t.Fatalf("a %s approval must not match", state)
			}
			if len(outcome.Mismatches) == 0 {
				t.Fatal("want a mismatch report naming the approval and why it no longer applies")
			}
			if outcome.Mismatches[0].ApprovalID != created.ID {
				t.Errorf("mismatch names approval %d, want %d", outcome.Mismatches[0].ApprovalID, created.ID)
			}
			joined := strings.Join(outcome.Mismatches[0].Differences, "\n")
			want := "the approval is " + strings.ToLower(string(state))
			if !strings.Contains(joined, want) {
				t.Errorf("differences must mention %q, got:\n%s", want, joined)
			}
		})
	}
}

// TestProbe_SemanticScopeWideningEveryTier extends
// TestInvariant_I1_ApprovalNeverCoversLargerEffects (EXACT only) to SEMANTIC,
// across every scope tier the approved action did not touch (§20.3 rule 4).
func TestProbe_SemanticScopeWideningEveryTier(t *testing.T) {
	store := newTestStore(t)
	request := createRequest(cleanupAction()) // DELETE ./dist [WORKSPACE_GENERATED]
	request.Kind = action.ApprovalSemantic
	mustCreate(t, store, request)
	matcher := NewMatcher(store, testEngineVersion)

	tests := []struct {
		name    string
		scope   action.Scope
		raw     string
		canon   string
		targetf []action.TargetFlag
	}{
		{"workspace", action.ScopeWorkspace, "./src", testWorkspace + "/src", nil},
		{"home", action.ScopeHome, "~/Documents", testHome + "/Documents", []action.TargetFlag{action.FlagBroad}},
		{"outside workspace", action.ScopeOutsideWorkspace, "/tmp/scratch/out", "/tmp/scratch/out", nil},
		{"system", action.ScopeSystem, "/etc/hosts", "/etc/hosts", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wider := cleanupAction()
			moved := target(tt.raw, tt.canon, tt.scope, tt.targetf...)
			deletion := effect(action.EffectDelete, moved, action.EffectFlagRecursive, action.EffectFlagForce)
			wider.Effects = []action.Effect{deletion}
			wider.Commands[0].Targets = []action.Target{moved}
			wider.Commands[0].Effects = wider.Effects

			outcome, err := matcher.Match(policyInput(wider))
			if err != nil {
				t.Fatalf("match: %v", err)
			}
			if outcome.Matched {
				t.Errorf("a SEMANTIC approval for %s must not cover a delete in %s",
					action.ScopeWorkspaceGenerated, tt.scope)
			}
		})
	}
}

// TestProbe_SemanticNewExecuteEffectDoesNotMatch checks a SEMANTIC approval
// for a delete never grows to also cover an added EXECUTE effect, even though
// the originally approved DELETE entry is still present unchanged (I-1:
// "superset of effects ... fails").
func TestProbe_SemanticNewExecuteEffectDoesNotMatch(t *testing.T) {
	store := newTestStore(t)
	request := createRequest(cleanupAction())
	request.Kind = action.ApprovalSemantic
	mustCreate(t, store, request)

	withExecute := cleanupAction()
	withExecute.Effects = append(withExecute.Effects, action.Effect{
		Type:    action.EffectExecute,
		Program: &action.ProgramRef{Name: "sh", Resolution: action.ProgramUnresolved, Streamed: true},
	})
	withExecute.Commands[0].Effects = withExecute.Effects

	outcome, err := NewMatcher(store, testEngineVersion).Match(policyInput(withExecute))
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if outcome.Matched {
		t.Error("a SEMANTIC approval for a delete must not also cover an added execution")
	}
}

// TestProbe_SemanticTargetGrowthIntoANewScopeDoesNotMatch checks that adding a
// second target in a wider scope, alongside the originally approved one,
// still fails to match — growth is caught even when the original effect is
// still present in the union (§20.3 rule 4).
func TestProbe_SemanticTargetGrowthIntoANewScopeDoesNotMatch(t *testing.T) {
	store := newTestStore(t)
	request := createRequest(cleanupAction()) // deletes ./dist [WORKSPACE_GENERATED]
	request.Kind = action.ApprovalSemantic
	mustCreate(t, store, request)

	grown := cleanupAction()
	documents := target("~/Documents", testHome+"/Documents", action.ScopeHome, action.FlagBroad)
	extra := effect(action.EffectDelete, documents, action.EffectFlagRecursive, action.EffectFlagForce)
	grown.Effects = append(grown.Effects, extra)
	grown.Commands[0].Effects = grown.Effects
	grown.Commands[0].Targets = append(grown.Commands[0].Targets, documents)

	outcome, err := NewMatcher(store, testEngineVersion).Match(policyInput(grown))
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if outcome.Matched {
		t.Error("a SEMANTIC approval must not match once a second, wider-scope target is added, " +
			"even though the originally approved target and effect are still present")
	}
}

// TestProbe_DisplayTargetCollisionAcrossProjectsDoesNotLeak checks that an
// approval whose display target string happens to equal a target's display
// string in an unrelated project never matches there: project_id gates
// candidacy before any target comparison is reached (§20.1, §20.3 rule 6),
// so a moved or re-cloned project's stale display strings cannot leak into a
// different project that happens to reuse the same relative name.
func TestProbe_DisplayTargetCollisionAcrossProjectsDoesNotLeak(t *testing.T) {
	store := newTestStore(t)
	otherRoot := "/w/other-project"
	other := action.Project{ID: action.ProjectID(otherRoot), RootPath: otherRoot}
	if err := store.Projects.Upsert(context.Background(), other); err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	mustCreate(t, store, createRequest(cleanupAction())) // approved in testWorkspace, target "./dist"

	elsewhere := cleanupAction()
	elsewhere.ProjectID = other.ID // same display target "./dist", unrelated project

	outcome, err := NewMatcher(store, testEngineVersion).Match(policyInput(elsewhere))
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if outcome.Matched {
		t.Error("an approval must not follow a colliding display target into an unrelated project")
	}
	if len(outcome.Mismatches) != 0 {
		t.Errorf("mismatches = %+v, want none: another project's approval is not related (§20.4)", outcome.Mismatches)
	}
}

// TestProbe_FingerprintCannotBeSatisfiedByAnUnrelatedFileWithTheSameHash
// checks that a fingerprint condition is matched by key, not by value alone:
// an action carrying the approved hash under a different key is still a
// mismatch (§20.3 rule 3 — "a key absent from X is a mismatch").
func TestProbe_FingerprintCannotBeSatisfiedByAnUnrelatedFileWithTheSameHash(t *testing.T) {
	store := newTestStore(t)
	mustCreate(t, store, createRequest(cleanupAction()))
	// cleanupAction's fingerprints:
	//   npm-script:package.json#scripts.cleanup = "hash-dist"
	//   npm-config:.npmrc#script-shell           = "unset"

	renamed := cleanupAction()
	renamed.Fingerprints = []action.Fingerprint{
		// Same hash value as the approved fingerprint, but under the key of a
		// different, unrelated script — a coincidental content match, e.g. two
		// empty scripts, must not stand in for the approved one.
		{Key: "npm-script:package.json#scripts.other", Value: "hash-dist"},
		{Key: "npm-config:.npmrc#script-shell", Value: "unset"},
	}

	outcome, err := NewMatcher(store, testEngineVersion).Match(policyInput(renamed))
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if outcome.Matched {
		t.Error("a fingerprint hash recorded under a different key must not satisfy the approved key")
	}
}
