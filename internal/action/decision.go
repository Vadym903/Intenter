package action

// Decision is the policy engine's verdict on one action
// (PROTOTYPE_SPEC.md §13.7, §18).
type Decision struct {
	Outcome DecisionOutcome `json:"outcome"`
	Class   DecisionClass   `json:"class"`
	Reason  string          `json:"reason"`
	// ApprovalID is set when an approval decided the outcome.
	ApprovalID *int64 `json:"approval_id,omitempty"`
	// HardRule is the rule id (R1…R12) when a hard rule decided the outcome.
	HardRule string `json:"hard_rule,omitempty"`
	// MismatchReports explain why related approvals did not match.
	MismatchReports []MismatchReport `json:"mismatch_reports,omitempty"`
	EngineVersion   int              `json:"engine_version"`
}

// MismatchReport lists, for one related approval, why it no longer covers the
// current action (PROTOTYPE_SPEC.md §20.4, §21).
type MismatchReport struct {
	ApprovalID  int64    `json:"approval_id"`
	Differences []string `json:"differences"`
}

// EvaluationResult is what the daemon returns for `evaluate`: the decision plus
// everything the adapter and the audit log need (contracts/ipc-protocol.md).
type EvaluationResult struct {
	AuditEventID       *int64           `json:"audit_event_id,omitempty"`
	Decision           DecisionOutcome  `json:"decision"`
	Class              DecisionClass    `json:"class"`
	Reason             string           `json:"reason"`
	ApprovalID         *int64           `json:"approval_id,omitempty"`
	HardRule           string           `json:"hard_rule,omitempty"`
	MismatchReports    []MismatchReport `json:"mismatch_reports,omitempty"`
	ResolutionStatus   ResolutionStatus `json:"resolution_status"`
	Explanation        []string         `json:"explanation,omitempty"`
	UserMessage        string           `json:"user_message,omitempty"`
	ImportedApprovalID *int64           `json:"imported_approval_id,omitempty"`
}

// RelatedApprovalIDs returns the approval ids named by the mismatch reports.
func (r *EvaluationResult) RelatedApprovalIDs() []int64 {
	out := make([]int64, 0, len(r.MismatchReports))
	for _, m := range r.MismatchReports {
		out = append(out, m.ApprovalID)
	}
	return out
}

// IsAllow reports whether the result permits the action.
func (r *EvaluationResult) IsAllow() bool { return r.Decision == OutcomeAllow }

// Ref returns a pointer to an int64, for the optional id fields above.
func Ref(v int64) *int64 { return &v }
