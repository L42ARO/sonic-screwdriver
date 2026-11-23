package store

import (
	"context"

	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/utils"
)

// Usage is one model call's token counts and cost.
type Usage struct {
	UserID              int64
	JobID               string
	Model               string
	InputTokens         int
	CachedTokens        int
	CacheWriteTokens    int
	OutputTokens        int
	ReasoningTokens     int
	CostUSD             float64
	CostConservativeUSD float64
}

func (s *Store) AddUsage(ctx context.Context, u Usage) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO model_usage (user_id, job_id, model, input_tokens, cached_tokens, cache_write_tokens, output_tokens,
			reasoning_tokens, cost_usd, cost_conservative_usd, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.UserID, nullable(u.JobID), u.Model, u.InputTokens, u.CachedTokens, u.CacheWriteTokens, u.OutputTokens,
		u.ReasoningTokens, u.CostUSD, u.CostConservativeUSD, utils.Now())
	return err
}

// Spend totals model usage.
type Spend struct {
	Calls               int     `json:"calls"`
	InputTokens         int     `json:"input_tokens"`
	CachedTokens        int     `json:"cached_tokens"`
	OutputTokens        int     `json:"output_tokens"`
	CostUSD             float64 `json:"cost_usd"`
	CostConservativeUSD float64 `json:"cost_conservative_usd"`
}

// UserSpend is one user's model usage so far.
func (s *Store) UserSpend(ctx context.Context, userID int64) (Spend, error) {
	var sp Spend
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(cached_tokens),0),
			COALESCE(SUM(output_tokens),0), COALESCE(SUM(cost_usd),0), COALESCE(SUM(cost_conservative_usd),0)
		FROM model_usage WHERE user_id = ?`, userID).Scan(&sp.Calls, &sp.InputTokens, &sp.CachedTokens, &sp.OutputTokens,
		&sp.CostUSD, &sp.CostConservativeUSD)
	return sp, err
}
