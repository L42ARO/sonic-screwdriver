package store

import (
	"context"
	"database/sql"

	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/utils"
)

// Machine is a local-agent the compute-box has seen.
type Machine struct {
	Name           string `json:"name"`
	Screen         string `json:"-"`
	LastSeen       string `json:"last_seen,omitempty"`
	TokenPrefix    string `json:"-"` // shown part of its token; empty until one is issued
	TokenCreatedAt string `json:"-"`
}

// TouchMachine records that a machine connected. Machines are created by issuing a token.
func (s *Store) TouchMachine(ctx context.Context, userID int64, name, screen string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE machines SET screen = ?, last_seen = ? WHERE user_id = ? AND name = ?`,
		screen, utils.Now(), userID, name)
	return err
}

// ListMachines returns every machine the user has, by name.
func (s *Store) ListMachines(ctx context.Context, userID int64) ([]Machine, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT name, screen, last_seen, token_prefix, token_created_at FROM machines WHERE user_id = ? ORDER BY name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Machine{}
	for rows.Next() {
		var m Machine
		var screen, seen, prefix, issued sql.NullString
		if err := rows.Scan(&m.Name, &screen, &seen, &prefix, &issued); err != nil {
			return nil, err
		}
		m.Screen, m.LastSeen, m.TokenPrefix, m.TokenCreatedAt = screen.String, seen.String, prefix.String, issued.String
		out = append(out, m)
	}
	return out, rows.Err()
}
