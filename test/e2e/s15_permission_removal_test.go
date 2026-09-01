package e2e

import (
	"path/filepath"
	"strings"
	"testing"
)

// S15 (007 FR-014, FR-015, SC-003): taking a permission back.
//
// This is the scenario the whole feature exists for, and it is the one that
// cannot be proved anywhere else. Revoking Intenter's own approval is not
// enough: a command with no matching approval is deferred to Claude, which then
// allows it silently through the rule that is still sitting in its settings. So
// the only honest test is the round trip — the command runs without a prompt,
// the permission is removed, and the very next run asks again.

func TestS15RemovingAPermissionMakesTheCommandAskAgain(t *testing.T) {
	env := NewEnv(t)
	env.SetScripts(`{"cleanup":"rm -rf ./dist"}`)
	claudeAllowRule(t, env, "Bash(npm run cleanup)")

	// It runs without a prompt: Claude's rule was imported into an approval.
	first := env.PreToolUse("session-1", "toolu_1", "npm run cleanup")
	if got := first.PermissionDecision(); got != "allow" {
		t.Fatalf("permissionDecision = %q, want allow\n%s", got, first.Stdout)
	}
	if count := env.ApprovalCount(); count != 1 {
		t.Fatalf("approvals = %d, want the imported one", count)
	}

	// The listing shows both the approval and the rule behind it, because both
	// are reasons the command runs silently.
	listed := env.MustCLI("approvals")
	for _, want := range []string{"Rules Claude holds of its own", "Bash(npm run cleanup)"} {
		if !strings.Contains(listed, want) {
			t.Errorf("the listing must show %q:\n%s", want, listed)
		}
	}

	removed := env.MustCLI("approval", "revoke", "1", "--yes")
	for _, want := range []string{"This will stop being trusted", "Removed Bash(npm run cleanup)", "will ask again"} {
		if !strings.Contains(removed, want) {
			t.Errorf("the removal must report %q:\n%s", want, removed)
		}
	}

	// The rule is gone from Claude's settings, not just from Intenter's record.
	settings := env.ReadWorkspaceFile(filepath.Join(".claude", "settings.local.json"))
	if strings.Contains(settings, "npm run cleanup") {
		t.Fatalf("the rule that grants the command is still in Claude's settings:\n%s", settings)
	}

	// The whole point: the next run is not allowed silently.
	second := env.PreToolUse("session-2", "toolu_2", "npm run cleanup")
	if got := second.PermissionDecision(); got == "allow" {
		t.Fatalf("the command still runs without a prompt after its permission was removed\n%s",
			second.Stdout)
	}
	if decision, _ := env.Decision("npm run cleanup"); decision == "allow" {
		t.Errorf("the recorded decision is still allow, so nothing was taken away")
	}
}

// A removal never deletes the record. The approval stays, marked revoked, and
// the settings backup holds the rule — otherwise "what did I used to trust?"
// would have no answer.
func TestS15WhatWasRemovedStaysOnRecord(t *testing.T) {
	env := NewEnv(t)
	env.SetScripts(`{"cleanup":"rm -rf ./dist"}`)
	claudeAllowRule(t, env, "Bash(npm run cleanup)")

	env.PreToolUse("session-1", "toolu_1", "npm run cleanup")
	env.MustCLI("approval", "revoke", "1", "--yes")

	inactive := env.MustCLI("approvals", "--inactive")
	if !strings.Contains(inactive, "REVOKED") {
		t.Errorf("the revoked approval must still be listed:\n%s", inactive)
	}

	detail := env.MustCLI("approval", "show", "1")
	if !strings.Contains(detail, "REVOKED") {
		t.Errorf("the approval record must survive:\n%s", detail)
	}
}

// Nothing changes without an answer. With no terminal to ask, the removal
// refuses rather than assuming consent for a change to the user's settings.
func TestS15RemovalWithoutAConfirmationChangesNothing(t *testing.T) {
	env := NewEnv(t)
	env.SetScripts(`{"cleanup":"rm -rf ./dist"}`)
	claudeAllowRule(t, env, "Bash(npm run cleanup)")

	env.PreToolUse("session-1", "toolu_1", "npm run cleanup")

	stdout, stderr, code := env.CLI("approval", "revoke", "1")
	if code == 0 {
		t.Fatalf("a removal must not proceed unasked:\n%s", stdout)
	}
	if !strings.Contains(stderr, "--yes") {
		t.Errorf("the refusal must say how to proceed: %q", stderr)
	}
	if !strings.Contains(stdout, "This will stop being trusted") {
		t.Errorf("the plan must still be printed:\n%s", stdout)
	}

	settings := env.ReadWorkspaceFile(filepath.Join(".claude", "settings.local.json"))
	if !strings.Contains(settings, "npm run cleanup") {
		t.Error("the settings were changed without a confirmation")
	}
	if allowed := env.PreToolUse("session-2", "toolu_2", "npm run cleanup"); allowed.PermissionDecision() != "allow" {
		t.Error("nothing was confirmed, so the permission must be exactly as it was")
	}
}
