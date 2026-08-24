package daemon

import (
	"context"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/ipc"
)

// handleListHistory answers `list_history`: the decision log, newest first.
func (d *Daemon) handleListHistory(ctx context.Context, req *ipc.Request) (any, error) {
	params, err := decodeParams[ipc.ListHistoryParams](req)
	if err != nil {
		return nil, err
	}

	filter := action.AuditFilter{Limit: params.Limit, Since: params.Since}
	if params.ProjectID != nil {
		filter.ProjectID = *params.ProjectID
	}
	if params.SessionID != nil {
		filter.SessionID = *params.SessionID
	}
	if params.Decision != nil {
		outcome, ok := action.ParseOutcome(*params.Decision)
		if !ok {
			return nil, ipc.Errorf(ipc.CodeBadRequest, "unknown decision %q", *params.Decision)
		}
		filter.Decision = &outcome
	}

	events, err := d.store.Audit.List(ctxOrBackground(ctx), filter)
	if err != nil {
		return nil, storageError(err)
	}
	if events == nil {
		// An empty log is an empty list, not a null. `list_approvals` already
		// answers that way, and a script that pipes one into the other should
		// not have to know which is which.
		events = []action.AuditEventSummary{}
	}
	return events, nil
}

// handleSummarize answers `summarize`: what Intenter decided over a session, a
// project or a period, counted in the database.
func (d *Daemon) handleSummarize(ctx context.Context, req *ipc.Request) (any, error) {
	params, err := decodeParams[ipc.SummarizeParams](req)
	if err != nil {
		return nil, err
	}

	filter := action.AuditFilter{Since: params.Since}
	if params.ProjectID != nil {
		filter.ProjectID = *params.ProjectID
	}
	if params.SessionID != nil {
		filter.SessionID = *params.SessionID
	}

	summary, err := d.store.Audit.Summarize(ctxOrBackground(ctx), filter)
	if err != nil {
		return nil, storageError(err)
	}
	return summary, nil
}

// handleGetHistoryEvent answers `get_history_event` with the full stored row.
//
// Everything the explanation needs was persisted at decision time, so this
// answers "why was that allowed or blocked?" without re-evaluating anything
// (INVARIANT I-17).
func (d *Daemon) handleGetHistoryEvent(ctx context.Context, req *ipc.Request) (any, error) {
	params, err := decodeParams[ipc.GetHistoryEventParams](req)
	if err != nil {
		return nil, err
	}
	event, err := d.store.Audit.Get(ctxOrBackground(ctx), params.ID)
	if err != nil {
		return nil, storageError(err)
	}
	return event, nil
}
