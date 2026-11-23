package store

import (
	"context"
	"encoding/json"

	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/utils"
)

// Program explains why an application is relevant to a project.
type Program struct {
	Description  string `json:"description"`
	Instructions string `json:"instructions,omitempty"`
}

// InstalledProgram describes an installation on one machine. Commands are never shell strings.
type InstalledProgram struct {
	Executable          string   `json:"executable"`
	Args                []string `json:"args,omitempty"`
	WorkingDirectory    string   `json:"working_directory,omitempty"`
	WindowTitleContains string   `json:"window_title_contains"`
	StartupTimeoutMS    int      `json:"startup_timeout_ms,omitempty"`
}

func (s *Store) SetProjectPrograms(ctx context.Context, userID int64, project string, programs map[string]Program) error {
	definitions := map[string]any{}
	for k, v := range programs {
		definitions[k] = v
	}
	return s.setPrograms(ctx, userID, "project", project, definitions)
}

func (s *Store) SetMachinePrograms(ctx context.Context, userID int64, machine string, programs map[string]InstalledProgram) error {
	definitions := map[string]any{}
	for k, v := range programs {
		definitions[k] = v
	}
	return s.setPrograms(ctx, userID, "machine", machine, definitions)
}

// setPrograms replaces only the selected owner's program configuration, atomically.
// kind is an internal constant, never request input.
func (s *Store) setPrograms(ctx context.Context, userID int64, kind, name string, definitions map[string]any) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if kind == "project" {
		_, err = tx.ExecContext(ctx, `INSERT INTO projects (user_id,name,created_at) VALUES (?,?,?) ON CONFLICT DO NOTHING`, userID, name, utils.Now())
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO machines (user_id,name) VALUES (?,?) ON CONFLICT DO NOTHING`, userID, name)
	}
	if err != nil {
		return err
	}
	var id int64
	if err = tx.QueryRowContext(ctx, "SELECT id FROM "+kind+"s WHERE user_id=? AND name=?", userID, name).Scan(&id); err != nil {
		return err
	}
	table, column := kind+"_programs", kind+"_id"
	if _, err = tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE "+column+"=?", id); err != nil {
		return err
	}
	for name, definition := range definitions {
		raw, err := json.Marshal(definition)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO "+table+" ("+column+",name,definition) VALUES (?,?,?)", id, name, string(raw)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func readPrograms[T any](ctx context.Context, s *Store, userID int64, kind, name string) (map[string]T, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT p.name,p.definition FROM "+kind+"_programs p JOIN "+kind+"s o ON o.id=p."+kind+"_id WHERE o.user_id=? AND o.name=? ORDER BY p.name", userID, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]T{}
	for rows.Next() {
		var name, raw string
		if err := rows.Scan(&name, &raw); err != nil {
			return nil, err
		}
		var definition T
		if err := json.Unmarshal([]byte(raw), &definition); err != nil {
			return nil, err
		}
		out[name] = definition
	}
	return out, rows.Err()
}

func (s *Store) ProjectPrograms(ctx context.Context, userID int64, project string) (map[string]Program, error) {
	return readPrograms[Program](ctx, s, userID, "project", project)
}
func (s *Store) MachinePrograms(ctx context.Context, userID int64, machine string) (map[string]InstalledProgram, error) {
	return readPrograms[InstalledProgram](ctx, s, userID, "machine", machine)
}
