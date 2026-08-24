package action

import "time"

// AuditEvent is the persisted record of one evaluation together with everything
// that happened around it. Its stored data alone must explain the decision
// (PROTOTYPE_SPEC.md §24, INVARIANT I-17, data-model.md §2.5).
type AuditEvent struct {
	ID           int64     `json:"id"`
	At           time.Time `json:"at"`
	Agent        string    `json:"agent"`
	AgentVersion string    `json:"agent_version,omitempty"`
	SessionID    string    `json:"session_id,omitempty"`
	ToolUseID    string    `json:"tool_use_id,omitempty"`
	HookEvent    string    `json:"hook_event,omitempty"`
	ProjectID    string    `json:"project_id,omitempty"`
	Cwd          string    `json:"cwd"`
	Tool         string    `json:"tool"`
	Dialect      Dialect   `json:"dialect"`
	RawCommand   string    `json:"raw_command"`

	// Resolved is the full ResolvedAction as evaluated.
	Resolved         *ResolvedAction  `json:"resolved,omitempty"`
	ResolutionStatus ResolutionStatus `json:"resolution_status"`
	Decision         DecisionOutcome  `json:"decision"`
	DecisionClass    DecisionClass    `json:"decision_class"`
	Reason           string           `json:"reason"`
	HardRule         string           `json:"hard_rule,omitempty"`

	MatchedApprovalID  *int64           `json:"matched_approval_id,omitempty"`
	RelatedApprovalIDs []int64          `json:"related_approval_ids,omitempty"`
	MismatchReport     []MismatchReport `json:"mismatch_report,omitempty"`
	ImportedApprovalID *int64           `json:"imported_approval_id,omitempty"`

	AdapterAction  AdapterAction  `json:"adapter_action,omitempty"`
	AdapterContext map[string]any `json:"adapter_context,omitempty"`

	PromptShown           bool  `json:"prompt_shown"`
	PermissionSuggestions []any `json:"permission_suggestions,omitempty"`

	ExecutionStatus ExecutionStatus `json:"execution_status,omitempty"`
	ExecutionAt     *time.Time      `json:"execution_at,omitempty"`
	ResponseSummary string          `json:"response_summary,omitempty"`

	EngineVersion int  `json:"engine_version"`
	DryRun        bool `json:"dry_run"`

	// Explanation is persisted with the event so `history show` can reproduce
	// the decision without re-evaluating (I-17).
	Explanation []string `json:"explanation,omitempty"`
}

// ResolvedSummary renders the resolved chain for list output, e.g.
// "npm run cleanup -> rm -rf ./dist".
func (e *AuditEvent) ResolvedSummary() string {
	if e.Resolved == nil {
		return ""
	}
	for _, cmd := range e.Resolved.Commands {
		if len(cmd.ResolvedFrom) > 1 {
			return cmd.ResolvedFrom[len(cmd.ResolvedFrom)-1]
		}
	}
	parts := make([]string, 0, len(e.Resolved.Commands))
	for _, cmd := range e.Resolved.Commands {
		if cmd.RawText != "" {
			parts = append(parts, cmd.RawText)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += "; " + p
	}
	return out
}

// AuditFilter selects audit events for `list_history` (contracts/ipc-protocol.md).
type AuditFilter struct {
	Decision  *DecisionOutcome `json:"decision,omitempty"`
	ProjectID string           `json:"project_id,omitempty"`
	SessionID string           `json:"session_id,omitempty"`
	Since     *time.Time       `json:"since,omitempty"`
	Limit     int              `json:"limit,omitempty"`
	// IncludeDryRun is false by default: dry-run rows are diagnostics.
	IncludeDryRun bool `json:"include_dry_run,omitempty"`
}

// ActivitySummary counts what Intenter decided over a slice of the audit log:
// one session, one project, or a period. It is what `intenter summary` reports
// and what the session-end notice is built from.
//
// The allow total is split because the two halves answer different questions. A
// baseline allow is a read Intenter let through; an approval allow is a prompt
// you answered once and did not have to answer again. Only the second is a
// prompt that would otherwise have been asked, so only the second can be
// counted as one avoided.
type ActivitySummary struct {
	Total int `json:"total"`
	// Allowed is every ALLOW: the two halves below together.
	Allowed           int `json:"allowed"`
	AllowedByApproval int `json:"allowed_by_approval"`
	AllowedBaseline   int `json:"allowed_baseline"`
	Asked             int `json:"asked"`
	Blocked           int `json:"blocked"`
	// FirstAt and LastAt bound the counted rows, and are nil when Total is 0.
	FirstAt *time.Time `json:"first_at,omitempty"`
	LastAt  *time.Time `json:"last_at,omitempty"`
}

// PromptsAvoided is the number of times a stored approval answered for the
// user. It is the one honest "what did this save me" figure the audit log
// supports: each is a dialog that did not appear because the same question had
// already been answered once.
func (s ActivitySummary) PromptsAvoided() int { return s.AllowedByApproval }

// Empty reports whether anything was decided at all.
func (s ActivitySummary) Empty() bool { return s.Total == 0 }

// AuditEventSummary is the row shape of `list_history`.
type AuditEventSummary struct {
	ID              int64           `json:"id"`
	At              time.Time       `json:"at"`
	Decision        DecisionOutcome `json:"decision"`
	Class           DecisionClass   `json:"class"`
	RawCommand      string          `json:"raw_command"`
	ResolvedSummary string          `json:"resolved_summary,omitempty"`
	Reason          string          `json:"reason"`
	ApprovalID      *int64          `json:"approval_id,omitempty"`
	AdapterAction   AdapterAction   `json:"adapter_action,omitempty"`
	ProjectID       string          `json:"project_id,omitempty"`
	SessionID       string          `json:"session_id,omitempty"`
}
