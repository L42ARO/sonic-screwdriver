package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"

	"sonic-screwdriver/compute-box/src/utils"
)

// Definition is an operation as callers define it.
type Definition struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	Instructions string          `json:"instructions"`
	Input        json.RawMessage `json:"input"`
	Output       json.RawMessage `json:"output"`
}

// SyncResult reports what a sync changed, by operation name.
type SyncResult struct {
	Created   []string `json:"created"`
	Updated   []string `json:"updated"`
	Removed   []string `json:"removed"`
	Unchanged []string `json:"unchanged"`
}

// SyncOperations makes a project's operations exactly `defs`, creating the project if
// needed. It runs in one transaction: either everything changes or nothing does.
func (s *Store) SyncOperations(ctx context.Context, userID int64, project string, defs []Definition) (SyncResult, error) {
	res := SyncResult{Created: []string{}, Updated: []string{}, Removed: []string{}, Unchanged: []string{}}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return res, err
	}
	defer tx.Rollback()

	now := utils.Now()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO projects (user_id, name, created_at) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`,
		userID, project, now); err != nil {
		return res, err
	}
	var projectID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM projects WHERE user_id = ? AND name = ?`,
		userID, project).Scan(&projectID); err != nil {
		return res, err
	}

	existing := map[string][]byte{}
	rows, err := tx.QueryContext(ctx, `SELECT name, definition FROM operations WHERE project_id = ?`, projectID)
	if err != nil {
		return res, err
	}
	for rows.Next() {
		var name string
		var def []byte
		if err := rows.Scan(&name, &def); err != nil {
			rows.Close()
			return res, err
		}
		existing[name] = def
	}
	rows.Close()

	for _, d := range defs {
		doc, err := json.Marshal(d)
		if err != nil {
			return res, err
		}
		old, found := existing[d.Name]
		delete(existing, d.Name)
		switch {
		case !found:
			res.Created = append(res.Created, d.Name)
		case sameJSON(old, doc):
			res.Unchanged = append(res.Unchanged, d.Name)
			continue
		default:
			res.Updated = append(res.Updated, d.Name)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO operations (project_id, name, definition, updated_at) VALUES (?, ?, ?, ?)
			ON CONFLICT (project_id, name) DO UPDATE SET definition = excluded.definition, updated_at = excluded.updated_at`,
			projectID, d.Name, string(doc), now); err != nil {
			return res, err
		}
	}
	for name := range existing {
		if _, err := tx.ExecContext(ctx, `DELETE FROM operations WHERE project_id = ? AND name = ?`, projectID, name); err != nil {
			return res, err
		}
		res.Removed = append(res.Removed, name)
	}
	sort.Strings(res.Removed)
	return res, tx.Commit()
}

// ListOperations returns a project's operations, or nil if the project doesn't exist.
func (s *Store) ListOperations(ctx context.Context, userID int64, project string) ([]Definition, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT o.definition FROM operations o JOIN projects p ON p.id = o.project_id
		WHERE p.user_id = ? AND p.name = ? ORDER BY o.name`, userID, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Definition{}
	for rows.Next() {
		var doc []byte
		if err := rows.Scan(&doc); err != nil {
			return nil, err
		}
		var d Definition
		if err := json.Unmarshal(doc, &d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetOperation returns one operation, or nil if it doesn't exist.
func (s *Store) GetOperation(ctx context.Context, userID int64, project, name string) (*Definition, error) {
	var doc []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT o.definition FROM operations o JOIN projects p ON p.id = o.project_id
		WHERE p.user_id = ? AND p.name = ? AND o.name = ?`, userID, project, name).Scan(&doc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var d Definition
	return &d, json.Unmarshal(doc, &d)
}

// sameJSON compares two JSON documents ignoring formatting.
func sameJSON(a, b []byte) bool {
	var x, y bytes.Buffer
	if json.Compact(&x, a) != nil || json.Compact(&y, b) != nil {
		return bytes.Equal(a, b)
	}
	return bytes.Equal(x.Bytes(), y.Bytes())
}
