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

// ApprovalRepo persists semantic approvals and their conditions
// (data-model.md §2.2, §2.3).
type ApprovalRepo struct{ db *DB }

// ApprovalFilter selects approvals for listing and for matching.
type ApprovalFilter struct {
	ProjectID string
	// IncludeInactive also returns DISABLED and REVOKED approvals.
	IncludeInactive bool
	Limit           int
}

// Insert stores an approval together with its fingerprint conditions and the
// `created` approval event, in one transaction. The assigned id is returned.
func (r *ApprovalRepo) Insert(ctx context.Context, approval *action.Approval) (int64, error) {
	semanticOps, err := encodeJSON(approval.SemanticOps)
	if err != nil {
		return 0, err
	}
	envelope, err := encodeJSON(approval.Envelope)
	if err != nil {
		return 0, err
	}
	network, err := encodeJSON(approval.Network)
	if err != nil {
		return 0, err
	}
	var targets sql.NullString
	if approval.Kind == action.ApprovalExact {
		encoded, err := encodeJSON(approval.Targets)
		if err != nil {
			return 0, err
		}
		targets = sql.NullString{String: encoded, Valid: true}
	}

	createdAt := approval.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
		approval.CreatedAt = createdAt
	}

	var id int64
	err = r.db.WithTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			INSERT INTO approvals (
				project_id, kind, semantic_ops, envelope, targets, network, engine_version,
				origin, origin_ref, created_from_event_id, created_from_raw_command,
				created_by_agent, state, note, created_at, last_used_at, use_count,
				disabled_at, revoked_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			approval.ProjectID, string(approval.Kind), semanticOps, envelope, targets, network,
			approval.EngineVersion, string(approval.Origin), nullString(approval.OriginRef),
			nullInt64(approval.CreatedFromEventID), approval.CreatedFromRawCommand,
			approval.CreatedByAgent, string(approval.State), nullString(approval.Note),
			formatTime(createdAt), nullTime(approval.LastUsedAt), approval.UseCount,
			nullTime(approval.DisabledAt), nullTime(approval.RevokedAt))
		if err != nil {
			return fmt.Errorf("storage: insert approval: %w", err)
		}
		if id, err = result.LastInsertId(); err != nil {
			return fmt.Errorf("storage: approval id: %w", err)
		}

		for _, condition := range approval.Conditions {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO approval_conditions (approval_id, kind, key, value) VALUES (?,?,?,?)`,
				id, condition.Kind, condition.Key, condition.Value); err != nil {
				return fmt.Errorf("storage: insert approval condition %s: %w", condition.Key, err)
			}
		}

		if _, err := tx.ExecContext(ctx,
			`INSERT INTO approval_events (approval_id, event_type, audit_event_id, at, details) VALUES (?,?,?,?,?)`,
			id, string(action.ApprovalEventCreated), nullInt64(approval.CreatedFromEventID),
			formatTime(createdAt), nullString("")); err != nil {
			return fmt.Errorf("storage: record approval creation: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	approval.ID = id
	return id, nil
}

// Get returns one approval with its conditions.
func (r *ApprovalRepo) Get(ctx context.Context, id int64) (*action.Approval, error) {
	row := r.db.sql.QueryRowContext(ctx, approvalSelect+` WHERE id = ?`, id)
	approval, err := scanApproval(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("approval %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	conditions, err := (&ConditionRepo{db: r.db}).ListByApproval(ctx, id)
	if err != nil {
		return nil, err
	}
	approval.Conditions = conditions
	return approval, nil
}

// List returns approvals in matching order: EXACT before SEMANTIC, then
// ascending id, so the first match is deterministic (§20.1).
func (r *ApprovalRepo) List(ctx context.Context, filter ApprovalFilter) ([]action.Approval, error) {
	query := approvalSelect
	args := make([]any, 0, 3)
	where := make([]string, 0, 2)

	if filter.ProjectID != "" {
		where = append(where, "project_id = ?")
		args = append(args, filter.ProjectID)
	}
	if !filter.IncludeInactive {
		where = append(where, "state = ?")
		args = append(args, string(action.ApprovalActive))
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY CASE kind WHEN 'EXACT' THEN 0 ELSE 1 END, id ASC"
	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
	}

	rows, err := r.db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("storage: list approvals: %w", err)
	}
	defer rows.Close()

	var out []action.Approval
	var ids []int64
	for rows.Next() {
		approval, err := scanApproval(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *approval)
		ids = append(ids, approval.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	conditions, err := (&ConditionRepo{db: r.db}).ListByApprovals(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Conditions = conditions[out[i].ID]
	}
	return out, nil
}

// SetState moves an approval between ACTIVE, DISABLED and REVOKED and records
// the transition as an approval event. REVOKED is terminal (I-15).
func (r *ApprovalRepo) SetState(ctx context.Context, id int64, state action.ApprovalState, at time.Time) error {
	if at.IsZero() {
		at = time.Now()
	}
	return r.db.WithTx(ctx, func(tx *sql.Tx) error {
		var current string
		err := tx.QueryRowContext(ctx, `SELECT state FROM approvals WHERE id = ?`, id).Scan(&current)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("approval %d: %w", id, ErrNotFound)
		}
		if err != nil {
			return fmt.Errorf("storage: read approval state: %w", err)
		}
		if action.ApprovalState(current) == action.ApprovalRevoked {
			return fmt.Errorf("storage: approval %d is revoked; revocation is permanent", id)
		}

		var eventType action.ApprovalEventType
		var column string
		switch state {
		case action.ApprovalActive:
			eventType, column = action.ApprovalEventEnabled, "disabled_at"
		case action.ApprovalDisabled:
			eventType, column = action.ApprovalEventDisabled, "disabled_at"
		case action.ApprovalRevoked:
			eventType, column = action.ApprovalEventRevoked, "revoked_at"
		default:
			return fmt.Errorf("storage: unknown approval state %q", state)
		}

		timestamp := nullString(formatTime(at))
		if state == action.ApprovalActive {
			timestamp = sql.NullString{}
		}
		if _, err := tx.ExecContext(ctx,
			fmt.Sprintf(`UPDATE approvals SET state = ?, %s = ? WHERE id = ?`, column),
			string(state), timestamp, id); err != nil {
			return fmt.Errorf("storage: set approval state: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO approval_events (approval_id, event_type, at) VALUES (?,?,?)`,
			id, string(eventType), formatTime(at)); err != nil {
			return fmt.Errorf("storage: record approval state change: %w", err)
		}
		return nil
	})
}

// RecordUse increments the usage counter and stamps last_used_at (§19.4).
func (r *ApprovalRepo) RecordUse(ctx context.Context, id int64, at time.Time) error {
	if at.IsZero() {
		at = time.Now()
	}
	result, err := r.db.sql.ExecContext(ctx,
		`UPDATE approvals SET use_count = use_count + 1, last_used_at = ? WHERE id = ?`,
		formatTime(at), id)
	if err != nil {
		return fmt.Errorf("storage: record approval use: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("storage: record approval use: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("approval %d: %w", id, ErrNotFound)
	}
	return nil
}

// CountByState returns how many approvals are in each state, for `status`.
func (r *ApprovalRepo) CountByState(ctx context.Context) (map[action.ApprovalState]int, error) {
	rows, err := r.db.sql.QueryContext(ctx, `SELECT state, COUNT(*) FROM approvals GROUP BY state`)
	if err != nil {
		return nil, fmt.Errorf("storage: count approvals: %w", err)
	}
	defer rows.Close()

	out := map[action.ApprovalState]int{
		action.ApprovalActive:   0,
		action.ApprovalDisabled: 0,
		action.ApprovalRevoked:  0,
	}
	for rows.Next() {
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			return nil, fmt.Errorf("storage: scan approval count: %w", err)
		}
		out[action.ApprovalState(state)] = count
	}
	return out, rows.Err()
}

const approvalSelect = `
	SELECT id, project_id, kind, semantic_ops, envelope, targets, network, engine_version,
	       origin, origin_ref, created_from_event_id, created_from_raw_command,
	       created_by_agent, state, note, created_at, last_used_at, use_count,
	       disabled_at, revoked_at
	FROM approvals`

// scanner abstracts *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanApproval(row scanner) (*action.Approval, error) {
	var (
		approval    action.Approval
		kind        string
		semanticOps string
		envelope    string
		targets     sql.NullString
		network     string
		origin      string
		originRef   sql.NullString
		eventID     sql.NullInt64
		state       string
		note        sql.NullString
		createdAt   string
		lastUsedAt  sql.NullString
		disabledAt  sql.NullString
		revokedAt   sql.NullString
	)
	if err := row.Scan(
		&approval.ID, &approval.ProjectID, &kind, &semanticOps, &envelope, &targets, &network,
		&approval.EngineVersion, &origin, &originRef, &eventID, &approval.CreatedFromRawCommand,
		&approval.CreatedByAgent, &state, &note, &createdAt, &lastUsedAt, &approval.UseCount,
		&disabledAt, &revokedAt,
	); err != nil {
		return nil, err
	}

	approval.Kind = action.ApprovalKind(kind)
	approval.Origin = action.ApprovalOrigin(origin)
	approval.OriginRef = originRef.String
	approval.CreatedFromEventID = int64Ptr(eventID)
	approval.State = action.ApprovalState(state)
	approval.Note = note.String

	if err := decodeJSON(sql.NullString{String: semanticOps, Valid: true}, &approval.SemanticOps); err != nil {
		return nil, err
	}
	if err := decodeJSON(sql.NullString{String: envelope, Valid: true}, &approval.Envelope); err != nil {
		return nil, err
	}
	if err := decodeJSON(targets, &approval.Targets); err != nil {
		return nil, err
	}
	if err := decodeJSON(sql.NullString{String: network, Valid: true}, &approval.Network); err != nil {
		return nil, err
	}

	var err error
	if approval.CreatedAt, err = parseTime(createdAt); err != nil {
		return nil, err
	}
	if approval.LastUsedAt, err = timePtr(lastUsedAt); err != nil {
		return nil, err
	}
	if approval.DisabledAt, err = timePtr(disabledAt); err != nil {
		return nil, err
	}
	if approval.RevokedAt, err = timePtr(revokedAt); err != nil {
		return nil, err
	}
	return &approval, nil
}
