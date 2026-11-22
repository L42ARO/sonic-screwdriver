package store

import (
	"context"
	"database/sql"

	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/utils"
)

// Machine is a local-agent the compute-box has seen.
type Machine struct {
	Name     string `json:"name"`
	Screen   string `json:"-"`
	LastSeen string `json:"last_seen,omitempty"`
}

// TouchMachine records that a machine connected, creating it on first sight.
func (s *Store) TouchMachine(ctx context.Context, userID int64, name, screen string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO machines (user_id, name, screen, last_seen) VALUES (?, ?, ?, ?)
		ON CONFLICT (user_id, name) DO UPDATE SET screen = excluded.screen, last_seen = excluded.last_seen`,
		userID, name, screen, utils.Now())
	return err
}

// ListMachines returns every machine the user has, by name.
func (s *Store) ListMachines(ctx context.Context, userID int64) ([]Machine, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT name, screen, last_seen FROM machines WHERE user_id = ? ORDER BY name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Machine{}
	for rows.Next() {
		var m Machine
		var screen, seen sql.NullString
		if err := rows.Scan(&m.Name, &screen, &seen); err != nil {
			return nil, err
		}
		m.Screen, m.LastSeen = screen.String, seen.String
		out = append(out, m)
	}
	return out, rows.Err()
}
