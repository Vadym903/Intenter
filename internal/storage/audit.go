package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Vadym903/Intenter/internal/action"
)

// AuditRepo persists the decision log (data-model.md §2.5, §24).
//
// The decision explanation is stored inside the `resolved` JSON blob
// (ResolvedAction.Explanation, §13.6) so that one row explains the decision
// without re-evaluation (I-17) and the schema stays exactly as contracted.
type AuditRepo struct{ db *DB }

// Insert writes one audit event and returns its id. It must complete before the
// evaluate response is returned; a failure makes the decision ASK/ENGINE_ERROR.
func (r *AuditRepo) Insert(ctx context.Context, event *action.AuditEvent) (int64, error) {
	at := event.At
	if at.IsZero() {
		at = time.Now()
		event.At = at
	}

	resolved := event.Resolved
	if resolved == nil {
		resolved = &action.ResolvedAction{
			RawCommand: event.RawCommand,
			Dialect:    event.Dialect,
			Status:     event.ResolutionStatus,
		}
	}
	if len(event.Explanation) > 0 {
		copied := *resolved
		copied.Explanation = event.Explanation
		resolved = &copied
	}
	resolvedJSON, err := encodeJSON(resolved)
	if err != nil {
		return 0, err
	}

	relatedJSON, err := encodeJSONNullable(event.RelatedApprovalIDs)
	if err != nil {
		return 0, err
	}
	mismatchJSON, err := encodeJSONNullable(event.MismatchReport)
	if err != nil {
		return 0, err
	}
	adapterContextJSON, err := encodeJSONNullable(event.AdapterContext)
	if err != nil {
		return 0, err
	}
	suggestionsJSON, err := encodeJSONNullable(event.PermissionSuggestions)
	if err != nil {
		return 0, err
	}

	result, err := r.db.sql.ExecContext(ctx, `
		INSERT INTO audit_events (
			at, agent, agent_version, session_id, tool_use_id, hook_event, project_id,
			cwd, tool, dialect, raw_command, resolved, resolution_status,
			decision, decision_class, reason, hard_rule,
			matched_approval_id, related_approval_ids, mismatch_report, imported_approval_id,
			adapter_action, adapter_context, prompt_shown, permission_suggestions,
			execution_status, execution_at, response_summary, engine_version, dry_run)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		formatTime(at), event.Agent, nullString(event.AgentVersion), nullString(event.SessionID),
		nullString(event.ToolUseID), nullString(event.HookEvent), nullString(event.ProjectID),
		event.Cwd, event.Tool, string(event.Dialect), event.RawCommand, resolvedJSON,
		string(event.ResolutionStatus), event.Decision.Wire(), string(event.DecisionClass),
		event.Reason, nullString(event.HardRule),
		nullInt64(event.MatchedApprovalID), relatedJSON, mismatchJSON, nullInt64(event.ImportedApprovalID),
		nullString(string(event.AdapterAction)), adapterContextJSON, boolToInt(event.PromptShown), suggestionsJSON,
		nullString(string(event.ExecutionStatus)), nullTime(event.ExecutionAt), nullString(event.ResponseSummary),
		event.EngineVersion, boolToInt(event.DryRun))
	if err != nil {
		return 0, fmt.Errorf("storage: insert audit event: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("storage: audit event id: %w", err)
	}
	event.ID = id
	return id, nil
}

// Get returns one full audit event.
func (r *AuditRepo) Get(ctx context.Context, id int64) (*action.AuditEvent, error) {
	row := r.db.sql.QueryRowContext(ctx, auditSelect+` WHERE id = ?`, id)
	event, err := scanAuditEvent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("audit event %d: %w", id, ErrNotFound)
	}
	return event, err
}

// List returns matching audit events, newest first (contracts/ipc-protocol.md
// `list_history`).
func (r *AuditRepo) List(ctx context.Context, filter action.AuditFilter) ([]action.AuditEventSummary, error) {
	query := auditSelect
	where := make([]string, 0, 5)
	args := make([]any, 0, 5)

	if filter.Decision != nil {
		where = append(where, "decision = ?")
		args = append(args, filter.Decision.Wire())
	}
	if filter.ProjectID != "" {
		where = append(where, "project_id = ?")
		args = append(args, filter.ProjectID)
	}
	if filter.SessionID != "" {
		where = append(where, "session_id = ?")
		args = append(args, filter.SessionID)
	}
	if filter.Since != nil {
		where = append(where, "at >= ?")
		args = append(args, formatTime(*filter.Since))
	}
	if !filter.IncludeDryRun {
		where = append(where, "dry_run = 0")
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	query += " ORDER BY at DESC, id DESC LIMIT ?"
	args = append(args, limit)

	rows, err := r.db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("storage: list audit events: %w", err)
	}
	defer rows.Close()

	var out []action.AuditEventSummary
	for rows.Next() {
		event, err := scanAuditEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, action.AuditEventSummary{
			ID:              event.ID,
			At:              event.At,
			Decision:        event.Decision,
			Class:           event.DecisionClass,
			RawCommand:      event.RawCommand,
			ResolvedSummary: event.ResolvedSummary(),
			Reason:          event.Reason,
			ApprovalID:      event.MatchedApprovalID,
			AdapterAction:   event.AdapterAction,
			ProjectID:       event.ProjectID,
			SessionID:       event.SessionID,
		})
	}
	return out, rows.Err()
}

// FindRecentBySessionCommand returns the newest non-dry-run event for a session
// and raw command that is not older than window. `record_prompt` correlates
// prompts this way (§24).
func (r *AuditRepo) FindRecentBySessionCommand(ctx context.Context, sessionID, rawCommand string, notBefore time.Time) (*action.AuditEvent, error) {
	row := r.db.sql.QueryRowContext(ctx, auditSelect+`
		WHERE session_id = ? AND raw_command = ? AND at >= ? AND dry_run = 0
		ORDER BY at DESC, id DESC LIMIT 1`,
		sessionID, rawCommand, formatTime(notBefore))
	event, err := scanAuditEvent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return event, err
}

// FindByToolUseID returns the evaluation recorded for one tool call.
// `report_execution` correlates by it (§24).
func (r *AuditRepo) FindByToolUseID(ctx context.Context, sessionID, toolUseID string) (*action.AuditEvent, error) {
	query := auditSelect + ` WHERE tool_use_id = ? AND dry_run = 0`
	args := []any{toolUseID}
	if sessionID != "" {
		query += ` AND session_id = ?`
		args = append(args, sessionID)
	}
	query += ` ORDER BY at DESC, id DESC LIMIT 1`

	row := r.db.sql.QueryRowContext(ctx, query, args...)
	event, err := scanAuditEvent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return event, err
}

// UpdatePrompt records that the agent showed its permission dialog, together
// with the verbatim suggestions it offered (§11.4).
func (r *AuditRepo) UpdatePrompt(ctx context.Context, id int64, suggestions []any) error {
	encoded, err := encodeJSONNullable(suggestions)
	if err != nil {
		return err
	}
	return r.update(ctx, id,
		`UPDATE audit_events SET prompt_shown = 1, permission_suggestions = COALESCE(?, permission_suggestions) WHERE id = ?`,
		encoded, id)
}

// UpdateExecution records what happened when the command ran (§11.5).
func (r *AuditRepo) UpdateExecution(ctx context.Context, id int64, status action.ExecutionStatus, at time.Time, summary string) error {
	if at.IsZero() {
		at = time.Now()
	}
	return r.update(ctx, id,
		`UPDATE audit_events SET execution_status = ?, execution_at = ?, response_summary = ? WHERE id = ?`,
		string(status), formatTime(at), nullString(summary), id)
}

// SetImportedApproval annotates the event whose consent produced an approval
// during report_execution (§19.5 path b).
func (r *AuditRepo) SetImportedApproval(ctx context.Context, id, approvalID int64) error {
	return r.update(ctx, id,
		`UPDATE audit_events SET imported_approval_id = ? WHERE id = ?`, approvalID, id)
}

// SetAdapterAction records what the adapter emitted after mapping the decision.
func (r *AuditRepo) SetAdapterAction(ctx context.Context, id int64, adapterAction action.AdapterAction) error {
	return r.update(ctx, id,
		`UPDATE audit_events SET adapter_action = ? WHERE id = ?`, string(adapterAction), id)
}

func (r *AuditRepo) update(ctx context.Context, id int64, query string, args ...any) error {
	result, err := r.db.sql.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("storage: update audit event %d: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("storage: update audit event %d: %w", id, err)
	}
	if affected == 0 {
		return fmt.Errorf("audit event %d: %w", id, ErrNotFound)
	}
	return nil
}

// CountByDecisionSince counts events per decision since a point in time, for
// `intenter status`.
func (r *AuditRepo) CountByDecisionSince(ctx context.Context, since time.Time) (map[action.DecisionOutcome]int, error) {
	rows, err := r.db.sql.QueryContext(ctx,
		`SELECT decision, COUNT(*) FROM audit_events WHERE at >= ? AND dry_run = 0 GROUP BY decision`,
		formatTime(since))
	if err != nil {
		return nil, fmt.Errorf("storage: count audit events: %w", err)
	}
	defer rows.Close()

	out := map[action.DecisionOutcome]int{
		action.OutcomeAllow: 0,
		action.OutcomeAsk:   0,
		action.OutcomeBlock: 0,
	}
	for rows.Next() {
		var decision string
		var count int
		if err := rows.Scan(&decision, &count); err != nil {
			return nil, fmt.Errorf("storage: scan audit count: %w", err)
		}
		if parsed, ok := action.ParseOutcome(decision); ok {
			out[parsed] = count
		}
	}
	return out, rows.Err()
}

// Summarize counts decisions over the rows a filter selects, for
// `intenter summary` and the session-end notice.
//
// It counts in SQL rather than listing and tallying in Go, because a session
// can hold thousands of events and the notice is written while the user is
// closing their terminal. Decision and Limit are ignored: a summary that
// honoured either would be a summary of a slice of the truth.
func (r *AuditRepo) Summarize(ctx context.Context, filter action.AuditFilter) (action.ActivitySummary, error) {
	where := make([]string, 0, 4)
	args := make([]any, 0, 4)

	if filter.ProjectID != "" {
		where = append(where, "project_id = ?")
		args = append(args, filter.ProjectID)
	}
	if filter.SessionID != "" {
		where = append(where, "session_id = ?")
		args = append(args, filter.SessionID)
	}
	if filter.Since != nil {
		where = append(where, "at >= ?")
		args = append(args, formatTime(*filter.Since))
	}
	if !filter.IncludeDryRun {
		where = append(where, "dry_run = 0")
	}

	query := `SELECT decision, decision_class, COUNT(*), MIN(at), MAX(at) FROM audit_events`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " GROUP BY decision, decision_class"

	rows, err := r.db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return action.ActivitySummary{}, fmt.Errorf("storage: summarize audit events: %w", err)
	}
	defer rows.Close()

	var summary action.ActivitySummary
	for rows.Next() {
		var decision, class, first, last string
		var count int
		if err := rows.Scan(&decision, &class, &count, &first, &last); err != nil {
			return action.ActivitySummary{}, fmt.Errorf("storage: scan audit summary: %w", err)
		}

		summary.Total += count
		switch outcome, _ := action.ParseOutcome(decision); outcome {
		case action.OutcomeAllow:
			summary.Allowed += count
			// Every allow is one or the other, and an unrecognized class counts
			// as an approval rather than baseline: the baseline is the narrow,
			// well-known case, and guessing the other way would overstate how
			// quiet Intenter is.
			if action.DecisionClass(class) == action.ClassPolicyReadonlyWorkspace {
				summary.AllowedBaseline += count
			} else {
				summary.AllowedByApproval += count
			}
		case action.OutcomeAsk:
			summary.Asked += count
		case action.OutcomeBlock:
			summary.Blocked += count
		}

		extendRange(&summary, first, last)
	}
	if err := rows.Err(); err != nil {
		return action.ActivitySummary{}, fmt.Errorf("storage: summarize audit events: %w", err)
	}
	return summary, nil
}

// extendRange widens the summary's time span to cover one group's bounds. A
// timestamp that will not parse is skipped rather than zeroing the span.
func extendRange(summary *action.ActivitySummary, first, last string) {
	if at, err := parseTime(first); err == nil {
		if summary.FirstAt == nil || at.Before(*summary.FirstAt) {
			summary.FirstAt = &at
		}
	}
	if at, err := parseTime(last); err == nil {
		if summary.LastAt == nil || at.After(*summary.LastAt) {
			summary.LastAt = &at
		}
	}
}

const auditSelect = `
	SELECT id, at, agent, agent_version, session_id, tool_use_id, hook_event, project_id,
	       cwd, tool, dialect, raw_command, resolved, resolution_status,
	       decision, decision_class, reason, hard_rule,
	       matched_approval_id, related_approval_ids, mismatch_report, imported_approval_id,
	       adapter_action, adapter_context, prompt_shown, permission_suggestions,
	       execution_status, execution_at, response_summary, engine_version, dry_run
	FROM audit_events`

func scanAuditEvent(row scanner) (*action.AuditEvent, error) {
	var (
		event           action.AuditEvent
		at              string
		agentVersion    sql.NullString
		sessionID       sql.NullString
		toolUseID       sql.NullString
		hookEvent       sql.NullString
		projectID       sql.NullString
		dialect         string
		resolvedJSON    string
		resolutionState string
		decision        string
		decisionClass   string
		hardRule        sql.NullString
		matchedApproval sql.NullInt64
		relatedIDs      sql.NullString
		mismatchReport  sql.NullString
		importedID      sql.NullInt64
		adapterAction   sql.NullString
		adapterContext  sql.NullString
		promptShown     int
		suggestions     sql.NullString
		executionStatus sql.NullString
		executionAt     sql.NullString
		responseSummary sql.NullString
		dryRun          int
	)

	if err := row.Scan(
		&event.ID, &at, &event.Agent, &agentVersion, &sessionID, &toolUseID, &hookEvent, &projectID,
		&event.Cwd, &event.Tool, &dialect, &event.RawCommand, &resolvedJSON, &resolutionState,
		&decision, &decisionClass, &event.Reason, &hardRule,
		&matchedApproval, &relatedIDs, &mismatchReport, &importedID,
		&adapterAction, &adapterContext, &promptShown, &suggestions,
		&executionStatus, &executionAt, &responseSummary, &event.EngineVersion, &dryRun,
	); err != nil {
		return nil, err
	}

	var err error
	if event.At, err = parseTime(at); err != nil {
		return nil, err
	}
	event.AgentVersion = agentVersion.String
	event.SessionID = sessionID.String
	event.ToolUseID = toolUseID.String
	event.HookEvent = hookEvent.String
	event.ProjectID = projectID.String
	event.Dialect = action.Dialect(dialect)
	event.ResolutionStatus = action.ResolutionStatus(resolutionState)
	if parsed, ok := action.ParseOutcome(decision); ok {
		event.Decision = parsed
	} else {
		return nil, fmt.Errorf("storage: audit event %d has an invalid decision %q", event.ID, decision)
	}
	event.DecisionClass = action.DecisionClass(decisionClass)
	event.HardRule = hardRule.String
	event.MatchedApprovalID = int64Ptr(matchedApproval)
	event.ImportedApprovalID = int64Ptr(importedID)
	event.AdapterAction = action.AdapterAction(adapterAction.String)
	event.PromptShown = promptShown != 0
	event.ExecutionStatus = action.ExecutionStatus(executionStatus.String)
	event.ResponseSummary = responseSummary.String
	event.DryRun = dryRun != 0

	var resolved action.ResolvedAction
	if err := decodeJSON(sql.NullString{String: resolvedJSON, Valid: true}, &resolved); err != nil {
		return nil, err
	}
	event.Resolved = &resolved
	event.Explanation = resolved.Explanation

	if err := decodeJSON(relatedIDs, &event.RelatedApprovalIDs); err != nil {
		return nil, err
	}
	if err := decodeJSON(mismatchReport, &event.MismatchReport); err != nil {
		return nil, err
	}
	if err := decodeJSON(adapterContext, &event.AdapterContext); err != nil {
		return nil, err
	}
	if err := decodeJSON(suggestions, &event.PermissionSuggestions); err != nil {
		return nil, err
	}
	if event.ExecutionAt, err = timePtr(executionAt); err != nil {
		return nil, err
	}
	return &event, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
