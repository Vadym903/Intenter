package policy

import (
	"fmt"

	"github.com/Vadym903/Intenter/internal/action"
)

// Explain renders the full explanation of a decision (§18.4). It is built from
// templates only, so the same action always explains itself the same way and
// the audit log can reproduce it from the stored row (I-17).
//
// The order is what a reader needs: what the command resolves to, what it would
// touch, which safety rules noticed it, and finally what decided the outcome.
func Explain(in Input, decision action.Decision, findings Findings) []string {
	explanation := make([]string, 0, 8)

	if in.Action != nil {
		explanation = append(explanation, in.Action.Explanation...)
		if len(in.Action.Unsupported) > 0 {
			explanation = append(explanation, "not interpreted: "+joinList(in.Action.Unsupported))
		}
	}

	explanation = append(explanation, findings.Reasons()...)
	explanation = append(explanation, decisionLine(decision))

	for _, report := range decision.MismatchReports {
		for _, difference := range report.Differences {
			explanation = append(explanation, fmt.Sprintf("approval %d: %s", report.ApprovalID, difference))
		}
	}
	return explanation
}

// decisionLine states what decided the outcome, in one line.
func decisionLine(decision action.Decision) string {
	switch {
	case decision.HardRule != "" && decision.Outcome == action.OutcomeBlock:
		return fmt.Sprintf("blocked by safety rule %s: %s", decision.HardRule, decision.Reason)
	case decision.HardRule != "":
		return fmt.Sprintf("confirmation required by safety rule %s: %s", decision.HardRule, decision.Reason)
	case decision.ApprovalID != nil && decision.Class == action.ClassRuleImport:
		return fmt.Sprintf("allowed by imported approval %d: %s", *decision.ApprovalID, decision.Reason)
	case decision.ApprovalID != nil:
		return fmt.Sprintf("allowed by approval %d", *decision.ApprovalID)
	case decision.Outcome == action.OutcomeAllow:
		return "allowed: " + decision.Reason
	default:
		return "asking: " + decision.Reason
	}
}

// UserMessage is the short line the adapter shows the user alongside the
// agent's own prompt (§11.3). It never contains anything the reason does not.
func UserMessage(decision action.Decision) string {
	switch decision.Outcome {
	case action.OutcomeBlock:
		return "Intenter blocked this command: " + decision.Reason
	case action.OutcomeAllow:
		return ""
	default:
		return "Intenter: " + decision.Reason
	}
}

func joinList(values []string) string {
	out := ""
	for i, value := range values {
		if i > 0 {
			out += ", "
		}
		out += value
	}
	return out
}
