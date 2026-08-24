package storage

import (
	"context"
	"fmt"
	"strings"

	"github.com/Vadym903/Intenter/internal/action"
)

// ConditionRepo stores the fingerprint conditions an approval depends on
// (data-model.md §2.3).
type ConditionRepo struct{ db *DB }

// ListByApproval returns the conditions of one approval, sorted by key.
func (r *ConditionRepo) ListByApproval(ctx context.Context, approvalID int64) ([]action.ApprovalCondition, error) {
	rows, err := r.db.sql.QueryContext(ctx,
		`SELECT kind, key, value FROM approval_conditions WHERE approval_id = ? ORDER BY key`, approvalID)
	if err != nil {
		return nil, fmt.Errorf("storage: list approval conditions: %w", err)
	}
	defer rows.Close()

	var out []action.ApprovalCondition
	for rows.Next() {
		var condition action.ApprovalCondition
		if err := rows.Scan(&condition.Kind, &condition.Key, &condition.Value); err != nil {
			return nil, fmt.Errorf("storage: scan approval condition: %w", err)
		}
		out = append(out, condition)
	}
	return out, rows.Err()
}

// ListByApprovals loads the conditions of many approvals in one query, keyed by
// approval id. Matching loads every candidate at once, so this avoids N+1.
func (r *ConditionRepo) ListByApprovals(ctx context.Context, approvalIDs []int64) (map[int64][]action.ApprovalCondition, error) {
	out := make(map[int64][]action.ApprovalCondition, len(approvalIDs))
	if len(approvalIDs) == 0 {
		return out, nil
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(approvalIDs)), ",")
	args := make([]any, 0, len(approvalIDs))
	for _, id := range approvalIDs {
		args = append(args, id)
	}

	rows, err := r.db.sql.QueryContext(ctx,
		`SELECT approval_id, kind, key, value FROM approval_conditions
		 WHERE approval_id IN (`+placeholders+`) ORDER BY approval_id, key`, args...)
	if err != nil {
		return nil, fmt.Errorf("storage: list approval conditions: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			approvalID int64
			condition  action.ApprovalCondition
		)
		if err := rows.Scan(&approvalID, &condition.Kind, &condition.Key, &condition.Value); err != nil {
			return nil, fmt.Errorf("storage: scan approval condition: %w", err)
		}
		out[approvalID] = append(out[approvalID], condition)
	}
	return out, rows.Err()
}

// FindApprovalIDsByFingerprintKeys returns approvals that share at least one
// fingerprint key with the given set. Mismatch reports use it to find related
// approvals (§20.4).
func (r *ConditionRepo) FindApprovalIDsByFingerprintKeys(ctx context.Context, projectID string, keys []string) ([]int64, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(keys)), ",")
	args := make([]any, 0, len(keys)+1)
	args = append(args, projectID)
	for _, key := range keys {
		args = append(args, key)
	}

	rows, err := r.db.sql.QueryContext(ctx, `
		SELECT DISTINCT c.approval_id
		FROM approval_conditions c
		JOIN approvals a ON a.id = c.approval_id
		WHERE a.project_id = ? AND c.key IN (`+placeholders+`)
		ORDER BY c.approval_id`, args...)
	if err != nil {
		return nil, fmt.Errorf("storage: find approvals by fingerprint key: %w", err)
	}
	defer rows.Close()

	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("storage: scan approval id: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
