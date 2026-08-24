package ipc

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/version"
)

// Method names of protocol v1 (contracts/ipc-protocol.md §Methods).
const (
	MethodPing                = "ping"
	MethodEvaluate            = "evaluate"
	MethodRecordPrompt        = "record_prompt"
	MethodRecordAdapterAction = "record_adapter_action"
	MethodReportExecution     = "report_execution"
	MethodAgentConfigChanged  = "agent_config_changed"
	MethodListApprovals       = "list_approvals"
	MethodGetApproval         = "get_approval"
	MethodSetApprovalState    = "set_approval_state"
	MethodCreateApproval      = "create_approval"
	MethodListHistory         = "list_history"
	MethodGetHistoryEvent     = "get_history_event"
	MethodSummarize           = "summarize"
	MethodStatus              = "status"
	MethodShutdown            = "shutdown"
)

// Error codes of protocol v1.
const (
	CodeUnsupportedProtocol = "UNSUPPORTED_PROTOCOL"
	CodeBadRequest          = "BAD_REQUEST"
	CodeNotFound            = "NOT_FOUND"
	CodeInternal            = "INTERNAL"
	CodeBusy                = "BUSY"
)

// Request is the wire envelope a client sends.
type Request struct {
	ProtocolVersion int    `json:"protocol_version"`
	RequestID       string `json:"request_id"`
	Method          string `json:"method"`
	// ClientVersion is the release the caller was built from. It is optional and
	// additive — the protocol stays v1 — and exists so a daemon left running
	// across an upgrade can notice that it is the older half of the pair and
	// step aside (contracts/release-artifacts.md).
	ClientVersion string          `json:"client_version,omitempty"`
	Params        json.RawMessage `json:"params,omitempty"`
}

// Response is the wire envelope the daemon returns.
type Response struct {
	ProtocolVersion int             `json:"protocol_version"`
	RequestID       string          `json:"request_id"`
	OK              bool            `json:"ok"`
	Result          json.RawMessage `json:"result,omitempty"`
	Error           *Error          `json:"error,omitempty"`
}

// Error is a structured protocol error.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Errorf builds a protocol error.
func Errorf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// NewRequest builds a request with the current protocol version and encoded
// parameters.
func NewRequest(requestID, method string, params any) (*Request, error) {
	req := &Request{
		ProtocolVersion: version.ProtocolVersion,
		RequestID:       requestID,
		Method:          method,
		ClientVersion:   version.Version,
	}
	if params != nil {
		encoded, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("ipc: encode params for %s: %w", method, err)
		}
		req.Params = encoded
	}
	return req, nil
}

// DecodeParams unmarshals the request parameters into target.
func (r *Request) DecodeParams(target any) error {
	if len(r.Params) == 0 {
		return nil
	}
	if err := json.Unmarshal(r.Params, target); err != nil {
		return fmt.Errorf("invalid params for %s: %w", r.Method, err)
	}
	return nil
}

// NewResponse builds a success response with an encoded result.
func NewResponse(requestID string, result any) (*Response, error) {
	resp := &Response{
		ProtocolVersion: version.ProtocolVersion,
		RequestID:       requestID,
		OK:              true,
	}
	if result != nil {
		encoded, err := json.Marshal(result)
		if err != nil {
			return nil, fmt.Errorf("ipc: encode result: %w", err)
		}
		resp.Result = encoded
	}
	return resp, nil
}

// NewErrorResponse builds a failure response.
func NewErrorResponse(requestID, code, message string) *Response {
	return &Response{
		ProtocolVersion: version.ProtocolVersion,
		RequestID:       requestID,
		OK:              false,
		Error:           &Error{Code: code, Message: message},
	}
}

// DecodeResult unmarshals a success result into target.
func (r *Response) DecodeResult(target any) error {
	if r.Error != nil {
		return r.Error
	}
	if len(r.Result) == 0 || target == nil {
		return nil
	}
	if err := json.Unmarshal(r.Result, target); err != nil {
		return fmt.Errorf("ipc: decode result: %w", err)
	}
	return nil
}

// SupportedProtocol reports whether the daemon speaks this protocol version.
func SupportedProtocol(v int) bool { return v == version.ProtocolVersion }

// PingResult answers `ping`.
type PingResult struct {
	Version         string `json:"version"`
	EngineVersion   int    `json:"engine_version"`
	ProtocolVersion int    `json:"protocol_version"`
	UptimeS         int64  `json:"uptime_s"`
	PID             int    `json:"pid"`
}

// EvaluateParams carries one action for evaluation.
type EvaluateParams struct {
	DryRun  bool                 `json:"dry_run"`
	Request action.ActionRequest `json:"request"`
}

// EvaluateResult is the evaluation outcome (action.EvaluationResult).
type EvaluateResult = action.EvaluationResult

// RecordPromptParams reports that the agent showed its permission dialog.
type RecordPromptParams struct {
	Agent       string `json:"agent"`
	SessionID   string `json:"session_id"`
	Tool        string `json:"tool"`
	RawCommand  string `json:"raw_command"`
	Suggestions []any  `json:"suggestions,omitempty"`
}

// RecordPromptResult names the audit event that was annotated.
type RecordPromptResult struct {
	AuditEventID *int64 `json:"audit_event_id,omitempty"`
}

// RecordAdapterActionParams reports what the adapter emitted to the agent after
// mapping a decision (§11.3).
//
// It is separate from the decision because they are separate facts: Intenter
// deciding ASK and the agent being shown a prompt are not the same event, and
// §11.3 deliberately defers some asks to the agent's own flow. Without this the
// audit log cannot tell the two apart.
type RecordAdapterActionParams struct {
	AuditEventID int64  `json:"audit_event_id"`
	Agent        string `json:"agent"`
	Action       string `json:"action"`
}

// ReportExecutionParams reports what happened when a command ran.
type ReportExecutionParams struct {
	Agent           string                 `json:"agent"`
	SessionID       string                 `json:"session_id"`
	ToolUseID       string                 `json:"tool_use_id"`
	Status          action.ExecutionStatus `json:"status"`
	ResponseSummary string                 `json:"response_summary,omitempty"`
	AgentConsent    *action.AgentConsent   `json:"agent_consent,omitempty"`
}

// ReportExecutionResult names an approval created by consent import.
type ReportExecutionResult struct {
	ImportedApprovalID *int64 `json:"imported_approval_id,omitempty"`
}

// AgentConfigChangedParams notifies the daemon that agent settings changed.
type AgentConfigChangedParams struct {
	Agent    string `json:"agent"`
	Source   string `json:"source"`
	FilePath string `json:"file_path"`
}

// ListApprovalsParams selects approvals to list.
type ListApprovalsParams struct {
	ProjectID       *string `json:"project_id,omitempty"`
	IncludeInactive bool    `json:"include_inactive,omitempty"`
	Limit           int     `json:"limit,omitempty"`
}

// ApprovalSummary is one row of `list_approvals`.
type ApprovalSummary struct {
	ID          int64                 `json:"id"`
	Kind        action.ApprovalKind   `json:"kind"`
	SemanticOps []action.SemanticOp   `json:"semantic_ops"`
	Summary     string                `json:"summary"`
	ProjectRoot string                `json:"project_root"`
	ProjectID   string                `json:"project_id"`
	UseCount    int64                 `json:"use_count"`
	LastUsedAt  *time.Time            `json:"last_used_at,omitempty"`
	State       action.ApprovalState  `json:"state"`
	Origin      action.ApprovalOrigin `json:"origin"`
	CreatedAt   time.Time             `json:"created_at"`
}

// GetApprovalParams selects one approval.
type GetApprovalParams struct {
	ID int64 `json:"id"`
}

// ApprovalDetail is the full approval record with its recent events.
type ApprovalDetail struct {
	Approval     action.Approval        `json:"approval"`
	ProjectRoot  string                 `json:"project_root,omitempty"`
	RecentEvents []action.ApprovalEvent `json:"recent_events,omitempty"`
}

// SetApprovalStateParams changes an approval's state.
type SetApprovalStateParams struct {
	ID    int64                `json:"id"`
	State action.ApprovalState `json:"state"`
}

// CreateApprovalParams creates an approval from an evaluated audit event.
type CreateApprovalParams struct {
	AuditEventID int64               `json:"audit_event_id"`
	Kind         action.ApprovalKind `json:"kind"`
	Note         string              `json:"note,omitempty"`
}

// ListHistoryParams filters the decision log.
type ListHistoryParams struct {
	Decision  *string    `json:"decision,omitempty"`
	ProjectID *string    `json:"project_id,omitempty"`
	SessionID *string    `json:"session_id,omitempty"`
	Since     *time.Time `json:"since,omitempty"`
	Limit     int        `json:"limit,omitempty"`
}

// GetHistoryEventParams selects one audit event.
type GetHistoryEventParams struct {
	ID int64 `json:"id"`
}

// SummarizeParams selects the slice of the decision log to count. All fields
// are optional; with none set the summary covers everything ever recorded.
type SummarizeParams struct {
	ProjectID *string    `json:"project_id,omitempty"`
	SessionID *string    `json:"session_id,omitempty"`
	Since     *time.Time `json:"since,omitempty"`
}

// SummarizeResult answers `summarize`.
type SummarizeResult = action.ActivitySummary

// StatusResult answers `status` (contracts/ipc-protocol.md).
type StatusResult struct {
	Daemon      StatusDaemon           `json:"daemon"`
	Counts      StatusCounts           `json:"counts"`
	Integration map[string]StatusAgent `json:"integration,omitempty"`
}

// StatusDaemon describes the running daemon.
type StatusDaemon struct {
	Version     string `json:"version"`
	UptimeS     int64  `json:"uptime_s"`
	Endpoint    string `json:"endpoint"`
	DBPath      string `json:"db_path"`
	ServiceMode string `json:"service_mode"`
	PID         int    `json:"pid"`
}

// StatusCounts holds the summary counters.
type StatusCounts struct {
	Approvals map[string]int `json:"approvals"`
	Events24h map[string]int `json:"events_24h"`
}

// StatusAgent describes one agent integration.
type StatusAgent struct {
	HooksInstalled bool   `json:"hooks_installed"`
	SettingsPath   string `json:"settings_path,omitempty"`
	AgentVersion   string `json:"claude_version,omitempty"`
}
