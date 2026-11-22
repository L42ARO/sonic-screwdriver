package store

import (
	"context"
	"database/sql"
)

// ProjectSummary is one project with counts, for the dashboard.
type ProjectSummary struct {
	Name       string
	CreatedAt  string
	Operations int
	Programs   int
	Jobs       int
	LastJobAt  string
	CostUSD    float64
}

const projectSummaryQuery = `
	SELECT p.name, p.created_at,
		(SELECT COUNT(*) FROM operations o WHERE o.project_id = p.id),
		(SELECT COUNT(*) FROM project_programs pp WHERE pp.project_id = p.id),
		(SELECT COUNT(*) FROM jobs j WHERE j.user_id = p.user_id AND j.project = p.name),
		(SELECT MAX(j.created_at) FROM jobs j WHERE j.user_id = p.user_id AND j.project = p.name),
		(SELECT COALESCE(SUM(u.cost_usd), 0) FROM model_usage u JOIN jobs j ON j.id = u.job_id
			WHERE j.user_id = p.user_id AND j.project = p.name)
	FROM projects p WHERE p.user_id = ?`

func scanProjectSummary(row *sql.Rows) (*ProjectSummary, error) {
	var p ProjectSummary
	var last sql.NullString
	if err := row.Scan(&p.Name, &p.CreatedAt, &p.Operations, &p.Programs, &p.Jobs, &last, &p.CostUSD); err != nil {
		return nil, err
	}
	p.LastJobAt = last.String
	return &p, nil
}

// ListProjects returns every project the user has, by name.
func (s *Store) ListProjects(ctx context.Context, userID int64) ([]*ProjectSummary, error) {
	rows, err := s.db.QueryContext(ctx, projectSummaryQuery+` ORDER BY p.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*ProjectSummary{}
	for rows.Next() {
		p, err := scanProjectSummary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
