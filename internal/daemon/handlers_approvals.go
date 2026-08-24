package daemon

import (
	"context"
	"errors"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/approval"
	"github.com/Vadym903/Intenter/internal/ipc"
	"github.com/Vadym903/Intenter/internal/policy"
	"github.com/Vadym903/Intenter/internal/storage"
)

// recentApprovalEvents is how much of an approval's history `get_approval`
// returns.
const recentApprovalEvents = 10

// handleListApprovals answers `list_approvals`: what is trusted, and where.
func (d *Daemon) handleListApprovals(ctx context.Context, req *ipc.Request) (any, error) {
	params, err := decodeParams[ipc.ListApprovalsParams](req)
	if err != nil {
		return nil, err
	}
	ctx = ctxOrBackground(ctx)

	filter := storage.ApprovalFilter{IncludeInactive: params.IncludeInactive, Limit: params.Limit}
	if params.ProjectID != nil {
		filter.ProjectID = *params.ProjectID
	}

	approvals, err := d.store.Approvals.List(ctx, filter)
	if err != nil {
		return nil, storageError(err)
	}

	roots := d.projectRoots(ctx)
	summaries := make([]ipc.ApprovalSummary, 0, len(approvals))
	for i := range approvals {
		record := &approvals[i]
		summaries = append(summaries, ipc.ApprovalSummary{
			ID:          record.ID,
			Kind:        record.Kind,
			SemanticOps: record.SemanticOps,
			Summary:     record.Summary(),
			ProjectRoot: roots[record.ProjectID],
			ProjectID:   record.ProjectID,
			UseCount:    record.UseCount,
			LastUsedAt:  record.LastUsedAt,
			State:       record.State,
			Origin:      record.Origin,
			CreatedAt:   record.CreatedAt,
		})
	}
	return summaries, nil
}

// handleGetApproval answers `get_approval` with the full record, including the
// fingerprints it depends on and its recent history.
func (d *Daemon) handleGetApproval(ctx context.Context, req *ipc.Request) (any, error) {
	params, err := decodeParams[ipc.GetApprovalParams](req)
	if err != nil {
		return nil, err
	}
	ctx = ctxOrBackground(ctx)

	record, err := d.store.Approvals.Get(ctx, params.ID)
	if err != nil {
		return nil, storageError(err)
	}
	record.Conditions, err = d.store.Conditions.ListByApproval(ctx, record.ID)
	if err != nil {
		return nil, storageError(err)
	}
	events, err := d.store.ApprovalEvents.ListByApproval(ctx, record.ID, recentApprovalEvents)
	if err != nil {
		return nil, storageError(err)
	}

	return ipc.ApprovalDetail{
		Approval:     *record,
		ProjectRoot:  d.projectRoots(ctx)[record.ProjectID],
		RecentEvents: events,
	}, nil
}

// handleSetApprovalState answers `set_approval_state`. Revocation is permanent;
// the record itself is never deleted (I-15).
func (d *Daemon) handleSetApprovalState(ctx context.Context, req *ipc.Request) (any, error) {
	params, err := decodeParams[ipc.SetApprovalStateParams](req)
	if err != nil {
		return nil, err
	}
	switch params.State {
	case action.ApprovalActive, action.ApprovalDisabled, action.ApprovalRevoked:
	default:
		return nil, ipc.Errorf(ipc.CodeBadRequest, "unknown approval state %q", params.State)
	}
	ctx = ctxOrBackground(ctx)

	if err := d.store.Approvals.SetState(ctx, params.ID, params.State, d.now()); err != nil {
		return nil, storageError(err)
	}
	// Trust changed, so the cached evaluations that assumed the old trust must
	// not be reused.
	d.cache.reset()

	record, err := d.store.Approvals.Get(ctx, params.ID)
	if err != nil {
		return nil, storageError(err)
	}
	return ipc.ApprovalDetail{Approval: *record}, nil
}

// handleCreateApproval answers `create_approval`: turn an evaluated event into
// remembered trust (§19.3 path 1).
//
// The approval is built from the resolved action as it was at evaluation time,
// so what is remembered is exactly what the user was shown.
func (d *Daemon) handleCreateApproval(ctx context.Context, req *ipc.Request) (any, error) {
	params, err := decodeParams[ipc.CreateApprovalParams](req)
	if err != nil {
		return nil, err
	}
	ctx = ctxOrBackground(ctx)

	event, err := d.store.Audit.Get(ctx, params.AuditEventID)
	if err != nil {
		return nil, storageError(err)
	}
	if event.Resolved == nil {
		return nil, ipc.Errorf(ipc.CodeBadRequest,
			"event %d has no resolved action to approve", params.AuditEventID)
	}

	kind := params.Kind
	if kind == "" {
		kind = action.ApprovalExact
	}
	if kind != action.ApprovalExact && kind != action.ApprovalSemantic {
		return nil, ipc.Errorf(ipc.CodeBadRequest, "unknown approval kind %q", kind)
	}

	// The safety floor is re-checked against the current workspace, so an
	// event can never be approved into something the rules would stop today.
	resolveContext := d.contexts.Build(event.Cwd, "")
	created, err := d.approvals.Create(ctx, approval.CreateRequest{
		Action: event.Resolved,
		Policy: policy.Input{
			Action:  event.Resolved,
			Context: resolveContext.Action,
			Config:  d.config,
			Rules:   d.platform.PathRules(),
			Agent:   event.Agent,
		},
		Kind:          kind,
		Origin:        action.OriginCLI,
		OriginRef:     "",
		SourceEventID: &event.ID,
		Agent:         event.Agent,
		Note:          params.Note,
		EngineVersion: d.engineVersion,
		Now:           d.now(),
	})
	if err != nil {
		if errors.Is(err, approval.ErrNotApprovable) {
			return nil, ipc.Errorf(ipc.CodeBadRequest, "%s", err.Error())
		}
		return nil, storageError(err)
	}

	// New trust invalidates cached "not approved" answers.
	d.cache.reset()

	return ipc.ApprovalDetail{
		Approval:    *created,
		ProjectRoot: d.projectRoots(ctx)[created.ProjectID],
	}, nil
}

// projectRoots indexes known project roots by id, for display. A failure is not
// worth failing a listing over: the id alone still identifies the project.
func (d *Daemon) projectRoots(ctx context.Context) map[string]string {
	projects, err := d.store.Projects.List(ctx)
	if err != nil {
		d.logger.Warn("daemon: could not list projects", "error", err)
		return map[string]string{}
	}
	roots := make(map[string]string, len(projects))
	for _, project := range projects {
		roots[project.ID] = project.RootPath
	}
	return roots
}
