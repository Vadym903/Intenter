package claude

import (
	"fmt"
	"strings"

	"github.com/Vadym903/Intenter/internal/action"
)

// Response is what the adapter writes to stdout. A nil response means silence,
// which leaves Claude's own permission flow exactly as it would have been.
type Response struct {
	SystemMessage      string `json:"systemMessage,omitempty"`
	HookSpecificOutput any    `json:"hookSpecificOutput,omitempty"`
}

// preToolUseOutput is Claude's PreToolUse decision shape.
type preToolUseOutput struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision"`
	PermissionDecisionReason string `json:"permissionDecisionReason"`
}

// permissionRequestOutput is Claude's PermissionRequest decision shape.
type permissionRequestOutput struct {
	HookEventName string              `json:"hookEventName"`
	Decision      *permissionDecision `json:"decision,omitempty"`
}

type permissionDecision struct {
	Behavior  string `json:"behavior"`
	Message   string `json:"message,omitempty"`
	Interrupt bool   `json:"interrupt"`
}

// Claude's permission decision values. "defer" is deliberately absent: it is
// not a value Claude accepts, and silence is how Intenter defers.
const (
	decisionAllow = "allow"
	decisionDeny  = "deny"
	decisionAsk   = "ask"
)

// forcedAskClasses are the ASK classes where Intenter has something specific
// to say and forces Claude's dialog with its own reason. Every other ASK class
// means Intenter could not judge the command, so the native flow decides
// unchanged (§11.3).
var forcedAskClasses = map[action.DecisionClass]bool{
	action.ClassApprovalMismatch:           true,
	action.ClassPolicyRequiresConfirmation: true,
}

// announcedAllowClasses are the ALLOW classes worth a line in the transcript:
// the ones an approval of Intenter's own decided, where a silent allow would
// leave the user unable to tell a working gate from an absent one.
//
// The read-only baseline is deliberately absent. It answers `git status`, `ls`
// and `cat` many times in a session, and a notice for each would bury the lines
// that carry information.
var announcedAllowClasses = map[action.DecisionClass]bool{
	action.ClassApprovalMatch: true,
	action.ClassRuleImport:    true,
}

// MapPreToolUse turns a decision into the PreToolUse response, and reports what
// the adapter actually did for the audit log.
//
// In bypassPermissions mode a forced ask would become a denial, so only BLOCK
// is emitted there and everything else stays silent (§11.3, Appendix C).
func MapPreToolUse(result action.EvaluationResult, bypassing bool) (*Response, action.AdapterAction) {
	switch result.Decision {
	case action.OutcomeAllow:
		return &Response{
			SystemMessage: allowMessage(result),
			HookSpecificOutput: preToolUseOutput{
				HookEventName:            EventPreToolUse,
				PermissionDecision:       decisionAllow,
				PermissionDecisionReason: prefixed(result, false),
			},
		}, action.AdapterAllow

	case action.OutcomeBlock:
		message := prefixed(result, true)
		return &Response{
			SystemMessage: message,
			HookSpecificOutput: preToolUseOutput{
				HookEventName:            EventPreToolUse,
				PermissionDecision:       decisionDeny,
				PermissionDecisionReason: message,
			},
		}, action.AdapterDeny
	}

	if bypassing {
		return nil, action.AdapterDefer
	}

	if forcedAskClasses[result.Class] {
		return &Response{HookSpecificOutput: preToolUseOutput{
			HookEventName:            EventPreToolUse,
			PermissionDecision:       decisionAsk,
			PermissionDecisionReason: prefixed(result, false) + approveHint(result),
		}}, action.AdapterPrompt
	}

	// A never-approved but fully understood action defers to Claude's own
	// dialog, which is the only place that offers "don't ask again" — with a
	// summary so the user sees what the command actually resolves to.
	if result.Class == action.ClassNoMatchingApproval {
		return &Response{SystemMessage: noApprovalMessage(result)}, action.AdapterDefer
	}

	return nil, action.AdapterDefer
}

// MapPermissionRequest turns a decision into the PermissionRequest response.
// ASK produces nothing: the dialog Claude is already showing proceeds (§11.4).
func MapPermissionRequest(result action.EvaluationResult) (*Response, action.AdapterAction) {
	switch result.Decision {
	case action.OutcomeAllow:
		return &Response{HookSpecificOutput: permissionRequestOutput{
			HookEventName: EventPermissionRequest,
			Decision:      &permissionDecision{Behavior: decisionAllow},
		}}, action.AdapterAllow

	case action.OutcomeBlock:
		return &Response{HookSpecificOutput: permissionRequestOutput{
			HookEventName: EventPermissionRequest,
			Decision: &permissionDecision{
				Behavior:  decisionDeny,
				Message:   prefixed(result, true),
				Interrupt: false,
			},
		}}, action.AdapterDeny
	}
	return nil, action.AdapterPrompt
}

// DaemonUnavailableResponse is the one-line notice shown when the daemon cannot
// be reached. It is rate limited by the caller so a broken daemon does not spam
// a session (§11.3, §26).
func DaemonUnavailableResponse() *Response {
	return &Response{
		SystemMessage: "Intenter: daemon unavailable — native permissions in effect (intenter daemon status)",
	}
}

// prefixed renders "Intenter [event N]: <reason>", the form every message
// shares so a user can always find the matching history entry.
func prefixed(result action.EvaluationResult, blocked bool) string {
	label := "Intenter"
	if blocked {
		label = "Intenter BLOCK"
	}
	if result.AuditEventID != nil {
		label = fmt.Sprintf("%s [event %d]", label, *result.AuditEventID)
	}
	reason := strings.TrimSpace(result.Reason)
	if reason == "" {
		reason = "no reason was recorded"
	}
	return label + ": " + reason
}

// approveHint tells the user how to make the answer stick, when there is an
// event to approve.
func approveHint(result action.EvaluationResult) string {
	if result.AuditEventID == nil {
		return ""
	}
	return fmt.Sprintf(". To approve permanently: intenter approve %d", *result.AuditEventID)
}

// noApprovalMessage explains what the command resolves to before Claude's own
// dialog appears, and says what the dialog's persistent answer will mean here.
//
// Naming that answer is the only way Intenter can put itself next to it: the
// dialog's option labels are Claude's own, built from its permission
// suggestions, and no hook output reaches them (research.md R-10, B-3).
func noApprovalMessage(result action.EvaluationResult) string {
	message := eventLabel(result) + ": " + resolutionSummary(result) +
		"\n  no approval yet — Claude will ask." +
		"\n  \"Yes, and don't ask again\" lets Intenter approve what the command does," +
		" not the text you typed."
	if result.AuditEventID != nil {
		message += fmt.Sprintf("\n  Or approve it later: intenter approve %d", *result.AuditEventID)
	}
	return message
}

// allowMessage announces an allow that one of Intenter's approvals is
// responsible for, so a user sees the gate working instead of inferring it from
// silence. An empty string means say nothing.
func allowMessage(result action.EvaluationResult) string {
	if !announcedAllowClasses[result.Class] {
		return ""
	}

	message := eventLabel(result) + " ✓ allowed: " + resolutionSummary(result)
	if result.ApprovalID == nil {
		return message
	}

	origin := ""
	if result.Class == action.ClassRuleImport {
		origin = `, imported from Claude's "don't ask again"`
	}
	return message + fmt.Sprintf("\n  approval %d%s · intenter approval show %d",
		*result.ApprovalID, origin, *result.ApprovalID)
}

// SessionSummaryMessage is the notice shown when a session ends: what Intenter
// did across it, in one place.
//
// Claude shows a SessionEnd `systemMessage` to the user and not to itself,
// which is what makes this the right event for a report meant for a person.
// A session in which nothing was decided produces no message at all, because a
// summary of nothing is only noise at the moment a terminal is closing.
func SessionSummaryMessage(summary action.ActivitySummary) string {
	if summary.Empty() {
		return ""
	}

	message := fmt.Sprintf("Intenter this session: %s checked — %d allowed",
		plural(summary.Total, "command"), summary.Allowed)
	if summary.Asked > 0 {
		message += fmt.Sprintf(", %d asked", summary.Asked)
	}
	if summary.Blocked > 0 {
		message += fmt.Sprintf(", %d blocked", summary.Blocked)
	}
	message += "."

	// The one figure that answers "what did this save me": each is a dialog
	// that did not appear because the same question was already answered.
	if avoided := summary.PromptsAvoided(); avoided > 0 {
		message += fmt.Sprintf("\n  %s allowed by approvals you gave once — %s you did not have to answer.",
			plural(avoided, "command"), plural(avoided, "prompt"))
	}
	return message + "\n  intenter summary"
}

// plural renders a count with its noun, so a message never reads "1 commands".
func plural(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", count, noun)
}

// eventLabel names Intenter and, when there is one, the audit event the message
// belongs to. A dry run has no event, and must not claim "event 0".
func eventLabel(result action.EvaluationResult) string {
	if result.AuditEventID == nil {
		return "Intenter"
	}
	return fmt.Sprintf("Intenter [event %d]", *result.AuditEventID)
}

// resolutionSummary is the shortest useful description of what would run: the
// resolved chain when there is one, else the decision's reason.
func resolutionSummary(result action.EvaluationResult) string {
	for _, line := range result.Explanation {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	if reason := strings.TrimSpace(result.Reason); reason != "" {
		return reason
	}
	return "this command has no recorded summary"
}
