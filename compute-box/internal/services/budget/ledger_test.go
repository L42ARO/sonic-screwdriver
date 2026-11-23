package budget

import (
	"context"
	"testing"

	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/store"
)

// Each user is held to their own cap; one user's spend never blocks another.
func TestCapIsPerUser(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	alice, _ := db.EnsureUser(ctx, "alice")
	bob, _ := db.EnsureUser(ctx, "bob")
	ledger := New(db, NewCapLimiter(db, 0.01, nil))

	if err := ledger.Allow(ctx, alice); err != nil {
		t.Fatalf("fresh user refused: %v", err)
	}
	for ledger.Allow(ctx, alice) == nil {
		ledger.Record(ctx, alice, "", "gpt-6-luna", Tokens{Input: 200_000, Output: 20_000})
	}
	if err := ledger.Allow(ctx, bob); err != nil {
		t.Fatalf("alice's spend blocked bob: %v", err)
	}
	spent, capUSD, _ := ledger.Budget(ctx, alice)
	if spent < 0.01 || capUSD != 0.01 {
		t.Fatalf("alice budget = %v of %v", spent, capUSD)
	}
	if spent, _, _ := ledger.Budget(ctx, bob); spent != 0 {
		t.Fatalf("bob charged %v for alice's calls", spent)
	}
	if err := New(db, NewCapLimiter(db, 0, nil)).Allow(ctx, alice); err != nil {
		t.Fatalf("a cap of 0 should mean no limit: %v", err)
	}
}
