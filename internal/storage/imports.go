package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Vadym903/Intenter/internal/action"
)

// ImportRepo enforces that an agent permission rule becomes an approval at most
// once per project and raw command (§19.5, data-model.md §2.6).
type ImportRepo struct{ db *DB }

// InsertOnce records an import. It reports inserted=false when the unique
// (project, agent, rule_key, raw_command) row already exists — the once-only
// guarantee that stops a string rule from being re-imported after the resolved
// behavior changed (§19.5, INVARIANT I-5).
func (r *ImportRepo) InsertOnce(ctx context.Context, record action.RuleImport) (inserted bool, err error) {
	at := record.ImportedAt
	if at.IsZero() {
		at = time.Now()
	}
	result, err := r.db.sql.ExecContext(ctx, `
		INSERT OR IGNORE INTO agent_rule_imports
			(project_id, agent, rule_key, raw_command, approval_id, imported_at)
		VALUES (?,?,?,?,?,?)`,
		record.ProjectID, record.Agent, record.RuleKey, record.RawCommand, record.ApprovalID, formatTime(at))
	if err != nil {
		return false, fmt.Errorf("storage: insert rule import: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("storage: insert rule import: %w", err)
	}
	return affected > 0, nil
}

// Exists reports whether this rule was already imported for this project and
// raw command.
func (r *ImportRepo) Exists(ctx context.Context, projectID, agent, ruleKey, rawCommand string) (bool, error) {
	var id int64
	err := r.db.sql.QueryRowContext(ctx, `
		SELECT id FROM agent_rule_imports
		WHERE project_id = ? AND agent = ? AND rule_key = ? AND raw_command = ?`,
		projectID, agent, ruleKey, rawCommand).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("storage: check rule import: %w", err)
	}
	return true, nil
}

// AnyExists reports whether any of the given rule keys was already imported for
// this project and raw command. Import requires that none of the consenting
// rules was used before (§19.5).
func (r *ImportRepo) AnyExists(ctx context.Context, projectID, agent string, ruleKeys []string, rawCommand string) (bool, error) {
	for _, key := range ruleKeys {
		exists, err := r.Exists(ctx, projectID, agent, key, rawCommand)
		if err != nil {
			return false, err
		}
		if exists {
			return true, nil
		}
	}
	return false, nil
}

// ListByApproval returns the import records that created one approval.
func (r *ImportRepo) ListByApproval(ctx context.Context, approvalID int64) ([]action.RuleImport, error) {
	rows, err := r.db.sql.QueryContext(ctx, `
		SELECT id, project_id, agent, rule_key, raw_command, approval_id, imported_at
		FROM agent_rule_imports WHERE approval_id = ? ORDER BY id`, approvalID)
	if err != nil {
		return nil, fmt.Errorf("storage: list rule imports: %w", err)
	}
	defer rows.Close()

	var out []action.RuleImport
	for rows.Next() {
		var (
			record     action.RuleImport
			importedAt string
		)
		if err := rows.Scan(&record.ID, &record.ProjectID, &record.Agent, &record.RuleKey,
			&record.RawCommand, &record.ApprovalID, &importedAt); err != nil {
			return nil, fmt.Errorf("storage: scan rule import: %w", err)
		}
		if record.ImportedAt, err = parseTime(importedAt); err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}
