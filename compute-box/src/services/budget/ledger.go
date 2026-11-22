package budget

import (
	"context"
	"fmt"
	"log"
	"sync"

	"sonic-screwdriver/compute-box/src/services/notify"
	"sonic-screwdriver/compute-box/src/store"
)

// Tokens is what a model call reports using.
type Tokens struct {
	Input      int
	Cached     int
	CacheWrite int
	Output     int
	Reasoning  int
}

// Ledger records every model call and refuses new ones once the cap is reached.
type Ledger struct {
	store    *store.Store
	capUSD   float64
	notifier *notify.Notifier

	mu       sync.Mutex
	alerted  bool
	spentUSD float64 // conservative running total
}

func New(ctx context.Context, s *store.Store, capUSD float64, n *notify.Notifier) (*Ledger, error) {
	total, err := s.TotalSpend(ctx)
	if err != nil {
		return nil, err
	}
	l := &Ledger{store: s, capUSD: capUSD, notifier: n, spentUSD: max(total.CostUSD, total.CostConservativeUSD)}
	log.Printf("budget: $%.4f spent of $%.2f cap", l.spentUSD, capUSD)
	return l, nil
}

// Allow returns an error if another model call would start past the cap.
func (l *Ledger) Allow() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.capUSD > 0 && l.spentUSD >= l.capUSD {
		l.alertOnce()
		return fmt.Errorf("model budget of $%.2f reached ($%.4f spent)", l.capUSD, l.spentUSD)
	}
	return nil
}

// Record stores a model call and returns its cost by the agreed formula.
func (l *Ledger) Record(ctx context.Context, jobID, model string, t Tokens) float64 {
	cost := CalculateTokenCost(model, t.Input, t.Cached, t.Output)
	safe := conservativeCost(model, t.Input, t.Cached, t.CacheWrite, t.Output)
	err := l.store.AddUsage(ctx, store.Usage{
		JobID: jobID, Model: model,
		InputTokens: t.Input, CachedTokens: t.Cached, CacheWriteTokens: t.CacheWrite,
		OutputTokens: t.Output, ReasoningTokens: t.Reasoning,
		CostUSD: cost, CostConservativeUSD: safe,
	})
	if err != nil {
		log.Printf("budget: recording usage: %v", err)
	}
	l.mu.Lock()
	l.spentUSD += max(cost, safe)
	if l.capUSD > 0 && l.spentUSD >= l.capUSD {
		l.alertOnce()
	}
	l.mu.Unlock()
	return cost
}

// Spent returns the conservative running total and the cap.
func (l *Ledger) Spent() (spent, capUSD float64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.spentUSD, l.capUSD
}

func (l *Ledger) alertOnce() {
	if l.alerted {
		return
	}
	l.alerted = true
	msg := fmt.Sprintf("Model spend reached $%.4f of the $%.2f cap. The compute-box now refuses new model calls.",
		l.spentUSD, l.capUSD)
	log.Print("budget: " + msg)
	go l.notifier.Send(notify.High, "sonic-screwdriver: budget cap reached", msg)
}
