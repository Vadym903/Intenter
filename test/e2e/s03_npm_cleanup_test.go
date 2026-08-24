package e2e

import (
	"strings"
	"testing"
)

// S3 (PROTOTYPE_SPEC.md §29) is the primary demo: `npm run cleanup` is
// understood as the `rm -rf ./dist` it actually runs, approved once, and
// auto-allowed afterwards — with the approval tied to the resolved effect
// rather than the command string.

const cleanupScripts = `{"cleanup":"rm -rf ./dist"}`

func TestS3FirstRunDefersWithASummary(t *testing.T) {
	env := NewEnv(t)
	env.SetScripts(cleanupScripts)

	result := env.PreToolUse("session-1", "toolu_1", "npm run cleanup")
	if !result.Deferred() {
		t.Fatalf("want a deferral so Claude's own dialog appears, got %s", result.Stdout)
	}

	message := result.SystemMessage()
	if message == "" {
		t.Fatal("want a systemMessage summarizing what the command resolves to")
	}
	for _, want := range []string{"Intenter", "npm run cleanup", "rm -rf ./dist", "intenter approve"} {
		if !strings.Contains(message, want) {
			t.Errorf("systemMessage must mention %q:\n%s", want, message)
		}
	}

	decision, class := env.Decision("npm run cleanup")
	if decision != "ask" || class != "NO_MATCHING_APPROVAL" {
		t.Errorf("recorded %s/%s, want ask/NO_MATCHING_APPROVAL", decision, class)
	}
}

func TestS3ResolutionIsRecordedInFull(t *testing.T) {
	env := NewEnv(t)
	env.SetScripts(cleanupScripts)
	env.PreToolUse("session-1", "toolu_1", "npm run cleanup")

	out := env.MustCLI("history", "show", itoa(env.LatestEventID()))
	for _, want := range []string{
		"npm run cleanup",
		"rm -rf ./dist",
		"./dist",
		"WORKSPACE_GENERATED",
		"DELETE",
		"recursive",
		"force",
		"npm-script:package.json#scripts.cleanup",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("history show must record %q:\n%s", want, out)
		}
	}
}

func TestS3ApproveOnceThenAutoAllowInANewSession(t *testing.T) {
	env := NewEnv(t)
	env.SetScripts(cleanupScripts)

	env.PreToolUse("session-1", "toolu_1", "npm run cleanup")
	env.MustCLI("approve", itoa(env.LatestEventID()))

	// A new session, the same command: allowed without asking.
	result := env.PreToolUse("session-2", "toolu_2", "npm run cleanup")
	if got := result.PermissionDecision(); got != "allow" {
		t.Fatalf("permissionDecision = %q, want allow\n%s", got, result.Stdout)
	}

	decision, class := env.Decision("npm run cleanup")
	if decision != "allow" || class != "APPROVAL_MATCH" {
		t.Errorf("recorded %s/%s, want allow/APPROVAL_MATCH", decision, class)
	}
}

func TestS3AnAllowedCommandSaysWhichApprovalAllowedIt(t *testing.T) {
	// An allow with no trace is indistinguishable from Intenter not running.
	// The notice names the approval so the user can go and look at it.
	env := NewEnv(t)
	env.SetScripts(cleanupScripts)

	env.PreToolUse("session-1", "toolu_1", "npm run cleanup")
	env.MustCLI("approve", itoa(env.LatestEventID()))

	result := env.PreToolUse("session-2", "toolu_2", "npm run cleanup")
	if got := result.PermissionDecision(); got != "allow" {
		t.Fatalf("permissionDecision = %q, want allow\n%s", got, result.Stdout)
	}

	message := result.SystemMessage()
	for _, want := range []string{"Intenter", "allowed", "rm -rf ./dist", "approval 1"} {
		if !strings.Contains(message, want) {
			t.Errorf("systemMessage must mention %q:\n%s", want, message)
		}
	}
}

func TestS3AnImportedAllowSaysWhereTheApprovalCameFrom(t *testing.T) {
	// The import happens on the user's behalf, so the run it first allows is
	// where they learn an approval now exists that they never typed.
	env := NewEnv(t)
	env.SetScripts(cleanupScripts)
	env.WriteClaudeSettings(`{"permissions":{"allow":["Bash(npm run cleanup)"]}}`)

	result := env.PreToolUse("session-1", "toolu_1", "npm run cleanup")
	if got := result.PermissionDecision(); got != "allow" {
		t.Fatalf("permissionDecision = %q, want allow\n%s", got, result.Stdout)
	}

	message := result.SystemMessage()
	for _, want := range []string{"Intenter", "allowed", "approval 1", "don't ask again"} {
		if !strings.Contains(message, want) {
			t.Errorf("systemMessage must mention %q:\n%s", want, message)
		}
	}
}

func TestS3TheDeferralNamesTheDialogAnswer(t *testing.T) {
	// Claude renders the dialog's options itself and no hook output reaches
	// them, so this notice is the only place the persistent answer and what
	// Intenter does with it can be connected.
	env := NewEnv(t)
	env.SetScripts(cleanupScripts)

	result := env.PreToolUse("session-1", "toolu_1", "npm run cleanup")
	if !result.Deferred() {
		t.Fatalf("want a deferral so Claude's own dialog appears, got %s", result.Stdout)
	}

	message := result.SystemMessage()
	for _, want := range []string{`"Yes, and don't ask again"`, "not the text you typed"} {
		if !strings.Contains(message, want) {
			t.Errorf("systemMessage must mention %q:\n%s", want, message)
		}
	}
}

func TestS3ApprovalIsTiedToTheResolvedEffectNotTheString(t *testing.T) {
	// §20.3 rule 3: the approval was granted through the wrapper, so it carries
	// the script fingerprint. The direct command produces no such fingerprint
	// and therefore does not match.
	env := NewEnv(t)
	env.SetScripts(cleanupScripts)

	env.PreToolUse("session-1", "toolu_1", "npm run cleanup")
	env.MustCLI("approve", itoa(env.LatestEventID()))

	result := env.PreToolUse("session-2", "toolu_2", "rm -rf ./dist")
	if got := result.PermissionDecision(); got == "allow" {
		t.Fatalf("a direct rm must not be covered by the npm run approval\n%s", result.Stdout)
	}
	decision, _ := env.Decision("rm -rf ./dist")
	if decision != "ask" {
		t.Errorf("recorded %s, want ask", decision)
	}
}

func TestS3ConsentImportDuringEvaluate(t *testing.T) {
	// §19.5 path (a): the user already has a Claude rule for this command, so
	// the first run imports it once, after full resolution and policy.
	env := NewEnv(t)
	env.SetScripts(cleanupScripts)
	env.WriteClaudeSettings(`{"permissions":{"allow":["Bash(npm run cleanup)"]}}`)

	result := env.PreToolUse("session-1", "toolu_1", "npm run cleanup")
	if got := result.PermissionDecision(); got != "allow" {
		t.Fatalf("permissionDecision = %q, want allow\n%s", got, result.Stdout)
	}

	decision, class := env.Decision("npm run cleanup")
	if decision != "allow" || class != "RULE_IMPORT" {
		t.Fatalf("recorded %s/%s, want allow/RULE_IMPORT", decision, class)
	}

	if count := env.ApprovalCount(); count != 1 {
		t.Fatalf("approvals = %d, want exactly one import", count)
	}
	listed := env.MustCLI("approvals")
	if !strings.Contains(listed, "claude_rule") {
		t.Errorf("the approval must record where it came from:\n%s", listed)
	}
}

func TestS3ConsentImportDuringPostToolUse(t *testing.T) {
	// §19.5 path (b): Intenter deferred, the user chose "don't ask again" in
	// Claude's dialog, and the rule arrives with the execution report.
	env := NewEnv(t)
	env.SetScripts(cleanupScripts)

	deferred := env.PreToolUse("session-1", "toolu_1", "npm run cleanup")
	if !deferred.Deferred() {
		t.Fatalf("want a deferral first, got %s", deferred.Stdout)
	}
	if count := env.ApprovalCount(); count != 0 {
		t.Fatalf("approvals = %d, want none before consent", count)
	}

	// Claude writes the rule the user just accepted, then reports the execution.
	env.WriteClaudeSettings(`{"permissions":{"allow":["Bash(npm run cleanup)"]}}`)
	env.PostToolUse("session-1", "toolu_1", "npm run cleanup")

	if count := env.ApprovalCount(); count != 1 {
		t.Fatalf("approvals = %d, want one imported approval", count)
	}

	// The next occurrence matches that approval rather than importing again.
	result := env.PreToolUse("session-2", "toolu_2", "npm run cleanup")
	if got := result.PermissionDecision(); got != "allow" {
		t.Fatalf("permissionDecision = %q, want allow\n%s", got, result.Stdout)
	}
	decision, class := env.Decision("npm run cleanup")
	if decision != "allow" || class != "APPROVAL_MATCH" {
		t.Errorf("recorded %s/%s, want allow/APPROVAL_MATCH", decision, class)
	}
	if count := env.ApprovalCount(); count != 1 {
		t.Errorf("approvals = %d, want no second import", count)
	}
}

func TestS3ChangedScriptIsNotSilentlyTrusted(t *testing.T) {
	// The hypothesis: the approval covers the effect, so rewriting the script
	// withdraws it — even though the command string and Claude's own rule are
	// unchanged.
	env := NewEnv(t)
	env.SetScripts(cleanupScripts)
	env.WriteClaudeSettings(`{"permissions":{"allow":["Bash(npm run cleanup)"]}}`)

	allowed := env.PreToolUse("session-1", "toolu_1", "npm run cleanup")
	if got := allowed.PermissionDecision(); got != "allow" {
		t.Fatalf("permissionDecision = %q, want allow\n%s", got, allowed.Stdout)
	}

	// The script now deletes a home directory.
	env.SetScripts(`{"cleanup":"rm -rf ~/Documents"}`)

	blocked := env.PreToolUse("session-2", "toolu_2", "npm run cleanup")
	if got := blocked.PermissionDecision(); got != "deny" {
		t.Fatalf("permissionDecision = %q, want deny\n%s", got, blocked.Stdout)
	}

	decision, class := env.Decision("npm run cleanup")
	if decision != "block" || class != "HARD_RULE_R2" {
		t.Errorf("recorded %s/%s, want block/HARD_RULE_R2", decision, class)
	}

	// The message names what changed, not just that something did.
	explanation := env.MustCLI("history", "show", itoa(env.LatestEventID()))
	for _, want := range []string{"rm -rf ~/Documents", "HOME", "R2"} {
		if !strings.Contains(explanation, want) {
			t.Errorf("history show must mention %q:\n%s", want, explanation)
		}
	}

	// The still-present Claude rule does not produce a second import.
	if count := env.ApprovalCount(); count != 1 {
		t.Errorf("approvals = %d, want no re-import for changed behavior (§19.5)", count)
	}
}

func TestS3MilderChangeAsksAndNamesTheApproval(t *testing.T) {
	env := NewEnv(t)
	env.SetScripts(cleanupScripts)

	env.PreToolUse("session-1", "toolu_1", "npm run cleanup")
	env.MustCLI("approve", itoa(env.LatestEventID()))

	// Still inside the workspace, but no longer the approved target.
	env.SetScripts(`{"cleanup":"rm -rf ./src"}`)

	result := env.PreToolUse("session-2", "toolu_2", "npm run cleanup")
	if got := result.PermissionDecision(); got != "ask" {
		t.Fatalf("permissionDecision = %q, want ask\n%s", got, result.Stdout)
	}
	if !strings.Contains(result.Reason(), "no longer covers") {
		t.Errorf("reason = %q, want it to say the approval stopped covering the action", result.Reason())
	}

	decision, class := env.Decision("npm run cleanup")
	if decision != "ask" || class != "APPROVAL_MISMATCH" {
		t.Fatalf("recorded %s/%s, want ask/APPROVAL_MISMATCH", decision, class)
	}

	explanation := env.MustCLI("history", "show", itoa(env.LatestEventID()))
	for _, want := range []string{
		"approval 1 no longer matches",
		"npm-script:package.json#scripts.cleanup changed",
		"./dist",
		"./src",
	} {
		if !strings.Contains(explanation, want) {
			t.Errorf("history show must mention %q:\n%s", want, explanation)
		}
	}
}

func TestS3PreAndPostScriptsAreIncluded(t *testing.T) {
	env := NewEnv(t)
	env.SetScripts(`{"precleanup":"mkdir -p ./dist","cleanup":"rm -rf ./dist"}`)

	env.PreToolUse("session-1", "toolu_1", "npm run cleanup")
	out := env.MustCLI("history", "show", itoa(env.LatestEventID()))

	for _, want := range []string{
		"npm-script:package.json#scripts.precleanup",
		"npm-script:package.json#scripts.cleanup",
		"CREATE",
		"DELETE",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the pre script must be part of the action, missing %q:\n%s", want, out)
		}
	}
}

func TestS3AddingAPreScriptInvalidatesTheApproval(t *testing.T) {
	// A new pre script is new behavior, so the approval stops matching.
	env := NewEnv(t)
	env.SetScripts(cleanupScripts)

	env.PreToolUse("session-1", "toolu_1", "npm run cleanup")
	env.MustCLI("approve", itoa(env.LatestEventID()))

	env.SetScripts(`{"precleanup":"rm -rf ./src","cleanup":"rm -rf ./dist"}`)

	result := env.PreToolUse("session-2", "toolu_2", "npm run cleanup")
	if got := result.PermissionDecision(); got == "allow" {
		t.Fatalf("an added pre script must withdraw the approval\n%s", result.Stdout)
	}
	decision, class := env.Decision("npm run cleanup")
	if decision != "ask" || class != "APPROVAL_MISMATCH" {
		t.Errorf("recorded %s/%s, want ask/APPROVAL_MISMATCH", decision, class)
	}
}

func TestS3ApprovalsListingShowsWhatIsTrusted(t *testing.T) {
	env := NewEnv(t)
	env.SetScripts(cleanupScripts)

	env.PreToolUse("session-1", "toolu_1", "npm run cleanup")
	env.MustCLI("approve", itoa(env.LatestEventID()))
	env.PreToolUse("session-2", "toolu_2", "npm run cleanup")

	listed := env.MustCLI("approvals")
	for _, want := range []string{"RUN_SCRIPT", "EXACT", "ACTIVE", "./dist"} {
		if !strings.Contains(listed, want) {
			t.Errorf("approvals listing must show %q:\n%s", want, listed)
		}
	}

	// Matched on label and value rather than on the padding between them, so
	// that a change to the shared field column is a layout change and not a
	// broken test.
	detail := env.MustCLI("approval", "show", "1")
	for _, want := range []string{
		"valid while unchanged",
		"npm-script:package.json#scripts.cleanup",
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("approval show must include %q:\n%s", want, detail)
		}
	}
	for label, want := range map[string]string{
		"created by": "npm run cleanup",
		"used":       "1 time(s)",
	} {
		if !fieldContains(detail, label, want) {
			t.Errorf("approval show must report %s = %q:\n%s", label, want, detail)
		}
	}
}

// fieldContains reports whether a detail view carries a labeled value.
func fieldContains(out, label, want string) bool {
	prefix := "  " + label + ":"
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.Contains(strings.TrimSpace(strings.TrimPrefix(line, prefix)), want)
		}
	}
	return false
}
