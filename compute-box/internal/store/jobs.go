package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/utils"
)

// Job statuses.
const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

type Job struct {
	ID             string          `json:"job_id"`
	UserID         int64           `json:"-"`
	Project        string          `json:"project"`
	Operation      string          `json:"operation"`
	Machine        string          `json:"machine"`
	Input          json.RawMessage `json:"input"`
	Definition     Definition      `json:"-"`
	Status         string          `json:"status"`
	Output         json.RawMessage `json:"output,omitempty"`
	ErrorCode      string          `json:"-"`
	ErrorMessage   string          `json:"-"`
	OutcomeUnknown bool            `json:"outcome_unknown,omitempty"`
	IdempotencyKey string          `json:"-"`
	Model          string          `json:"model,omitempty"`
	CreatedAt      string          `json:"created_at"`
	StartedAt      string          `json:"started_at,omitempty"`
	FinishedAt     string          `json:"finished_at,omitempty"`
	CostUSD        float64         `json:"cost_usd"`
	Steps          int             `json:"steps"`
}

// Done reports whether the job reached a final status.
func (j *Job) Done() bool {
	return j.Status == StatusSucceeded || j.Status == StatusFailed || j.Status == StatusCancelled
}

func (s *Store) CreateJob(ctx context.Context, j *Job) error {
	def, _ := json.Marshal(j.Definition)
	var key any
	if j.IdempotencyKey != "" {
		key = j.IdempotencyKey
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO jobs (id, user_id, project, operation, machine, input, definition, status, idempotency_key, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		j.ID, j.UserID, j.Project, j.Operation, j.Machine, string(j.Input), string(def), j.Status, key, j.CreatedAt)
	return err
}

func (s *Store) StartJob(ctx context.Context, id, model string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET status = ?, started_at = ?, model = ? WHERE id = ?`,
		StatusRunning, utils.Now(), model, id)
	return err
}

// FinishJob records a job's final state.
func (s *Store) FinishJob(ctx context.Context, j *Job) error {
	var output any
	if len(j.Output) > 0 {
		output = string(j.Output)
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE jobs SET status = ?, output = ?, error_code = ?, error_message = ?, outcome_unknown = ?, finished_at = ?
		WHERE id = ?`,
		j.Status, output, nullable(j.ErrorCode), nullable(j.ErrorMessage), j.OutcomeUnknown, utils.Now(), j.ID)
	return err
}

const jobColumns = `j.id, j.user_id, j.project, j.operation, j.machine, j.input, j.definition, j.status,
	j.output, j.error_code, j.error_message, j.outcome_unknown, j.idempotency_key, j.model,
	j.created_at, j.started_at, j.finished_at,
	(SELECT COALESCE(SUM(cost_usd), 0) FROM model_usage u WHERE u.job_id = j.id),
	(SELECT COUNT(*) FROM job_steps st WHERE st.job_id = j.id)`

func scanJob(row interface{ Scan(...any) error }) (*Job, error) {
	var j Job
	var input, def string
	var output, errCode, errMsg, key, model, started, finished sql.NullString
	err := row.Scan(&j.ID, &j.UserID, &j.Project, &j.Operation, &j.Machine, &input, &def, &j.Status,
		&output, &errCode, &errMsg, &j.OutcomeUnknown, &key, &model,
		&j.CreatedAt, &started, &finished, &j.CostUSD, &j.Steps)
	if err != nil {
		return nil, err
	}
	j.Input = json.RawMessage(input)
	json.Unmarshal([]byte(def), &j.Definition)
	if output.Valid {
		j.Output = json.RawMessage(output.String)
	}
	j.ErrorCode, j.ErrorMessage, j.IdempotencyKey = errCode.String, errMsg.String, key.String
	j.Model, j.StartedAt, j.FinishedAt = model.String, started.String, finished.String
	return &j, nil
}

// GetJob returns a job, or nil if it doesn't exist.
func (s *Store) GetJob(ctx context.Context, userID int64, id string) (*Job, error) {
	j, err := scanJob(s.db.QueryRowContext(ctx,
		`SELECT `+jobColumns+` FROM jobs j WHERE j.user_id = ? AND j.id = ?`, userID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return j, err
}

// JobByIdempotencyKey returns the job created with this key, or nil.
func (s *Store) JobByIdempotencyKey(ctx context.Context, userID int64, key string) (*Job, error) {
	j, err := scanJob(s.db.QueryRowContext(ctx,
		`SELECT `+jobColumns+` FROM jobs j WHERE j.user_id = ? AND j.idempotency_key = ?`, userID, key))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return j, err
}

// ListJobs returns a machine's jobs with the given statuses, newest first.
func (s *Store) ListJobs(ctx context.Context, userID int64, machine string, statuses []string, limit int) ([]*Job, error) {
	q := `SELECT ` + jobColumns + ` FROM jobs j WHERE j.user_id = ?`
	args := []any{userID}
	if machine != "" {
		q += ` AND j.machine = ?`
		args = append(args, machine)
	}
	if len(statuses) > 0 {
		q += ` AND j.status IN (?` + strings.Repeat(",?", len(statuses)-1) + `)`
		for _, st := range statuses {
			args = append(args, st)
		}
	}
	q += ` ORDER BY j.created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// QueuedJobs returns every user's waiting jobs, oldest first, to re-queue after a restart.
func (s *Store) QueuedJobs(ctx context.Context) ([]*Job, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+jobColumns+` FROM jobs j WHERE j.status = ? ORDER BY j.created_at`, StatusQueued)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// InterruptRunning fails jobs left running by a previous process (it stopped mid-job).
func (s *Store) InterruptRunning(ctx context.Context) (int64, error) {
	r, err := s.db.ExecContext(ctx, `
		UPDATE jobs SET status = ?, error_code = 'machine_disconnected',
		error_message = 'the compute-box restarted while this job was running', outcome_unknown = 1, finished_at = ?
		WHERE status = ?`, StatusFailed, utils.Now(), StatusRunning)
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
