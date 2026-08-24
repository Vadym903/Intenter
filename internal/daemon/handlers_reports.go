package daemon

import (
	"context"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/audit"
	"github.com/Vadym903/Intenter/internal/ipc"
	"github.com/Vadym903/Intenter/internal/policy"
)

// handleRecordPrompt answers `record_prompt`: the agent showed its own
// permission dialog for a command (§11.4, §24).
func (d *Daemon) handleRecordPrompt(ctx context.Context, req *ipc.Request) (any, error) {
	params, err := decodeParams[ipc.RecordPromptParams](req)
	if err != nil {
		return nil, err
	}

	id, err := d.auditor.RecordPrompt(ctxOrBackground(ctx), action.ActionRequest{
		Agent:      params.Agent,
		SessionID:  params.SessionID,
		Tool:       params.Tool,
		RawCommand: params.RawCommand,
	}, params.Suggestions)
	if err != nil {
		return nil, storageError(err)
	}
	return ipc.RecordPromptResult{AuditEventID: &id}, nil
}

// handleRecordAdapterAction answers `record_adapter_action`: what the adapter
// emitted to the agent after mapping the decision (§11.3, §23.2).
//
// The decision and its delivery are different facts. A never-approved but
// understood action is ASK, and §11.3 has the adapter *defer* it to the agent's
// own dialog; an approval mismatch is also ASK, and there the adapter *forces*
// the prompt. Recording only the decision leaves a user asking why one "ask"
// produced a prompt and the other produced nothing.
func (d *Daemon) handleRecordAdapterAction(ctx context.Context, req *ipc.Request) (any, error) {
	params, err := decodeParams[ipc.RecordAdapterActionParams](req)
	if err != nil {
		return nil, err
	}

	adapterAction, ok := action.ParseAdapterAction(params.Action)
	if !ok {
		return nil, ipc.Errorf(ipc.CodeBadRequest, "unknown adapter action %q", params.Action)
	}
	if params.AuditEventID <= 0 {
		return nil, ipc.Errorf(ipc.CodeBadRequest, "audit_event_id is required")
	}

	if err := d.auditor.RecordAdapterAction(ctxOrBackground(ctx), params.AuditEventID, adapterAction); err != nil {
		return nil, storageError(err)
	}
	return struct{}{}, nil
}

// handleReportExecution answers `report_execution`: what happened when a
// command ran, plus the second path of consent import (§19.5 path b).
//
// This is how "yes, and don't ask again" in the agent's own dialog becomes an
// Intenter approval: Intenter deferred, the user consented in the native
// flow, and the agent reports that consent alongside the execution.
func (d *Daemon) handleReportExecution(ctx context.Context, req *ipc.Request) (any, error) {
	params, err := decodeParams[ipc.ReportExecutionParams](req)
	if err != nil {
		return nil, err
	}
	ctx = ctxOrBackground(ctx)

	event, err := d.auditor.RecordExecution(ctx, audit.Execution{
		SessionID: params.SessionID,
		ToolUseID: params.ToolUseID,
		Status:    params.Status,
		Summary:   d.responseSummary(params.ResponseSummary),
		At:        d.now(),
	})
	if err != nil {
		return nil, storageError(err)
	}
	if event == nil || !params.AgentConsent.Usable() {
		return ipc.ReportExecutionResult{}, nil
	}

	imported, err := d.importConsent(ctx, event, params.AgentConsent)
	if err != nil {
		// The command already ran; failing the report would only lose the
		// execution record we just wrote.
		d.logger.Warn("daemon: could not import the agent's consent", "error", err)
		return ipc.ReportExecutionResult{}, nil
	}
	return ipc.ReportExecutionResult{ImportedApprovalID: imported}, nil
}

// importConsent replays the recorded decision and, if Intenter deferred it
// without a matching approval, converts the agent's persistent consent into one
// validated approval.
func (d *Daemon) importConsent(ctx context.Context, event *action.AuditEvent, consent *action.AgentConsent) (*int64, error) {
	if event.Resolved == nil {
		return nil, nil
	}

	resolveContext := d.contexts.Build(event.Cwd, "")
	in := policy.Input{
		Action:  event.Resolved,
		Context: resolveContext.Action,
		Config:  d.config,
		Rules:   d.platform.PathRules(),
		Agent:   event.Agent,
	}
	decision := action.Decision{Outcome: event.Decision, Class: event.DecisionClass}

	outcome, err := d.importer.ImportForExecution(in, consent, decision)
	if err != nil {
		return nil, err
	}
	if !outcome.Matched {
		return nil, nil
	}

	if err := d.auditor.RecordImportedApproval(ctx, event.ID, outcome.ApprovalID); err != nil {
		return nil, err
	}
	// The next occurrence must see the new approval rather than a cached ask.
	d.cache.reset()

	return action.Ref(outcome.ApprovalID), nil
}

// handleAgentConfigChanged answers `agent_config_changed`: the agent's settings
// were edited, so anything cached from them is stale.
func (d *Daemon) handleAgentConfigChanged(ctx context.Context, req *ipc.Request) (any, error) {
	params, err := decodeParams[ipc.AgentConfigChangedParams](req)
	if err != nil {
		return nil, err
	}
	d.logger.Info("daemon: agent configuration changed",
		"agent", params.Agent, "source", params.Source, "file", params.FilePath)
	d.cache.reset()
	return struct{}{}, nil
}

// responseSummary applies the configured retention: a command's output can
// contain anything, so it is only stored when the user asked for it (§12.6).
func (d *Daemon) responseSummary(summary string) string {
	if !d.config.Audit.StoreResponseSummary {
		return ""
	}
	return summary
}
