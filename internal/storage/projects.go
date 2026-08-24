package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Vadym903/Intenter/internal/action"
)

// ProjectRepo stores the workspaces Intenter has seen (data-model.md §2.1).
type ProjectRepo struct{ db *DB }

// Upsert records a project or refreshes its last_seen_at. The remote URL is
// informational only; approvals are keyed by project id (research R-19).
func (r *ProjectRepo) Upsert(ctx context.Context, project action.Project) error {
	now := project.LastSeenAt
	if now.IsZero() {
		now = time.Now()
	}
	first := project.FirstSeenAt
	if first.IsZero() {
		first = now
	}

	_, err := r.db.sql.ExecContext(ctx, `
		INSERT INTO projects (id, root_path, remote_url, first_seen_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			root_path    = excluded.root_path,
			remote_url   = COALESCE(excluded.remote_url, projects.remote_url),
			last_seen_at = excluded.last_seen_at`,
		project.ID, project.RootPath, nullString(project.RemoteURL), formatTime(first), formatTime(now))
	if err != nil {
		return fmt.Errorf("storage: upsert project %s: %w", project.ID, err)
	}
	return nil
}

// Get returns one project.
func (r *ProjectRepo) Get(ctx context.Context, id string) (action.Project, error) {
	var (
		project   action.Project
		remote    sql.NullString
		firstSeen string
		lastSeen  string
	)
	err := r.db.sql.QueryRowContext(ctx,
		`SELECT id, root_path, remote_url, first_seen_at, last_seen_at FROM projects WHERE id = ?`, id,
	).Scan(&project.ID, &project.RootPath, &remote, &firstSeen, &lastSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return action.Project{}, fmt.Errorf("project %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return action.Project{}, fmt.Errorf("storage: get project %s: %w", id, err)
	}

	project.RemoteURL = remote.String
	if project.FirstSeenAt, err = parseTime(firstSeen); err != nil {
		return action.Project{}, err
	}
	if project.LastSeenAt, err = parseTime(lastSeen); err != nil {
		return action.Project{}, err
	}
	return project, nil
}

// FindByRoot returns the project whose canonical root matches path.
func (r *ProjectRepo) FindByRoot(ctx context.Context, root string) (action.Project, error) {
	var id string
	err := r.db.sql.QueryRowContext(ctx, `SELECT id FROM projects WHERE root_path = ?`, root).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return action.Project{}, fmt.Errorf("project at %s: %w", root, ErrNotFound)
	}
	if err != nil {
		return action.Project{}, fmt.Errorf("storage: find project %s: %w", root, err)
	}
	return r.Get(ctx, id)
}

// List returns every known project, most recently seen first.
func (r *ProjectRepo) List(ctx context.Context) ([]action.Project, error) {
	rows, err := r.db.sql.QueryContext(ctx,
		`SELECT id, root_path, remote_url, first_seen_at, last_seen_at FROM projects ORDER BY last_seen_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("storage: list projects: %w", err)
	}
	defer rows.Close()

	var out []action.Project
	for rows.Next() {
		var (
			project   action.Project
			remote    sql.NullString
			firstSeen string
			lastSeen  string
		)
		if err := rows.Scan(&project.ID, &project.RootPath, &remote, &firstSeen, &lastSeen); err != nil {
			return nil, fmt.Errorf("storage: scan project: %w", err)
		}
		project.RemoteURL = remote.String
		if project.FirstSeenAt, err = parseTime(firstSeen); err != nil {
			return nil, err
		}
		if project.LastSeenAt, err = parseTime(lastSeen); err != nil {
			return nil, err
		}
		out = append(out, project)
	}
	return out, rows.Err()
}
