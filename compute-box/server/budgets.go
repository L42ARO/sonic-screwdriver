package server

import (
	"context"

	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/store"
)

// UserBudget is one user's model spend cap and spend so far, in USD.
type UserBudget struct {
	ID       int64
	Name     string  // "default" is the self-hosted owner
	CapUSD   float64 // the cap in effect; 0 means no limit
	Custom   bool    // the user has their own cap, rather than Config.BudgetUSD
	SpentUSD float64
}

// Budgets lists every user's cap and spend. It can run while the server is running.
func Budgets(ctx context.Context, cfg Config) ([]UserBudget, error) {
	db, err := store.Open(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.UserBudgets(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]UserBudget, 0, len(rows))
	for _, r := range rows {
		b := UserBudget{ID: r.ID, Name: r.Name, CapUSD: cfg.BudgetUSD, Custom: r.Custom, SpentUSD: r.SpentUSD}
		if r.Custom {
			b.CapUSD = r.CapUSD
		}
		out = append(out, b)
	}
	return out, nil
}

// SetBudget gives the named user their own spend cap (0 means no limit), or with nil
// returns them to Config.BudgetUSD. It takes effect on their next model call, without a
// restart; it can run while the server is running.
func SetBudget(ctx context.Context, cfg Config, user string, capUSD *float64) error {
	db, err := store.Open(cfg.DataDir)
	if err != nil {
		return err
	}
	defer db.Close()
	return db.SetUserBudget(ctx, user, capUSD)
}
