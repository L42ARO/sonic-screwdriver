package store

import (
	"context"
	"database/sql"
	"fmt"
)

// UserBudget is the user's own spend cap, if one is set (ok is false when the server
// default applies). 0 means no limit.
func (s *Store) UserBudget(ctx context.Context, userID int64) (capUSD float64, ok bool, err error) {
	var v sql.NullFloat64
	err = s.db.QueryRowContext(ctx, `SELECT budget_usd FROM users WHERE id = ?`, userID).Scan(&v)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	return v.Float64, v.Valid, err
}

// SetUserBudget sets the named user's spend cap; nil returns them to the server default.
func (s *Store) SetUserBudget(ctx context.Context, name string, capUSD *float64) error {
	var v any
	if capUSD != nil {
		if *capUSD < 0 {
			return fmt.Errorf("a budget can't be negative")
		}
		v = *capUSD
	}
	res, err := s.db.ExecContext(ctx, `UPDATE users SET budget_usd = ? WHERE name = ?`, v, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no user named %q (they appear after their first sign-in)", name)
	}
	return nil
}

// UserBudgetRow is one user's cap and spend, for listing.
type UserBudgetRow struct {
	ID       int64
	Name     string
	CapUSD   float64 // valid only if Custom
	Custom   bool
	SpentUSD float64 // conservative: the larger of the two cost estimates
}

// UserBudgets lists every user with their own cap (if any) and spend so far.
func (s *Store) UserBudgets(ctx context.Context) ([]UserBudgetRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.id, u.name, u.budget_usd,
			MAX(COALESCE(SUM(m.cost_usd),0), COALESCE(SUM(m.cost_conservative_usd),0))
		FROM users u LEFT JOIN model_usage m ON m.user_id = u.id
		GROUP BY u.id ORDER BY u.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserBudgetRow
	for rows.Next() {
		var r UserBudgetRow
		var c sql.NullFloat64
		if err := rows.Scan(&r.ID, &r.Name, &c, &r.SpentUSD); err != nil {
			return nil, err
		}
		r.CapUSD, r.Custom = c.Float64, c.Valid
		out = append(out, r)
	}
	return out, rows.Err()
}
