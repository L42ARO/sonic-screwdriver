package budget

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/L42ARO/sonic-screwdriver/compute-box/ext"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/services/notify"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/store"
)

// Tokens is what a model call reports using.
type Tokens struct {
	Input      int
	Cached     int
	CacheWrite int
	Output     int
	Reasoning  int
}

// Ledger records every model call against its user and asks the limiter before new ones.
type Ledger struct {
	store   *store.Store
	limiter ext.Limiter
}

func New(s *store.Store, limiter ext.Limiter) *Ledger {
	return &Ledger{store: s, limiter: limiter}
}

// Allow returns an error if the user may not start another model call.
func (l *Ledger) Allow(ctx context.Context, userID int64) error {
	return l.limiter.Allow(ctx, ext.UserID(userID))
}

// Budget reports what the user has spent and may spend.
func (l *Ledger) Budget(ctx context.Context, userID int64) (spent, capUSD float64, err error) {
	return l.limiter.Budget(ctx, ext.UserID(userID))
}

// Record stores a model call and returns its cost by the agreed formula.
func (l *Ledger) Record(ctx context.Context, userID int64, jobID, model string, t Tokens) float64 {
	cost := CalculateTokenCost(model, t.Input, t.Cached, t.Output)
	safe := conservativeCost(model, t.Input, t.Cached, t.CacheWrite, t.Output)
	err := l.store.AddUsage(ctx, store.Usage{
		UserID: userID, JobID: jobID, Model: model,
		InputTokens: t.Input, CachedTokens: t.Cached, CacheWriteTokens: t.CacheWrite,
		OutputTokens: t.Output, ReasoningTokens: t.Reasoning,
		CostUSD: cost, CostConservativeUSD: safe,
	})
	if err != nil {
		log.Printf("budget: recording usage: %v", err)
	}
	return cost
}

// CapLimiter is the default Limiter: each user may spend up to their own cap if one is
// set (store.SetUserBudget), else the server-wide cap, counted conservatively (cache
// writes at their premium). A cap of 0 means no limit.
type CapLimiter struct {
	store    *store.Store
	capUSD   float64
	notifier *notify.Notifier

	mu      sync.Mutex
	alerted map[ext.UserID]bool
}

func NewCapLimiter(s *store.Store, capUSD float64, n *notify.Notifier) *CapLimiter {
	return &CapLimiter{store: s, capUSD: capUSD, notifier: n, alerted: map[ext.UserID]bool{}}
}

func (c *CapLimiter) Budget(ctx context.Context, user ext.UserID) (float64, float64, error) {
	capUSD := c.capUSD
	if own, ok, err := c.store.UserBudget(ctx, int64(user)); err != nil {
		return 0, capUSD, err
	} else if ok {
		capUSD = own
	}
	sp, err := c.store.UserSpend(ctx, int64(user))
	if err != nil {
		return 0, capUSD, err
	}
	return max(sp.CostUSD, sp.CostConservativeUSD), capUSD, nil
}

func (c *CapLimiter) Allow(ctx context.Context, user ext.UserID) error {
	spent, capUSD, err := c.Budget(ctx, user)
	if err != nil {
		return err
	}
	if capUSD <= 0 || spent < capUSD {
		c.mu.Lock()
		delete(c.alerted, user) // raised cap: alert again next time it's reached
		c.mu.Unlock()
		return nil
	}
	c.alertOnce(user, spent, capUSD)
	return fmt.Errorf("model budget of $%.2f reached ($%.4f spent)", capUSD, spent)
}

func (c *CapLimiter) alertOnce(user ext.UserID, spent, capUSD float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.alerted[user] {
		return
	}
	c.alerted[user] = true
	msg := fmt.Sprintf("User %d reached $%.4f of the $%.2f model budget. The compute-box now refuses their model calls.",
		user, spent, capUSD)
	log.Print("budget: " + msg)
	go c.notifier.Send(notify.High, "sonic-screwdriver: budget cap reached", msg)
}
