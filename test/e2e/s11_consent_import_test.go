package e2e

import (
	"path/filepath"
	"strings"
	"testing"
)

// S11 (PROTOTYPE_SPEC.md §29, §19.5): turning Claude's own "don't ask again"
// into an Intenter approval.
//
// This is the path most users will actually take — nobody types
// `intenter approve`. Claude records a rule for the command *string*;
// Intenter resolves that command fully, checks it against every rule, and
// only then records an approval for what it resolved to. The string rule keeps
// existing on Claude's side and is simply no longer the only thing between the
// agent and the filesystem (INVARIANT I-8).

// claudeAllowRule writes a Claude settings file granting a persistent rule, the
// way answering "Yes, and don't ask again" does.
func claudeAllowRule(t *testing.T, env *Env, rule string) {
	t.Helper()

	settings := `{"permissions":{"allow":["` + rule + `"]}}`
	path := filepath.Join(env.Workspace, ".claude", "settings.local.json")
	env.WriteWorkspaceFile(filepath.Join(".claude", "settings.local.json"), settings)
	t.Logf("wrote %s", path)
}

func TestS11ARuleAlreadyPresentIsImportedOnFirstUse(t *testing.T) {
	// Path (a): the rule exists before Intenter ever sees the command —
	// someone approved it in an earlier session, or the project ships one.
	env := NewEnv(t)
	env.SetScripts(`{"cleanup":"rm -rf ./dist"}`)
	claudeAllowRule(t, env, "Bash(npm run cleanup)")

	result := env.PreToolUse("session-1", "toolu_1", "npm run cleanup")
	if got := result.PermissionDecision(); got != "allow" {
		t.Fatalf("permissionDecision = %q, want allow\n%s", got, result.Stdout)
	}

	decision, class := env.Decision("npm run cleanup")
	if decision != "allow" || class != "RULE_IMPORT" {
		t.Fatalf("recorded %s/%s, want allow/RULE_IMPORT", decision, class)
	}

	// The approval is for the resolved effects, not the string.
	if count := env.ApprovalCount(); count != 1 {
		t.Fatalf("approvals = %d, want the imported one", count)
	}
	detail := env.MustCLI("approval", "show", "1")
	for _, want := range []string{"./dist", "DELETE", "claude_rule"} {
		if !strings.Contains(detail, want) {
			t.Errorf("the imported approval must record %q:\n%s", want, detail)
		}
	}
}

func TestS11ImportHappensOnlyOnce(t *testing.T) {
	// A second occurrence must match the approval that already exists rather
	// than importing the rule again, or the store would fill with duplicates
	// and "when was this trusted?" would have no answer.
	env := NewEnv(t)
	env.SetScripts(`{"cleanup":"rm -rf ./dist"}`)
	claudeAllowRule(t, env, "Bash(npm run cleanup)")

	env.PreToolUse("session-1", "toolu_1", "npm run cleanup")
	env.PreToolUse("session-2", "toolu_2", "npm run cleanup")
	env.PreToolUse("session-3", "toolu_3", "npm run cleanup")

	if count := env.ApprovalCount(); count != 1 {
		t.Errorf("approvals = %d, want exactly one however many times it runs", count)
	}

	event := env.FullEvent(env.LatestEventID())
	if class, _ := event["decision_class"].(string); class != "APPROVAL_MATCH" {
		t.Errorf("the third run recorded %s, want APPROVAL_MATCH", class)
	}
}

func TestS11ConsentAfterTheFactIsImportedFromPostToolUse(t *testing.T) {
	// Path (b), the common one: Intenter defers, Claude's dialog appears, the
	// user chooses "don't ask again", Claude writes the rule, and the
	// PostToolUse that follows carries the consent.
	env := NewEnv(t)
	env.SetScripts(`{"cleanup":"rm -rf ./dist"}`)

	deferred := env.PreToolUse("session-1", "toolu_1", "npm run cleanup")
	if !deferred.Deferred() {
		t.Fatalf("a never-approved understood action must defer\n%s", deferred.Stdout)
	}
	if count := env.ApprovalCount(); count != 0 {
		t.Fatalf("approvals = %d before any consent, want none", count)
	}

	// The user answers "don't ask again"; Claude writes its rule.
	claudeAllowRule(t, env, "Bash(npm run cleanup)")
	env.PostToolUse("session-1", "toolu_1", "npm run cleanup")

	if count := env.ApprovalCount(); count != 1 {
		t.Fatalf("approvals = %d after consent, want one", count)
	}

	// And the next session is allowed without a prompt.
	allowed := env.PreToolUse("session-2", "toolu_2", "npm run cleanup")
	if got := allowed.PermissionDecision(); got != "allow" {
		t.Errorf("permissionDecision = %q, want allow\n%s", got, allowed.Stdout)
	}
}

func TestS11AnImportedApprovalStopsMatchingWhenTheScriptChanges(t *testing.T) {
	// The whole point. Claude's rule still matches the string forever; the
	// imported approval does not, because it was for the behavior.
	env := NewEnv(t)
	env.SetScripts(`{"cleanup":"rm -rf ./dist"}`)
	claudeAllowRule(t, env, "Bash(npm run cleanup)")

	if got := env.PreToolUse("session-1", "toolu_1", "npm run cleanup").PermissionDecision(); got != "allow" {
		t.Fatalf("permissionDecision = %q, want allow", got)
	}

	// The script now deletes a home directory. Claude's rule is unchanged.
	env.SetScripts(`{"cleanup":"rm -rf ~/Documents"}`)

	blocked := env.PreToolUse("session-2", "toolu_2", "npm run cleanup")
	if got := blocked.PermissionDecision(); got != "deny" {
		t.Fatalf("permissionDecision = %q, want deny — the rule must not survive the change\n%s",
			got, blocked.Stdout)
	}

	event := env.FullEvent(env.LatestEventID())
	if rule, _ := event["hard_rule"].(string); rule != "R2" {
		t.Errorf("hard_rule = %v, want R2", event["hard_rule"])
	}

	// And the explanation names what changed.
	explanation := env.MustCLI("history", "show", itoa(env.LatestEventID()))
	for _, want := range []string{"~/Documents", "HOME", "R2"} {
		if !strings.Contains(explanation, want) {
			t.Errorf("history show must mention %q:\n%s", want, explanation)
		}
	}
}

func TestS11ARuleIsNeverImportedPastTheSafetyFloor(t *testing.T) {
	// INVARIANT I-8 and I-4 together: a Claude rule is evidence of intent, not
	// authority. A command the hard rules stop is not importable however
	// broadly the rule was written.
	env := NewEnv(t)
	env.SetScripts(`{"cleanup":"rm -rf ~/Documents"}`)
	claudeAllowRule(t, env, "Bash(npm run cleanup)")

	result := env.PreToolUse("session-1", "toolu_1", "npm run cleanup")
	if got := result.PermissionDecision(); got != "deny" {
		t.Fatalf("permissionDecision = %q, want deny\n%s", got, result.Stdout)
	}
	if count := env.ApprovalCount(); count != 0 {
		t.Errorf("approvals = %d; nothing the safety floor stops may be imported", count)
	}
}

func TestS11APartialRuleIsNotConsent(t *testing.T) {
	// A rule that covers only part of a command line says nothing about the
	// rest, and a guess would be an approval nobody gave.
	env := NewEnv(t)
	env.SetScripts(`{"cleanup":"rm -rf ./dist"}`)
	claudeAllowRule(t, env, "Bash(npm run cleanup)")

	result := env.PreToolUse("session-1", "toolu_1", "npm run cleanup && rm -rf ./src")
	if got := result.PermissionDecision(); got == "allow" {
		t.Fatalf("a rule for one command must not cover a second\n%s", result.Stdout)
	}
	if count := env.ApprovalCount(); count != 0 {
		t.Errorf("approvals = %d, want none from a partial match", count)
	}
}
