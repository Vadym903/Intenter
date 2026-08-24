package e2e

import (
	"strings"
	"testing"
)

// S1 (PROTOTYPE_SPEC.md §29): reading inside the project is allowed by the
// baseline; everything the baseline does not cover still has to be asked about,
// and an approval for one read never widens into another operation.

func TestS1ReadOnlyWorkspaceIsAllowedByTheBaseline(t *testing.T) {
	env := NewEnv(t)

	result := env.PreToolUse("session-1", "toolu_1", "git status")
	if got := result.PermissionDecision(); got != "allow" {
		t.Fatalf("permissionDecision = %q, want allow\n%s", got, result.Stdout)
	}
	if !strings.Contains(result.Reason(), "Intenter") {
		t.Errorf("reason = %q, want it to name Intenter", result.Reason())
	}

	decision, class := env.Decision("git status")
	if decision != "allow" || class != "POLICY_READONLY_WORKSPACE" {
		t.Errorf("recorded %s/%s, want allow/POLICY_READONLY_WORKSPACE", decision, class)
	}
}

func TestS1TheBaselineAllowsQuietly(t *testing.T) {
	// The baseline answers most commands in a session. A notice on each would
	// bury the ones that carry information, so it says nothing at all.
	env := NewEnv(t)

	for _, command := range []string{"git status", "ls", "cat README.md"} {
		result := env.PreToolUse("session-1", "toolu_"+command, command)
		if got := result.PermissionDecision(); got != "allow" {
			t.Fatalf("%q: permissionDecision = %q, want allow\n%s", command, got, result.Stdout)
		}
		if message := result.SystemMessage(); message != "" {
			t.Errorf("%q: want no transcript notice, got %q", command, message)
		}
	}
}

func TestS1OtherReadsInsideTheWorkspaceAreAllowed(t *testing.T) {
	env := NewEnv(t)

	for _, command := range []string{
		"git status",
		"git diff",
		"cat README.md",
		"grep -r demo ./src",
		"find . -name '*.go'",
		"git log --oneline",
	} {
		result := env.PreToolUse("session-1", "toolu_"+command, command)
		if got := result.PermissionDecision(); got != "allow" {
			t.Errorf("%q: permissionDecision = %q, want allow (%s)", command, got, result.Stdout)
		}
	}
}

func TestS1BaselineCanBeTurnedOff(t *testing.T) {
	env := NewEnv(t)
	env.WriteConfig("[policy]\nallow_readonly_workspace = false\n")
	env.StopDaemon()
	env.StartDaemon()

	result := env.PreToolUse("session-1", "toolu_1", "git status")
	if !result.Deferred() {
		t.Fatalf("want a deferral with a summary, got %s", result.Stdout)
	}
	if !strings.Contains(result.SystemMessage(), "no approval yet") {
		t.Errorf("systemMessage = %q, want the deferral summary", result.SystemMessage())
	}

	decision, class := env.Decision("git status")
	if decision != "ask" || class != "NO_MATCHING_APPROVAL" {
		t.Errorf("recorded %s/%s, want ask/NO_MATCHING_APPROVAL", decision, class)
	}
}

func TestS1ApprovalCoversEquivalentInvocations(t *testing.T) {
	// With the baseline off, an approval for `git status` must cover the other
	// spellings of the same resolved action — and nothing else.
	env := NewEnv(t)
	env.WriteConfig("[policy]\nallow_readonly_workspace = false\n")
	env.StopDaemon()
	env.StartDaemon()

	env.PreToolUse("session-1", "toolu_1", "git status")
	eventID := env.LatestEventID()
	env.MustCLI("approve", itoa(eventID))

	for _, command := range []string{
		"git status",
		"git status --short",
		"git -C . status",
		"git --no-pager status",
	} {
		result := env.PreToolUse("session-2", "toolu_"+command, command)
		if got := result.PermissionDecision(); got != "allow" {
			t.Errorf("%q: permissionDecision = %q, want allow (%s)", command, got, result.Stdout)
			continue
		}
		decision, class := env.Decision(command)
		if decision != "allow" || class != "APPROVAL_MATCH" {
			t.Errorf("%q: recorded %s/%s, want allow/APPROVAL_MATCH", command, decision, class)
		}
	}
}

func TestS1ApprovalDoesNotGeneralizeToAnotherOperation(t *testing.T) {
	// Each git subcommand is its own semantic op, so approving `git status`
	// never silently approves `git diff` (§15.4).
	env := NewEnv(t)
	env.WriteConfig("[policy]\nallow_readonly_workspace = false\n")
	env.StopDaemon()
	env.StartDaemon()

	env.PreToolUse("session-1", "toolu_1", "git status")
	env.MustCLI("approve", itoa(env.LatestEventID()))

	result := env.PreToolUse("session-2", "toolu_2", "git diff")
	if got := result.PermissionDecision(); got == "allow" {
		t.Fatalf("git diff must not be covered by a git status approval\n%s", result.Stdout)
	}
	decision, _ := env.Decision("git diff")
	if decision != "ask" {
		t.Errorf("recorded %s, want ask", decision)
	}
}

func TestS1ApprovalNeverCoversADestructiveSecondCommand(t *testing.T) {
	// INVARIANT I-4: the safety floor holds regardless of what was approved.
	env := NewEnv(t)
	env.WriteConfig("[policy]\nallow_readonly_workspace = false\n")
	env.StopDaemon()
	env.StartDaemon()

	env.PreToolUse("session-1", "toolu_1", "git status")
	env.MustCLI("approve", itoa(env.LatestEventID()))

	result := env.PreToolUse("session-2", "toolu_2", "git status && rm -rf ~")
	if got := result.PermissionDecision(); got != "deny" {
		t.Fatalf("permissionDecision = %q, want deny\n%s", got, result.Stdout)
	}
	if !strings.Contains(result.SystemMessage(), "Intenter BLOCK") {
		t.Errorf("systemMessage = %q", result.SystemMessage())
	}

	decision, class := env.Decision("git status && rm -rf ~")
	if decision != "block" || !strings.HasPrefix(class, "HARD_RULE_R2") {
		t.Errorf("recorded %s/%s, want block/HARD_RULE_R2", decision, class)
	}
}

func TestS1ChangingDirectoryChangesTheScope(t *testing.T) {
	// §16.2: after `cd ~` the read targets HOME, so neither the workspace
	// baseline nor a workspace-scoped approval applies.
	env := NewEnv(t)

	result := env.PreToolUse("session-1", "toolu_1", "cd ~ && git status")
	if got := result.PermissionDecision(); got == "allow" {
		t.Fatalf("a read in HOME must not be allowed by the workspace baseline\n%s", result.Stdout)
	}

	decision, _ := env.Decision("cd ~ && git status")
	if decision != "ask" {
		t.Errorf("recorded %s, want ask", decision)
	}
}

func TestS1HistoryExplainsTheBaselineAllow(t *testing.T) {
	env := NewEnv(t)
	env.PreToolUse("session-1", "toolu_1", "git status")

	out := env.MustCLI("history", "show", itoa(env.LatestEventID()))
	for _, want := range []string{
		"ALLOW", "POLICY_READONLY_WORKSPACE", "git status",
		"targets:", "WORKSPACE", "effects:", "READ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("history show missing %q:\n%s", want, out)
		}
	}
}

func TestS1PermissionRequestSecondEnforcement(t *testing.T) {
	// §11.4: the PermissionRequest hook re-checks and can allow outright.
	env := NewEnv(t)

	env.PreToolUse("session-1", "toolu_1", "git status")
	result := env.PermissionRequest("session-1", "git status", []any{
		map[string]any{
			"type": "addRules",
			"rules": []any{map[string]any{
				"toolName":    "Bash",
				"ruleContent": "git status",
				"behavior":    "allow",
				"destination": "localSettings",
			}},
		},
	})
	if got := result.PermissionBehavior(); got != "allow" {
		t.Fatalf("behavior = %q, want allow\n%s", got, result.Stdout)
	}

	// The dialog and its suggestions are recorded on the same event.
	event := env.FullEvent(env.EventIDFor("git status"))
	if shown, _ := event["prompt_shown"].(bool); !shown {
		t.Error("prompt_shown must be recorded (§24)")
	}
	suggestions, _ := event["permission_suggestions"].([]any)
	if len(suggestions) != 1 {
		t.Errorf("permission_suggestions = %+v, want them stored verbatim", suggestions)
	}
}

func TestS1TheAuditRecordsWhatTheAgentWasTold(t *testing.T) {
	// §23.2's `adapter_action`, through the real binary: the decision and its
	// delivery are separate facts, and both survive to `history show`.
	env := NewEnv(t)

	env.PreToolUse("session-1", "toolu_1", "git status")
	allowed := env.FullEvent(env.EventIDFor("git status"))
	if got, _ := allowed["adapter_action"].(string); got != "allow" {
		t.Errorf("an allowed command was delivered as %q, want allow", got)
	}

	env.PreToolUse("session-1", "toolu_2", "rm -rf ~/Documents")
	blocked := env.FullEvent(env.EventIDFor("rm -rf ~/Documents"))
	if got, _ := blocked["adapter_action"].(string); got != "deny" {
		t.Errorf("a blocked command was delivered as %q, want deny", got)
	}

	// An ask that Intenter hands to Claude's own dialog is recorded as a
	// deferral, which is what tells a user why they saw no Intenter prompt.
	env.PreToolUse("session-1", "toolu_3", "some-unknown-tool")
	deferred := env.FullEvent(env.EventIDFor("some-unknown-tool"))
	if got, _ := deferred["adapter_action"].(string); got != "defer" {
		t.Errorf("a deferred ask was delivered as %q, want defer", got)
	}

	out := env.MustCLI("history", "show", itoa(env.EventIDFor("rm -rf ~/Documents")))
	if !strings.Contains(out, "delivered:") {
		t.Errorf("history show must report what the agent was told:\n%s", out)
	}
}
