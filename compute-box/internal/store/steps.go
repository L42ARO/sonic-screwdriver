package store

import (
	"context"
	"database/sql"

	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/utils"
)

// Step is one screen action a job took.
type Step struct {
	JobID       string `json:"-"`
	N           int    `json:"n"`
	Description string `json:"description"`
	Action      string `json:"-"`
	OK          bool   `json:"-"`
	Error       string `json:"-"`
	Screenshot  string `json:"-"` // file name under the job's screenshot folder
	Note        string `json:"-"` // the model's words around this action, if any
	CreatedAt   string `json:"-"`
}

func (s *Store) AddStep(ctx context.Context, st Step) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO job_steps (job_id, n, description, action, ok, error, screenshot, note, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		st.JobID, st.N, st.Description, st.Action, st.OK, nullable(st.Error), nullable(st.Screenshot),
		nullable(st.Note), utils.Now())
	return err
}

func (s *Store) ListSteps(ctx context.Context, jobID string) ([]Step, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT n, description, action, ok, error, screenshot, note, created_at
		FROM job_steps WHERE job_id = ? ORDER BY n`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Step
	for rows.Next() {
		st := Step{JobID: jobID}
		var e, shot, note sql.NullString
		if err := rows.Scan(&st.N, &st.Description, &st.Action, &st.OK, &e, &shot, &note, &st.CreatedAt); err != nil {
			return nil, err
		}
		st.Error, st.Screenshot, st.Note = e.String, shot.String, note.String
		out = append(out, st)
	}
	return out, rows.Err()
}
