// Package audit persists one row per evaluated action, complete enough that a
// user can later ask why something was allowed or blocked without the engine
// re-evaluating anything (PROTOTYPE_SPEC.md §24, INVARIANT I-17).
package audit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/storage"
)

// PromptCorrelationWindow is how far back a PermissionRequest looks for the
// evaluate row it belongs to, matched on session and raw command (§24).
const PromptCorrelationWindow = 60 * time.Second

// Recorder writes audit events.
type Recorder struct {
	store *storage.Store
	// now is injectable so stored timestamps are deterministic in tests.
	now func() time.Time
}

// NewRecorder builds a recorder over a store.
func NewRecorder(store *storage.Store) *Recorder {
	return &Recorder{store: store, now: time.Now}
}

// Evaluation is everything one evaluate produced, ready to persist.
type Evaluation struct {
	Request     action.ActionRequest
	Context     *action.Context
	Resolved    *action.ResolvedAction
	Decision    action.Decision
	Explanation []string
	// DryRun marks the setup self-test, which must leave no trace (§12.2).
	DryRun bool
}

// RecordEvaluation writes exactly one row for an evaluation, before the
// response is returned. A write failure is reported to the caller, which turns
// it into ASK/ENGINE_ERROR rather than allowing on an unrecorded decision
// (§24, I-3).
//
// A dry run writes nothing and reports no id, so the self-test cannot pollute
// the history a user reads.
func (r *Recorder) RecordEvaluation(ctx context.Context, evaluation Evaluation) (*int64, error) {
	if evaluation.DryRun {
		return nil, nil
	}

	event := r.buildEvent(evaluation)
	id, err := r.store.Audit.Insert(ctx, event)
	if err != nil {
		return nil, fmt.Errorf("audit: record evaluation: %w", err)
	}
	return &id, nil
}

// buildEvent projects an evaluation onto the persisted row. Everything the
// explanation needs is stored, so `history show` never re-resolves (I-17).
func (r *Recorder) buildEvent(evaluation Evaluation) *action.AuditEvent {
	request := evaluation.Request

	at := request.ReceivedAt
	if at.IsZero() {
		at = r.now()
	}

	event := &action.AuditEvent{
		At:             at,
		Agent:          request.Agent,
		AgentVersion:   request.AgentVersion,
		SessionID:      request.SessionID,
		ToolUseID:      request.ToolUseID,
		HookEvent:      hookEventOf(request),
		Cwd:            request.Cwd,
		Tool:           request.Tool,
		Dialect:        request.Dialect,
		RawCommand:     request.RawCommand,
		Decision:       evaluation.Decision.Outcome,
		DecisionClass:  evaluation.Decision.Class,
		Reason:         evaluation.Decision.Reason,
		HardRule:       evaluation.Decision.HardRule,
		MismatchReport: evaluation.Decision.MismatchReports,
		AdapterContext: request.AdapterContext,
		EngineVersion:  evaluation.Decision.EngineVersion,
		Explanation:    evaluation.Explanation,
	}

	if evaluation.Context != nil {
		event.ProjectID = evaluation.Context.ProjectID
	}
	if evaluation.Resolved != nil {
		event.Resolved = evaluation.Resolved
		event.ResolutionStatus = evaluation.Resolved.Status
		if event.ProjectID == "" {
			event.ProjectID = evaluation.Resolved.ProjectID
		}
	}
	if evaluation.Decision.ApprovalID != nil && evaluation.Decision.Outcome == action.OutcomeAllow {
		event.MatchedApprovalID = evaluation.Decision.ApprovalID
	}
	for _, report := range evaluation.Decision.MismatchReports {
		event.RelatedApprovalIDs = append(event.RelatedApprovalIDs, report.ApprovalID)
	}
	return event
}

// hookEventOf reports which agent hook produced a request, for the audit row.
func hookEventOf(request action.ActionRequest) string {
	if request.AdapterContext == nil {
		return ""
	}
	if value, ok := request.AdapterContext["hook_event"].(string); ok {
		return value
	}
	return ""
}

// RecordPrompt notes that the agent showed its own permission dialog for a
// command. It updates the evaluate row for the same session and command within
// the correlation window, or creates a row when no evaluation preceded it
// (§24).
func (r *Recorder) RecordPrompt(ctx context.Context, request action.ActionRequest, suggestions []any) (int64, error) {
	notBefore := r.now().Add(-PromptCorrelationWindow)

	// No correlated evaluation is an ordinary case: the agent may prompt for a
	// command Intenter never saw, or the window may have passed.
	existing, err := r.store.Audit.FindRecentBySessionCommand(ctx, request.SessionID, request.RawCommand, notBefore)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return 0, fmt.Errorf("audit: correlate prompt: %w", err)
	}
	if err == nil && existing != nil {
		if err := r.store.Audit.UpdatePrompt(ctx, existing.ID, suggestions); err != nil {
			return 0, fmt.Errorf("audit: record prompt: %w", err)
		}
		return existing.ID, nil
	}

	event := &action.AuditEvent{
		At:                    r.now(),
		Agent:                 request.Agent,
		AgentVersion:          request.AgentVersion,
		SessionID:             request.SessionID,
		ToolUseID:             request.ToolUseID,
		HookEvent:             "PermissionRequest",
		Cwd:                   request.Cwd,
		Tool:                  request.Tool,
		Dialect:               request.Dialect,
		RawCommand:            request.RawCommand,
		Decision:              action.OutcomeAsk,
		DecisionClass:         action.ClassNoMatchingApproval,
		Reason:                "the agent asked for permission without a preceding evaluation",
		PromptShown:           true,
		PermissionSuggestions: suggestions,
	}
	id, err := r.store.Audit.Insert(ctx, event)
	if err != nil {
		return 0, fmt.Errorf("audit: record prompt: %w", err)
	}
	return id, nil
}

// Execution is what the agent reported about a command it ran.
type Execution struct {
	SessionID string
	ToolUseID string
	Status    action.ExecutionStatus
	// Summary is the bounded response summary, stored only when configured.
	Summary string
	At      time.Time
}

// RecordExecution attaches the outcome of a command to the row that decided it,
// found by tool_use_id (§24). A report for an unknown tool use is not an error:
// the daemon may have been restarted between the decision and the execution.
func (r *Recorder) RecordExecution(ctx context.Context, execution Execution) (*action.AuditEvent, error) {
	event, err := r.store.Audit.FindByToolUseID(ctx, execution.SessionID, execution.ToolUseID)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("audit: correlate execution: %w", err)
	}
	if event == nil {
		return nil, nil
	}

	at := execution.At
	if at.IsZero() {
		at = r.now()
	}
	if err := r.store.Audit.UpdateExecution(ctx, event.ID, execution.Status, at, execution.Summary); err != nil {
		return nil, fmt.Errorf("audit: record execution: %w", err)
	}

	event.ExecutionStatus = execution.Status
	event.ExecutionAt = &at
	event.ResponseSummary = execution.Summary
	return event, nil
}

// RecordAdapterAction stores what the adapter actually emitted to the agent.
// The core never decides it; it is recorded so a decision and its delivery can
// be told apart afterwards.
func (r *Recorder) RecordAdapterAction(ctx context.Context, eventID int64, adapterAction action.AdapterAction) error {
	if err := r.store.Audit.SetAdapterAction(ctx, eventID, adapterAction); err != nil {
		return fmt.Errorf("audit: record adapter action: %w", err)
	}
	return nil
}

// RecordImportedApproval notes the approval a late consent import produced for
// an event that had already been deferred (§19.5 path b).
func (r *Recorder) RecordImportedApproval(ctx context.Context, eventID, approvalID int64) error {
	if err := r.store.Audit.SetImportedApproval(ctx, eventID, approvalID); err != nil {
		return fmt.Errorf("audit: record imported approval: %w", err)
	}
	return nil
}
