package claude

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Vadym903/Intenter/internal/action"
)

// result builds an evaluation result with an audit event id.
func result(decision action.DecisionOutcome, class action.DecisionClass, reason string) action.EvaluationResult {
	return action.EvaluationResult{
		AuditEventID: action.Ref(42),
		Decision:     decision,
		Class:        class,
		Reason:       reason,
		Explanation:  []string{"npm run cleanup -> rm -rf ./dist"},
	}
}

// encode renders a response the way the hook writes it.
func encode(t *testing.T, response *Response) map[string]any {
	t.Helper()
	if response == nil {
		return nil
	}
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return decoded
}

// hookOutput extracts hookSpecificOutput as a map, or nil.
func hookOutput(decoded map[string]any) map[string]any {
	if decoded == nil {
		return nil
	}
	out, _ := decoded["hookSpecificOutput"].(map[string]any)
	return out
}

// allAskClasses is every class an ASK decision can carry (§18.5).
var allAskClasses = []action.DecisionClass{
	action.ClassNoMatchingApproval,
	action.ClassApprovalMismatch,
	action.ClassPolicyRequiresConfirmation,
	action.ClassUnresolvedCommand,
	action.ClassUnsupportedSyntax,
	action.ClassAmbiguousPath,
	action.ClassContextUnavailable,
	action.ClassAgentRuleConflict,
	action.ClassEngineError,
}

func TestPreToolUseMappingMatrix(t *testing.T) {
	// The table in contracts/claude-hooks.md, for every outcome and class.
	tests := []struct {
		name          string
		result        action.EvaluationResult
		wantDecision  string
		wantSilent    bool
		wantSystemMsg bool
		wantAction    action.AdapterAction
	}{
		{
			name:         "allow",
			result:       result(action.OutcomeAllow, action.ClassApprovalMatch, "approval 7 covers this action"),
			wantDecision: "allow", wantSystemMsg: true, wantAction: action.AdapterAllow,
		},
		{
			name:         "allow from the baseline",
			result:       result(action.OutcomeAllow, action.ClassPolicyReadonlyWorkspace, "reads only files inside this project"),
			wantDecision: "allow", wantAction: action.AdapterAllow,
		},
		{
			name:         "allow from an imported rule",
			result:       result(action.OutcomeAllow, action.ClassRuleImport, "your agent already holds permission"),
			wantDecision: "allow", wantSystemMsg: true, wantAction: action.AdapterAllow,
		},
		{
			name:         "block",
			result:       result(action.OutcomeBlock, action.HardRuleClass("R2"), "deleting ~/Documents"),
			wantDecision: "deny", wantSystemMsg: true, wantAction: action.AdapterDeny,
		},
		{
			name:         "ask on an approval mismatch forces the dialog",
			result:       result(action.OutcomeAsk, action.ClassApprovalMismatch, "approval 7 no longer covers this action"),
			wantDecision: "ask", wantAction: action.AdapterPrompt,
		},
		{
			name:         "ask on a policy confirmation forces the dialog",
			result:       result(action.OutcomeAsk, action.ClassPolicyRequiresConfirmation, "the command discards uncommitted changes"),
			wantDecision: "ask", wantAction: action.AdapterPrompt,
		},
		{
			name:          "ask without an approval defers with a summary",
			result:        result(action.OutcomeAsk, action.ClassNoMatchingApproval, "not approved yet"),
			wantSilent:    true,
			wantSystemMsg: true,
			wantAction:    action.AdapterDefer,
		},
		{
			name:       "ask on an unresolved command defers silently",
			result:     result(action.OutcomeAsk, action.ClassUnresolvedCommand, "cannot tell what this does"),
			wantSilent: true, wantAction: action.AdapterDefer,
		},
		{
			name:       "ask on unsupported syntax defers silently",
			result:     result(action.OutcomeAsk, action.ClassUnsupportedSyntax, "uses a loop"),
			wantSilent: true, wantAction: action.AdapterDefer,
		},
		{
			name:       "ask on an ambiguous path defers silently",
			result:     result(action.OutcomeAsk, action.ClassAmbiguousPath, "depends on a variable"),
			wantSilent: true, wantAction: action.AdapterDefer,
		},
		{
			name:       "ask without context defers silently",
			result:     result(action.OutcomeAsk, action.ClassContextUnavailable, "no project"),
			wantSilent: true, wantAction: action.AdapterDefer,
		},
		{
			name:       "ask on an engine error defers silently",
			result:     result(action.OutcomeAsk, action.ClassEngineError, "database is locked"),
			wantSilent: true, wantAction: action.AdapterDefer,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response, adapterAction := MapPreToolUse(tt.result, false)
			if adapterAction != tt.wantAction {
				t.Errorf("adapter action = %s, want %s", adapterAction, tt.wantAction)
			}

			decoded := encode(t, response)
			output := hookOutput(decoded)

			if tt.wantSilent {
				if output != nil {
					t.Errorf("want no permissionDecision, got %+v", output)
				}
			} else {
				if output == nil {
					t.Fatalf("want a permissionDecision, got %+v", decoded)
				}
				if got := output["permissionDecision"]; got != tt.wantDecision {
					t.Errorf("permissionDecision = %v, want %q", got, tt.wantDecision)
				}
				if output["hookEventName"] != EventPreToolUse {
					t.Errorf("hookEventName = %v", output["hookEventName"])
				}
				reason, _ := output["permissionDecisionReason"].(string)
				if !strings.Contains(reason, "Intenter") || !strings.Contains(reason, "event 42") {
					t.Errorf("reason = %q, want it to name Intenter and the event", reason)
				}
			}

			// Asserted in both directions: a message that appears where none was
			// wanted is noise in the user's transcript, which is as much of a
			// regression as a missing one.
			message, _ := decoded["systemMessage"].(string)
			if tt.wantSystemMsg && message == "" {
				t.Errorf("want a systemMessage, got %+v", decoded)
			}
			if !tt.wantSystemMsg && message != "" {
				t.Errorf("want no systemMessage, got %q", message)
			}
		})
	}
}

func TestBypassModeEmitsOnlyBlock(t *testing.T) {
	// §11.3: a forced ask becomes a denial in bypassPermissions, so everything
	// except BLOCK stays silent there.
	blocked, adapterAction := MapPreToolUse(
		result(action.OutcomeBlock, action.HardRuleClass("R1"), "deleting /usr"), true)
	if blocked == nil || adapterAction != action.AdapterDeny {
		t.Fatalf("BLOCK must still be emitted in bypass mode, got %+v/%s", blocked, adapterAction)
	}

	allowed, adapterAction := MapPreToolUse(
		result(action.OutcomeAllow, action.ClassApprovalMatch, "approved"), true)
	if allowed == nil || adapterAction != action.AdapterAllow {
		t.Errorf("ALLOW is still emitted in bypass mode, got %+v", allowed)
	}

	for _, class := range allAskClasses {
		response, adapterAction := MapPreToolUse(result(action.OutcomeAsk, class, "reason"), true)
		if response != nil {
			t.Errorf("class %s: want silence in bypass mode, got %+v", class, response)
		}
		if adapterAction != action.AdapterDefer {
			t.Errorf("class %s: adapter action = %s, want defer", class, adapterAction)
		}
	}
}

// allPermissionModes is every mode Claude reports.
var allPermissionModes = []string{
	ModeDefault, ModeAcceptEdits, ModePlan, ModeDontAsk, ModeBypassPermissions,
}

func TestPermissionModeMatrix(t *testing.T) {
	// §11.3: only bypassPermissions changes what may be emitted, because a
	// forced ask becomes a denial there. Every interactive mode behaves the
	// same, and nothing an agent is doing can turn a BLOCK into an allow.
	outcomes := []struct {
		outcome action.DecisionOutcome
		class   action.DecisionClass
	}{
		{action.OutcomeAllow, action.ClassApprovalMatch},
		{action.OutcomeAllow, action.ClassPolicyReadonlyWorkspace},
		{action.OutcomeAllow, action.ClassRuleImport},
		{action.OutcomeBlock, action.HardRuleClass("R1")},
		{action.OutcomeBlock, action.HardRuleClass("R2")},
		{action.OutcomeBlock, action.HardRuleClass("R5")},
	}
	for _, class := range allAskClasses {
		outcomes = append(outcomes, struct {
			outcome action.DecisionOutcome
			class   action.DecisionClass
		}{action.OutcomeAsk, class})
	}

	for _, mode := range allPermissionModes {
		bypassing := mode == ModeBypassPermissions

		for _, tt := range outcomes {
			name := mode + "/" + string(tt.outcome) + "/" + string(tt.class)
			t.Run(name, func(t *testing.T) {
				response, adapterAction := MapPreToolUse(result(tt.outcome, tt.class, "reason"), bypassing)
				decision := hookOutput(encode(t, response))

				switch tt.outcome {
				case action.OutcomeAllow:
					if decision == nil || decision["permissionDecision"] != "allow" {
						t.Fatalf("want allow in every mode, got %+v", decision)
					}
					if adapterAction != action.AdapterAllow {
						t.Errorf("adapter action = %s, want allow", adapterAction)
					}

				case action.OutcomeBlock:
					if decision == nil || decision["permissionDecision"] != "deny" {
						t.Fatalf("a block must be emitted in every mode, got %+v", decision)
					}
					if adapterAction != action.AdapterDeny {
						t.Errorf("adapter action = %s, want deny", adapterAction)
					}

				case action.OutcomeAsk:
					if bypassing {
						if response != nil {
							t.Errorf("bypass mode emits nothing for an ask, got %+v", response)
						}
						if adapterAction != action.AdapterDefer {
							t.Errorf("adapter action = %s, want defer", adapterAction)
						}
						return
					}
					if forcedAskClasses[tt.class] {
						if decision == nil || decision["permissionDecision"] != "ask" {
							t.Errorf("class %s forces the dialog, got %+v", tt.class, decision)
						}
						return
					}
					if decision != nil {
						t.Errorf("class %s defers, got %+v", tt.class, decision)
					}
				}
			})
		}
	}
}

func TestBlockIsNeverDowngradedByAnyMode(t *testing.T) {
	// The single property that matters most in this table.
	for _, mode := range allPermissionModes {
		bypassing := mode == ModeBypassPermissions
		response, adapterAction := MapPreToolUse(
			result(action.OutcomeBlock, action.HardRuleClass("R2"), "deleting ~/Documents"), bypassing)

		decision := hookOutput(encode(t, response))
		if decision == nil || decision["permissionDecision"] != "deny" {
			t.Errorf("mode %s: want deny, got %+v", mode, decision)
		}
		if adapterAction != action.AdapterDeny {
			t.Errorf("mode %s: adapter action = %s, want deny", mode, adapterAction)
		}
	}
}

func TestPreToolUseNeverEmitsForbiddenFields(t *testing.T) {
	// contracts/claude-hooks.md: never `permissionDecision: "defer"`,
	// `updatedInput` or `updatedPermissions`.
	outcomes := []action.DecisionOutcome{action.OutcomeAllow, action.OutcomeAsk, action.OutcomeBlock}
	classes := append([]action.DecisionClass{
		action.ClassApprovalMatch,
		action.ClassPolicyReadonlyWorkspace,
		action.ClassRuleImport,
		action.HardRuleClass("R2"),
	}, allAskClasses...)

	for _, outcome := range outcomes {
		for _, class := range classes {
			for _, bypassing := range []bool{false, true} {
				response, _ := MapPreToolUse(result(outcome, class, "reason"), bypassing)
				decoded := encode(t, response)
				if decoded == nil {
					continue
				}
				for _, forbidden := range []string{"updatedInput", "updatedPermissions"} {
					if _, present := decoded[forbidden]; present {
						t.Errorf("%s/%s: response must never carry %q", outcome, class, forbidden)
					}
				}
				if output := hookOutput(decoded); output != nil {
					if got := output["permissionDecision"]; got == "defer" {
						t.Errorf("%s/%s: permissionDecision must never be \"defer\"", outcome, class)
					}
					if _, present := output["updatedPermissions"]; present {
						t.Errorf("%s/%s: must never carry updatedPermissions", outcome, class)
					}
				}
			}
		}
	}
}

func TestPermissionRequestMapping(t *testing.T) {
	tests := []struct {
		name       string
		result     action.EvaluationResult
		wantSilent bool
		behavior   string
		wantAction action.AdapterAction
	}{
		{
			name:     "allow",
			result:   result(action.OutcomeAllow, action.ClassApprovalMatch, "approved"),
			behavior: "allow", wantAction: action.AdapterAllow,
		},
		{
			name:     "block",
			result:   result(action.OutcomeBlock, action.HardRuleClass("R2"), "deleting ~/Documents"),
			behavior: "deny", wantAction: action.AdapterDeny,
		},
		{
			name:       "ask lets the dialog proceed",
			result:     result(action.OutcomeAsk, action.ClassNoMatchingApproval, "not approved yet"),
			wantSilent: true, wantAction: action.AdapterPrompt,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response, adapterAction := MapPermissionRequest(tt.result)
			if adapterAction != tt.wantAction {
				t.Errorf("adapter action = %s, want %s", adapterAction, tt.wantAction)
			}
			if tt.wantSilent {
				if response != nil {
					t.Errorf("want silence, got %+v", response)
				}
				return
			}

			output := hookOutput(encode(t, response))
			if output == nil {
				t.Fatal("want a decision")
			}
			if output["hookEventName"] != EventPermissionRequest {
				t.Errorf("hookEventName = %v", output["hookEventName"])
			}
			decision, _ := output["decision"].(map[string]any)
			if decision == nil {
				t.Fatalf("want a decision object, got %+v", output)
			}
			if decision["behavior"] != tt.behavior {
				t.Errorf("behavior = %v, want %q", decision["behavior"], tt.behavior)
			}
			if tt.behavior == "deny" {
				if message, _ := decision["message"].(string); !strings.Contains(message, "Intenter BLOCK") {
					t.Errorf("message = %q", message)
				}
				if decision["interrupt"] != false {
					t.Errorf("interrupt = %v, want false", decision["interrupt"])
				}
			}
		})
	}
}

func TestApproveHintAppearsWhereItIsActionable(t *testing.T) {
	mismatch, _ := MapPreToolUse(
		result(action.OutcomeAsk, action.ClassApprovalMismatch, "approval 7 no longer covers this"), false)
	output := hookOutput(encode(t, mismatch))
	reason, _ := output["permissionDecisionReason"].(string)
	if !strings.Contains(reason, "intenter approve 42") {
		t.Errorf("reason = %q, want the approve hint", reason)
	}

	deferred, _ := MapPreToolUse(
		result(action.OutcomeAsk, action.ClassNoMatchingApproval, "not approved yet"), false)
	message, _ := encode(t, deferred)["systemMessage"].(string)
	if !strings.Contains(message, "intenter approve 42") {
		t.Errorf("systemMessage = %q, want the approve hint", message)
	}
	if !strings.Contains(message, "npm run cleanup -> rm -rf ./dist") {
		t.Errorf("systemMessage = %q, want the resolution summary", message)
	}
}

func TestAllowIsAnnouncedOnlyWhenAnApprovalDecidedIt(t *testing.T) {
	// An allow that one of Intenter's approvals produced is worth a line: it is
	// the only way a user sees the gate working rather than absent. The
	// read-only baseline is not, because it answers `git status` and `ls` all
	// session long and would bury everything else.
	matched := result(action.OutcomeAllow, action.ClassApprovalMatch, "approval 7 covers this action")
	matched.ApprovalID = action.Ref(7)

	response, _ := MapPreToolUse(matched, false)
	message, _ := encode(t, response)["systemMessage"].(string)
	for _, want := range []string{
		"Intenter [event 42]",
		"allowed",
		"npm run cleanup -> rm -rf ./dist",
		"approval 7",
		"intenter approval show 7",
	} {
		if !strings.Contains(message, want) {
			t.Errorf("systemMessage must mention %q:\n%s", want, message)
		}
	}

	imported := result(action.OutcomeAllow, action.ClassRuleImport, "your agent already holds permission")
	imported.ApprovalID = action.Ref(3)

	response, _ = MapPreToolUse(imported, false)
	message, _ = encode(t, response)["systemMessage"].(string)
	// The import happens on the user's behalf, so the notice says where the
	// approval came from; otherwise an approval they never typed appears from
	// nowhere.
	for _, want := range []string{"approval 3", "don't ask again"} {
		if !strings.Contains(message, want) {
			t.Errorf("an imported allow must name its origin, missing %q:\n%s", want, message)
		}
	}

	baseline := result(action.OutcomeAllow, action.ClassPolicyReadonlyWorkspace, "reads only files inside this project")
	response, _ = MapPreToolUse(baseline, false)
	if message, _ := encode(t, response)["systemMessage"].(string); message != "" {
		t.Errorf("the baseline must stay quiet, got %q", message)
	}
}

func TestAllowIsStillAnnouncedWithoutAnApprovalID(t *testing.T) {
	// The id is optional in the result; the line must still read, and must not
	// invent an approval.
	allowed := result(action.OutcomeAllow, action.ClassApprovalMatch, "approval covers this action")
	response, _ := MapPreToolUse(allowed, false)

	message, _ := encode(t, response)["systemMessage"].(string)
	if !strings.Contains(message, "allowed") {
		t.Errorf("systemMessage = %q", message)
	}
	if strings.Contains(message, "approval show") {
		t.Errorf("no approval id was known, so nothing may point at one: %q", message)
	}
}

func TestDeferralNamesTheDialogAnswerThatIntenterImports(t *testing.T) {
	// The dialog's own option labels are Claude's and no hook output reaches
	// them (research.md R-10, B-3), so this notice is the only place the two can
	// be connected for the user.
	deferred, _ := MapPreToolUse(
		result(action.OutcomeAsk, action.ClassNoMatchingApproval, "not approved yet"), false)

	message, _ := encode(t, deferred)["systemMessage"].(string)
	for _, want := range []string{
		"no approval yet",
		`"Yes, and don't ask again"`,
		"not the text you typed",
	} {
		if !strings.Contains(message, want) {
			t.Errorf("systemMessage must mention %q:\n%s", want, message)
		}
	}
}

func TestMessagesWithoutAnAuditEventStillRead(t *testing.T) {
	// A dry-run evaluation has no event id; the messages must not say "event 0".
	dryRun := action.EvaluationResult{
		Decision: action.OutcomeBlock,
		Class:    action.HardRuleClass("R2"),
		Reason:   "deleting ~/Documents",
	}
	response, _ := MapPreToolUse(dryRun, false)
	decoded := encode(t, response)
	message, _ := decoded["systemMessage"].(string)

	if strings.Contains(message, "event") {
		t.Errorf("message = %q, want no event reference", message)
	}
	if !strings.Contains(message, "Intenter BLOCK") || !strings.Contains(message, "deleting ~/Documents") {
		t.Errorf("message = %q", message)
	}
}

func TestDaemonUnavailableMessage(t *testing.T) {
	response := DaemonUnavailableResponse()
	decoded := encode(t, response)

	message, _ := decoded["systemMessage"].(string)
	if !strings.Contains(message, "daemon unavailable") {
		t.Errorf("message = %q", message)
	}
	if hookOutput(decoded) != nil {
		t.Error("the notice must not carry a permission decision")
	}
}
