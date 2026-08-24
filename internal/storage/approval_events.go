package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Vadym903/Intenter/internal/action"
)

// ApprovalEventRepo stores the history of each approval: creation, matches,
// mismatches and state changes (data-model.md §2.4, §19.4).
type ApprovalEventRepo struct{ db *DB }

// Insert appends one approval event.
func (r *ApprovalEventRepo) Insert(ctx context.Context, event action.ApprovalEvent) (int64, error) {
	at := event.At
	if at.IsZero() {
		at = time.Now()
	}
	details, err := encodeJSONNullable(event.Details)
	if err != nil {
		return 0, err
	}

	result, err := r.db.sql.ExecContext(ctx,
		`INSERT INTO approval_events (approval_id, event_type, audit_event_id, at, details) VALUES (?,?,?,?,?)`,
		event.ApprovalID, string(event.Type), nullInt64(event.AuditEventID), formatTime(at), details)
	if err != nil {
		return 0, fmt.Errorf("storage: insert approval event: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("storage: approval event id: %w", err)
	}
	return id, nil
}

// ListByApproval returns the most recent events of one approval, newest first.
func (r *ApprovalEventRepo) ListByApproval(ctx context.Context, approvalID int64, limit int) ([]action.ApprovalEvent, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := r.db.sql.QueryContext(ctx,
		`SELECT id, approval_id, event_type, audit_event_id, at, details
		 FROM approval_events WHERE approval_id = ? ORDER BY at DESC, id DESC LIMIT ?`,
		approvalID, limit)
	if err != nil {
		return nil, fmt.Errorf("storage: list approval events: %w", err)
	}
	defer rows.Close()

	var out []action.ApprovalEvent
	for rows.Next() {
		var (
			event     action.ApprovalEvent
			eventType string
			auditID   sql.NullInt64
			at        string
			details   sql.NullString
		)
		if err := rows.Scan(&event.ID, &event.ApprovalID, &eventType, &auditID, &at, &details); err != nil {
			return nil, fmt.Errorf("storage: scan approval event: %w", err)
		}
		event.Type = action.ApprovalEventType(eventType)
		event.AuditEventID = int64Ptr(auditID)
		if event.At, err = parseTime(at); err != nil {
			return nil, err
		}
		if err := decodeJSON(details, &event.Details); err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	return out, rows.Err()
}
